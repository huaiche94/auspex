// precompact.go projects parsed PreCompact/PostCompact hook events
// (internal/hooks/claude/precompact.go, issue #114) into the frozen
// provider.session.compacted event — the EventType the closed taxonomy
// already carries for compaction (pkg/protocol/v1.
// EventProviderSessionCompacted; no new type is invented, per this
// package's own doc-comment rule). The two hook moments share the one
// type and are distinguished by the payload's "phase" key: "pre" (about
// to compact — the checkpoint moment, ADD §22.4 "`PreCompact` 一律
// capture state checkpoint") vs "post" (compaction finished). Consumers
// counting COMPACTIONS (features.SessionFeatures.CompactionCount,
// internal/evaluation.SQLDataSource.Session) count non-"post" events so
// a provider that someday reports both moments for one compaction is not
// double-counted.
package claude

import (
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	claudehooks "github.com/huaiche94/auspex/internal/hooks/claude"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// Compaction phase payload values (the "phase" key on
// provider.session.compacted events this package emits).
const (
	// CompactionPhasePre marks the PreCompact observation — recorded
	// BEFORE the provider's compaction ran.
	CompactionPhasePre = "pre"
	// CompactionPhasePost marks the PostCompact observation.
	CompactionPhasePost = "post"
)

// CompactionCheckpoint is the outcome of the pre-compaction State
// Checkpoint capture (internal/orchestrator/hooksprecompact.go), recorded
// onto the PreCompact event's payload so the durable event log says
// whether a durable state snapshot actually preceded each compaction.
// Declared here (not imported from internal/orchestrator) because the
// dependency arrow points the other way: the orchestrator consumes this
// package. IDs and enum-ish reason strings only — no content.
type CompactionCheckpoint struct {
	// Captured is true when BOTH the state and repository checkpoints
	// were durably created before the compaction proceeded.
	Captured bool
	// StateCheckpointID / RepositoryCheckpointID identify the created
	// checkpoints when Captured. Empty otherwise.
	StateCheckpointID      string
	RepositoryCheckpointID string
	// SkipReason is a short machine-readable reason when not Captured
	// (e.g. "no_task", "checkpoint_failed"). Never user/provider text.
	SkipReason string
}

// NormalizePreCompact projects a parsed PreCompactEvent into a
// provider.session.compacted Event with phase "pre". ckpt, when non-nil,
// is the just-attempted checkpoint capture's outcome (nil means capture
// was not configured at all — HookDeps.CompactCheckpoint nil — and no
// checkpoint keys are stamped: unknown is not zero). Per Constitution §7
// rule 2 only derived signals reach the payload: the trigger enum, the
// custom-instructions byte LENGTH (raw text never left the parser), the
// cwd path (the same allowance every hook event has), and the checkpoint
// outcome's IDs/reason.
func (n *Normalizer) NormalizePreCompact(ev claudehooks.PreCompactEvent, observedAt time.Time, ckpt *CompactionCheckpoint) v1.Event {
	out := n.envelope(v1.EventProviderSessionCompacted, observedAt, ev.SessionID)
	out.Source = string(domain.SourceHook)
	out.IdempotencyKey = digestKey("precompact", string(ev.SessionID), observedAt.UTC().Format(time.RFC3339Nano))

	payload := map[string]any{
		"phase": CompactionPhasePre,
	}
	if ev.Trigger != nil && *ev.Trigger != "" {
		payload["trigger"] = *ev.Trigger // provider enum value, not user text
	}
	if ev.CustomInstructionsLen != nil {
		payload["custom_instructions_len"] = *ev.CustomInstructionsLen
	}
	if ev.CWD != nil && *ev.CWD != "" {
		payload["cwd"] = *ev.CWD // a path, same allowance as turn.started
	}
	stampCompactionCheckpoint(payload, ckpt)
	out.Payload = payload
	return out
}

// NormalizePostCompact projects a parsed PostCompactEvent into a
// provider.session.compacted Event with phase "post". No checkpoint
// parameter: the checkpoint moment is PreCompact by design (ADD §22.4) —
// after compaction the pre-compaction context is already gone.
func (n *Normalizer) NormalizePostCompact(ev claudehooks.PostCompactEvent, observedAt time.Time) v1.Event {
	out := n.envelope(v1.EventProviderSessionCompacted, observedAt, ev.SessionID)
	out.Source = string(domain.SourceHook)
	out.IdempotencyKey = digestKey("postcompact", string(ev.SessionID), observedAt.UTC().Format(time.RFC3339Nano))

	payload := map[string]any{
		"phase": CompactionPhasePost,
	}
	if ev.Trigger != nil && *ev.Trigger != "" {
		payload["trigger"] = *ev.Trigger
	}
	if ev.CWD != nil && *ev.CWD != "" {
		payload["cwd"] = *ev.CWD
	}
	out.Payload = payload
	return out
}

// stampCompactionCheckpoint records a checkpoint-capture outcome onto a
// pre-compaction payload. nil stamps nothing (capture not configured);
// otherwise checkpoint_captured is always present, with the IDs on
// success and the skip reason on failure — so the event log never shows
// a bare "false" with no explanation.
func stampCompactionCheckpoint(payload map[string]any, ckpt *CompactionCheckpoint) {
	if ckpt == nil {
		return
	}
	payload["checkpoint_captured"] = ckpt.Captured
	if ckpt.StateCheckpointID != "" {
		payload["state_checkpoint_id"] = ckpt.StateCheckpointID
	}
	if ckpt.RepositoryCheckpointID != "" {
		payload["repository_checkpoint_id"] = ckpt.RepositoryCheckpointID
	}
	if !ckpt.Captured && ckpt.SkipReason != "" {
		payload["checkpoint_skip_reason"] = ckpt.SkipReason
	}
}
