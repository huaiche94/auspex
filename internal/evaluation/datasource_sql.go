// datasource_sql.go: SQLDataSource, the real, storage-backed production
// implementation of this package's own DataSource interface
// (datasource.go). This is a Final-integration-gate corrective addition
// (contract-integrator-final review finding, not a numbered DAG node): every
// pipeline stage (Scope/Token/Quota/Risk/Policy) was built and tested across
// predictor-01 through predictor-11, but the layer that feeds them real
// signals from the live system — DataSource itself — had never been given a
// concrete implementation, so cmd/auspex/main.go could not wire a real
// EvaluationService. See docs/implementation/vertical-slice/predictor.md's
// "Final-integration-gate correction" entry for the full account.
//
// SQLDataSource queries, read-only, across several other roles' tables:
// foundation's provider_sessions/worktrees/repositories/tasks (0001-0004),
// claude-provider's events (0010), checkpoint's progress_nodes/progress_edges
// (0020-0021), and this package's own runway_forecasts/policy_decisions
// (0042-0043, already handled by store.go's row types). No migration is
// added by this file (schema is frozen for this corrective addition) and no
// file outside internal/evaluation/** is modified. Where checkpoint's own
// exported internal/progress.NodeStore/EdgeStore types offer the needed
// read, this file calls them directly (read-only) rather than duplicating
// their SQL — the same established precedent qa/runtime use elsewhere in
// this project for calling into another role's real exported API instead of
// a mock. Every other cross-role table (events, provider_sessions,
// worktrees, repositories, tasks) has no owning role's Go store exported for
// the specific read shape this file needs (e.g. no
// "list recent events for a session" method exists anywhere), so this file
// issues its own plain SQL against those tables directly — a normal,
// expected pattern for a durable, schema-stable, append-only/reference
// table with no owning service abstraction to call into instead.
//
// # Method-by-method: real-data-backed vs. honest cold-start
//
// Real, storage-backed queries: Resolve, Classification (size-only prompt
// signal — see below), Progress, RecentSimilarTurnTokens, Quota, Context,
// RunwayForecast, PriorRunwayHitConfirmed.
//
// Honest cold-start (ok=false) always, by design: Repository, Session.
// Both return ok=false unconditionally — not because the query is hard, but
// because the schema reachable from this package's exclusive path
// (internal/evaluation/**) has no real backing data for the specific
// features.RepositoryFeatures/SessionFeatures fields these methods promise
// (TrackedFileCount, LanguageCount, DirtyFileCount, RecentTurnUsageP50/80/90
// quantiles, ChangedFilesRecentP50/90, etc. — see each method's own doc
// comment for the field-by-field reasoning). Fabricating a plausible-looking
// value for these would violate this package's own "no fabrication, cold-
// start is a valid answer" testing discipline (predictor-05 through
// predictor-11's established precedent) more than it would help; per the
// DataSource interface's own doc comment, ok=false here is the honest
// answer, not a shortcut.
//
// Classification's real signal: Constitution §7 rule 2 means raw prompt
// text is never persisted anywhere this package can read — but since issue
// #42, the DERIVED feature booleans/counts are (computed by
// features.ExtractPromptFeatures inside the hook/evaluate process where
// the raw text lives, persisted as bool/int payload fields by
// claude-telemetry's normalizer alongside the original
// prompt_sha256/prompt_byte_length/prompt_approx_tokens trio). This file
// rebuilds a real, non-fabricated features.PromptFeatures from exactly
// those persisted fields and runs it through the real features.ClassifyTask,
// the same production classifier predictor-03 built — it does not
// reimplement or approximate classification itself. On events persisted
// before #42 the feature keys are simply absent, and every
// verb/domain-indicator boolean stays at its zero value (false) — the
// honest state for signal that was never captured (unknown is not zero) —
// so old sessions still classify as TaskClassUnknown/
// ReasonInsufficientSignal, ClassifyTask's own designed response to
// insufficient signal.
package evaluation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/features"
	"github.com/huaiche94/auspex/internal/pricing"
	"github.com/huaiche94/auspex/internal/progress"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// SQLDataSource is the real, SQLite-backed implementation of DataSource. It
// depends only on *sqlite.DB (this package's own storage handle, already
// used by store.go/service.go) and read-only queries/exported store calls
// against other roles' tables — it never writes to a table this package
// does not own.
type SQLDataSource struct {
	DB *sqlite.DB

	// Pricing is the model-family resolution table the cohort ladder
	// (RecentSimilarTurnTokens, #20 Phase 1) uses to normalize model IDs
	// into families. Optional, following Service.Pricing's exact
	// convention: nil falls back to pricing.DefaultTable().
	Pricing *pricing.Table

	// nodes/edges are checkpoint's own exported, read-only-safe stores
	// (internal/progress.NodeStore/EdgeStore) reused directly rather than
	// this file re-implementing progress_nodes/progress_edges SQL —
	// mirrors this project's established precedent of one role calling
	// directly into another role's real exported API for a read (qa,
	// runtime). Constructed lazily against DB in NewSQLDataSource so
	// callers never have to wire them separately.
	nodes *progress.NodeStore
	edges *progress.EdgeStore
}

// NewSQLDataSource constructs a SQLDataSource bound to db. db must be
// non-nil and already migrated (NewSQLDataSource itself does not run
// migrations — matches every other constructor in this package, e.g.
// evaluation.New, which panics on a nil dependency rather than deferring
// the failure to first use).
func NewSQLDataSource(db *sqlite.DB) *SQLDataSource {
	if db == nil {
		panic("evaluation: NewSQLDataSource requires a non-nil *sqlite.DB")
	}
	return &SQLDataSource{
		DB: db,
		// internal/progress.NewNodeStore takes a domain.Clock because it
		// writes updated_at on mutating calls; this file only ever calls
		// its read methods (Get/ListByTask), so a nil Clock is safe here
		// — passing one would require this package to take on a Clock
		// dependency it does not otherwise need for a store it never
		// writes through.
		nodes: progress.NewNodeStore(db, nil),
		edges: progress.NewEdgeStore(db),
	}
}

