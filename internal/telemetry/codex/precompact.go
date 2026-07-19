// precompact.go projects parsed Codex PreCompact/PostCompact hook events
// (internal/hooks/codex/precompact.go, issue #114) into the frozen
// provider.session.compacted event — the codex sibling of
// internal/telemetry/claude/precompact.go, sharing the same "phase"
// payload vocabulary ("pre"/"post") so downstream compaction counting
// (features.SessionFeatures.CompactionCount) is provider-independent.
// NormalizeSessionStart's source="compact" mapping (a phase-less
// provider.session.compacted) remains the one compaction signal the
// pinned Codex v0.144.4 verifiably emits today — see the capability note
// in internal/hooks/codex/precompact.go.
package codex

import (
	"time"

	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"

	codexhooks "github.com/huaiche94/auspex/internal/hooks/codex"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// CompactionCheckpoint mirrors internal/telemetry/claude's
// CompactionCheckpoint (the checkpoint-capture outcome stamped onto a
// pre-compaction event). Aliased rather than re-declared so the
// orchestrator hands one record type to either provider's normalizer and
// the payload vocabulary cannot drift between them.
type CompactionCheckpoint = claudetelemetry.CompactionCheckpoint

// NormalizePreCompact projects a parsed PreCompactEvent into a
// provider.session.compacted Event with phase "pre". ckpt, when non-nil,
// is the pre-compaction checkpoint capture's outcome (nil = capture not
// configured; no checkpoint keys stamped). Payload keys carry only enum
// values, ids, lengths, and paths — the same discipline as
// NormalizeSessionStart.
func (n *Normalizer) NormalizePreCompact(ev codexhooks.PreCompactEvent, observedAt time.Time, ckpt *CompactionCheckpoint) v1.Event {
	out := n.envelope(v1.EventProviderSessionCompacted, observedAt, ev.SessionID)
	out.IdempotencyKey = digestKey("codex.precompact", string(ev.SessionID), observedAt.UTC().Format(time.RFC3339Nano))
	payload := compactionPayload(claudetelemetry.CompactionPhasePre, ev.Trigger, ev.Model, ev.PermissionMode, ev.CWD)
	stampCompactionCheckpoint(payload, ckpt)
	out.Payload = payload
	return out
}

// NormalizePostCompact projects a parsed PostCompactEvent into a
// provider.session.compacted Event with phase "post". No checkpoint
// parameter — the checkpoint moment is PreCompact by design (ADD §21.10).
func (n *Normalizer) NormalizePostCompact(ev codexhooks.PostCompactEvent, observedAt time.Time) v1.Event {
	out := n.envelope(v1.EventProviderSessionCompacted, observedAt, ev.SessionID)
	out.IdempotencyKey = digestKey("codex.postcompact", string(ev.SessionID), observedAt.UTC().Format(time.RFC3339Nano))
	out.Payload = compactionPayload(claudetelemetry.CompactionPhasePost, ev.Trigger, ev.Model, ev.PermissionMode, ev.CWD)
	return out
}

// compactionPayload assembles the shared pre/post payload: the phase
// marker plus whichever optional identity/context fields the payload
// actually carried (unknown is not zero — absent fields stamp nothing).
func compactionPayload(phase string, trigger, model, permissionMode, cwd *string) map[string]any {
	payload := map[string]any{"phase": phase}
	if trigger != nil && *trigger != "" {
		payload["trigger"] = *trigger // provider enum value
	}
	if model != nil && *model != "" {
		payload["model_id"] = *model
	}
	if permissionMode != nil && *permissionMode != "" {
		payload["permission_mode"] = *permissionMode // provider enum value
	}
	if cwd != nil && *cwd != "" {
		payload["cwd"] = *cwd // a path, same allowance as sessionstart
	}
	return payload
}

// stampCompactionCheckpoint mirrors internal/telemetry/claude's helper of
// the same name over the shared CompactionCheckpoint record (the alias
// above): nil stamps nothing; otherwise checkpoint_captured is always
// present, IDs on success, skip reason on failure.
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
