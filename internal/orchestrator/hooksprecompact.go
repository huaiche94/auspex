// hooksprecompact.go implements the `auspex hook claude pre-compact` /
// `post-compact` and `auspex hook codex pre-compact` / `post-compact`
// orchestration functions (issue #114, M4/M10) that internal/cli's command
// constructors call into — the compaction siblings of hooks.go's and
// codexhooks.go's handlers, reusing the same HookDeps collaborators.
//
// Design intent (ADD §22.4 "`PreCompact` 一律 capture state checkpoint",
// §21.10 "`PreCompact` 前建立 State Checkpoint"): the PreCompact hook runs
// synchronously BEFORE the provider's compaction, so this is the last
// moment the session's full working state is still addressable — the
// handler captures a State Checkpoint (then a repository checkpoint, in
// CheckpointCreate's frozen state-first order) and only then records the
// compaction observation event, with the capture's outcome stamped onto
// the event payload.
//
// Every handler follows hooks.go's JSON/error contract verbatim, and one
// rule dominates this file: hooks FAIL OPEN. A checkpoint failure — or a
// session that resolves to no task/worktree to checkpoint at all — must
// never block the provider's compaction or session (issue #114's design
// constraint; ADD §17.5). The failure is recorded on the persisted event
// (checkpoint_captured=false + a machine-readable skip reason) and the
// hook answers its usual no-opinion `{}`. The strict-policy
// `continue:false` escalation ADD §21.10 sketches for Codex is future
// policy work, deliberately not implemented here.
package orchestrator