var _ DataSource = (*SQLDataSource)(nil)

// --- 1. Resolve ----------------------------------------------------------

// Resolve looks up provider_sessions by sessionID to find its worktree_id,
// then worktrees to find repository_id (foundation's 0001-0003). TaskID
// resolution heuristic (this package's own documented judgment call, since
// neither the DataSource interface nor any frozen contract names one): the
// most-recently-created task for that worktree, preferring a task bound to
// this exact session (tasks.session_id) over a worktree-wide match — a
// worktree may have tasks left over from earlier sessions (session_id set
// but that session long ended), so this prefers the caller's own session's
// task first and only falls back to "most recent task in the worktree" when
// this session has not created one of its own yet (e.g. a task created by
// an earlier session in the same worktree that the current session is
// continuing).
func (s *SQLDataSource) Resolve(ctx context.Context, sessionID domain.SessionID) (ResolvedSession, error) {
	if sessionID == "" {
		return ResolvedSession{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "evaluation: SQLDataSource.Resolve requires a non-empty SessionID",
			Retryable: false,
		}
	}

	q := sqlite.QuerierFromContext(ctx, s.DB)

	var worktreeID string
	err := q.QueryRowContext(ctx, `
		SELECT worktree_id FROM provider_sessions WHERE id = ?`, string(sessionID),
	).Scan(&worktreeID)
	if errors.Is(err, sql.ErrNoRows) {
		return ResolvedSession{}, &domain.Error{
			Code:      domain.ErrCodeNotFound,
			Message:   "evaluation: no provider_sessions row for session",
			Retryable: false,
			Details:   map[string]string{"session_id": string(sessionID)},
		}
	}
	if err != nil {
		return ResolvedSession{}, fmt.Errorf("evaluation: resolve session %s: query provider_sessions: %w", sessionID, err)
	}

	var repositoryID string
	err = q.QueryRowContext(ctx, `
		SELECT repository_id FROM worktrees WHERE id = ?`, worktreeID,
	).Scan(&repositoryID)
	if errors.Is(err, sql.ErrNoRows) {
		return ResolvedSession{}, &domain.Error{
			Code:      domain.ErrCodeIntegrity,
			Message:   "evaluation: provider_sessions.worktree_id references a missing worktrees row",
			Retryable: false,
			Details:   map[string]string{"session_id": string(sessionID), "worktree_id": worktreeID},
		}
	}
	if err != nil {
		return ResolvedSession{}, fmt.Errorf("evaluation: resolve session %s: query worktrees: %w", sessionID, err)
	}

	taskID, err := s.mostRelevantTaskID(ctx, sessionID, worktreeID)
	if err != nil {
		return ResolvedSession{}, fmt.Errorf("evaluation: resolve session %s: %w", sessionID, err)
	}

	return ResolvedSession{
		RepositoryID: domain.RepositoryID(repositoryID),
		TaskID:       taskID,
	}, nil
}

// mostRelevantTaskID implements Resolve's documented heuristic: prefer the
// most recently created task bound to this exact session, else the most
// recently created task anywhere in the worktree. Returns nil (no error)
// when the worktree has no tasks at all — a brand-new worktree/session
// legitimately has none yet, which is cold-start, not a failure.
func (s *SQLDataSource) mostRelevantTaskID(ctx context.Context, sessionID domain.SessionID, worktreeID string) (*domain.TaskID, error) {
	q := sqlite.QuerierFromContext(ctx, s.DB)

	var taskID string
	err := q.QueryRowContext(ctx, `
		SELECT id FROM tasks
		WHERE session_id = ? AND worktree_id = ?
		ORDER BY created_at DESC, rowid DESC LIMIT 1`,
		string(sessionID), worktreeID,
	).Scan(&taskID)
	if err == nil {
		id := domain.TaskID(taskID)
		return &id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("query tasks by session: %w", err)
	}

	err = q.QueryRowContext(ctx, `
		SELECT id FROM tasks
		WHERE worktree_id = ?
		ORDER BY created_at DESC, rowid DESC LIMIT 1`,
		worktreeID,
	).Scan(&taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // no task yet for this worktree: cold-start, not an error
	}
	if err != nil {
		return nil, fmt.Errorf("query tasks by worktree: %w", err)
	}
	id := domain.TaskID(taskID)
	return &id, nil
}

// --- 2. Classification ----------------------------------------------------

// Classification builds a real (not fabricated) features.PromptFeatures
// from the most recent provider.turn.started event for this session — its
// prompt_sha256/prompt_byte_length/prompt_approx_tokens size fields plus,
// since issue #42, the derived verb/domain/structure booleans and counts
// claude-telemetry's normalizer persists (all computed from raw text
// inside the hook/evaluate process; only booleans, counts, and the hash
// ever reach storage — Constitution §7 rule 2: raw prompt text is never
// persisted). It then runs that through the real features.ClassifyTask
// (predictor-03), optionally sharpened by Progress features when a task is
// known. Feature keys absent from the payload (events persisted before
// #42) decode to their zero values — the honest state for signal that was
// never captured, which typically yields TaskClassUnknown, exactly as
// before; see this file's package doc comment.
func (s *SQLDataSource) Classification(ctx context.Context, sessionID domain.SessionID, taskID *domain.TaskID) (features.Classification, features.PromptFeatures, error) {
	pf, found, err := s.latestPromptFeatures(ctx, sessionID)
	if err != nil {
		return features.Classification{}, features.PromptFeatures{}, fmt.Errorf("evaluation: Classification: %w", err)
	}
	if !found {
		// No provider.turn.started event yet for this session: cold-start,
		// not an error. ClassifyTask's own contract already returns
		// TaskClassUnknown/ConfidenceUnavailable for a zero-value
		// PromptFeatures (ApproxTokens < 2), so this is not a special case
		// to hand-roll here.
		return features.ClassifyTask(features.ClassifierInput{}), features.PromptFeatures{}, nil
	}

	var progPtr *features.ProgressFeatures
	if taskID != nil {
		if prog, ok, err := s.Progress(ctx, taskID); err != nil {
			return features.Classification{}, features.PromptFeatures{}, fmt.Errorf("evaluation: Classification: load progress: %w", err)
		} else if ok {
			progPtr = &prog
		}
	}

	class := features.ClassifyTask(features.ClassifierInput{Prompt: pf, Progress: progPtr})
	return class, pf, nil
}

