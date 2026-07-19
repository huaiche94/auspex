package claude

import (
	"errors"
	"strings"
	"testing"

	"github.com/huaiche94/auspex/internal/domain"
)

func TestParsePreCompact_Full(t *testing.T) {
	raw := []byte(`{
		"session_id": "sess-1",
		"transcript_path": "/home/dev/.claude/projects/p/t.jsonl",
		"cwd": "/home/dev/projects/sample",
		"hook_event_name": "PreCompact",
		"trigger": "auto",
		"custom_instructions": "keep the migration plan"
	}`)
	ev, err := ParsePreCompact(raw)
	if err != nil {
		t.Fatalf("ParsePreCompact: %v", err)
	}
	if ev.SessionID != domain.SessionID("sess-1") {
		t.Errorf("SessionID = %q, want sess-1", ev.SessionID)
	}
	if ev.TranscriptPath == nil || *ev.TranscriptPath != "/home/dev/.claude/projects/p/t.jsonl" {
		t.Errorf("TranscriptPath = %v", ev.TranscriptPath)
	}
	if ev.CWD == nil || *ev.CWD != "/home/dev/projects/sample" {
		t.Errorf("CWD = %v", ev.CWD)
	}
	if ev.Trigger == nil || *ev.Trigger != CompactTriggerAuto {
		t.Errorf("Trigger = %v, want auto", ev.Trigger)
	}
	if ev.CustomInstructionsLen == nil || *ev.CustomInstructionsLen != len("keep the migration plan") {
		t.Errorf("CustomInstructionsLen = %v, want %d", ev.CustomInstructionsLen, len("keep the migration plan"))
	}
}

// TestParsePreCompact_RawInstructionsNeverRetained pins the privacy
// contract: the parsed struct has no field that could carry the
// custom_instructions text (only its length), so the raw user text cannot
// survive the parser's stack frame by construction.
func TestParsePreCompact_RawInstructionsNeverRetained(t *testing.T) {
	secret := "SECRET-INSTRUCTION-TEXT-XYZZY"
	ev, err := ParsePreCompact([]byte(`{"session_id":"s","trigger":"manual","custom_instructions":"` + secret + `"}`))
	if err != nil {
		t.Fatalf("ParsePreCompact: %v", err)
	}
	if ev.CustomInstructionsLen == nil || *ev.CustomInstructionsLen != len(secret) {
		t.Fatalf("CustomInstructionsLen = %v, want %d", ev.CustomInstructionsLen, len(secret))
	}
	// Belt and braces: no string field on the struct holds the text.
	for _, s := range []*string{ev.TranscriptPath, ev.CWD, ev.Trigger} {
		if s != nil && strings.Contains(*s, secret) {
			t.Fatalf("raw custom_instructions text leaked into parsed struct: %q", *s)
		}
	}
}

func TestParsePreCompact_MissingOptionalFieldsAreNil(t *testing.T) {
	ev, err := ParsePreCompact([]byte(`{"session_id":"sess-2","hook_event_name":"PreCompact"}`))
	if err != nil {
		t.Fatalf("ParsePreCompact: %v", err)
	}
	if ev.TranscriptPath != nil || ev.CWD != nil || ev.Trigger != nil || ev.CustomInstructionsLen != nil {
		t.Errorf("absent optional fields must parse to nil, got %+v", ev)
	}
}

func TestParsePreCompact_EmptyInstructionsIsZeroNotNil(t *testing.T) {
	ev, err := ParsePreCompact([]byte(`{"session_id":"s","custom_instructions":""}`))
	if err != nil {
		t.Fatalf("ParsePreCompact: %v", err)
	}
	if ev.CustomInstructionsLen == nil || *ev.CustomInstructionsLen != 0 {
		t.Errorf("present-but-empty custom_instructions must parse to *0, got %v", ev.CustomInstructionsLen)
	}
}

func TestParsePreCompact_UnknownFieldsTolerated(t *testing.T) {
	if _, err := ParsePreCompact([]byte(`{"session_id":"s","some_future_field":{"a":1}}`)); err != nil {
		t.Fatalf("unknown fields must be tolerated: %v", err)
	}
}

func TestParsePreCompact_Errors(t *testing.T) {
	cases := map[string][]byte{
		"malformed JSON":     []byte(`{not json`),
		"missing session_id": []byte(`{"hook_event_name":"PreCompact","trigger":"auto"}`),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParsePreCompact(raw)
			if err == nil {
				t.Fatal("expected error")
			}
			var derr *domain.Error
			if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
				t.Fatalf("expected domain validation error, got %v", err)
			}
		})
	}
}

func TestParsePostCompact_Full(t *testing.T) {
	ev, err := ParsePostCompact([]byte(`{
		"session_id": "sess-3",
		"transcript_path": "/t.jsonl",
		"cwd": "/repo",
		"hook_event_name": "PostCompact",
		"trigger": "manual"
	}`))
	if err != nil {
		t.Fatalf("ParsePostCompact: %v", err)
	}
	if ev.SessionID != domain.SessionID("sess-3") {
		t.Errorf("SessionID = %q", ev.SessionID)
	}
	if ev.Trigger == nil || *ev.Trigger != CompactTriggerManual {
		t.Errorf("Trigger = %v, want manual", ev.Trigger)
	}
	if ev.CWD == nil || *ev.CWD != "/repo" {
		t.Errorf("CWD = %v", ev.CWD)
	}
}

func TestParsePostCompact_Errors(t *testing.T) {
	if _, err := ParsePostCompact([]byte(`{`)); err == nil {
		t.Fatal("malformed JSON must error")
	}
	_, err := ParsePostCompact([]byte(`{"hook_event_name":"PostCompact"}`))
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("missing session_id must be a validation error, got %v", err)
	}
}