import (
	"context"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	claudehooks "github.com/huaiche94/auspex/internal/hooks/claude"
	codexhooks "github.com/huaiche94/auspex/internal/hooks/codex"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// Compaction-checkpoint skip reasons (machine-readable, recorded on the
// persisted event's checkpoint_skip_reason payload key — never user or
// provider text). "Skip" covers both "structurally could not attempt"
// (unconfigured/unresolvable) and "attempted and failed"; the
// CompactCheckpointOutcome.Attempted flag distinguishes the two.
const (
	// CompactSkipNotConfigured: HookDeps.CompactCheckpoint was nil or
	// missing a required collaborator — this composition does not capture
	// compaction checkpoints at all.
	CompactSkipNotConfigured = "not_configured"
	// CompactSkipSessionUnresolved: the session has no
	// provider_sessions/worktrees chain to resolve (e.g. its hooks never
	// carried a cwd, or Auspex was installed mid-session).
	CompactSkipSessionUnresolved = "session_unresolved"
	// CompactSkipNoTask: the session resolved but has no task — a State
	// Checkpoint is task-scoped (app.CreateStateCheckpointRequest) and a
	// task that does not exist cannot honestly be checkpointed.
	CompactSkipNoTask = "no_task"
	// CompactSkipNoWorktree: no worktree binding for the session, so the
	// repository half has nothing to capture (and the state half is not
	// attempted alone: a checkpoint pair with a known-impossible second
	// stage would leave exactly the partial-sequence state
	// CheckpointCreate's ordering doc warns about, silently, on every
	// compaction).
	CompactSkipNoWorktree = "no_worktree"
	// CompactSkipCheckpointFailed: CheckpointCreate was attempted and
	// returned an error (either stage). The hook continues fail-open.
	CompactSkipCheckpointFailed = "checkpoint_failed"
)

// SessionWorktreeResolver resolves a session's worktree binding — the one
// lookup a compaction checkpoint needs that the frozen
// app.FeatureDataSource.Resolve does not answer (ResolvedSession carries
// RepositoryID/TaskID only). ok=false means "no binding known" (or a
// resolver-side failure): the caller skips, never fabricates.
// Implementations must be fail-open.
type SessionWorktreeResolver interface {
	WorktreeForSession(ctx context.Context, sessionID domain.SessionID) (domain.WorktreeID, bool)
}

// SessionWorktreeStore is the SQL-backed SessionWorktreeResolver, reading
// the provider_sessions row the issue-#17 bootstrap writes. Lives in this
// package for CodexStatusStore's reason (codexstatus.go): hook-path
// infrastructure worth testing against a real migrated DB in-package.
type SessionWorktreeStore struct {
	DB *sqlite.DB
}

var _ SessionWorktreeResolver = (*SessionWorktreeStore)(nil)

// WorktreeForSession implements SessionWorktreeResolver. Fail-open by that
// contract: nil receiver/DB, a query error, and no row are all ok=false.
func (s *SessionWorktreeStore) WorktreeForSession(ctx context.Context, sessionID domain.SessionID) (domain.WorktreeID, bool) {
	if s == nil || s.DB == nil || sessionID == "" {
		return "", false
	}
	var worktreeID string
	err := s.DB.Conn().QueryRowContext(ctx,
		`SELECT worktree_id FROM provider_sessions WHERE id = ?`, string(sessionID),
	).Scan(&worktreeID)
	if err != nil || worktreeID == "" {
		return "", false
	}
	return domain.WorktreeID(worktreeID), true
}

// CompactCheckpointOutcome is one Capture call's result — the
// orchestrator-side record the handlers translate into the telemetry
// payload (claudetelemetry.CompactionCheckpoint). IDs and enum reasons
// only.
type CompactCheckpointOutcome struct {
	// Attempted is true when CheckpointCreate was actually invoked
	// (resolution succeeded); false when capture was skipped before any
	// checkpoint write could start.
	Attempted bool
	// Captured is true when both checkpoint stages durably succeeded.
	Captured bool
	// StateCheckpointID / RepositoryCheckpointID identify the created
	// checkpoints when Captured.
	StateCheckpointID      domain.StateCheckpointID
	RepositoryCheckpointID domain.RepositoryCheckpointID
	// SkipReason names why not-Captured (one of the CompactSkip*
	// constants). Empty when Captured.
	SkipReason string
}

// CompactCheckpointer captures the pre-compaction State Checkpoint (+
// repository checkpoint) for a session (issue #114). It resolves the
// session to its task (Sessions — the same narrow SessionResolver view of
// the frozen app.FeatureDataSource the correlator uses) and worktree
// (Worktrees), then runs the frozen CheckpointCreate orchestration
// (checkpoint.go) so the state-before-repository ordering — and the
// orphaned-repository-checkpoint impossibility it guarantees — is exactly
// the `auspex checkpoint create` path, not a reimplementation.
//
// A nil *CompactCheckpointer, or one missing any collaborator, is a valid,
// documented degrade (capture skipped, reason recorded) — mirroring
// HookDeps' Correlator/Bootstrapper pointer-field convention.
type CompactCheckpointer struct {
	Sessions   SessionResolver
	Worktrees  SessionWorktreeResolver
	State      app.StateCheckpointService
	Repository app.RepositoryCheckpointService
}

// Capture resolves sessionID and creates the checkpoint pair. It never
// returns an error — every failure mode is an outcome with a skip reason,
// per this file's fail-open rule. (CheckpointCreate itself stays
// fail-closed for its own callers; the fail-open translation happens
// here, at the hook boundary, where blocking the provider is the one
// unacceptable outcome.)
func (c *CompactCheckpointer) Capture(ctx context.Context, sessionID domain.SessionID) CompactCheckpointOutcome {
	if c == nil || c.Sessions == nil || c.Worktrees == nil || c.State == nil || c.Repository == nil {
		return CompactCheckpointOutcome{SkipReason: CompactSkipNotConfigured}
	}
	resolved, err := c.Sessions.Resolve(ctx, sessionID)
	if err != nil {
		return CompactCheckpointOutcome{SkipReason: CompactSkipSessionUnresolved}
	}
	if resolved.TaskID == nil || *resolved.TaskID == "" {
		return CompactCheckpointOutcome{SkipReason: CompactSkipNoTask}
	}
	worktreeID, ok := c.Worktrees.WorktreeForSession(ctx, sessionID)
	if !ok || worktreeID == "" {
		return CompactCheckpointOutcome{SkipReason: CompactSkipNoWorktree}
	}
	result, err := CheckpointCreate(ctx, CheckpointCreateDeps{
		StateCheckpoint:      c.State,
		RepositoryCheckpoint: c.Repository,
	}, CheckpointCreateRequest{
		TaskID:     *resolved.TaskID,
		WorktreeID: worktreeID,
	})
	if err != nil {
		return CompactCheckpointOutcome{Attempted: true, SkipReason: CompactSkipCheckpointFailed}
	}
	return CompactCheckpointOutcome{
		Attempted:              true,
		Captured:               true,
		StateCheckpointID:      result.State.ID,
		RepositoryCheckpointID: result.Repository.ID,
	}
}

// compactionCheckpointRecord translates an outcome into the telemetry
// payload record. Shared by the claude and codex pre-compact handlers
// (codextelemetry.CompactionCheckpoint aliases the claude type).
func compactionCheckpointRecord(outcome CompactCheckpointOutcome) *claudetelemetry.CompactionCheckpoint {
	return &claudetelemetry.CompactionCheckpoint{
		Captured:               outcome.Captured,
		StateCheckpointID:      string(outcome.StateCheckpointID),
		RepositoryCheckpointID: string(outcome.RepositoryCheckpointID),
		SkipReason:             outcome.SkipReason,
	}
}

// captureCompactCheckpoint runs the capture through HookDeps' optional
// seam. A nil CompactCheckpoint field degrades to the not_configured
// outcome (Capture is nil-receiver-safe), so handlers need no branching.
func (d HookDeps) captureCompactCheckpoint(ctx context.Context, sessionID domain.SessionID) CompactCheckpointOutcome {
	return d.CompactCheckpoint.Capture(ctx, sessionID)
}

// --- auspex hook claude pre-compact ----------------------------------------

// PreCompactResult is HandlePreCompact's (and HandleCodexPreCompact's)
// return value.
type PreCompactResult struct {
	EventsNormalized int
	Persisted        bool
	// CheckpointCaptured is true when the pre-compaction checkpoint pair
	// was durably created; CheckpointSkipReason names why not (one of the
	// CompactSkip* constants) otherwise.
	CheckpointCaptured   bool
	CheckpointSkipReason string
}

// HandlePreCompact implements `auspex hook claude pre-compact`: parse the
// PreCompact payload, lazily register the session (issue #17), capture the
// State Checkpoint + repository checkpoint BEFORE the provider's
// compaction proceeds (the hook is synchronous — returning is what lets
// the compaction run), then normalize and best-effort persist one
// provider.session.compacted event (phase "pre") carrying the capture's
// outcome. Fail-open at every step: malformed stdin, an unresolvable
// session, and a failed checkpoint all still answer the CLI layer's `{}`.
func HandlePreCompact(ctx context.Context, deps HookDeps, stdin []byte) (PreCompactResult, error) {
	parsed, err := claudehooks.ParsePreCompact(stdin)
	if err != nil {
		return PreCompactResult{}, nil //nolint:nilerr // fail-open: malformed hook input must not fail the PreCompact hook.
	}
	// PreCompact payloads carry a cwd, so a session whose first observed
	// hook is a compaction still gets registered (same reasoning as
	// HandleStop).
	deps.bootstrapSession(ctx, parsed.SessionID, parsed.CWD, nil, nil)

	// The whole point of this hook (ADD §22.4): checkpoint FIRST, while
	// the pre-compaction state is still the live state — before the
	// observation event is even assembled, and structurally before the
	// provider's compaction (which waits on this process).
	outcome := deps.captureCompactCheckpoint(ctx, parsed.SessionID)

	observedAt := deps.Clock.Now()
	event := deps.normalizer().NormalizePreCompact(parsed, observedAt, compactionCheckpointRecord(outcome))
	events := []v1.Event{event}
	// An auto-triggered compaction fires mid-turn; attribute the
	// observation to the session's latest started turn where one is known
	// (same fail-open stamping as the terminal hooks).
	deps.stampOpenTurn(ctx, parsed.SessionID, events)
	persisted := deps.persist(ctx, events)
	return PreCompactResult{
		EventsNormalized:     1,
		Persisted:            persisted,
		CheckpointCaptured:   outcome.Captured,
		CheckpointSkipReason: outcome.SkipReason,
	}, nil
}

// PostCompactResult is HandlePostCompact's (and HandleCodexPostCompact's)
// return value.
type PostCompactResult struct {
	EventsNormalized int
	Persisted        bool
}

// HandlePostCompact implements `auspex hook claude post-compact`:
// parse, register, normalize one provider.session.compacted event (phase
// "post"), best-effort persist. Observation only — no checkpoint (the
// pre-compaction state is gone by now) and no context re-injection (ADD
// §21.10's PostCompact inject step is later scope). See
// internal/hooks/claude/precompact.go's capability note: Claude Code
// ships no PostCompact hook event today, so this command is not
// registered in integrations/claude/hooks.json.
func HandlePostCompact(ctx context.Context, deps HookDeps, stdin []byte) (PostCompactResult, error) {
	parsed, err := claudehooks.ParsePostCompact(stdin)
	if err != nil {
		return PostCompactResult{}, nil //nolint:nilerr // fail-open: malformed hook input must not fail the PostCompact hook.
	}
	deps.bootstrapSession(ctx, parsed.SessionID, parsed.CWD, nil, nil)
	observedAt := deps.Clock.Now()
	event := deps.normalizer().NormalizePostCompact(parsed, observedAt)
	events := []v1.Event{event}
	deps.stampOpenTurn(ctx, parsed.SessionID, events)
	persisted := deps.persist(ctx, events)
	return PostCompactResult{EventsNormalized: 1, Persisted: persisted}, nil
}

// --- auspex hook codex pre-compact / post-compact ---------------------------

// HandleCodexPreCompact implements `auspex hook codex pre-compact` —
// HandlePreCompact's codex twin over the codex parser/normalizer and the
// SAME CompactCheckpointer seam (capture is provider-independent: it
// resolves by session, and codex sessions bootstrap through the same
// provider_sessions chain). See internal/hooks/codex/precompact.go's
// capability note: the pinned Codex v0.144.4 does not verifiably emit this
// event yet, so integrations/codex/hooks.json does not register it.
func HandleCodexPreCompact(ctx context.Context, deps HookDeps, stdin []byte) (PreCompactResult, error) {
	parsed, err := codexhooks.ParsePreCompact(stdin)
	if err != nil {
		return PreCompactResult{}, nil //nolint:nilerr // fail-open: malformed hook input must not fail the PreCompact hook.
	}
	deps.bootstrapCodexSession(ctx, parsed.SessionID, parsed.CWD, parsed.Model)

	outcome := deps.captureCompactCheckpoint(ctx, parsed.SessionID)

	observedAt := deps.Clock.Now()
	event := deps.codexNormalizer().NormalizePreCompact(parsed, observedAt, compactionCheckpointRecord(outcome))
	events := []v1.Event{event}
	deps.stampOpenTurn(ctx, parsed.SessionID, events)
	persisted := deps.persist(ctx, events)
	return PreCompactResult{
		EventsNormalized:     1,
		Persisted:            persisted,
		CheckpointCaptured:   outcome.Captured,
		CheckpointSkipReason: outcome.SkipReason,
	}, nil
}

// HandleCodexPostCompact implements `auspex hook codex post-compact` —
// HandlePostCompact's codex twin. Same capability note as
// HandleCodexPreCompact.
func HandleCodexPostCompact(ctx context.Context, deps HookDeps, stdin []byte) (PostCompactResult, error) {
	parsed, err := codexhooks.ParsePostCompact(stdin)
	if err != nil {
		return PostCompactResult{}, nil //nolint:nilerr // fail-open: malformed hook input must not fail the PostCompact hook.
	}
	deps.bootstrapCodexSession(ctx, parsed.SessionID, parsed.CWD, parsed.Model)
	observedAt := deps.Clock.Now()
	event := deps.codexNormalizer().NormalizePostCompact(parsed, observedAt)
	events := []v1.Event{event}
	deps.stampOpenTurn(ctx, parsed.SessionID, events)
	persisted := deps.persist(ctx, events)
	return PostCompactResult{EventsNormalized: 1, Persisted: persisted}, nil
}
