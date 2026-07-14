// sessionbootstrap.go implements the lazy in-hook session bootstrap that
// closes GitHub issue #17 ("Native hook mode never creates repository/
// worktree/session rows — evaluation pipeline inert in production, found
// by dogfooding"): before this file, NO production code path ever wrote a
// repositories/worktrees/provider_sessions row (only tests seeded them),
// so evaluation.SQLDataSource.Resolve(sessionID) returned ErrCodeNotFound
// for every real native-hook session — EvaluateTurn always failed, the
// UserPromptSubmit hook permanently degraded to a plain allow, the
// issue-#14 forecast card never rendered outside tests, and issue #1's
// event correlation never resolved a TaskID.
//
// # The ID spaces this must line up with (load-bearing)
//
// SQLDataSource.Resolve (internal/evaluation/datasource_sql.go) looks up
// `provider_sessions WHERE id = ?` with the SessionID the hook payload
// carried — i.e. Claude Code's own session UUID, the same value
// claude-provider's normalizer stamps onto events.session_id (migration
// 0010) and tasks.session_id joins on. So the bootstrap MUST write the
// provider's session UUID as provider_sessions.id (primary key), not a
// freshly minted synthetic ID — a synthetic id would leave Resolve exactly
// as blind as before. The same UUID is also recorded as
// provider_session_id (0003's "the provider's own session identifier"
// column), which is trivially true here since native-hook sessions ARE
// identified by the provider's UUID; a future non-hook invocation mode
// with its own internal ID space can diverge without breaking this.
//
// # Idempotency without a new migration
//
// The task brief for issue #17 reserved migration 0011 (claude-provider's
// 0010-0019 range, CONTRACT_FREEZE.md "Migration ranges") in case an
// upsert natural key lacked a unique constraint. None does — every
// ON CONFLICT target below is an existing, frozen constraint:
//
//   - repositories:      UNIQUE(git_common_dir)            (0001)
//   - worktrees:         UNIQUE(repository_id, root_path)  (0002)
//   - provider_sessions: PRIMARY KEY id                    (0003)
//
// so no 0011 migration is added; concurrent hook invocations for the same
// session/worktree race benignly into the same rows (proven by this
// file's concurrent test), with SQLite's writer serialization plus the
// busy_timeout pragma (internal/storage/sqlite/db.go) handling the lock
// contention.
//
// # Fail-open discipline (ADD §17.5)
//
// Bootstrap runs on the hook path, so it inherits hooks.go's contract
// verbatim: "a hook must never fail the user's actual prompt/turn because
// Auspex's own event log could not be written". A missing cwd, a non-git
// directory, a git executable failure, and a SQL error are all silent
// no-ops (Bootstrap returns false, never an error) — an unbootstrapped
// session degrades to exactly the pre-issue-#17 behavior (Resolve
// not_found -> plain allow), it never becomes a new way for a hook to
// fail. A nil *SessionBootstrapper is a documented no-op, mirroring
// EventCorrelator's nil-receiver convention (correlate.go).
package orchestrator

