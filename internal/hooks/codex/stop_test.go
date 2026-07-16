package codex

import (
	"fmt"
	"strings"
	"testing"
)

// rawStopAssistantMessage is the last_assistant_message text embedded
// verbatim in stop/normal.json — the privacy needle proving the parser
// never retains raw response text.
const rawStopAssistantMessage = "Done - the rollout reader now scans a bounded tail window."

func TestParseStop_Normal(t *testing.T) {
	ev, err := ParseStop(fixture(t, "stop", "normal.json"))
	if err != nil {
		t.Fatalf("ParseStop: %v", err)
	}
	if ev.SessionID != "019f0000-1111-7aaa-8bbb-ccccdddd0001" {
		t.Errorf("SessionID = %q", ev.SessionID)
	}
	if ev.TurnID != "019f0000-2222-7aaa-8bbb-ccccdddd0101" {
		t.Errorf("TurnID = %q", ev.TurnID)
	}
	if ev.TranscriptPath == nil || !strings.Contains(*ev.TranscriptPath, "rollout-") {
		t.Errorf("TranscriptPath = %v, want the rollout path", ev.TranscriptPath)
	}
	if ev.StopHookActive == nil || *ev.StopHookActive {
		t.Errorf("StopHookActive = %v, want false", ev.StopHookActive)
	}
	if ev.Model == nil || *ev.Model != "gpt-5.2-codex" {
		t.Errorf("Model = %v", ev.Model)
	}
}

func TestParseStop_LastAssistantMessageNeverRetained(t *testing.T) {
	raw := fixture(t, "stop", "normal.json")
	if !strings.Contains(string(raw), rawStopAssistantMessage) {
		t.Fatal("fixture drifted: stop/normal.json no longer contains the privacy needle")
	}
	ev, err := ParseStop(raw)
	if err != nil {
		t.Fatalf("ParseStop: %v", err)
	}
	dump := fmt.Sprintf("%#v", ev)
	if strings.Contains(dump, rawStopAssistantMessage) {
		t.Errorf("raw assistant message leaked into parsed StopEvent: %s", dump)
	}
}

func TestParseStop_MissingFields(t *testing.T) {
	ev, err := ParseStop(fixture(t, "stop", "missing_fields.json"))
	if err != nil {
		t.Fatalf("ParseStop: %v", err)
	}
	if ev.TurnID != "" {
		t.Errorf("TurnID = %q, want empty", ev.TurnID)
	}
	if ev.StopHookActive != nil {
		t.Errorf("StopHookActive = %v, want nil for an absent field (unknown is not false)", ev.StopHookActive)
	}
	if ev.TranscriptPath != nil {
		t.Errorf("null transcript_path must decode to nil, got %v", ev.TranscriptPath)
	}
}

func TestParseStop_UnknownFieldsTolerated(t *testing.T) {
	ev, err := ParseStop(fixture(t, "stop", "unknown_fields.json"))
	if err != nil {
		t.Fatalf("ParseStop (must tolerate unknown fields): %v", err)
	}
	if ev.StopHookActive == nil || !*ev.StopHookActive {
		t.Errorf("StopHookActive = %v, want true", ev.StopHookActive)
	}
}

func TestParseStop_Malformed(t *testing.T) {
	_, err := ParseStop(fixture(t, "stop", "malformed.json"))
	assertValidationError(t, err)
}

func TestParseStop_MissingSessionID(t *testing.T) {
	_, err := ParseStop([]byte(`{"hook_event_name":"Stop"}`))
	assertValidationError(t, err)
}
