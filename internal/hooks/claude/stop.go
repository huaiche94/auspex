package claude

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/huaiche94/auspex/internal/domain"
)

// StopEvent is the parsed representation of a Claude Code Stop hook
// payload — a clean turn/session stop with no failure.
type StopEvent struct {
	SessionID      domain.SessionID
	TranscriptPath *string
	CWD            *string

	// StopHookActive reports whether Claude Code is already continuing
	// because of a previous Stop hook's block decision (present in the
	// provider payload as stop_hook_active). nil means the field was
	// absent/null in the source payload — unknown, not false.
	StopHookActive *bool
}

type rawStop struct {
	SessionID      string  `json:"session_id"`
	TranscriptPath *string `json:"transcript_path"`
	CWD            *string `json:"cwd"`
	HookEventName  string  `json:"hook_event_name"`
	StopHookActive *bool   `json:"stop_hook_active"`
}

// ParseStop parses a Claude Code Stop hook stdin payload, tolerating
// unknown fields.
func ParseStop(raw []byte) (StopEvent, error) {
	var r rawStop
	if err := json.Unmarshal(raw, &r); err != nil {
		return StopEvent{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   fmt.Sprintf("claude stop: invalid JSON: %v", err),
			Retryable: false,
		}
	}

	if r.SessionID == "" {
		return StopEvent{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "claude stop: missing session_id",
			Retryable: false,
		}
	}

	return StopEvent{
		SessionID:      domain.SessionID(r.SessionID),
		TranscriptPath: r.TranscriptPath,
		CWD:            r.CWD,
		StopHookActive: r.StopHookActive,
	}, nil
}

// StopFailureEvent is the parsed representation of a Claude Code
// StopFailure hook payload — a turn/session stop caused by an error.
// FailureClass is mapped from the provider's raw error type/status/message
// into the frozen domain.FailureClass enum (internal/domain/failure.go).
type StopFailureEvent struct {
	SessionID      domain.SessionID
	TranscriptPath *string
	CWD            *string

	RawErrorType    *string
	RawStatusCode   *int64
	ErrorMessageLen int // length only; raw error message text is not retained beyond classification.

	FailureClass domain.FailureClass
}

type rawStopFailure struct {
	SessionID      string  `json:"session_id"`
	TranscriptPath *string `json:"transcript_path"`
	CWD            *string `json:"cwd"`
	HookEventName  string  `json:"hook_event_name"`
	Error          *struct {
		Type       *string `json:"type"`
		Message    *string `json:"message"`
		StatusCode *int64  `json:"status_code"`
	} `json:"error"`
}

// ParseStopFailure parses a Claude Code StopFailure hook stdin payload,
// tolerating unknown fields, and classifies the failure into the frozen
// domain.FailureClass enum.
func ParseStopFailure(raw []byte) (StopFailureEvent, error) {
	var r rawStopFailure
	if err := json.Unmarshal(raw, &r); err != nil {
		return StopFailureEvent{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   fmt.Sprintf("claude stopfailure: invalid JSON: %v", err),
			Retryable: false,
		}
	}

	if r.SessionID == "" {
		return StopFailureEvent{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "claude stopfailure: missing session_id",
			Retryable: false,
		}
	}

	ev := StopFailureEvent{
		SessionID:      domain.SessionID(r.SessionID),
		TranscriptPath: r.TranscriptPath,
		CWD:            r.CWD,
		FailureClass:   domain.FailureUnknown,
	}

	if r.Error != nil {
		ev.RawErrorType = r.Error.Type
		ev.RawStatusCode = r.Error.StatusCode
		if r.Error.Message != nil {
			ev.ErrorMessageLen = len(*r.Error.Message)
		}

		var errType, errMsg string
		if r.Error.Type != nil {
			errType = *r.Error.Type
		}
		if r.Error.Message != nil {
			errMsg = *r.Error.Message
		}
		ev.FailureClass = classifyFailure(errType, errMsg, r.Error.StatusCode)
	}

	return ev, nil
}

// classifyFailure maps Claude/Anthropic API error shapes into Auspex's
// frozen domain.FailureClass enum. This mapping is Auspex's own
// heuristic (not part of any frozen contract) and may need refinement once
// real StopFailure payloads are observed against a live account — see the
// progress artifact's "assumptions" for this wave.
func classifyFailure(errType, message string, statusCode *int64) domain.FailureClass {
	t := strings.ToLower(errType)
	m := strings.ToLower(message)

	switch {
	case t == "rate_limit_error" || strings.Contains(t, "rate_limit") || (statusCode != nil && *statusCode == 429):
		return domain.FailureProviderRateLimit
	case t == "overloaded_error" || strings.Contains(t, "overload") || (statusCode != nil && *statusCode == 529):
		return domain.FailureProviderInternal
	case strings.Contains(m, "prompt is too long") || strings.Contains(m, "context") && strings.Contains(m, "too long") || strings.Contains(m, "maximum context"):
		return domain.FailureContext
	case t == "connection_error" || strings.Contains(t, "connection") || strings.Contains(t, "network"):
		return domain.FailureNetwork
	case t == "permission_error" || (statusCode != nil && *statusCode == 403):
		return domain.FailurePermission
	case t == "authentication_error" || (statusCode != nil && *statusCode == 401):
		return domain.FailurePermission
	case t == "timeout_error" || strings.Contains(t, "timeout"):
		return domain.FailureTimeout
	case t == "api_error" || (statusCode != nil && *statusCode >= 500 && *statusCode < 600):
		return domain.FailureProviderInternal
	default:
		return domain.FailureUnknown
	}
}