import (
	"context"
	"path/filepath"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/gitx"
	claudeprovider "github.com/huaiche94/auspex/internal/providers/claude"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// invocationModeNativeHook is the provider_sessions.invocation_mode value
// for rows created by this bootstrap: the session was observed via Claude
// Code's native lifecycle hooks (issue #17's approved design), as opposed
// to a future managed-runner or interactive registration mode.
const invocationModeNativeHook = "native-hook"

// InvocationModeManagedStreamJSON is the provider_sessions.invocation_mode
// value for sessions registered by the managed one-shot runner (`auspex
// run`, issue #8 — ADD §8.1's `claude -p --output-format stream-json`
// path). The spelling matches ADD §16's policy vocabulary
// (invocation_mode_in: [managed_app_server, managed_stream_json]) so a
// future policy rule can target managed sessions without a data
// migration, and so the row honestly records HOW the session was driven
// rather than fabricating the hook default (Constitution rule 3: provider
// capability/mode differences are surfaced explicitly, never silently
// assumed away).
const InvocationModeManagedStreamJSON = "managed_stream_json"

// RepoResolver is the narrow, package-local view of *gitx.Client this
// bootstrapper actually consumes — the same one-method-view convention
// SessionResolver (correlate.go) established over app.FeatureDataSource.
// The real implementation is checkpoint-b02's internal/gitx.Client
// (reused, not reimplemented, per issue #17's design); tests supply a
// fake.
type RepoResolver interface {
	// ResolveRepo resolves the Git repository containing path. See
	// gitx.(*Client).ResolveRepo for the error contract (ErrCodeNotFound
	// for a non-git path, ErrCodeUnavailable when git cannot run).
	ResolveRepo(ctx context.Context, path string) (gitx.RepoInfo, error)
}

var _ RepoResolver = (*gitx.Client)(nil)

// SessionBootstrap is one Bootstrap request: the session to register, the
// directory the provider reported for it (hook payload cwd / statusline
// workspace dir), the provider identifier for the natural key on
// provider_sessions(provider, provider_session_id), and the model when
// the payload carried one. Model is a pointer per the Constitution's
// "unknown is not zero" rule (ADD principle 1): UserPromptSubmit/Stop
// payloads carry no model field, and nil here leaves the model column
// NULL rather than writing a fabricated empty-string "model".
type SessionBootstrap struct {
	SessionID domain.SessionID
	Dir       string
	Provider  string
	Model     *string
	// Effort is the reasoning-effort level when the payload carried one
	// (statusline effort.level — #20 Phase 0); same pointer semantics as
	// Model. Like model, provider_sessions holds the LATEST observed
	// value as the resolution cache for per-turn stamping (migration
	// 0046); the turn-level record lives on the prediction row.
	Effort *string
	// InvocationMode records HOW the session is being driven
	// (provider_sessions.invocation_mode). Empty means the hook default
	// (invocationModeNativeHook) — every pre-issue-#8 caller in hooks.go
	// leaves it empty and keeps its exact prior behavior; the managed
	// one-shot runner passes InvocationModeManagedStreamJSON. Like the
	// worktree binding, the mode is first-observation-wins on conflict
	// (the managed runner bootstraps BEFORE its gate evaluation runs, so
	// a managed session's row is born managed even though the shared
	// gate path re-bootstraps it with the hook default a moment later).
	InvocationMode string
}

// SessionBootstrapper lazily registers the repositories -> worktrees ->
// provider_sessions chain for a session observed via a native hook, so
// that evaluation.SQLDataSource.Resolve(sessionID) succeeds from the
// session's first hook invocation onward (issue #17). All four fields are
// required for Bootstrap to do anything; a nil receiver or any nil field
// is a documented no-op, the same optional-dep convention HookDeps'
// Persister/Correlator/Forecast already use (hooks.go).
type SessionBootstrapper struct {
	// DB is the migrated Auspex database. Bootstrap writes the three
	// foundation tables (0001-0003) through it in one transaction.
	DB *sqlite.DB
	// Git resolves the reported directory to its repository/worktree
	// (checkpoint-b02's real gitx.Client in production).
	Git RepoResolver
	// Clock stamps created_at/last_seen_at/started_at.
	Clock domain.Clock
	// IDs mints repositories.id/worktrees.id for first-time inserts.
	// provider_sessions.id deliberately does NOT come from here — it is
	// the provider's own session UUID (see the file doc comment's ID-space
	// section).
	IDs domain.IDGenerator
}

// Bootstrap idempotently upserts the repositories/worktrees/
// provider_sessions rows for req, returning true when a provider_sessions
// row for req.SessionID durably exists afterward (freshly inserted or
// already present). Every failure path returns false and nothing else —
// no error, no panic — per the file doc comment's ADD §17.5 fail-open
// contract; callers on the hook path treat false exactly like the
// pre-issue-#17 world (evaluation degrades, hook response unaffected).
//
// Repeated and concurrent calls are safe: every INSERT targets an
// existing unique constraint (see the file doc comment), refreshing
// last_seen_at and — when req.Model is non-nil — the session's model on
// conflict. A session's worktree binding is first-observation-wins: a
// later hook reporting a different cwd does not rebind the session,
// keeping Resolve's answer stable for the session's whole lifetime (the
// documented judgment call here; a genuinely re-homed session is
// indistinguishable from a transient `cd` elsewhere, and rebinding on
// every hook would let one stray invocation flip every downstream
// feature/correlation lookup).
func (b *SessionBootstrapper) Bootstrap(ctx context.Context, req SessionBootstrap) bool {
	if b == nil || b.DB == nil || b.Git == nil || b.Clock == nil || b.IDs == nil {
		return false
	}
	if req.SessionID == "" || req.Dir == "" || req.Provider == "" {
		// No directory (payload cwd absent/empty) means there is nothing
		// to resolve a repository from — unknown is not zero, so no row is
		// fabricated (Constitution/ADD principle 1). Empty provider would
		// corrupt the (provider, provider_session_id) natural key the same
		// way.
		return false
	}

	info, err := b.Git.ResolveRepo(ctx, req.Dir)
	if err != nil {
		// Non-git directory (ErrCodeNotFound), bare repo
		// (ErrCodeValidation), or git not executable (ErrCodeUnavailable):
		// all fail-open no-ops per the file doc comment. A session running
		// outside any repository legitimately has no repositories/
		// worktrees chain to register.
		return false
	}

	invocationMode := req.InvocationMode
	if invocationMode == "" {
		invocationMode = invocationModeNativeHook
	}

	now := b.Clock.Now().UTC().Format(time.RFC3339Nano)
	// IDs are minted before the transaction so a retried/conflicting
	// insert does not burn IDs mid-transaction; on conflict the fresh ID
	// is simply discarded in favor of the existing row's.
	repoID := b.IDs.NewID()
	worktreeID := b.IDs.NewID()

	err = b.DB.WithTx(ctx, func(txCtx context.Context) error {
		q := sqlite.QuerierFromContext(txCtx, b.DB)

		// repositories: keyed by git_common_dir (0001's UNIQUE), which is
		// identical across all worktrees of one repository per `git
		// worktree` semantics — exactly why 0001 chose it over
		// canonical_root as the uniqueness key.
		if _, err := q.ExecContext(txCtx, `
			INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(git_common_dir) DO UPDATE SET last_seen_at = excluded.last_seen_at`,
			repoID, canonicalRoot(info), info.CommonDir, now, now,
		); err != nil {
			return err
		}
		var repositoryID string
		if err := q.QueryRowContext(txCtx,
			`SELECT id FROM repositories WHERE git_common_dir = ?`, info.CommonDir,
		).Scan(&repositoryID); err != nil {
			return err
		}

		// worktrees: keyed by (repository_id, root_path) (0002's UNIQUE).
		// branch_name stays NULL: gitx.ResolveRepo does not report a
		// branch, and running extra git commands here just to fill an
		// optional column is not worth widening the hook path's process
		// surface — NULL is the honest "not observed" per ADD principle 1.
		if _, err := q.ExecContext(txCtx, `
			INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(repository_id, root_path) DO UPDATE SET last_seen_at = excluded.last_seen_at`,
			worktreeID, repositoryID, info.WorktreeRoot, info.GitDir, now, now,
		); err != nil {
			return err
		}
		var resolvedWorktreeID string
		if err := q.QueryRowContext(txCtx,
			`SELECT id FROM worktrees WHERE repository_id = ? AND root_path = ?`,
			repositoryID, info.WorktreeRoot,
		).Scan(&resolvedWorktreeID); err != nil {
			return err
		}

		// provider_sessions: id IS the provider's session UUID (the file
		// doc comment's ID-space section — this is what makes
		// SQLDataSource.Resolve find the row). On conflict, only the model
		// and effort are touched, and only via COALESCE: a payload that
		// carried a value updates it, a payload that carried none
		// (excluded NULL) preserves whatever was already known — the
		// Constitution's "unknown is not zero" made load-bearing in SQL.
		// worktree_id/started_at keep their first-observation values (see
		// this method's doc comment).
		if _, err := q.ExecContext(txCtx, `
			INSERT INTO provider_sessions (id, worktree_id, provider, provider_session_id, invocation_mode, model, effort, started_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				model  = COALESCE(excluded.model, provider_sessions.model),
				effort = COALESCE(excluded.effort, provider_sessions.effort)`,
			string(req.SessionID), resolvedWorktreeID, req.Provider, string(req.SessionID),
			invocationMode, req.Model, req.Effort, now,
		); err != nil {
			return err
		}
		return nil
	})
	return err == nil
}

// canonicalRoot derives repositories.canonical_root from a resolved
// RepoInfo: for the main worktree it is the worktree root itself; for a
// linked worktree it is the main repository root, recovered as the parent
// of the shared common dir when that dir is a conventional "<root>/.git"
// (falling back to the linked worktree's own root when the layout is
// unconventional, e.g. a repo initialized with --separate-git-dir — a
// documented approximation, not a guess presented as exact: the
// git_common_dir column next to it is the actual identity key, per 0001's
// own schema comment).
func canonicalRoot(info gitx.RepoInfo) string {
	if !info.IsLinkedWorktree {
		return info.WorktreeRoot
	}
	if filepath.Base(info.CommonDir) == ".git" {
		return filepath.Dir(info.CommonDir)
	}
	return info.WorktreeRoot
}

// statusLineWorkspaceDir picks the directory a status-line snapshot
// reports for bootstrap: the current dir when present, else the project
// dir (internal/providers/claude.StatusLineSnapshot's workspace fields —
// both optional pointers, nil meaning unknown). nil means the snapshot
// carried no usable directory and bootstrap must no-op.
func statusLineWorkspaceDir(snap claudeprovider.StatusLineSnapshot) *string {
	if snap.CurrentDir != nil && *snap.CurrentDir != "" {
		return snap.CurrentDir
	}
	if snap.ProjectDir != nil && *snap.ProjectDir != "" {
		return snap.ProjectDir
	}
	return nil
}

// statusLineModel picks the model identity a status-line snapshot carries
// for provider_sessions.model: the stable model ID when present, else the
// display name, else nil (unknown is not zero — issue #17 deliverable 3:
// "statusline payload carries model — populate provider_sessions.model
// when available").
func statusLineModel(snap claudeprovider.StatusLineSnapshot) *string {
	if snap.ModelID != nil && *snap.ModelID != "" {
		return snap.ModelID
	}
	if snap.ModelDisplayName != nil && *snap.ModelDisplayName != "" {
		return snap.ModelDisplayName
	}
	return nil
}
