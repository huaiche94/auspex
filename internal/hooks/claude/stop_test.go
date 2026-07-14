package claude

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/huaiche94/auspex/internal/domain"
)

func readStopFixture(t *testing.T, dir, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "provider-events", "claude", dir, name))
	if err != nil {
		t.Fatalf("reading fixture %s/%s: %v", dir, name, err)
	}
	return b
}

func TestParseStop(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		wantErr bool
		check   func(t *testing.T, ev StopEvent)
	}{
		{
			name:    "normal",
			fixture: "normal.json",
			check: func(t *testing.T, ev StopEvent) {
				if ev.SessionID != "sess_01H9X8K7QZ3M4N5P6R7S8T9V0W" {
					t.Errorf("SessionID = %q", ev.SessionID)
				}
				if ev.StopHookActive == nil || *ev.StopHookActive != false {
					t.Errorf("StopHookActive = %v, want false", ev.StopHookActive)
				}
				if ev.TranscriptPath == nil {
					t.Errorf("TranscriptPath = nil, want set")
				}
				// #20 Phase 0: hooks.md documents effort on Stop payloads
				// (tool-use-context events) — the turn-end calibration label.
				if ev.EffortLevel == nil || *ev.EffortLevel != "xhigh" {
					t.Errorf("EffortLevel = %v, want xhigh", ev.EffortLevel)
				}
			},
		},
		{
			name:    "missing_fields",
			fixture: "missing_fields.json",
			check: func(t *testing.T, ev StopEvent) {
				if ev.SessionID != "sess_01H9X8K7QZ3M4N5P6R7S8T9V0X" {
					t.Errorf("SessionID = %q", ev.SessionID)
				}
				if ev.StopHookActive != nil {
					t.Errorf("StopHookActive = %v, want nil (unknown, not false)", *ev.StopHookActive)
				}
				if ev.EffortLevel != nil {
					t.Errorf("EffortLevel = %v, want nil (unknown, not a fabricated level)", *ev.EffortLevel)
				}
				if ev.TranscriptPath != nil {
					t.Errorf("TranscriptPath = %v, want nil", *ev.TranscriptPath)
				}
			},
		},
		{
			name:    "unknown_fields",
			fixture: "unknown_fields.json",
			check: func(t *testing.T, ev StopEvent) {
				if ev.SessionID != "sess_01H9X8K7QZ3M4N5P6R7S8T9V0Z" {
					t.Errorf("SessionID = %q", ev.SessionID)
				}
				if ev.StopHookActive == nil || *ev.StopHookActive != true {
					t.Errorf("StopHookActive = %v, want true", ev.StopHookActive)
				}
			},
		},
		{
			name:    "malformed",
			fixture: "malformed.json",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := ParseStop(readStopFixture(t, "stop", tt.fixture))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				var derr *domain.Error
				if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
					t.Fatalf("expected ErrCodeValidation, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tt.check(t, ev)
		})
	}
}

func TestParseStopFailure(t *testing.T) {
	tests := []struct {
		name       string
		fixture    string
		wantErr    bool
		wantClass  domain.FailureClass
		checkExtra func(t *testing.T, ev StopFailureEvent)
	}{
		{
			name:      "rate_limit",
			fixture:   "rate_limit.json",
			wantClass: domain.FailureProviderRateLimit,
			checkExtra: func(t *testing.T, ev StopFailureEvent) {
				if ev.RawStatusCode == nil || *ev.RawStatusCode != 429 {
					t.Errorf("RawStatusCode = %v, want 429", ev.RawStatusCode)
				}
				if ev.RawErrorType == nil || *ev.RawErrorType != "rate_limit_error" {
					t.Errorf("RawErrorType = %v, want rate_limit_error", ev.RawErrorType)
				}
			},
		},
		{
			name:      "overloaded",
			fixture:   "overloaded.json",
			wantClass: domain.FailureProviderInternal,
		},
		{
			name:      "context_length",
			fixture:   "context_length.json",
			wantClass: domain.FailureContext,
		},
		{
			name:      "network",
			fixture:   "network.json",
			wantClass: domain.FailureNetwork,
		},
		{
			name:      "unknown_category",
			fixture:   "unknown_category.json",
			wantClass: domain.FailureUnknown,
		},
		{
			name:      "missing_fields",
			fixture:   "missing_fields.json",
			wantClass: domain.FailureUnknown,
			checkExtra: func(t *testing.T, ev StopFailureEvent) {
				if ev.RawErrorType != nil {
					t.Errorf("RawErrorType = %v, want nil", *ev.RawErrorType)
				}
			},
		},
		{
			name:    "malformed",
			fixture: "malformed.json",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := ParseStopFailure(readStopFixture(t, "stopfailure", tt.fixture))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				var derr *domain.Error
				if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
					t.Fatalf("expected ErrCodeValidation, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ev.FailureClass != tt.wantClass {
				t.Errorf("FailureClass = %q, want %q", ev.FailureClass, tt.wantClass)
			}
			if tt.checkExtra != nil {
				tt.checkExtra(t, ev)
			}
		})
	}
}

// TestParseStopFailure_ErrorMessageNotRetained asserts that the raw error
// message text (which could theoretically echo back sensitive data from the
// provider) is never retained verbatim on StopFailureEvent - only its
// length and the classified FailureClass survive. Same privacy discipline
// as claude-provider-02's raw-prompt-never-persisted assertion.
func TestParseStopFailure_ErrorMessageNotRetained(t *testing.T) {
	const rawMessage = "SECRET-CANARY-message-body-should-not-be-retained"
	raw := []byte(`{
		"session_id": "sess_privacy",
		"hook_event_name": "StopFailure",
		"error": {
			"type": "rate_limit_error",
			"message": "` + rawMessage + `",
			"status_code": 429
		}
	}`)
	ev, err := ParseStopFailure(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.FailureClass != domain.FailureProviderRateLimit {
		t.Errorf("FailureClass = %q", ev.FailureClass)
	}
	if ev.ErrorMessageLen != len(rawMessage) {
		t.Errorf("ErrorMessageLen = %d, want %d", ev.ErrorMessageLen, len(rawMessage))
	}
	// StopFailureEvent has no string field capable of holding the message
	// itself (only RawErrorType, a short taxonomy string, and
	// ErrorMessageLen, an int) - reflect over every string field as an
	// explicit runtime guarantee, not just a structural argument.
	assertNoRawText(t, reflect.ValueOf(ev), rawMessage)
}

func TestClassifyFailure_StatusCodeFallback(t *testing.T) {
	code500 := int64(500)
	if got := classifyFailure("api_error", "internal server error", &code500); got != domain.FailureProviderInternal {
		t.Errorf("classifyFailure(500) = %q, want provider_internal", got)
	}
	code403 := int64(403)
	if got := classifyFailure("", "forbidden", &code403); got != domain.FailurePermission {
		t.Errorf("classifyFailure(403) = %q, want permission", got)
	}
	if got := classifyFailure("", "", nil); got != domain.FailureUnknown {
		t.Errorf("classifyFailure(empty) = %q, want unknown", got)
	}
}
