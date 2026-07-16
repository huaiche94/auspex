package codex

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/huaiche94/auspex/internal/domain"
)

func fixture(t *testing.T, dir, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "provider-events", "codex", dir, name))
	if err != nil {
		t.Fatalf("reading fixture %s/%s: %v", dir, name, err)
	}
	return b
}

func TestParseSessionStart_Normal(t *testing.T) {
	ev, err := ParseSessionStart(fixture(t, "sessionstart", "normal.json"))
	if err != nil {
		t.Fatalf("ParseSessionStart: %v", err)
	}
	if ev.SessionID != "019f0000-1111-7aaa-8bbb-ccccdddd0001" {
		t.Errorf("SessionID = %q", ev.SessionID)
	}
	if ev.Source != SessionStartStartup {
		t.Errorf("Source = %q, want startup", ev.Source)
	}
	if ev.CWD == nil || *ev.CWD != "/home/dev/projects/sample" {
		t.Errorf("CWD = %v", ev.CWD)
	}
	if ev.TranscriptPath == nil || *ev.TranscriptPath == "" {
		t.Errorf("TranscriptPath = %v, want the rollout path", ev.TranscriptPath)
	}
	if ev.Model == nil || *ev.Model != "gpt-5.2-codex" {
		t.Errorf("Model = %v", ev.Model)
	}
	if ev.PermissionMode == nil || *ev.PermissionMode != "default" {
		t.Errorf("PermissionMode = %v", ev.PermissionMode)
	}
}

func TestParseSessionStart_SourceVariants(t *testing.T) {
	for file, want := range map[string]SessionStartSource{
		"resume.json":  SessionStartResume,
		"compact.json": SessionStartCompact,
	} {
		ev, err := ParseSessionStart(fixture(t, "sessionstart", file))
		if err != nil {
			t.Fatalf("ParseSessionStart(%s): %v", file, err)
		}
		if ev.Source != want {
			t.Errorf("%s: Source = %q, want %q", file, ev.Source, want)
		}
	}
}

func TestParseSessionStart_MissingFields(t *testing.T) {
	ev, err := ParseSessionStart(fixture(t, "sessionstart", "missing_fields.json"))
	if err != nil {
		t.Fatalf("ParseSessionStart: %v", err)
	}
	if ev.Source != "" {
		t.Errorf("Source = %q, want empty for an absent field", ev.Source)
	}
	if ev.CWD != nil || ev.Model != nil || ev.PermissionMode != nil {
		t.Errorf("absent fields must stay nil: CWD=%v Model=%v PermissionMode=%v", ev.CWD, ev.Model, ev.PermissionMode)
	}
	if ev.TranscriptPath != nil {
		t.Errorf("null transcript_path must decode to nil, got %v", ev.TranscriptPath)
	}
}

func TestParseSessionStart_UnknownFieldsTolerated(t *testing.T) {
	ev, err := ParseSessionStart(fixture(t, "sessionstart", "unknown_fields.json"))
	if err != nil {
		t.Fatalf("ParseSessionStart (must tolerate unknown fields): %v", err)
	}
	if ev.SessionID == "" {
		t.Error("SessionID empty")
	}
}

func TestParseSessionStart_Malformed(t *testing.T) {
	_, err := ParseSessionStart(fixture(t, "sessionstart", "malformed.json"))
	assertValidationError(t, err)
}

func TestParseSessionStart_MissingSessionID(t *testing.T) {
	_, err := ParseSessionStart([]byte(`{"hook_event_name":"SessionStart","source":"startup"}`))
	assertValidationError(t, err)
}

func assertValidationError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var de *domain.Error
	if !errors.As(err, &de) {
		t.Fatalf("error type = %T, want *domain.Error", err)
	}
	if de.Code != domain.ErrCodeValidation {
		t.Errorf("error code = %q, want %q", de.Code, domain.ErrCodeValidation)
	}
	if de.Retryable {
		t.Error("validation errors must not be retryable")
	}
}
