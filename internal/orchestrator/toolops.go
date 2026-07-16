// toolops.go: the issue-#67 slice-3a capture step (approved by ADR-052) —
// `auspex hook claude post-tool-use` handling plus the SQL-backed
// per-turn scratch counter it accumulates into, and the Stop-time fold
// that turns that scratch into the five provider.turn.completed payload
// aggregates. Lives in this package for the same reason openturn.go does:
// hook-path infrastructure with its own behavior worth testing against a
// real migrated DB in-package.
//
// Division of labor (mirrors the usage-enrichment rail, #72):
//
//	PostToolUse hook  -> parse+classify (internal/hooks/claude/posttooluse.go)
//	                  -> +1 into toolop_scratch (this file; counters/ids only)
//	Stop hook         -> fold scratch + transcript replay into TurnToolOps
//	                     (internal/telemetry/claude/toolops.go)
//	                  -> NormalizeStop stamps the five additive payload keys
//	                  -> scratch cleared (turn close)
//
// Privacy: nothing in this file ever receives a file path. The parsed
// PostToolUseEvent carries only a presence bit by construction, and the
// scratch table's schema (migration 0011) has no column a path — raw or
// hashed — could occupy. Path→ordinal interning happens in the Stop
// process's memory only (ADR-052's binding invariant).
package orchestrator

import (
	"context"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	claudehooks "github.com/huaiche94/auspex/internal/hooks/claude"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"
)

// ToolOpScratch accumulates and folds the per-turn file-operation counter
// (HookDeps.ToolOps). Every method is fail-open by contract: a nil
// receiver/DB or a storage error is a false/ok=false, never a hook
// failure — losing one tool-op sample must never block the provider's
// turn (ADD §17.5).
type ToolOpScratch interface {
	// RecordFileOp adds one observed view/modify file operation for the
	// session's turn. turnID may be "" when no open turn is resolvable —
	// the row still accumulates and folds under that same empty key.
	RecordFileOp(ctx context.Context, sessionID domain.SessionID, turnID domain.TurnID) bool
	// FoldFileOps reads the accumulated op count for the session's turn.
	// ok=false means no PostToolUse capture ran for that turn (hook not
	// registered, or the turn performed no file ops) — the caller stamps
	// nothing (absence stays honest, §7.3).
	FoldFileOps(ctx context.Context, sessionID domain.SessionID, turnID domain.TurnID) (int64, bool)
	// Clear deletes ALL scratch rows for the session — the turn-close
	// cleanup. Deleting session-wide (not per-turn) also purges rows
	// crash-orphaned by earlier turns that never saw a terminal hook.
	Clear(ctx context.Context, sessionID domain.SessionID) bool
}

// ToolOpScratchStore is the SQL-backed ToolOpScratch over migration
// 0011's toolop_scratch table. Clock is optional (updated_at bookkeeping
// only); nil falls back to wall-clock time.
type ToolOpScratchStore struct {
	DB    *sqlite.DB
	Clock domain.Clock
}

var _ ToolOpScratch = (*ToolOpScratchStore)(nil)

