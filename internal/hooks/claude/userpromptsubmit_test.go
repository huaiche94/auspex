package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/huaiche94/auspex/internal/domain"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "provider-events", "claude", "userpromptsubmit", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return b
}

func TestParseUserPromptSubmit(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		wantErr bool
		check   func(t *testing.T, ev UserPromptSubmitEvent)
	}{
		{
			name:    "normal",
			fixture: "normal.json",
			check: func(t *testing.T, ev UserPromptSubmitEvent) {
				if ev.SessionID != "sess_01H9X8K7QZ3M4N5P6R7S8T9V0W" {
					t.Errorf("SessionID = %q", ev.SessionID)
				}
				wantHash := sha256.Sum256([]byte("Refactor the checkpoint manifest writer to use atomic rename."))
				if ev.PromptSHA256 != hex.EncodeToString(wantHash[:]) {
					t.Errorf("PromptSHA256 mismatch")
				}
				if ev.PromptByteLength != len("Refactor the checkpoint manifest writer to use atomic rename.") {
					t.Errorf("PromptByteLength = %d", ev.PromptByteLength)
				}
				if ev.TranscriptPath == nil || !strings.Contains(*ev.TranscriptPath, "sess_01H9X8K7QZ3M4N5P6R7S8T9V0W") {
					t.Errorf("TranscriptPath = %v", ev.TranscriptPath)
				}
				// Issue #42: the full derived feature set is computed
				// here, where the raw text lives — the fixture prompt
				// ("Refactor the checkpoint manifest writer to use
				// atomic rename.") must yield a real refactor-verb
				// signal, and the embedded features must agree with the
				// legacy top-level fields (one derivation, not two).
				if !ev.Features.HasRefactorVerb {
					t.Error("Features.HasRefactorVerb = false, want true for a 'Refactor ...' prompt")
				}
				if ev.Features.HasFixVerb {
					t.Error("Features.HasFixVerb = true, want false (no fix vocabulary in fixture prompt)")
				}
				if ev.Features.SHA256Hex != ev.PromptSHA256 {
					t.Errorf("Features.SHA256Hex = %q, want the same digest as PromptSHA256 %q", ev.Features.SHA256Hex, ev.PromptSHA256)
				}
				if ev.Features.ByteLength != ev.PromptByteLength {
					t.Errorf("Features.ByteLength = %d, want PromptByteLength %d", ev.Features.ByteLength, ev.PromptByteLength)
				}
				if ev.Features.ApproxTokens != ev.PromptApproxTokens {
					t.Errorf("Features.ApproxTokens = %d, want PromptApproxTokens %d", ev.Features.ApproxTokens, ev.PromptApproxTokens)
				}
			},
		},
		{
			name:    "missing_fields",
			fixture: "missing_fields.json",
			check: func(t *testing.T, ev UserPromptSubmitEvent) {
				if ev.SessionID != "sess_01H9X8K7QZ3M4N5P6R7S8T9V0X" {
					t.Errorf("SessionID = %q", ev.SessionID)
				}
				if ev.TranscriptPath != nil {
					t.Errorf("TranscriptPath = %v, want nil", *ev.TranscriptPath)
				}
				if ev.CWD != nil {
					t.Errorf("CWD = %v, want nil", *ev.CWD)
				}
			},
		},
		{
			name:    "unknown_fields",
			fixture: "unknown_fields.json",
			check: func(t *testing.T, ev UserPromptSubmitEvent) {
				if ev.SessionID != "sess_01H9X8K7QZ3M4N5P6R7S8T9V0Z" {
					t.Errorf("SessionID = %q", ev.SessionID)
				}
			},
		},
		{
			name:    "empty_prompt",
			fixture: "empty_prompt.json",
			check: func(t *testing.T, ev UserPromptSubmitEvent) {
				if ev.PromptByteLength != 0 {
					t.Errorf("PromptByteLength = %d, want 0", ev.PromptByteLength)
				}
				if ev.PromptApproxTokens != 0 {
					t.Errorf("PromptApproxTokens = %d, want 0", ev.PromptApproxTokens)
				}
				wantHash := sha256.Sum256([]byte(""))
				if ev.PromptSHA256 != hex.EncodeToString(wantHash[:]) {
					t.Errorf("PromptSHA256 mismatch for empty prompt")
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
			ev, err := ParseUserPromptSubmit(readFixture(t, tt.fixture))
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

// TestParseUserPromptSubmit_RawPromptNeverPersisted is the required privacy
// assertion (packet "Tests" section / Constitution §7 rule 2): raw prompt
// text must never appear as a field value anywhere in the parsed struct.
// It reflects over every string field of UserPromptSubmitEvent and asserts
// none of them contain (or equal) the raw prompt text.
func TestParseUserPromptSubmit_RawPromptNeverPersisted(t *testing.T) {
	const rawPrompt = "SECRET-CANARY-do-not-persist-this-exact-string-42"
	payload := map[string]any{
		"session_id":      "sess_privacy_test",
		"transcript_path": "/Users/dev/.claude/projects/x/sess_privacy_test.jsonl",
		"cwd":             "/Users/dev/projects/x",
		"hook_event_name": "UserPromptSubmit",
		"prompt":          rawPrompt,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal fixture payload: %v", err)
	}

	ev, err := ParseUserPromptSubmit(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertNoRawText(t, reflect.ValueOf(ev), rawPrompt)

	// Also assert %+v formatting (as would appear in a log line) never
	// leaks the raw text, since fmt.Sprintf is a common accidental leak path.
	if strings.Contains(fmt.Sprintf("%+v", ev), rawPrompt) {
		t.Fatalf("raw prompt leaked via %%+v formatting of UserPromptSubmitEvent")
	}

	if ev.PromptSHA256 == "" {
		t.Fatal("expected non-empty PromptSHA256")
	}
	if ev.PromptSHA256 == rawPrompt {
		t.Fatal("PromptSHA256 must not equal the raw prompt")
	}
}

func assertNoRawText(t *testing.T, v reflect.Value, needle string) {
	t.Helper()
	switch v.Kind() {
	case reflect.String:
		if strings.Contains(v.String(), needle) {
			t.Fatalf("field of kind string contains raw prompt text: %q", v.String())
		}
	case reflect.Pointer:
		if !v.IsNil() {
			assertNoRawText(t, v.Elem(), needle)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			assertNoRawText(t, v.Field(i), needle)
		}
	}
}

func TestEncodeUserPromptSubmitResponse_Allow(t *testing.T) {
	got, err := EncodeUserPromptSubmitResponse(UserPromptSubmitResponse{Decision: HookDecisionAllow})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := readFixture(t, "response_allow.golden.json")
	assertJSONEqual(t, got, want)
}

func TestEncodeUserPromptSubmitResponse_Block(t *testing.T) {
	got, err := EncodeUserPromptSubmitResponse(UserPromptSubmitResponse{
		Decision:          HookDecisionBlock,
		Reason:            "Auspex evaluation eval_123 requires a checkpoint or explicit override before this task starts.",
		AdditionalContext: "Use the durable Auspex Progress Tree and checkpoint policy.",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := readFixture(t, "response_block.golden.json")
	assertJSONEqual(t, got, want)
}

func TestEncodeUserPromptSubmitResponse_UnknownDecision(t *testing.T) {
	_, err := EncodeUserPromptSubmitResponse(UserPromptSubmitResponse{Decision: "maybe"})
	if err == nil {
		t.Fatal("expected error for unknown decision")
	}
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("expected ErrCodeValidation, got %v", err)
	}
}

func TestFallbackAllowResponse_IsValidJSON(t *testing.T) {
	b := FallbackAllowResponse()
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("FallbackAllowResponse produced invalid JSON: %v", err)
	}
	if _, blocked := v["decision"]; blocked {
		t.Fatalf("FallbackAllowResponse must never block, got %s", b)
	}
}

func TestParseUserPromptSubmit_MalformedProducesFallbackFlow(t *testing.T) {
	// Simulates the hook wrapper's behavior: on parse failure, use
	// FallbackAllowResponse so Claude Code always receives valid JSON.
	_, err := ParseUserPromptSubmit(readFixture(t, "malformed.json"))
	if err == nil {
		t.Fatal("expected parse error for malformed fixture")
	}
	fallback := FallbackAllowResponse()
	var v map[string]any
	if jsonErr := json.Unmarshal(fallback, &v); jsonErr != nil {
		t.Fatalf("fallback response is not valid JSON: %v", jsonErr)
	}
}

func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()
	var gv, wv any
	if err := json.Unmarshal(got, &gv); err != nil {
		t.Fatalf("got is not valid JSON: %v\n%s", err, got)
	}
	if err := json.Unmarshal(want, &wv); err != nil {
		t.Fatalf("want (golden file) is not valid JSON: %v\n%s", err, want)
	}
	if !reflect.DeepEqual(gv, wv) {
		t.Fatalf("JSON mismatch:\n got:  %s\n want: %s", got, want)
	}
}
