// datasource_sql_test.go: integration tests for SQLDataSource
// (datasource_sql.go), this package's real, storage-backed DataSource
// implementation (Final-integration-gate corrective addition). Every test
// runs against a real, migrated SQLite DB (openMigratedDB, helpers_test.go)
// with realistic seeded rows across the tables SQLDataSource reads —
// repositories/worktrees/provider_sessions/tasks (foundation),
// events (claude-provider), progress_nodes/progress_edges (checkpoint), and
// predictions/policy_decisions/runway_forecasts (this package's own) —
// mirroring this role's established "no fabrication, cold-start is a valid
// answer" testing discipline (predictor-05 through predictor-11): every
// method is proven to return correct real data when the backing rows exist,
// and an honest ok=false/cold-start/zero-value when they don't.
package evaluation_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/evaluation"
	"github.com/huaiche94/auspex/internal/features"
	"github.com/huaiche94/auspex/internal/policy"
	"github.com/huaiche94/auspex/internal/predictor/quota"
	"github.com/huaiche94/auspex/internal/predictor/risk"
	"github.com/huaiche94/auspex/internal/predictor/scope"
	"github.com/huaiche94/auspex/internal/predictor/token"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// --- compile-time interface assertion --------------------------------------

var _ evaluation.DataSource = (*evaluation.SQLDataSource)(nil)

// --- seeding helpers ---------------------------------------------------------

type seededIDs struct {
	repositoryID string
	worktreeID   string
	sessionID    string
	taskID       string
}