// latestPromptFeatures queries the most recent provider.turn.started event
// for sessionID and decodes its size fields plus the issue-#42 derived
// feature booleans/counts into a real features.PromptFeatures. found=false
// means no such event exists yet (cold-start). Keys are the stable
// snake_case mirrors NormalizeUserPromptSubmit writes; a key absent from
// the payload (an event persisted before #42) decodes to its zero value,
// never a fabricated signal.
func (s *SQLDataSource) latestPromptFeatures(ctx context.Context, sessionID domain.SessionID) (features.PromptFeatures, bool, error) {
	q := sqlite.QuerierFromContext(ctx, s.DB)
	var payloadJSON string
	err := q.QueryRowContext(ctx, `
		SELECT payload_json FROM events
		WHERE session_id = ? AND event_type = 'provider.turn.started'
		ORDER BY occurred_at DESC, rowid DESC LIMIT 1`,
		string(sessionID),
	).Scan(&payloadJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return features.PromptFeatures{}, false, nil
	}
	if err != nil {
		return features.PromptFeatures{}, false, fmt.Errorf("query latest provider.turn.started event: %w", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return features.PromptFeatures{}, false, fmt.Errorf("decode provider.turn.started payload: %w", err)
	}

	pf := features.PromptFeatures{
		SHA256Hex:       payloadString(payload, "prompt_sha256"),
		ByteLength:      payloadInt(payload, "prompt_byte_length"),
		ApproxTokens:    payloadInt(payload, "prompt_approx_tokens"),
		TokenConfidence: domain.ConfidenceLow, // mirrors ExtractPromptFeatures: always an estimate

		RuneCount: payloadInt(payload, "prompt_rune_count"),
		LineCount: payloadInt(payload, "prompt_line_count"),

		ExplicitPathCount:       payloadInt(payload, "explicit_path_count"),
		ListItemCount:           payloadInt(payload, "list_item_count"),
		AcceptanceCriteriaCount: payloadInt(payload, "acceptance_criteria_count"),

		HasFixVerb:         payloadBool(payload, "has_fix_verb"),
		HasImplementVerb:   payloadBool(payload, "has_implement_verb"),
		HasRefactorVerb:    payloadBool(payload, "has_refactor_verb"),
		HasInvestigateVerb: payloadBool(payload, "has_investigate_verb"),
		HasMigrateVerb:     payloadBool(payload, "has_migrate_verb"),

		MentionsTests:           payloadBool(payload, "mentions_tests"),
		MentionsSchemaOrAPI:     payloadBool(payload, "mentions_schema_or_api"),
		MentionsSecurity:        payloadBool(payload, "mentions_security"),
		MentionsPerformance:     payloadBool(payload, "mentions_performance"),
		MentionsDocumentation:   payloadBool(payload, "mentions_documentation"),
		LongDocumentIndicator:   payloadBool(payload, "long_document_indicator"),
		QuestionIndicator:       payloadBool(payload, "question_indicator"),
		OpenEndedIndicator:      payloadBool(payload, "open_ended_indicator"),
		CrossLayerIndicator:     payloadBool(payload, "cross_layer_indicator"),
		RepositoryWideIndicator: payloadBool(payload, "repository_wide_indicator"),
	}
	return pf, true, nil
}

// --- 3. Repository ---------------------------------------------------------

// Repository always returns ok=false. Every field on features.RepositoryFeatures
// (TrackedFileCount, LanguageCount, GoModuleCount, GoPackageCount,
// DotNetProjectRefs, DirtyFileCount, DirtyLineCount, TargetDirFanOut,
// TestProjectCount, IsMonorepo, RecentChangedPathCount) describes a
// repository-content signal (file counts, language mix, working-tree dirty
// state) that no table reachable from internal/evaluation's exclusive path
// persists: repositories/worktrees (foundation 0001-0002) carry only
// identity/path columns, and repository_checkpoints (checkpoint's
// 0030-0039 range) carries only diff hashes and byte totals for a
// checkpoint artifact, not a working-tree file/language census. Populating
// these fields would require either a new repository-scanning capability
// (out of this role's Boundary — "No ... Git commands", agents/
// predictor.md) or a new cross-role telemetry table that does not exist
// (schema is frozen for this corrective addition). Per this package's own
// established discipline (predictor-05 through -11: "no fabrication, cold-
// start is a valid answer"), the honest response is ok=false, not an
// invented file count. WorktreeID alone (the one field with an obvious
// real backing value) is not populated either, since returning a
// partially-populated struct with ok=true would misrepresent every other
// field as "considered and found zero" rather than "never queried" — the
// interface's own ok bool is the correct, coarser signal here.
func (s *SQLDataSource) Repository(_ context.Context, _ domain.RepositoryID) (features.RepositoryFeatures, bool, error) {
	return features.RepositoryFeatures{}, false, nil
}

// --- 4. Session -------------------------------------------------------------

// Session always returns ok=false. features.SessionFeatures' fields
// (RecentTurnUsageP50/80/90, ChangedFilesRecentP50/90,
// ChangedLinesRecentP50/90, RetryRate, TestFailureRate,
// ToolOutputBytesP50, ContextGrowthRateP50, CompactionCount,
// CheckpointAge) are almost entirely empirical quantiles/rates over a
// well-defined historical window that this wave's schema cannot honestly
// reconstruct end-to-end: events.payload_json carries per-observation
// usage/quota/context numbers (consumed directly and correctly by Quota/
// Context/RecentSimilarTurnTokens below, which need only raw observations,
// not a rate), but turn-level "was this turn a retry", "how many files did
// this turn change", and "how many lines did this turn change" are not
// separately tagged anywhere in the events schema (no retry flag, no
// files/lines-changed payload field on any EventType) — inventing a proxy
// definition for "retry" from turn.failed/turn.started counts alone would
// be a real, material modeling decision (what counts as a retry? within
// what window? does a provider.turn.failed followed by a new
// provider.turn.started even mean the user retried, vs. gave up?) with no
// existing precedent in this codebase to anchor it, unlike
// RecentSimilarTurnTokens below where internal/predictor/token's own
// cold-start/empirical-quantile machinery already defines exactly what
// shape of input it expects. Rather than manufacturing that definition
// unilaterally in a storage-layer bridge, this method returns ok=false —
// honest cold-start, matching this package's own "leave a genuine schema
// gap honest rather than over-engineer a new modeling decision into a
// corrective addition" discipline.
func (s *SQLDataSource) Session(_ context.Context, _ domain.SessionID) (features.SessionFeatures, bool, error) {
	return features.SessionFeatures{}, false, nil
}

// --- 5. Progress -------------------------------------------------------------

// Progress queries checkpoint's real progress_nodes/progress_edges tables
// (via internal/progress.NodeStore/EdgeStore, called directly and
// read-only — see this file's package doc comment) for taskID's current
// state. "Current node" heuristic (this package's own documented judgment
// call, since ProgressFeatures.NodeID is a single optional pointer and a
// task may have many nodes): the most recently updated non-terminal node
// (status not completed/failed/skipped), falling back to the most recently
// updated node of any status if every node is terminal (e.g. a fully
// completed task still being evaluated for one more turn). ok=false when
// taskID is nil or the task has no nodes yet.
func (s *SQLDataSource) Progress(ctx context.Context, taskID *domain.TaskID) (features.ProgressFeatures, bool, error) {
	if taskID == nil {
		return features.ProgressFeatures{}, false, nil
	}

	nodes, err := s.nodes.ListByTask(ctx, *taskID)
	if err != nil {
		return features.ProgressFeatures{}, false, fmt.Errorf("evaluation: Progress: list nodes for task %s: %w", *taskID, err)
	}
	if len(nodes) == 0 {
		return features.ProgressFeatures{}, false, nil
	}

	current := currentNode(nodes)

	edges, err := s.edges.ListByTask(ctx, *taskID)
	if err != nil {
		return features.ProgressFeatures{}, false, fmt.Errorf("evaluation: Progress: list edges for task %s: %w", *taskID, err)
	}

	completed := 0
	for _, n := range nodes {
		if n.Status == domain.NodeCompleted || n.Status == domain.NodeSkipped {
			completed++
		}
	}
	completedRatio := float64(completed) / float64(len(nodes))

	descendantsRemaining := countDescendantsRemaining(nodes, current.ID)
	unresolvedBlockers := countUnresolvedBlockers(nodes, edges, current.ID)

	nodeID := current.ID
	pf := features.ProgressFeatures{
		TaskID:               *taskID,
		NodeID:               &nodeID,
		CurrentNodeKind:      current.Kind,
		DescendantsRemaining: descendantsRemaining,
		CriticalPathLength:   descendantsRemaining, // documented approximation, see below
		CompletedRatio:       &completedRatio,
		IsDocumentSection:    current.Kind == domain.NodeDocumentSection,
		UnresolvedBlockers:   unresolvedBlockers,
		Confidence:           domain.ConfidenceMedium, // real counts from durable storage, but a day-one heuristic for "current node"/"critical path"
	}
	return pf, true, nil
}

// currentNode picks the most-recently-updated non-terminal node, falling
// back to the most-recently-updated node overall when every node is
// terminal. nodes is assumed non-empty (checked by the caller).
func currentNode(nodes []progress.Node) progress.Node {
	var best *progress.Node
	for i := range nodes {
		n := &nodes[i]
		if isTerminalNodeStatus(n.Status) {
			continue
		}
		if best == nil || n.UpdatedAt > best.UpdatedAt {
			best = n
		}
	}
	if best != nil {
		return *best
	}
	best = &nodes[0]
	for i := range nodes {
		n := &nodes[i]
		if n.UpdatedAt > best.UpdatedAt {
			best = n
		}
	}
	return *best
}

func isTerminalNodeStatus(status domain.ProgressNodeStatus) bool {
	switch status {
	case domain.NodeCompleted, domain.NodeFailed, domain.NodeSkipped:
		return true
	default:
		return false
	}
}

// countDescendantsRemaining approximates ProgressFeatures.DescendantsRemaining
// / CriticalPathLength as the count of non-terminal nodes in the task other
// than the current node — the Progress Tree's parent_id column gives a
// direct tree shape but no separate "critical path" cost model exists
// anywhere in this codebase yet (the same gap
// internal/predictor/token.progressMultiplier's own doc comment already
// notes and approximates via CompletedRatio); counting remaining
// non-terminal nodes is a documented, conservative stand-in using only
// real, durable data (no fabricated cost weights), consistent with that
// established precedent.
func countDescendantsRemaining(nodes []progress.Node, currentID domain.ProgressNodeID) int {
	n := 0
	for _, node := range nodes {
		if node.ID == currentID {
			continue
		}
		if !isTerminalNodeStatus(node.Status) {
			n++
		}
	}
	return n
}

// countUnresolvedBlockers counts real progress_edges depends_on edges from
// currentID to a node that is not yet completed/skipped — exactly
// EdgeStore.DependenciesOf's own documented semantics ("must be completed
// or skipped before nodeID may complete"), cross-referenced against nodes'
// real status.
func countUnresolvedBlockers(nodes []progress.Node, edges []progress.Edge, currentID domain.ProgressNodeID) int {
	statusByID := make(map[domain.ProgressNodeID]domain.ProgressNodeStatus, len(nodes))
	for _, n := range nodes {
		statusByID[n.ID] = n.Status
	}
	n := 0
	for _, e := range edges {
		if e.FromNodeID != currentID || e.Kind != progress.EdgeDependsOn {
			continue
		}
		status, known := statusByID[e.ToNodeID]
		if !known {
			continue
		}
		if status != domain.NodeCompleted && status != domain.NodeSkipped {
			n++
		}
	}
	return n
}

// --- 6. RecentSimilarTurnTokens ---------------------------------------------

// RecentSimilarTurnTokens queries claude-provider's events table for recent
// provider.usage.observed total-token observations matching the ADD §15.2
// "similar" cohort, selected via the provider/model/effort fallback ladder
// (#20 Phase 1, ADR-047; docs/backlog/provider-model-effort-features.md
// §3.4): the turn's identity is resolved from provider_sessions (the same
// resolution cache EvaluateTurn's prediction stamp uses), candidate
// observations are drawn provider-wide, and the most specific rung whose
// sample count meets the ADD §15.2 gate answers —
//
//	provider + model family + effort  (CohortRungModelEffort)
//	provider + model family           (CohortRungModelFamily)
//	provider                          (CohortRungProvider)
//	this session's recent turns       (CohortRungSession — the pre-ladder
//	                                   behavior, and the terminal fallback)
//
// A rung whose TURN-side label is unobserved is skipped rather than
// matched-as-empty (unknown is not zero: an unlabeled turn must not
// pretend to match unlabeled samples). Sample-side labels come from the
// usage payload's model_id/effort (stamped by claude-telemetry's
// normalizer at observation granularity — session-level labels would
// mis-assign cohorts after a mid-session /model or /fast switch); model
// family is resolved through the same pricing-table rules the prediction
// stamp uses, so cohort membership and the persisted prediction label
// can never disagree on family.
//
// The full ADD §15.2 cohort also names task class + repository; neither
// is carried on the sample surface (task classification is this
// package's own derived signal, computed after the fact and never
// persisted back onto usage events; events.repository_id is not
// populated by the statusline ingest path), so those dimensions remain
// honestly out of the ladder — the class parameter is accepted (to
// satisfy the interface) but not used to filter, documented here rather
// than silently ignored, exactly as before this ladder existed.
//
// No usage payload carries a total_tokens field yet (the normalizer's
// payload is cost/duration/lines + identity labels), so every rung
// yields zero samples today and the method lands on the session rung
// with an empty slice — RuleTokenForecaster's >= MinSimilarSamples gate
// turns that into the cold-start default, unchanged. When a future
// claude-provider wave adds total_tokens, the ladder activates for free
// with no further change here (the same dormant-machinery contract the
// pre-ladder implementation documented for its flat query).
func (s *SQLDataSource) RecentSimilarTurnTokens(ctx context.Context, sessionID domain.SessionID, _ features.TaskClass) (features.SimilarTurnTokens, error) {
	q := sqlite.QuerierFromContext(ctx, s.DB)

	provider, turnFamily, turnEffort := s.turnCohortIdentity(ctx, sessionID)
	if provider != "" {
		pool, err := s.cohortCandidates(ctx, q, provider)
		if err != nil {
			return features.SimilarTurnTokens{}, err
		}

		rungs := []struct {
			id     features.SimilarTurnCohortRung
			usable bool
			match  func(c cohortCandidate) bool
		}{
			{
				id:     features.CohortRungModelEffort,
				usable: turnFamily != "" && turnEffort != "",
				match:  func(c cohortCandidate) bool { return c.family == turnFamily && c.effort == turnEffort },
			},
			{
				id:     features.CohortRungModelFamily,
				usable: turnFamily != "",
				match:  func(c cohortCandidate) bool { return c.family == turnFamily },
			},
			{
				id:     features.CohortRungProvider,
				usable: true,
				match:  func(cohortCandidate) bool { return true },
			},
		}
		for _, r := range rungs {
			if !r.usable {
				continue
			}
			var samples []float64
			for _, c := range pool {
				if len(samples) == recentSimilarTurnTokensLimit {
					break
				}
				if r.match(c) {
					samples = append(samples, c.tokens)
				}
			}
			if len(samples) >= minSimilarTurnSamples {
				// Sorted for the same determinism rationale as the
				// session rung below (quantiles are order-insensitive).
				sort.Float64s(samples)
				return features.SimilarTurnTokens{Samples: samples, Rung: r.id}, nil
			}
		}
	}

	samples, err := s.sessionRecentTurnTokens(ctx, q, sessionID)
	if err != nil {
		return features.SimilarTurnTokens{}, err
	}
	return features.SimilarTurnTokens{Samples: samples, Rung: features.CohortRungSession}, nil
}

// cohortCandidate is one provider-wide usage observation eligible for
// cohort matching: its total-token sample plus the identity labels it
// was stamped with at observation time. Unlabeled candidates (family/
// effort empty) still belong to the provider rung — the label absence
// excludes them from the narrower rungs only.
type cohortCandidate struct {
	tokens float64
	family string
	effort string
}

// turnCohortIdentity resolves the evaluated turn's cohort identity from
// provider_sessions (provider + latest observed model/effort — the same
// source and fail-open discipline as Service.sessionIdentity's
// prediction stamp; a lookup failure or never-observed session resolves
// to empties, never blocks the forecast). The model resolves to its
// pricing family so cohort matching operates on the same normalization
// the prediction row's model_family column persists.
func (s *SQLDataSource) turnCohortIdentity(ctx context.Context, sessionID domain.SessionID) (provider, family, effort string) {
	q := sqlite.QuerierFromContext(ctx, s.DB)
	var p string
	var m, e sql.NullString
	if err := q.QueryRowContext(ctx,
		`SELECT provider, model, effort FROM provider_sessions WHERE id = ?`,
		string(sessionID),
	).Scan(&p, &m, &e); err != nil {
		return "", "", ""
	}
	if m.Valid {
		_, family = s.pricingTable().Price(m.String)
	}
	if e.Valid {
		effort = e.String
	}
	return p, family, effort
}

// cohortCandidates fetches the provider-wide candidate pool: recent
// usage.observed events for provider (across all sessions), keeping only
// rows that actually carry a total_tokens sample, each labeled with its
// payload-stamped model family and effort.
func (s *SQLDataSource) cohortCandidates(ctx context.Context, q sqlite.Querier, provider string) ([]cohortCandidate, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT payload_json FROM events
		WHERE provider = ? AND event_type = 'provider.usage.observed'
		ORDER BY occurred_at DESC, rowid DESC LIMIT ?`,
		provider, cohortCandidatePoolLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("evaluation: RecentSimilarTurnTokens: query cohort candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var pool []cohortCandidate
	for rows.Next() {
		var payloadJSON string
		if err := rows.Scan(&payloadJSON); err != nil {
			return nil, fmt.Errorf("evaluation: RecentSimilarTurnTokens: scan cohort row: %w", err)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			return nil, fmt.Errorf("evaluation: RecentSimilarTurnTokens: decode cohort payload: %w", err)
		}
		v, ok := payload["total_tokens"]
		if !ok {
			continue
		}
		tokens, ok := toFloat64(v)
		if !ok {
			continue
		}
		c := cohortCandidate{tokens: tokens}
		if modelID, ok := payload["model_id"].(string); ok {
			_, c.family = s.pricingTable().Price(modelID)
		}
		if effort, ok := payload["effort"].(string); ok {
			c.effort = effort
		}
		pool = append(pool, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("evaluation: RecentSimilarTurnTokens: %w", err)
	}
	return pool, nil
}

// sessionRecentTurnTokens is the pre-ladder cohort, verbatim: recent
// usage.observed total-token observations for this exact session,
// regardless of identity labels.
func (s *SQLDataSource) sessionRecentTurnTokens(ctx context.Context, q sqlite.Querier, sessionID domain.SessionID) ([]float64, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT payload_json FROM events
		WHERE session_id = ? AND event_type = 'provider.usage.observed'
		ORDER BY occurred_at DESC, rowid DESC LIMIT ?`,
		string(sessionID), recentSimilarTurnTokensLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("evaluation: RecentSimilarTurnTokens: query events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []float64
	for rows.Next() {
		var payloadJSON string
		if err := rows.Scan(&payloadJSON); err != nil {
			return nil, fmt.Errorf("evaluation: RecentSimilarTurnTokens: scan event row: %w", err)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			return nil, fmt.Errorf("evaluation: RecentSimilarTurnTokens: decode payload: %w", err)
		}
		if v, ok := payload["total_tokens"]; ok {
			if f, ok := toFloat64(v); ok {
				out = append(out, f)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("evaluation: RecentSimilarTurnTokens: %w", err)
	}
	// Oldest-first, matching internal/predictor.EmpiricalQuantiles' own
	// expectation of an unordered-but-representative sample set (order
	// does not affect quantile computation, but a stable, deterministic
	// order makes this method's own tests reproducible).
	sort.Float64s(out)
	return out, nil
}

const recentSimilarTurnTokensLimit = 50

// minSimilarTurnSamples is the ladder's per-rung answer gate, mirroring
// RuleTokenForecaster.MinSimilarSamples' default (the single ADD §15.2
// "count(similar) >= 8" constant, applied here to rung SELECTION and
// there to empirical-vs-cold-start): a rung with fewer matches falls
// through to the next wider rung rather than answering with a sample
// set the forecaster would reject anyway — which would hide the wider
// rung's potentially sufficient samples behind a too-narrow one.
const minSimilarTurnSamples = 8

// cohortCandidatePoolLimit bounds the provider-wide candidate fetch: one
// recentSimilarTurnTokensLimit's worth of headroom per identity-filtered
// rung above the session fallback (exact, drop-effort, drop-model), plus
// one spare — so even when the narrowest rung's matches are diluted 4:1
// in the recent stream, a full rung answer can still be assembled from
// one query rather than tuning a bespoke constant per rung.
const cohortCandidatePoolLimit = 4 * recentSimilarTurnTokensLimit

// pricingTable mirrors Service.pricingTable's convention exactly (see
// forecastcard.go): Pricing is optional, nil falls back to the default
// table, so every existing NewSQLDataSource call site keeps compiling
// and cohort family resolution can never disagree with the prediction
// stamp's resolution by construction.
func (s *SQLDataSource) pricingTable() *pricing.Table {
	if s.Pricing != nil {
		return s.Pricing
	}
	return pricing.DefaultTable()
}

// --- 7. Quota ----------------------------------------------------------------

// Quota queries claude-provider's events table for this session's most
// recent provider.quota.observed events, one per limit_id (ADD §15.3/
// CONTRACT_FREEZE.md's Stage 3 input), decoding each payload's
// used_percent/resets_at/limit_id fields (normalizer.go's quotaEvent) into
// a real domain.QuotaObservation.
func (s *SQLDataSource) Quota(ctx context.Context, sessionID domain.SessionID) ([]domain.QuotaObservation, error) {
	q := sqlite.QuerierFromContext(ctx, s.DB)
	rows, err := q.QueryContext(ctx, `
		SELECT event_id, occurred_at, payload_json FROM events
		WHERE session_id = ? AND event_type = 'provider.quota.observed'
		ORDER BY occurred_at DESC, rowid DESC LIMIT ?`,
		string(sessionID), recentQuotaEventScanLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("evaluation: Quota: query events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	latestByLimit := make(map[string]domain.QuotaObservation)
	order := make([]string, 0, 4)
	for rows.Next() {
		var eventID, occurredAt, payloadJSON string
		if err := rows.Scan(&eventID, &occurredAt, &payloadJSON); err != nil {
			return nil, fmt.Errorf("evaluation: Quota: scan event row: %w", err)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
			return nil, fmt.Errorf("evaluation: Quota: decode payload: %w", err)
		}
		limitID := payloadString(payload, "limit_id")
		if limitID == "" {
			continue
		}
		if _, seen := latestByLimit[limitID]; seen {
			continue // rows are ordered most-recent-first; keep only the first (latest) per limit_id
		}

		observedAt, err := parseEventTime(occurredAt)
		if err != nil {
			return nil, fmt.Errorf("evaluation: Quota: parse occurred_at for event %s: %w", eventID, err)
		}

		obs := domain.QuotaObservation{
			ID:          eventID,
			SessionID:   sessionID,
			Provider:    Provider,
			LimitID:     limitID,
			UsedPercent: payloadFloatPtr(payload, "used_percent"),
			ResetsAt:    payloadTimePtr(payload, "resets_at"),
			Source:      domain.SourceStatusLine,
			Confidence:  domain.ConfidenceMedium,
			ObservedAt:  observedAt,
		}
		latestByLimit[limitID] = obs
		order = append(order, limitID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("evaluation: Quota: %w", err)
	}

	out := make([]domain.QuotaObservation, 0, len(order))
	for _, limitID := range order {
		out = append(out, latestByLimit[limitID])
	}
	return out, nil
}

const recentQuotaEventScanLimit = 20

// Provider is the fixed provider identifier this file stamps onto
// QuotaObservation.Provider when the source event itself carries no
// provider-scoped limit-owner field of its own to copy from (events.provider
// already names the producing provider at the envelope level, but
// domain.QuotaObservation.Provider is a separate field on the decoded
// observation — this mirrors what the row's own events.provider column
// would say for every event this package reads, since claude-provider is
// this codebase's only provider integration wired up so far).
const Provider = "claude"

// --- 8. Context ----------------------------------------------------------

// Context queries claude-provider's events table for this session's most
// recent provider.context.observed event, decoding its used_tokens/
// window_tokens/used_percent payload fields (normalizer.go's contextEvent)
// into a real domain.ContextObservation. Returns the zero value (not an
// error) when no such event exists yet for this session — matches
// app.ForecastQuotaRequest.Context's own "unknown is not zero" pointer-field
// discipline: every field on the returned zero-value ContextObservation is
// nil/empty, which QuotaForecaster's own cold-start handling already treats
// as "unknown," not "measured zero."
func (s *SQLDataSource) Context(ctx context.Context, sessionID domain.SessionID) (domain.ContextObservation, error) {
	q := sqlite.QuerierFromContext(ctx, s.DB)
	var eventID, occurredAt, payloadJSON string
	err := q.QueryRowContext(ctx, `
		SELECT event_id, occurred_at, payload_json FROM events
		WHERE session_id = ? AND event_type = 'provider.context.observed'
		ORDER BY occurred_at DESC, rowid DESC LIMIT 1`,
		string(sessionID),
	).Scan(&eventID, &occurredAt, &payloadJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ContextObservation{}, nil
	}
	if err != nil {
		return domain.ContextObservation{}, fmt.Errorf("evaluation: Context: query events: %w", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return domain.ContextObservation{}, fmt.Errorf("evaluation: Context: decode payload: %w", err)
	}

	observedAt, err := parseEventTime(occurredAt)
	if err != nil {
		return domain.ContextObservation{}, fmt.Errorf("evaluation: Context: parse occurred_at for event %s: %w", eventID, err)
	}

	return domain.ContextObservation{
		ID:           eventID,
		SessionID:    sessionID,
		UsedTokens:   payloadInt64Ptr(payload, "used_tokens"),
		WindowTokens: payloadInt64Ptr(payload, "window_tokens"),
		UsedPercent:  payloadFloatPtr(payload, "used_percent"),
		Source:       domain.SourceStatusLine,
		Confidence:   domain.ConfidenceMedium,
		ObservedAt:   observedAt,
	}, nil
}

// --- 9. RunwayForecast -----------------------------------------------------

// RunwayForecast queries this package's own runway_forecasts table
// (migration 0042) for the most recent row bound to sessionID. ok=false
// when no row exists — exactly per the DataSource interface's own doc
// comment ("a brand-new session, or GracefulPauseService.Observe has not
// run"). As of this corrective addition, no production code path persists
// a runway_forecasts row yet (internal/pause.Service.Observe computes a
// domain.RunwayForecast via runway.Scorer.Score and returns it directly to
// its caller, but does not itself write it to this table) — this query is
// nonetheless real and correct against the frozen schema; it activates
// automatically, with no further change to this file, the moment a future
// wave wires persistence for that table (out of this role's exclusive path
// to add, per Constitution §4).
func (s *SQLDataSource) RunwayForecast(ctx context.Context, sessionID domain.SessionID) (domain.RunwayForecast, bool, error) {
	// Note: runway_forecasts (migration 0042) has no sample_count column,
	// so domain.RunwayForecast.SampleCount is left at its zero value below
	// — honest "not persisted here," not a fabricated count. Every other
	// field below has a real, directly corresponding column.
	q := sqlite.QuerierFromContext(ctx, s.DB)
	var (
		limitID                                       string
		horizonSeconds                                int64
		hitProbability, riskScore, currentUsedPercent sql.NullFloat64
		burnRateP50, burnRateP90                      sql.NullFloat64
		calibratedInt                                 int64
		confidence                                    string
		estTTLp50, estTTLp90                          sql.NullInt64
		reasonCodesJSON                               string
	)
	err := q.QueryRowContext(ctx, `
		SELECT limit_id, horizon_seconds, hit_probability, risk_score, calibrated,
		       confidence, current_used_percent, burn_rate_p50, burn_rate_p90,
		       estimated_time_to_limit_p50_seconds, estimated_time_to_limit_p90_seconds,
		       reason_codes_json
		FROM runway_forecasts
		WHERE session_id = ?
		ORDER BY created_at DESC, rowid DESC LIMIT 1`,
		string(sessionID),
	).Scan(
		&limitID, &horizonSeconds, &hitProbability, &riskScore, &calibratedInt,
		&confidence, &currentUsedPercent, &burnRateP50, &burnRateP90,
		&estTTLp50, &estTTLp90, &reasonCodesJSON,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RunwayForecast{}, false, nil
	}
	if err != nil {
		return domain.RunwayForecast{}, false, fmt.Errorf("evaluation: RunwayForecast: query runway_forecasts: %w", err)
	}

	var reasonCodes []string
	if reasonCodesJSON != "" {
		if err := json.Unmarshal([]byte(reasonCodesJSON), &reasonCodes); err != nil {
			return domain.RunwayForecast{}, false, fmt.Errorf("evaluation: RunwayForecast: decode reason_codes_json: %w", err)
		}
	}

	forecast := domain.RunwayForecast{
		LimitID:        limitID,
		HorizonSeconds: horizonSeconds,
		RiskScore:      riskScore.Float64,
		Calibrated:     calibratedInt != 0,
		Confidence:     domain.Confidence(confidence),
		ReasonCodes:    reasonCodes,
	}
	if hitProbability.Valid {
		v := hitProbability.Float64
		forecast.HitProbability = &v
	}
	if currentUsedPercent.Valid {
		v := currentUsedPercent.Float64
		forecast.CurrentUsedPercent = &v
	}
	if burnRateP50.Valid {
		v := burnRateP50.Float64
		forecast.BurnRateP50 = &v
	}
	if burnRateP90.Valid {
		v := burnRateP90.Float64
		forecast.BurnRateP90 = &v
	}
	if estTTLp50.Valid {
		v := estTTLp50.Int64
		forecast.EstimatedTimeToLimitP50Seconds = &v
	}
	if estTTLp90.Valid {
		v := estTTLp90.Int64
		forecast.EstimatedTimeToLimitP90Seconds = &v
	}

	return forecast, true, nil
}

// --- 10. PriorRunwayHitConfirmed --------------------------------------------

// PriorRunwayHitConfirmed queries this package's own policy_decisions table
// (migration 0043) for whether the immediately preceding decision for this
// session already saw a qualifying calibrated hit-probability. Per
// internal/policy/decide.go's runwayPauseDecision (the single authoritative
// definition of this bit's semantics), a decision "qualifies" when its
// PolicyReasonCodes contains either "runway_hit_probability_armed_pending_confirmation"
// (the first qualifying observation, WARN) or
// "runway_hit_probability_confirmed_twice" (the second, PAUSE) — those are
// the only two reason codes runwayPauseDecision's calibrated branch ever
// emits, and both represent "this decision's Runway input was calibrated
// with HitProbability >= threshold." This method finds the session's most
// recent policy_decisions row (across all of that session's turns, joining
// through predictions.turn_id -> events to recover session scoping, since
// policy_decisions/predictions carry no direct session_id column of their
// own) and reports whether ITS reason codes contain either marker string.
//
// Session scoping note: predictions/policy_decisions are keyed by turn_id,
// not session_id (0041/0043's own schema) — this package's own tables were
// deliberately turn-scoped, not session-scoped, since a "turn" is the unit
// EvaluateTurn operates on. To find "the immediately preceding decision for
// this session," this method joins through events (session_id -> turn_id is
// carried on claude-provider's provider.turn.started rows) to recover which
// turn_ids belong to sessionID, then finds the most recent policy_decisions
// row among predictions for those turn_ids. A session with no events yet
// (and therefore no known turn_ids) honestly returns false, not an error —
// exactly the "not confirmed yet" cold-start answer this bit's own zero
// value already means.
func (s *SQLDataSource) PriorRunwayHitConfirmed(ctx context.Context, sessionID domain.SessionID) (bool, error) {
	q := sqlite.QuerierFromContext(ctx, s.DB)

	var reasonCodesJSON string
	err := q.QueryRowContext(ctx, `
		SELECT pd.reason_codes_json
		FROM policy_decisions pd
		JOIN predictions p ON p.id = pd.prediction_id
		JOIN events e ON e.turn_id = p.turn_id AND e.session_id = ?
		ORDER BY pd.decided_at DESC, pd.rowid DESC LIMIT 1`,
		string(sessionID),
	).Scan(&reasonCodesJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("evaluation: PriorRunwayHitConfirmed: query policy_decisions: %w", err)
	}

	var codes []string
	if reasonCodesJSON != "" {
		if err := json.Unmarshal([]byte(reasonCodesJSON), &codes); err != nil {
			return false, fmt.Errorf("evaluation: PriorRunwayHitConfirmed: decode reason_codes_json: %w", err)
		}
	}
	for _, c := range codes {
		if c == runwayHitProbabilityArmedReason || c == runwayHitProbabilityConfirmedReason {
			return true, nil
		}
	}
	return false, nil
}

// runwayHitProbabilityArmedReason/ConfirmedReason mirror
// internal/policy/decide.go's runwayPauseDecision literal reason-code
// strings exactly (that file's own comments name these as its sole two
// producers of a calibrated Decision.Probability). Declared as unexported
// constants in this file rather than imported from internal/policy, since
// internal/policy has no exported constant for them today (they are
// written as inline literals in decide.go) and this file must not add an
// export to another path this corrective addition is not scoped to widen —
// duplicating the two literal strings here, with an explicit doc-comment
// cross-reference, is the documented, honest choice.
const (
	runwayHitProbabilityArmedReason     = "runway_hit_probability_armed_pending_confirmation"
	runwayHitProbabilityConfirmedReason = "runway_hit_probability_confirmed_twice"
)

// --- payload decode helpers -------------------------------------------------

func payloadString(payload map[string]any, key string) string {
	v, ok := payload[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// payloadBool decodes a boolean payload field. An absent key or a
// non-boolean value returns false — for the issue-#42 derived feature
// fields that is the honest zero value for signal that was never captured
// (events persisted before those fields existed), matching the
// pre-#42 behavior exactly.
func payloadBool(payload map[string]any, key string) bool {
	v, ok := payload[key]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

func payloadInt(payload map[string]any, key string) int {
	v, ok := payload[key]
	if !ok {
		return 0
	}
	f, ok := toFloat64(v)
	if !ok {
		return 0
	}
	return int(f)
}

func payloadFloatPtr(payload map[string]any, key string) *float64 {
	v, ok := payload[key]
	if !ok {
		return nil
	}
	f, ok := toFloat64(v)
	if !ok {
		return nil
	}
	return &f
}

func payloadInt64Ptr(payload map[string]any, key string) *int64 {
	v, ok := payload[key]
	if !ok {
		return nil
	}
	f, ok := toFloat64(v)
	if !ok {
		return nil
	}
	i := int64(f)
	return &i
}

func payloadTimePtr(payload map[string]any, key string) *time.Time {
	s := payloadString(payload, key)
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return nil
	}
	return &t
}

// toFloat64 handles the fact that json.Unmarshal into map[string]any
// always decodes a JSON number as float64, regardless of whether the
// original Go value marshaled into it was an int/int64/float64 — this
// package's own event payloads (normalizer.go) marshal a mix of all three,
// so every numeric payload read in this file goes through this helper
// rather than a direct type assertion to a specific numeric type.
func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func parseEventTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}