func (s *ToolOpScratchStore) now() string {
	if s.Clock != nil {
		return s.Clock.Now().UTC().Format(time.RFC3339Nano)
	}
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// RecordFileOp implements ToolOpScratch: one upsert, +1 per observed op.
func (s *ToolOpScratchStore) RecordFileOp(ctx context.Context, sessionID domain.SessionID, turnID domain.TurnID) bool {
	if s == nil || s.DB == nil {
		return false
	}
	_, err := s.DB.Conn().ExecContext(ctx, `
		INSERT INTO toolop_scratch (session_id, turn_id, file_ops, updated_at)
		VALUES (?, ?, 1, ?)
		ON CONFLICT(session_id, turn_id)
		DO UPDATE SET file_ops = file_ops + 1, updated_at = excluded.updated_at`,
		string(sessionID), string(turnID), s.now(),
	)
	return err == nil
}

// FoldFileOps implements ToolOpScratch.
func (s *ToolOpScratchStore) FoldFileOps(ctx context.Context, sessionID domain.SessionID, turnID domain.TurnID) (int64, bool) {
	if s == nil || s.DB == nil {
		return 0, false
	}
	var ops int64
	err := s.DB.Conn().QueryRowContext(ctx,
		`SELECT file_ops FROM toolop_scratch WHERE session_id = ? AND turn_id = ?`,
		string(sessionID), string(turnID),
	).Scan(&ops)
	if err != nil {
		return 0, false
	}
	return ops, true
}

// Clear implements ToolOpScratch.
func (s *ToolOpScratchStore) Clear(ctx context.Context, sessionID domain.SessionID) bool {
	if s == nil || s.DB == nil {
		return false
	}
	_, err := s.DB.Conn().ExecContext(ctx,
		`DELETE FROM toolop_scratch WHERE session_id = ?`, string(sessionID))
	return err == nil
}

// --- auspex hook claude post-tool-use -------------------------------------

// PostToolUseResult is HandlePostToolUse's return value.
type PostToolUseResult struct {
	// FileOp is true when the payload parsed as a countable view/modify
	// file operation (§7.2 classification with a file target).
	FileOp bool
	// Recorded is true when the scratch increment durably persisted. A
	// FileOp with Recorded=false is the documented degrade (nil/failed
	// scratch), never an error.
	Recorded bool
}

// HandlePostToolUse implements `auspex hook claude post-tool-use` (issue
// #67 slice 3a, ADR-052 approval touch 1): parse the PostToolUse payload
// through the privacy-reducing parser (no path, no tool input content
// ever leaves it — claudehooks.ParsePostToolUse), classify the tool, and
// — for view/modify operations that named a file — record one counter
// increment into the per-turn scratch keyed by the session's open turn.
//
// Fail-open on every rung, like every handler in hooks.go: malformed
// stdin, an ignored tool, a nil scratch store, an unresolvable turn, and
// a failed write all return a zero-ish result and a nil error — this
// hook runs once per tool call inside the user's live turn, and no
// Auspex condition may slow or break that turn beyond one bounded
// counter write. No event is normalized or persisted here by design:
// per-tool-call events would fight ADR-046 retention, and the aggregate
// rides the existing provider.turn.completed at Stop (§7.4).
func HandlePostToolUse(ctx context.Context, deps HookDeps, stdin []byte) (PostToolUseResult, error) {
	parsed, err := claudehooks.ParsePostToolUse(stdin)
	if err != nil {
		return PostToolUseResult{}, nil //nolint:nilerr // deliberate fail-open: malformed hook input must not fail the tool call.
	}
	if !parsed.FileOp() {
		return PostToolUseResult{}, nil
	}
	result := PostToolUseResult{FileOp: true}
	if deps.ToolOps == nil {
		return result, nil
	}
	turnID := deps.resolveOpenTurn(ctx, parsed.SessionID, parsed.TurnID)
	result.Recorded = deps.ToolOps.RecordFileOp(ctx, parsed.SessionID, turnID)
	return result, nil
}

// resolveOpenTurn returns the turn a mid/end-of-turn hook payload belongs
// to: the payload's own turn_id when the provider sent one (none does
// today — parsed defensively), else the session's latest started turn
// (the same resolution stampOpenTurn uses), else "" — an empty key is
// still a consistent accumulate/fold key for sessions Auspex never saw
// start a turn.
func (d HookDeps) resolveOpenTurn(ctx context.Context, sessionID domain.SessionID, payloadTurnID *string) domain.TurnID {
	if payloadTurnID != nil && *payloadTurnID != "" {
		return domain.TurnID(*payloadTurnID)
	}
	if d.OpenTurns == nil {
		return ""
	}
	turnID, ok := d.OpenTurns.LatestStartedTurn(ctx, sessionID)
	if !ok {
		return ""
	}
	return turnID
}

// foldTurnToolOps is HandleStop's enrichment step for the five #67
// aggregates, mirroring how the #72 usage enrichment rides the same
// handler. It always CLEARS the session's scratch (turn close — a
// re-entrant/duplicate Stop folds an empty scratch and stamps nothing,
// so downstream's earliest-terminal-event-per-turn join sees exactly one
// aggregate-bearing event), then returns:
//
//   - nil when capture never ran for this turn (no scratch row): the
//     payload stays byte-identical to the pre-#67 shape;
//   - the full five-field aggregate when the transcript replay succeeds
//     (one internally consistent, main-chain-attributed set);
//   - the hook-counted total-only degrade when it does not (identity
//     needs the replay; the count needs only the hook's own scratch).
func (d HookDeps) foldTurnToolOps(ctx context.Context, parsed claudehooks.StopEvent) *claudetelemetry.TurnToolOps {
	if d.ToolOps == nil {
		return nil
	}
	turnID := d.resolveOpenTurn(ctx, parsed.SessionID, nil)
	hookOps, ok := d.ToolOps.FoldFileOps(ctx, parsed.SessionID, turnID)
	d.ToolOps.Clear(ctx, parsed.SessionID)
	if !ok {
		return nil
	}
	if parsed.TranscriptPath != nil && *parsed.TranscriptPath != "" {
		if ops, replayOK := claudetelemetry.ReadTurnToolOps(*parsed.TranscriptPath); replayOK {
			return &ops
		}
	}
	degraded := claudetelemetry.HookCountedToolOps(hookOps)
	return &degraded
}