// seedRepoWorktreeSessionTask inserts one row each into repositories,
// worktrees, provider_sessions, and tasks — the minimal chain Resolve walks.
func seedRepoWorktreeSessionTask(t *testing.T, db *sqlite.DB) seededIDs {
	t.Helper()
	ctx := context.Background()
	ids := seededIDs{
		repositoryID: "repo-1",
		worktreeID:   "worktree-1",
		sessionID:    "session-1",
		taskID:       "task-1",
	}
	exec(t, db, `INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at) VALUES (?, ?, ?, ?, ?)`,
		ids.repositoryID, "/repo", "/repo/.git", "2026-07-12T00:00:00Z", "2026-07-12T00:00:00Z")
	exec(t, db, `INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at) VALUES (?, ?, ?, ?, ?, ?)`,
		ids.worktreeID, ids.repositoryID, "/repo", "/repo/.git", "2026-07-12T00:00:00Z", "2026-07-12T00:00:00Z")
	exec(t, db, `INSERT INTO provider_sessions (id, worktree_id, provider, invocation_mode, started_at) VALUES (?, ?, ?, ?, ?)`,
		ids.sessionID, ids.worktreeID, "claude", "interactive", "2026-07-12T00:00:00Z")
	exec(t, db, `INSERT INTO tasks (id, session_id, worktree_id, objective_hash, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ids.taskID, ids.sessionID, ids.worktreeID, "objhash1", "in_progress", "2026-07-12T00:00:00Z", "2026-07-12T00:00:00Z")
	_ = ctx
	return ids
}

func exec(t *testing.T, db *sqlite.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Conn().ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("seed exec %q: %v", query, err)
	}
}

func insertEvent(t *testing.T, db *sqlite.DB, eventID, sessionID, eventType, occurredAt string, payload map[string]any) {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	exec(t, db, `
		INSERT INTO events (event_id, schema_version, event_type, occurred_at, observed_at, source, session_id, payload_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		eventID, "auspex.event.v1", eventType, occurredAt, occurredAt, "hook", sessionID, string(b),
	)
}

// --- Resolve -----------------------------------------------------------------

func TestSQLDataSource_Resolve_RealData(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	src := evaluation.NewSQLDataSource(db)

	resolved, err := src.Resolve(context.Background(), domain.SessionID(ids.sessionID))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(resolved.RepositoryID) != ids.repositoryID {
		t.Errorf("RepositoryID = %q, want %q", resolved.RepositoryID, ids.repositoryID)
	}
	if resolved.TaskID == nil || string(*resolved.TaskID) != ids.taskID {
		t.Errorf("TaskID = %v, want %q", resolved.TaskID, ids.taskID)
	}
}

func TestSQLDataSource_Resolve_NoTaskYet(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	// Remove the task: a session may resolve to a repository with no task yet.
	exec(t, db, `DELETE FROM tasks WHERE id = ?`, ids.taskID)
	src := evaluation.NewSQLDataSource(db)

	resolved, err := src.Resolve(context.Background(), domain.SessionID(ids.sessionID))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(resolved.RepositoryID) != ids.repositoryID {
		t.Errorf("RepositoryID = %q, want %q", resolved.RepositoryID, ids.repositoryID)
	}
	if resolved.TaskID != nil {
		t.Errorf("TaskID = %v, want nil (cold-start)", resolved.TaskID)
	}
}

func TestSQLDataSource_Resolve_PrefersTaskBoundToSession(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	// Another, unrelated session in the same worktree with a MORE RECENT task
	// that is NOT bound to our session — Resolve must still prefer the task
	// bound to our own session over a more-recently-created worktree-wide task.
	exec(t, db, `INSERT INTO provider_sessions (id, worktree_id, provider, invocation_mode, started_at) VALUES (?, ?, ?, ?, ?)`,
		"session-2", ids.worktreeID, "claude", "interactive", "2026-07-12T00:00:01Z")
	exec(t, db, `INSERT INTO tasks (id, session_id, worktree_id, objective_hash, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"task-2", "session-2", ids.worktreeID, "objhash2", "in_progress", "2026-07-12T00:10:00Z", "2026-07-12T00:10:00Z")

	src := evaluation.NewSQLDataSource(db)
	resolved, err := src.Resolve(context.Background(), domain.SessionID(ids.sessionID))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.TaskID == nil || string(*resolved.TaskID) != ids.taskID {
		t.Errorf("TaskID = %v, want %q (session-bound task, not the more recent worktree-wide one)", resolved.TaskID, ids.taskID)
	}
}

func TestSQLDataSource_Resolve_FallsBackToMostRecentWorktreeTask(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	// A new session in the same worktree that has not created its own task
	// yet: Resolve should fall back to the most recent worktree-wide task.
	exec(t, db, `INSERT INTO provider_sessions (id, worktree_id, provider, invocation_mode, started_at) VALUES (?, ?, ?, ?, ?)`,
		"session-continuer", ids.worktreeID, "claude", "interactive", "2026-07-12T01:00:00Z")

	src := evaluation.NewSQLDataSource(db)
	resolved, err := src.Resolve(context.Background(), domain.SessionID("session-continuer"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.TaskID == nil || string(*resolved.TaskID) != ids.taskID {
		t.Errorf("TaskID = %v, want fallback %q", resolved.TaskID, ids.taskID)
	}
}

func TestSQLDataSource_Resolve_UnknownSessionIsNotFound(t *testing.T) {
	db := openMigratedDB(t)
	src := evaluation.NewSQLDataSource(db)

	_, err := src.Resolve(context.Background(), domain.SessionID("does-not-exist"))
	_ = requireDomainError(t, err, domain.ErrCodeNotFound)
}

func TestSQLDataSource_Resolve_EmptySessionIDIsValidationError(t *testing.T) {
	db := openMigratedDB(t)
	src := evaluation.NewSQLDataSource(db)

	_, err := src.Resolve(context.Background(), domain.SessionID(""))
	_ = requireDomainError(t, err, domain.ErrCodeValidation)
}

// --- Classification ------------------------------------------------------

func TestSQLDataSource_Classification_NoEventsIsColdStartUnknown(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	src := evaluation.NewSQLDataSource(db)

	class, pf, err := src.Classification(context.Background(), domain.SessionID(ids.sessionID), nil)
	if err != nil {
		t.Fatalf("Classification: %v", err)
	}
	if class.Class != features.TaskClassUnknown {
		t.Errorf("Class = %q, want unknown (cold-start)", class.Class)
	}
	if class.Confidence != domain.ConfidenceUnavailable {
		t.Errorf("Confidence = %q, want unavailable", class.Confidence)
	}
	if pf.ApproxTokens != 0 {
		t.Errorf("ApproxTokens = %d, want 0 (no event yet)", pf.ApproxTokens)
	}
}

func TestSQLDataSource_Classification_RealSizeOnlySignal_NeverFabricatesVerbSignal(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	insertEvent(t, db, "ev-1", ids.sessionID, "provider.turn.started", "2026-07-12T00:05:00Z", map[string]any{
		"prompt_sha256":        "abc123",
		"prompt_byte_length":   500,
		"prompt_approx_tokens": 120,
	})
	src := evaluation.NewSQLDataSource(db)

	class, pf, err := src.Classification(context.Background(), domain.SessionID(ids.sessionID), nil)
	if err != nil {
		t.Fatalf("Classification: %v", err)
	}
	if pf.SHA256Hex != "abc123" {
		t.Errorf("SHA256Hex = %q, want abc123", pf.SHA256Hex)
	}
	if pf.ByteLength != 500 {
		t.Errorf("ByteLength = %d, want 500", pf.ByteLength)
	}
	if pf.ApproxTokens != 120 {
		t.Errorf("ApproxTokens = %d, want 120", pf.ApproxTokens)
	}
	// No verb/indicator signal was ever available (raw text never
	// persisted) — every such boolean must be false, never fabricated.
	if pf.HasFixVerb || pf.HasImplementVerb || pf.HasRefactorVerb || pf.HasInvestigateVerb || pf.HasMigrateVerb {
		t.Errorf("a verb flag was set from size-only signal: %+v", pf)
	}
	if pf.MentionsTests || pf.MentionsSecurity || pf.MentionsDocumentation {
		t.Errorf("a domain-indicator flag was set from size-only signal: %+v", pf)
	}
	// With no verb/indicator signal, ClassifyTask's own real logic
	// legitimately returns Unknown — proving this bridge invokes the real
	// classifier rather than fabricating a class.
	if class.Class != features.TaskClassUnknown {
		t.Errorf("Class = %q, want unknown (no verb/indicator signal available)", class.Class)
	}
}

func TestSQLDataSource_Classification_UsesMostRecentEvent(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	insertEvent(t, db, "ev-1", ids.sessionID, "provider.turn.started", "2026-07-12T00:05:00Z", map[string]any{
		"prompt_sha256": "old", "prompt_byte_length": 10, "prompt_approx_tokens": 3,
	})
	insertEvent(t, db, "ev-2", ids.sessionID, "provider.turn.started", "2026-07-12T00:06:00Z", map[string]any{
		"prompt_sha256": "new", "prompt_byte_length": 20, "prompt_approx_tokens": 5,
	})
	src := evaluation.NewSQLDataSource(db)

	_, pf, err := src.Classification(context.Background(), domain.SessionID(ids.sessionID), nil)
	if err != nil {
		t.Fatalf("Classification: %v", err)
	}
	if pf.SHA256Hex != "new" {
		t.Errorf("SHA256Hex = %q, want the most recent event's hash (new)", pf.SHA256Hex)
	}
}

// --- Repository / Session: always honest cold-start -------------------------

func TestSQLDataSource_Repository_AlwaysColdStart(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	src := evaluation.NewSQLDataSource(db)

	feat, ok, err := src.Repository(context.Background(), domain.RepositoryID(ids.repositoryID))
	if err != nil {
		t.Fatalf("Repository: %v", err)
	}
	if ok {
		t.Errorf("ok = true, want false (no repository-content signal is backed by this schema)")
	}
	if feat != (features.RepositoryFeatures{}) {
		t.Errorf("feat = %+v, want zero value", feat)
	}
}

func TestSQLDataSource_Session_AlwaysColdStart(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	src := evaluation.NewSQLDataSource(db)

	feat, ok, err := src.Session(context.Background(), domain.SessionID(ids.sessionID))
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if ok {
		t.Errorf("ok = true, want false (no session-quantile signal is backed by this schema)")
	}
	if feat != (features.SessionFeatures{}) {
		t.Errorf("feat = %+v, want zero value", feat)
	}
}

// --- Progress ------------------------------------------------------------

func TestSQLDataSource_Progress_NilTaskIsColdStart(t *testing.T) {
	db := openMigratedDB(t)
	src := evaluation.NewSQLDataSource(db)

	feat, ok, err := src.Progress(context.Background(), nil)
	if err != nil {
		t.Fatalf("Progress: %v", err)
	}
	if ok {
		t.Errorf("ok = true, want false for nil taskID")
	}
	if feat != (features.ProgressFeatures{}) {
		t.Errorf("feat = %+v, want zero value", feat)
	}
}

func TestSQLDataSource_Progress_NoNodesIsColdStart(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	src := evaluation.NewSQLDataSource(db)

	taskID := domain.TaskID(ids.taskID)
	_, ok, err := src.Progress(context.Background(), &taskID)
	if err != nil {
		t.Fatalf("Progress: %v", err)
	}
	if ok {
		t.Errorf("ok = true, want false for a task with no progress_nodes rows yet")
	}
}

func seedNode(t *testing.T, db *sqlite.DB, id, taskID string, parentID *string, ordinal int64, kind, status, updatedAt string) {
	t.Helper()
	exec(t, db, `
		INSERT INTO progress_nodes (id, task_id, parent_id, ordinal, kind, title, status, version, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, taskID, nullable(parentID), ordinal, kind, "title-"+id, status, 1, updatedAt,
	)
}

func nullable(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func TestSQLDataSource_Progress_RealNodesAndEdges(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)

	seedNode(t, db, "node-1", ids.taskID, nil, 0, string(domain.NodeCodeChange), string(domain.NodeCompleted), "2026-07-12T00:01:00Z")
	seedNode(t, db, "node-2", ids.taskID, nil, 1, string(domain.NodeCodeChange), string(domain.NodeInProgress), "2026-07-12T00:02:00Z")
	seedNode(t, db, "node-3", ids.taskID, nil, 2, string(domain.NodeTest), string(domain.NodePending), "2026-07-12T00:00:30Z")
	// node-2 depends_on node-3, which is not yet completed: an unresolved blocker.
	exec(t, db, `INSERT INTO progress_edges (task_id, from_node_id, to_node_id, edge_kind) VALUES (?, ?, ?, ?)`,
		ids.taskID, "node-2", "node-3", "depends_on")

	src := evaluation.NewSQLDataSource(db)
	taskID := domain.TaskID(ids.taskID)
	feat, ok, err := src.Progress(context.Background(), &taskID)
	if err != nil {
		t.Fatalf("Progress: %v", err)
	}
	if !ok {
		t.Fatalf("ok = false, want true (real progress_nodes/progress_edges rows exist)")
	}
	if feat.NodeID == nil || string(*feat.NodeID) != "node-2" {
		t.Errorf("NodeID = %v, want node-2 (most recently updated non-terminal node)", feat.NodeID)
	}
	if feat.CurrentNodeKind != domain.NodeCodeChange {
		t.Errorf("CurrentNodeKind = %q, want code_change", feat.CurrentNodeKind)
	}
	if feat.CompletedRatio == nil || *feat.CompletedRatio != 1.0/3.0 {
		t.Errorf("CompletedRatio = %v, want 1/3 (1 of 3 nodes completed)", feat.CompletedRatio)
	}
	if feat.UnresolvedBlockers != 1 {
		t.Errorf("UnresolvedBlockers = %d, want 1 (node-2 depends_on pending node-3)", feat.UnresolvedBlockers)
	}
	if feat.IsDocumentSection {
		t.Errorf("IsDocumentSection = true, want false (current node kind is code_change)")
	}
}

func TestSQLDataSource_Progress_AllNodesTerminal_FallsBackToMostRecentlyUpdated(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	seedNode(t, db, "node-1", ids.taskID, nil, 0, string(domain.NodeCodeChange), string(domain.NodeCompleted), "2026-07-12T00:01:00Z")
	seedNode(t, db, "node-2", ids.taskID, nil, 1, string(domain.NodeDocumentSection), string(domain.NodeCompleted), "2026-07-12T00:02:00Z")

	src := evaluation.NewSQLDataSource(db)
	taskID := domain.TaskID(ids.taskID)
	feat, ok, err := src.Progress(context.Background(), &taskID)
	if err != nil {
		t.Fatalf("Progress: %v", err)
	}
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if feat.NodeID == nil || string(*feat.NodeID) != "node-2" {
		t.Errorf("NodeID = %v, want node-2 (most recently updated, even though all nodes are terminal)", feat.NodeID)
	}
	if feat.CompletedRatio == nil || *feat.CompletedRatio != 1.0 {
		t.Errorf("CompletedRatio = %v, want 1.0", feat.CompletedRatio)
	}
}

// --- RecentSimilarTurnTokens ------------------------------------------------

func TestSQLDataSource_RecentSimilarTurnTokens_NoUsageEventsIsEmpty(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	src := evaluation.NewSQLDataSource(db)

	similar, err := src.RecentSimilarTurnTokens(context.Background(), domain.SessionID(ids.sessionID), features.TaskClassBugfixLocal)
	if err != nil {
		t.Fatalf("RecentSimilarTurnTokens: %v", err)
	}
	if len(similar.Samples) != 0 {
		t.Errorf("samples = %v, want empty (no provider.usage.observed events carry total_tokens today)", similar.Samples)
	}
	if similar.Rung != features.CohortRungSession {
		t.Errorf("rung = %q, want %q (no identity-labeled rung can answer with zero samples)", similar.Rung, features.CohortRungSession)
	}
}

func TestSQLDataSource_RecentSimilarTurnTokens_RealTotalTokensField(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	insertEvent(t, db, "ev-1", ids.sessionID, "provider.usage.observed", "2026-07-12T00:01:00Z", map[string]any{"total_tokens": 4000.0})
	insertEvent(t, db, "ev-2", ids.sessionID, "provider.usage.observed", "2026-07-12T00:02:00Z", map[string]any{"total_tokens": 6000.0})
	src := evaluation.NewSQLDataSource(db)

	similar, err := src.RecentSimilarTurnTokens(context.Background(), domain.SessionID(ids.sessionID), features.TaskClassBugfixLocal)
	if err != nil {
		t.Fatalf("RecentSimilarTurnTokens: %v", err)
	}
	if len(similar.Samples) != 2 {
		t.Fatalf("len(samples) = %d, want 2", len(similar.Samples))
	}
	if similar.Samples[0] != 4000.0 || similar.Samples[1] != 6000.0 {
		t.Errorf("samples = %v, want [4000 6000] sorted ascending", similar.Samples)
	}
	if similar.Rung != features.CohortRungSession {
		t.Errorf("rung = %q, want %q (2 samples is below the ladder gate, so the session fallback answers)", similar.Rung, features.CohortRungSession)
	}
}

// setSessionIdentity stamps the seeded session's latest observed identity
// (the ladder's turn-side labels — what statusline ingest's COALESCE
// upsert maintains in production).
func setSessionIdentity(t *testing.T, db *sqlite.DB, sessionID, model, effort string) {
	t.Helper()
	exec(t, db, `UPDATE provider_sessions SET model = ?, effort = ? WHERE id = ?`, model, effort, sessionID)
}

// insertLabeledUsageEvent inserts a provider.usage.observed event the way
// the post-Phase-1 normalizer emits it: events.provider populated and the
// payload carrying identity labels alongside the (future) total_tokens
// sample. sessionID is deliberately arbitrary — cohort candidates span
// sessions.
func insertLabeledUsageEvent(t *testing.T, db *sqlite.DB, eventID, sessionID, occurredAt string, tokens float64, modelID, effort string) {
	t.Helper()
	payload := map[string]any{"total_tokens": tokens}
	if modelID != "" {
		payload["model_id"] = modelID
	}
	if effort != "" {
		payload["effort"] = effort
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	exec(t, db, `
		INSERT INTO events (event_id, schema_version, event_type, occurred_at, observed_at, source, provider, session_id, payload_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		eventID, "auspex.event.v1", "provider.usage.observed", occurredAt, occurredAt, "statusline", "claude", sessionID, string(b),
	)
}

// seedCohortLadderFixture writes a provider-wide sample population with
// three identity strata (numbers chosen so each ladder rung has a
// distinct, gate-crossing answer):
//   - 8 × (fable, high)   → 100k tokens each: the exact cohort
//   - 8 × (fable, low)    → 200k tokens each: family matches, effort doesn't
//   - 8 × (haiku, high)   → 300k tokens each: provider matches, family doesn't
func seedCohortLadderFixture(t *testing.T, db *sqlite.DB) {
	t.Helper()
	for i := 0; i < 8; i++ {
		ts := fmt.Sprintf("2026-07-12T01:%02d:00Z", i)
		insertLabeledUsageEvent(t, db, fmt.Sprintf("ev-exact-%d", i), "other-session-a", ts, 100_000, "claude-fable-5", "high")
		insertLabeledUsageEvent(t, db, fmt.Sprintf("ev-family-%d", i), "other-session-b", ts, 200_000, "claude-fable-5", "low")
		insertLabeledUsageEvent(t, db, fmt.Sprintf("ev-provider-%d", i), "other-session-c", ts, 300_000, "claude-haiku-4-5", "high")
	}
}

func TestSQLDataSource_RecentSimilarTurnTokens_ExactCohortRungAnswers(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	setSessionIdentity(t, db, ids.sessionID, "claude-fable-5", "high")
	seedCohortLadderFixture(t, db)
	src := evaluation.NewSQLDataSource(db)

	similar, err := src.RecentSimilarTurnTokens(context.Background(), domain.SessionID(ids.sessionID), features.TaskClassBugfixLocal)
	if err != nil {
		t.Fatalf("RecentSimilarTurnTokens: %v", err)
	}
	if similar.Rung != features.CohortRungModelEffort {
		t.Fatalf("rung = %q, want %q", similar.Rung, features.CohortRungModelEffort)
	}
	if len(similar.Samples) != 8 {
		t.Fatalf("len(samples) = %d, want exactly the 8 exact-cohort samples", len(similar.Samples))
	}
	for _, s := range similar.Samples {
		if s != 100_000 {
			t.Fatalf("samples = %v: a non-exact-cohort sample (family or effort mismatch) leaked into the exact rung", similar.Samples)
		}
	}
}

func TestSQLDataSource_RecentSimilarTurnTokens_DropEffortRung(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	// Turn ran at an effort no sample matches: the exact rung starves,
	// drop-effort answers from BOTH fable strata (16 samples).
	setSessionIdentity(t, db, ids.sessionID, "claude-fable-5", "max")
	seedCohortLadderFixture(t, db)
	src := evaluation.NewSQLDataSource(db)

	similar, err := src.RecentSimilarTurnTokens(context.Background(), domain.SessionID(ids.sessionID), features.TaskClassBugfixLocal)
	if err != nil {
		t.Fatalf("RecentSimilarTurnTokens: %v", err)
	}
	if similar.Rung != features.CohortRungModelFamily {
		t.Fatalf("rung = %q, want %q", similar.Rung, features.CohortRungModelFamily)
	}
	if len(similar.Samples) != 16 {
		t.Fatalf("len(samples) = %d, want 16 (both fable strata, no haiku)", len(similar.Samples))
	}
	for _, s := range similar.Samples {
		if s != 100_000 && s != 200_000 {
			t.Fatalf("samples = %v: a family-mismatched sample leaked into the drop-effort rung", similar.Samples)
		}
	}
}

func TestSQLDataSource_RecentSimilarTurnTokens_DropModelRung(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	// Turn ran on a family no sample matches: both model rungs starve,
	// the provider rung answers with everything.
	setSessionIdentity(t, db, ids.sessionID, "claude-opus-4-8", "high")
	seedCohortLadderFixture(t, db)
	src := evaluation.NewSQLDataSource(db)

	similar, err := src.RecentSimilarTurnTokens(context.Background(), domain.SessionID(ids.sessionID), features.TaskClassBugfixLocal)
	if err != nil {
		t.Fatalf("RecentSimilarTurnTokens: %v", err)
	}
	if similar.Rung != features.CohortRungProvider {
		t.Fatalf("rung = %q, want %q", similar.Rung, features.CohortRungProvider)
	}
	if len(similar.Samples) != 24 {
		t.Fatalf("len(samples) = %d, want all 24 provider-wide samples", len(similar.Samples))
	}
}

func TestSQLDataSource_RecentSimilarTurnTokens_UnlabeledTurnSkipsIdentityRungs(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	// No setSessionIdentity: the turn's model/effort were never observed.
	// Unknown is not zero — the identity rungs must be skipped, not
	// matched-as-empty, and the provider rung still answers.
	seedCohortLadderFixture(t, db)
	src := evaluation.NewSQLDataSource(db)

	similar, err := src.RecentSimilarTurnTokens(context.Background(), domain.SessionID(ids.sessionID), features.TaskClassBugfixLocal)
	if err != nil {
		t.Fatalf("RecentSimilarTurnTokens: %v", err)
	}
	if similar.Rung != features.CohortRungProvider {
		t.Fatalf("rung = %q, want %q", similar.Rung, features.CohortRungProvider)
	}
	if len(similar.Samples) != 24 {
		t.Fatalf("len(samples) = %d, want all 24 provider-wide samples", len(similar.Samples))
	}
}

// --- Quota -----------------------------------------------------------------

func TestSQLDataSource_Quota_NoEventsIsEmpty(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	src := evaluation.NewSQLDataSource(db)

	obs, err := src.Quota(context.Background(), domain.SessionID(ids.sessionID))
	if err != nil {
		t.Fatalf("Quota: %v", err)
	}
	if len(obs) != 0 {
		t.Errorf("obs = %v, want empty", obs)
	}
}

func TestSQLDataSource_Quota_RealMultiWindowLatestOnly(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	insertEvent(t, db, "ev-1", ids.sessionID, "provider.quota.observed", "2026-07-12T00:01:00Z", map[string]any{
		"limit_id": "five_hour", "used_percent": 40.0,
	})
	// A later observation for the SAME limit_id: only the latest should win.
	insertEvent(t, db, "ev-2", ids.sessionID, "provider.quota.observed", "2026-07-12T00:05:00Z", map[string]any{
		"limit_id": "five_hour", "used_percent": 55.0, "resets_at": "2026-07-12T05:00:00Z",
	})
	insertEvent(t, db, "ev-3", ids.sessionID, "provider.quota.observed", "2026-07-12T00:03:00Z", map[string]any{
		"limit_id": "seven_day", "used_percent": 10.0,
	})
	src := evaluation.NewSQLDataSource(db)

	obs, err := src.Quota(context.Background(), domain.SessionID(ids.sessionID))
	if err != nil {
		t.Fatalf("Quota: %v", err)
	}
	if len(obs) != 2 {
		t.Fatalf("len(obs) = %d, want 2 (one per limit_id)", len(obs))
	}
	byLimit := map[string]domain.QuotaObservation{}
	for _, o := range obs {
		byLimit[o.LimitID] = o
	}
	fiveHour, ok := byLimit["five_hour"]
	if !ok {
		t.Fatalf("no five_hour observation in %v", obs)
	}
	if fiveHour.UsedPercent == nil || *fiveHour.UsedPercent != 55.0 {
		t.Errorf("five_hour UsedPercent = %v, want 55.0 (the latest observation, not 40.0)", fiveHour.UsedPercent)
	}
	if fiveHour.ResetsAt == nil {
		t.Errorf("five_hour ResetsAt = nil, want parsed resets_at")
	}
	sevenDay, ok := byLimit["seven_day"]
	if !ok {
		t.Fatalf("no seven_day observation in %v", obs)
	}
	if sevenDay.UsedPercent == nil || *sevenDay.UsedPercent != 10.0 {
		t.Errorf("seven_day UsedPercent = %v, want 10.0", sevenDay.UsedPercent)
	}
}

// --- Context -----------------------------------------------------------------

func TestSQLDataSource_Context_NoEventIsZeroValue(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	src := evaluation.NewSQLDataSource(db)

	obs, err := src.Context(context.Background(), domain.SessionID(ids.sessionID))
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	if obs.UsedTokens != nil || obs.WindowTokens != nil || obs.UsedPercent != nil {
		t.Errorf("obs = %+v, want every pointer field nil (unknown, not zero)", obs)
	}
}

func TestSQLDataSource_Context_RealMostRecentEvent(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	insertEvent(t, db, "ev-1", ids.sessionID, "provider.context.observed", "2026-07-12T00:01:00Z", map[string]any{
		"used_tokens": 1000.0, "window_tokens": 200000.0, "used_percent": 0.5,
	})
	insertEvent(t, db, "ev-2", ids.sessionID, "provider.context.observed", "2026-07-12T00:05:00Z", map[string]any{
		"used_tokens": 5000.0, "window_tokens": 200000.0, "used_percent": 2.5,
	})
	src := evaluation.NewSQLDataSource(db)

	obs, err := src.Context(context.Background(), domain.SessionID(ids.sessionID))
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	if obs.UsedTokens == nil || *obs.UsedTokens != 5000 {
		t.Errorf("UsedTokens = %v, want 5000 (most recent event)", obs.UsedTokens)
	}
	if obs.WindowTokens == nil || *obs.WindowTokens != 200000 {
		t.Errorf("WindowTokens = %v, want 200000", obs.WindowTokens)
	}
	if obs.UsedPercent == nil || *obs.UsedPercent != 2.5 {
		t.Errorf("UsedPercent = %v, want 2.5", obs.UsedPercent)
	}
}

// --- RunwayForecast ----------------------------------------------------------

func TestSQLDataSource_RunwayForecast_NoRowIsColdStart(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	src := evaluation.NewSQLDataSource(db)

	_, ok, err := src.RunwayForecast(context.Background(), domain.SessionID(ids.sessionID))
	if err != nil {
		t.Fatalf("RunwayForecast: %v", err)
	}
	if ok {
		t.Errorf("ok = true, want false (no runway_forecasts row exists yet)")
	}
}

func seedRunwayForecast(t *testing.T, db *sqlite.DB, id, sessionID string, hitProbability *float64, calibrated bool, createdAt string) {
	t.Helper()
	reasonJSON, err := json.Marshal([]string{"quota_burn_accelerating"})
	if err != nil {
		t.Fatalf("marshal reason codes: %v", err)
	}
	exec(t, db, `
		INSERT INTO runway_forecasts (
			id, session_id, limit_id, horizon_seconds, hit_probability, risk_score,
			calibrated, confidence, current_used_percent, reason_codes_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, sessionID, "five_hour", 600, nullableFloat(hitProbability), 0.7,
		boolToIntTest(calibrated), string(domain.ConfidenceMedium), 62.5, string(reasonJSON), createdAt,
	)
}

func nullableFloat(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}

func boolToIntTest(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func TestSQLDataSource_RunwayForecast_RealMostRecentRow(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	seedRunwayForecast(t, db, "rf-1", ids.sessionID, nil, false, "2026-07-12T00:01:00Z")
	p := 0.85
	seedRunwayForecast(t, db, "rf-2", ids.sessionID, &p, true, "2026-07-12T00:05:00Z")

	src := evaluation.NewSQLDataSource(db)
	forecast, ok, err := src.RunwayForecast(context.Background(), domain.SessionID(ids.sessionID))
	if err != nil {
		t.Fatalf("RunwayForecast: %v", err)
	}
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if !forecast.Calibrated {
		t.Errorf("Calibrated = false, want true (the most recent row, rf-2)")
	}
	if forecast.HitProbability == nil || *forecast.HitProbability != 0.85 {
		t.Errorf("HitProbability = %v, want 0.85", forecast.HitProbability)
	}
	if forecast.LimitID != "five_hour" {
		t.Errorf("LimitID = %q, want five_hour", forecast.LimitID)
	}
	if len(forecast.ReasonCodes) != 1 || forecast.ReasonCodes[0] != "quota_burn_accelerating" {
		t.Errorf("ReasonCodes = %v, want [quota_burn_accelerating]", forecast.ReasonCodes)
	}
}

// --- PriorRunwayHitConfirmed --------------------------------------------------

func TestSQLDataSource_PriorRunwayHitConfirmed_NoDecisionsIsFalse(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	src := evaluation.NewSQLDataSource(db)

	confirmed, err := src.PriorRunwayHitConfirmed(context.Background(), domain.SessionID(ids.sessionID))
	if err != nil {
		t.Fatalf("PriorRunwayHitConfirmed: %v", err)
	}
	if confirmed {
		t.Errorf("confirmed = true, want false (no policy_decisions rows exist)")
	}
}

// seedPredictionAndDecision inserts a minimal predictions row plus a linked
// policy_decisions row carrying the given reason codes, plus a
// provider.turn.started event binding turnID to sessionID (the join
// PriorRunwayHitConfirmed uses to recover session scoping).
func seedPredictionAndDecision(t *testing.T, db *sqlite.DB, sessionID, turnID, predictionID, decisionID string, reasonCodes []string, decidedAt string) {
	t.Helper()
	insertEvent(t, db, "turnev-"+turnID, sessionID, "provider.turn.started", decidedAt, map[string]any{
		"prompt_sha256": "x", "prompt_byte_length": 1, "prompt_approx_tokens": 1,
	})
	exec(t, db, `UPDATE events SET turn_id = ? WHERE event_id = ?`, turnID, "turnev-"+turnID)

	exec(t, db, `
		INSERT INTO predictions (
			id, turn_id, predictor_id, predictor_version, feature_set_version,
			quota_risk_score, context_risk_score, completion_risk_score,
			blast_radius_risk_score, overall_risk_score, confidence, calibrated,
			reason_codes_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		predictionID, turnID, "predictor.RulePipeline", "v1", "v1",
		0.1, 0.1, 0.1, 0.1, 0.1, string(domain.ConfidenceLow), 0,
		"[]", decidedAt,
	)

	reasonJSON, err := json.Marshal(reasonCodes)
	if err != nil {
		t.Fatalf("marshal reason codes: %v", err)
	}
	exec(t, db, `
		INSERT INTO policy_decisions (
			id, prediction_id, policy_version, action, severity,
			requires_confirmation, reason_codes_json, decided_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		decisionID, predictionID, "policy.Decider/v1", "warn", "high", 0, string(reasonJSON), decidedAt,
	)
}

func TestSQLDataSource_PriorRunwayHitConfirmed_RealArmedReasonCode(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	seedPredictionAndDecision(t, db, ids.sessionID, "turn-1", "pred-1", "dec-1",
		[]string{"runway_hit_probability_armed_pending_confirmation"}, "2026-07-12T00:01:00Z")

	src := evaluation.NewSQLDataSource(db)
	confirmed, err := src.PriorRunwayHitConfirmed(context.Background(), domain.SessionID(ids.sessionID))
	if err != nil {
		t.Fatalf("PriorRunwayHitConfirmed: %v", err)
	}
	if !confirmed {
		t.Errorf("confirmed = false, want true (armed reason code present on most recent decision)")
	}
}

func TestSQLDataSource_PriorRunwayHitConfirmed_UsesMostRecentDecisionOnly(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	// Older decision qualifies...
	seedPredictionAndDecision(t, db, ids.sessionID, "turn-1", "pred-1", "dec-1",
		[]string{"runway_hit_probability_confirmed_twice"}, "2026-07-12T00:01:00Z")
	// ...but the most recent decision does not (e.g. runway pressure resolved).
	seedPredictionAndDecision(t, db, ids.sessionID, "turn-2", "pred-2", "dec-2",
		[]string{"low_risk_band"}, "2026-07-12T00:05:00Z")

	src := evaluation.NewSQLDataSource(db)
	confirmed, err := src.PriorRunwayHitConfirmed(context.Background(), domain.SessionID(ids.sessionID))
	if err != nil {
		t.Fatalf("PriorRunwayHitConfirmed: %v", err)
	}
	if confirmed {
		t.Errorf("confirmed = true, want false (most recent decision does not carry a qualifying reason code)")
	}
}

func TestSQLDataSource_PriorRunwayHitConfirmed_NonQualifyingReasonCodeIsFalse(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	seedPredictionAndDecision(t, db, ids.sessionID, "turn-1", "pred-1", "dec-1",
		[]string{"critical_risk_band"}, "2026-07-12T00:01:00Z")

	src := evaluation.NewSQLDataSource(db)
	confirmed, err := src.PriorRunwayHitConfirmed(context.Background(), domain.SessionID(ids.sessionID))
	if err != nil {
		t.Fatalf("PriorRunwayHitConfirmed: %v", err)
	}
	if confirmed {
		t.Errorf("confirmed = true, want false (critical_risk_band is not a runway hit-probability marker)")
	}
}

// --- full DataSource satisfied end-to-end via a real evaluation.Service -----

// TestSQLDataSource_WiredIntoRealService proves SQLDataSource is not just
// individually correct but actually usable as the DataSource behind a real
// evaluation.Service, wired to the SAME real pipeline-stage implementations
// (scope/token/quota/risk/policy) production wiring would use — the whole
// point of this corrective addition (per the task's own framing: "this is
// why cmd/auspex/main.go still cannot be wired with a real
// EvaluationService"). This does not re-prove the pipeline's own internal
// correctness (pipeline_e2e_test.go already does that against
// fakeDataSource) — it proves SQLDataSource satisfies every hand-off
// EvaluateTurn/Decide make into DataSource without panicking or erroring,
// both on a cold-start (near-empty) database (the realistic first-turn
// case) and against a database with real seeded rows across every table
// this file reads.
func TestSQLDataSource_WiredIntoRealService_ColdStartNeverErrors(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	src := evaluation.NewSQLDataSource(db)
	clk := newFakeClock(time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC))
	ids2 := &sequentialIDs{prefix: "coldid"}

	svc := newSQLBackedService(db, src, clk, ids2)

	result, err := svc.EvaluateTurn(context.Background(), app.EvaluateTurnRequest{
		SessionID: domain.SessionID(ids.sessionID),
		TurnID:    domain.TurnID("turn-cold"),
		Provider:  "claude",
	})
	if err != nil {
		t.Fatalf("EvaluateTurn against a cold-start-only SQLDataSource errored: %v", err)
	}
	if result.Calibrated {
		t.Errorf("Calibrated = true, want false (nothing in this DB is calibrated data yet)")
	}

	decision, err := svc.Decide(context.Background(), app.DecideRequest{EvaluationID: result.ID})
	if err != nil {
		t.Fatalf("Decide against a cold-start-only SQLDataSource errored: %v", err)
	}
	if decision.Action == "" {
		t.Errorf("Decide returned an empty Action")
	}
}

// TestSQLDataSource_WiredIntoRealService_WithRealSeededData proves the same
// wiring succeeds once real rows exist across every table SQLDataSource
// reads (events, progress_nodes/edges, runway_forecasts) — not just on an
// empty database.
func TestSQLDataSource_WiredIntoRealService_WithRealSeededData(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	insertEvent(t, db, "ev-prompt", ids.sessionID, "provider.turn.started", "2026-07-12T00:00:30Z", map[string]any{
		"prompt_sha256": "abc", "prompt_byte_length": 300, "prompt_approx_tokens": 80,
	})
	insertEvent(t, db, "ev-quota", ids.sessionID, "provider.quota.observed", "2026-07-12T00:00:31Z", map[string]any{
		"limit_id": "five_hour", "used_percent": 62.0,
	})
	insertEvent(t, db, "ev-context", ids.sessionID, "provider.context.observed", "2026-07-12T00:00:32Z", map[string]any{
		"used_tokens": 8000.0, "window_tokens": 200000.0, "used_percent": 4.0,
	})
	seedNode(t, db, "node-1", ids.taskID, nil, 0, string(domain.NodeCodeChange), string(domain.NodeInProgress), "2026-07-12T00:00:33Z")

	src := evaluation.NewSQLDataSource(db)
	clk := newFakeClock(time.Date(2026, 7, 12, 0, 1, 0, 0, time.UTC))
	ids2 := &sequentialIDs{prefix: "seededid"}

	svc := newSQLBackedService(db, src, clk, ids2)

	result, err := svc.EvaluateTurn(context.Background(), app.EvaluateTurnRequest{
		SessionID: domain.SessionID(ids.sessionID),
		TurnID:    domain.TurnID("turn-seeded"),
		Provider:  "claude",
	})
	if err != nil {
		t.Fatalf("EvaluateTurn against a seeded SQLDataSource errored: %v", err)
	}

	got, err := svc.GetEvaluation(context.Background(), result.ID)
	if err != nil {
		t.Fatalf("GetEvaluation: %v", err)
	}
	if got.ID != result.ID {
		t.Errorf("GetEvaluation round-trip ID = %q, want %q", got.ID, result.ID)
	}
}

// newSQLBackedService wires a real evaluation.Service against the SAME real
// pipeline-stage implementations (scope/token/quota/risk/policy) production
// wiring would use, backed by src (a *evaluation.SQLDataSource) instead of
// the fakeDataSource helpers_test.go's own newTestService uses — this is
// the one place in this test file that proves SQLDataSource type-checks and
// behaves correctly as the concrete DataSource behind every sibling
// predictor package's own FeatureSource-adapter pattern (scopeSourceAdapter/
// tokenSourceAdapter, helpers_test.go), not just against evaluation.DataSource
// in isolation.
func newSQLBackedService(db *sqlite.DB, src *evaluation.SQLDataSource, clk domain.Clock, ids domain.IDGenerator) *evaluation.Service {
	return evaluation.New(
		db,
		src,
		scope.NewRuleScopeEstimator(sqlScopeAdapter{src: src}),
		token.NewRuleTokenForecaster(sqlTokenAdapter{src: src}),
		quota.NewRuleQuotaForecaster(),
		risk.NewRuleRiskCombiner(),
		policy.NewDecider(),
		clk, ids,
	)
}

// sqlScopeAdapter/sqlTokenAdapter adapt *evaluation.SQLDataSource to
// internal/predictor/scope.FeatureSource and
// internal/predictor/token.FeatureSource respectively — the same adaptation
// helpers_test.go's scopeSourceAdapter/tokenSourceAdapter already perform
// for *fakeDataSource, applied here to the real SQLDataSource instead.
type sqlScopeAdapter struct{ src *evaluation.SQLDataSource }

func (a sqlScopeAdapter) Classification(ctx context.Context, sessionID domain.SessionID, taskID *domain.TaskID) (features.Classification, features.PromptFeatures, error) {
	return a.src.Classification(ctx, sessionID, taskID)
}
func (a sqlScopeAdapter) Repository(ctx context.Context, repositoryID domain.RepositoryID) (features.RepositoryFeatures, bool, error) {
	return a.src.Repository(ctx, repositoryID)
}
func (a sqlScopeAdapter) Session(ctx context.Context, sessionID domain.SessionID) (features.SessionFeatures, bool, error) {
	return a.src.Session(ctx, sessionID)
}
func (a sqlScopeAdapter) Progress(ctx context.Context, taskID *domain.TaskID) (features.ProgressFeatures, bool, error) {
	return a.src.Progress(ctx, taskID)
}

type sqlTokenAdapter struct{ src *evaluation.SQLDataSource }

func (a sqlTokenAdapter) Classification(ctx context.Context, sessionID domain.SessionID) (features.Classification, features.PromptFeatures, error) {
	return a.src.Classification(ctx, sessionID, nil)
}
func (a sqlTokenAdapter) Session(ctx context.Context, sessionID domain.SessionID) (features.SessionFeatures, bool, error) {
	return a.src.Session(ctx, sessionID)
}
func (a sqlTokenAdapter) Progress(ctx context.Context, sessionID domain.SessionID) (features.ProgressFeatures, bool, error) {
	resolved, err := a.src.Resolve(ctx, sessionID)
	if err != nil {
		return features.ProgressFeatures{}, false, nil //nolint:nilerr // Progress degrades to cold-start on a resolve failure rather than failing the whole token forecast
	}
	return a.src.Progress(ctx, resolved.TaskID)
}
func (a sqlTokenAdapter) RecentSimilarTurnTokens(ctx context.Context, sessionID domain.SessionID, class features.TaskClass) (features.SimilarTurnTokens, error) {
	return a.src.RecentSimilarTurnTokens(ctx, sessionID, class)
}
