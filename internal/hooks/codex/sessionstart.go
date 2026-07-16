// Package codex parses Codex CLI's native lifecycle-hook stdin payloads
// (SessionStart, UserPromptSubmit, Stop) and encodes the provider-compatible
// stdout responses those hooks expect back — the issue-#9 Phase 1 analog of
// internal/hooks/claude. Codex CLI (v0.144.x) ships a deliberately
// Claude-Code-compatible hook protocol: hooks.json uses the same
// matcher/type:"command"/timeout schema, hook stdin is a single JSON object
// carrying session_id/hook_event_name/cwd/transcript_path/permission_mode/
// model (payload shapes pinned against the JSON schemas embedded in the
// codex v0.144.4 binary: session-start.command.input,
// user-prompt-submit.command.input, stop.command.input), and stdout
// decisions use the same decision/reason/hookSpecificOutput wire shape.
// Two Codex extensions matter here: hook payloads carry the provider's own
// turn_id (so Auspex never has to mint or resolve one for turn-scoped
// events), and transcript_path points at the session's rollout JSONL under
// $CODEX_HOME/sessions (verified against codex-rs rust-v0.144.4:
// Session::hook_transcript_path returns current_rollout_path, nullable when
// the rollout has not been materialized).
//
// Like internal/hooks/claude, this package stops at parsing/encoding;
// producing the frozen pkg/protocol/v1.Event envelope is
// internal/telemetry/codex's job. The same privacy contract applies
// verbatim (Constitution §7 rule 2): raw prompt text is hashed in
// ParseUserPromptSubmit's own stack frame and never retained, and the Stop
// payload's last_assistant_message — raw response text — is never copied
// out of the transient decode struct at all.
package codex

import (
	"encoding/json"
	"fmt"

	"github.com/huaiche94/auspex/internal/domain"
)

// SessionStartSource is Codex's session-start trigger enum
// (session-start.command.input's source field: startup, resume, clear,
// compact). Kept as a plain string so an enum value a future Codex adds
// flows through untouched rather than being dropped (issue #21's
// unknown-window lesson applied to enums).
type SessionStartSource = string

const (
	SessionStartStartup SessionStartSource = "startup"
	SessionStartResume  SessionStartSource = "resume"
	SessionStartClear   SessionStartSource = "clear"
	SessionStartCompact SessionStartSource = "compact"
)

// SessionStartEvent is the parsed, privacy-safe representation of a Codex
// SessionStart hook payload. Optional fields are pointers per the
// repository-wide rule: nil means the payload did not carry the field —
// unknown, never a substituted zero (transcript_path is nullable on the
// wire even when present as a key).
type SessionStartEvent struct {
	SessionID      domain.SessionID
	Source         SessionStartSource // "" when absent
	CWD            *string
	TranscriptPath *string
	Model          *string
	PermissionMode *string
}

type rawSessionStart struct {
	SessionID      string  `json:"session_id"`
	HookEventName  string  `json:"hook_event_name"`
	Source         *string `json:"source"`
	CWD            *string `json:"cwd"`
	TranscriptPath *string `json:"transcript_path"`
	Model          *string `json:"model"`
	PermissionMode *string `json:"permission_mode"`
}

// ParseSessionStart parses a Codex SessionStart hook stdin payload,
// tolerating unknown fields (encoding/json ignores what we don't decode).
func ParseSessionStart(raw []byte) (SessionStartEvent, error) {
	var r rawSessionStart
	if err := json.Unmarshal(raw, &r); err != nil {
		return SessionStartEvent{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   fmt.Sprintf("codex sessionstart: invalid JSON: %v", err),
			Retryable: false,
		}
	}
	if r.SessionID == "" {
		return SessionStartEvent{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "codex sessionstart: missing session_id",
			Retryable: false,
		}
	}

	ev := SessionStartEvent{
		SessionID:      domain.SessionID(r.SessionID),
		CWD:            r.CWD,
		TranscriptPath: r.TranscriptPath,
		Model:          r.Model,
		PermissionMode: r.PermissionMode,
	}
	if r.Source != nil {
		ev.Source = *r.Source
	}
	return ev, nil
}
