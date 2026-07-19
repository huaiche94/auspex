package codex

import (
	"errors"
	"testing"

	"github.com/huaiche94/auspex/internal/domain"
)

func TestParsePreCompact_Full(t *testing.T) {
	ev, err := ParsePreCompact([]byte(`{
		"session_id": "019f-sess",
		"hook_event_name": "PreCompact",
		"cwd": "/home/dev/projects/sample",
		"transcript_path": "/home/dev/.codex/sessions/r.jsonl",
		"model": "gpt-5.2-codex",
		"permission_mode": "default",
		"trigger": "auto"
	}`))
	if err != nil {
		t.Fatalf("ParsePreCompact: %v", err)
	}
	if ev.SessionID != domain.SessionID("019f-sess") {
		t.Errorf("SessionID = %q", ev.SessionID)
	}
	if ev.CWD == nil || *ev.CWD != "/home/dev/projects/sample" {
		t.Errorf("CWD = %v", ev.CWD)
	}
	if ev.TranscriptPath == nil || *ev.TranscriptPath != "/home/dev/.codex/sessions/r.jsonl" {
		t.Errorf("TranscriptPath = %v", ev.TranscriptPath)
	}
	if ev.Model == nil || *ev.Model != "gpt-5.2-codex" {
		t.Errorf("Model = %v", ev.Model)
	}
	if ev.PermissionMode == nil || *ev.PermissionMode != "default" {
		t.Errorf("PermissionMode = %v", ev.PermissionMode)
	}
	if ev.Trigger == nil || *ev.Trigger != "auto" {
		t.Errorf("Trigger = %v", ev.Trigger)
	}
}

func TestParsePreCompact_MissingOptionalFieldsAreNil(t *testing.T) {
	ev, err := ParsePreCompact([]byte(`{"session_id":"s","hook_event_name":"PreCompact"}`))
	if err != nil {
		t.Fatalf("ParsePreCompact: %v", err)
	}
	if ev.CWD != nil || ev.TranscriptPath != nil || ev.Model != nil || ev.PermissionMode != nil || ev.Trigger != nil {
		t.Errorf("absent optional fields must parse to nil, got %+v", ev)
	}
}

func TestParsePreCompact_UnknownFieldsTolerated(t *testing.T) {
	if _, err := ParsePreCompact([]byte(`{"session_id":"s","future":{"x":true}}`)); err != nil {
		t.Fatalf("unknown fields must be tolerated: %v", err)
	}
}

func TestParsePreCompact_Errors(t *testing.T) {
	if _, err := ParsePreCompact([]byte(`{oops`)); err == nil {
		t.Fatal("malformed JSON must error")
	}
	_, err := ParsePreCompact([]byte(`{"hook_event_name":"PreCompact"}`))
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("missing session_id must be a validation error, got %v", err)
	}
}

func TestParsePostCompact_Full(t *testing.T) {
	ev, err := ParsePostCompact([]byte(`{
		"session_id": "019f-sess",
		"hook_event_name": "PostCompact",
		"cwd": "/repo",
		"model": "gpt-5.2-codex",
		"trigger": "manual"
	}`))
	if err != nil {
		t.Fatalf("ParsePostCompact: %v", err)
	}
	if ev.SessionID != domain.SessionID("019f-sess") {
		t.Errorf("SessionID = %q", ev.SessionID)
	}
	if ev.Trigger == nil || *ev.Trigger != "manual" {
		t.Errorf("Trigger = %v", ev.Trigger)
	}
}

func TestParsePostCompact_Errors(t *testing.T) {
	if _, err := ParsePostCompact([]byte(`[`)); err == nil {
		t.Fatal("malformed JSON must error")
	}
	_, err := ParsePostCompact([]byte(`{"cwd":"/repo"}`))
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("missing session_id must be a validation error, got %v", err)
	}
}
