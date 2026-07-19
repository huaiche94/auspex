// precompact.go parses the Codex PreCompact/PostCompact hook payloads ADD
// Appendix E.1 drafts entry points for (`auspex hook codex pre-compact` /
// `post-compact`; §21.10 "`PreCompact` 前建立 State Checkpoint") — issue
// #114's codex half.
//
// CAPABILITY NOTE (issue #114 audit, do not fake capability): the Codex
// CLI version this adapter is pinned against (v0.144.4 — see
// internal/providers/codex.CapabilityReader) embeds hook schemas for
// SessionStart/UserPromptSubmit/Stop ONLY; no pre-compact/post-compact
// hook event is verified to exist there today. Codex compaction is
// currently observable only as SessionStart with source "compact"
// (sessionstart.go -> provider.session.compacted). These parsers implement
// the payload shape the ADD specifies — the same
// session_id/hook_event_name/cwd/transcript_path/model/permission_mode
// envelope every verified Codex hook carries, plus PreCompact's trigger
// enum — so the drafted hooks.json entries work the day Codex ships the
// events; the entries are deliberately NOT added to
// integrations/codex/hooks.json until a Codex version verifiably emits
// them.
package codex

import (
	"encoding/json"
	"fmt"

	"github.com/huaiche94/auspex/internal/domain"
)

// PreCompactEvent is the parsed, privacy-safe representation of a Codex
// PreCompact hook payload — the moment BEFORE the provider compacts the
// session context. Optional fields are pointers per the repository-wide
// rule: nil means the payload did not carry the field.
type PreCompactEvent struct {
	SessionID      domain.SessionID
	CWD            *string
	TranscriptPath *string
	Model          *string
	PermissionMode *string

	// Trigger is the compaction-trigger enum ("manual" | "auto"),
	// mirroring internal/hooks/claude's PreCompact trigger. nil means the
	// payload carried none.
	Trigger *string
}

type rawCompact struct {
	SessionID      string  `json:"session_id"`
	HookEventName  string  `json:"hook_event_name"`
	CWD            *string `json:"cwd"`
	TranscriptPath *string `json:"transcript_path"`
	Model          *string `json:"model"`
	PermissionMode *string `json:"permission_mode"`
	Trigger        *string `json:"trigger"`
}

// ParsePreCompact parses a Codex PreCompact hook stdin payload, tolerating
// unknown fields.
func ParsePreCompact(raw []byte) (PreCompactEvent, error) {
	r, err := parseCompactEnvelope(raw, "precompact")
	if err != nil {
		return PreCompactEvent{}, err
	}
	return PreCompactEvent{
		SessionID:      domain.SessionID(r.SessionID),
		CWD:            r.CWD,
		TranscriptPath: r.TranscriptPath,
		Model:          r.Model,
		PermissionMode: r.PermissionMode,
		Trigger:        r.Trigger,
	}, nil
}

// PostCompactEvent is the parsed representation of a Codex PostCompact
// hook payload — the moment AFTER compaction replaced the session context
// (ADD §21.10's inject-concise-context point; this branch's scope stops at
// observing it). Same capability note as the file doc comment.
type PostCompactEvent struct {
	SessionID      domain.SessionID
	CWD            *string
	TranscriptPath *string
	Model          *string
	PermissionMode *string
	Trigger        *string
}

// ParsePostCompact parses a Codex PostCompact hook stdin payload,
// tolerating unknown fields.
func ParsePostCompact(raw []byte) (PostCompactEvent, error) {
	r, err := parseCompactEnvelope(raw, "postcompact")
	if err != nil {
		return PostCompactEvent{}, err
	}
	return PostCompactEvent{
		SessionID:      domain.SessionID(r.SessionID),
		CWD:            r.CWD,
		TranscriptPath: r.TranscriptPath,
		Model:          r.Model,
		PermissionMode: r.PermissionMode,
		Trigger:        r.Trigger,
	}, nil
}

// parseCompactEnvelope is the shared decode+validate core for the two
// compaction payloads (identical envelopes; only the hook_event_name —
// which neither parser branches on — differs).
func parseCompactEnvelope(raw []byte, which string) (rawCompact, error) {
	var r rawCompact
	if err := json.Unmarshal(raw, &r); err != nil {
		return rawCompact{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   fmt.Sprintf("codex %s: invalid JSON: %v", which, err),
			Retryable: false,
		}
	}
	if r.SessionID == "" {
		return rawCompact{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   fmt.Sprintf("codex %s: missing session_id", which),
			Retryable: false,
		}
	}
	return r, nil
}
