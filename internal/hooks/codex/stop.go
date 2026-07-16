package codex

import (
	"encoding/json"
	"fmt"

	"github.com/huaiche94/auspex/internal/domain"
)

// StopEvent is the parsed representation of a Codex Stop hook payload — a
// clean turn stop (stop.command.input in the codex v0.144.4 embedded
// schemas; Codex has no StopFailure analog today, failures surface as the
// turn simply ending).
//
// Privacy note: the wire payload carries last_assistant_message — RAW
// response text. It is deliberately absent from this struct: it is decoded
// transiently (encoding/json must consume the field) and dropped in
// ParseStop's stack frame, never copied out, not even as a length
// (Constitution §7 rule 2; nothing downstream needs it).
type StopEvent struct {
	SessionID      domain.SessionID
	TurnID         domain.TurnID // Codex extension; "" when absent
	TranscriptPath *string       // the session rollout JSONL (nullable on the wire)
	CWD            *string
	Model          *string
	PermissionMode *string

	// StopHookActive reports whether Codex is already continuing because
	// of a previous Stop hook's block decision. nil means the field was
	// absent/null — unknown, not false.
	StopHookActive *bool
}

type rawStop struct {
	SessionID      string  `json:"session_id"`
	HookEventName  string  `json:"hook_event_name"`
	TurnID         *string `json:"turn_id"`
	TranscriptPath *string `json:"transcript_path"`
	CWD            *string `json:"cwd"`
	Model          *string `json:"model"`
	PermissionMode *string `json:"permission_mode"`
	StopHookActive *bool   `json:"stop_hook_active"`
	// last_assistant_message is intentionally NOT decoded: we never need
	// it, and not naming the field at all means the raw response text is
	// skipped by encoding/json without ever being copied into a Go string
	// this package could accidentally retain.
}

// ParseStop parses a Codex Stop hook stdin payload, tolerating unknown
// fields.
func ParseStop(raw []byte) (StopEvent, error) {
	var r rawStop
	if err := json.Unmarshal(raw, &r); err != nil {
		return StopEvent{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   fmt.Sprintf("codex stop: invalid JSON: %v", err),
			Retryable: false,
		}
	}
	if r.SessionID == "" {
		return StopEvent{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "codex stop: missing session_id",
			Retryable: false,
		}
	}

	ev := StopEvent{
		SessionID:      domain.SessionID(r.SessionID),
		TranscriptPath: r.TranscriptPath,
		CWD:            r.CWD,
		Model:          r.Model,
		PermissionMode: r.PermissionMode,
		StopHookActive: r.StopHookActive,
	}
	if r.TurnID != nil {
		ev.TurnID = domain.TurnID(*r.TurnID)
	}
	return ev, nil
}
