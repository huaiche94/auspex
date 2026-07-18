package cli

import (
	"bytes"
	"errors"
	"testing"

	"github.com/huaiche94/auspex/internal/domain"
)

// TestNotImplementedShape confirms notImplemented produces the frozen
// domain.Error shape (CONTRACT_FREEZE.md "Error contract"): ErrCodeUnavailable,
// Retryable: true, and a non-empty message that names the command.
func TestNotImplementedShape(t *testing.T) {
	err := notImplemented("checkpoint create")

	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		t.Fatalf("notImplemented(...) = %v, want a *domain.Error", err)
	}
	if domainErr.Code != domain.ErrCodeUnavailable {
		t.Errorf("Code = %q, want %q", domainErr.Code, domain.ErrCodeUnavailable)
	}
	if !domainErr.Retryable {
		t.Error("Retryable = false, want true (service will exist once wired in a later node)")
	}
	if domainErr.Message == "" {
		t.Error("Message is empty")
	}
	if domainErr.Details["command"] != "checkpoint create" {
		t.Errorf(`Details["command"] = %q, want "checkpoint create"`, domainErr.Details["command"])
	}
}

// TestStubCommandsReturnNotImplemented drives every P0 command that has no
// real implementation this phase and confirms it returns the typed stub
// error rather than doing anything real or silently succeeding. `version`
// is deliberately excluded — it is the one command in this tree that is
// not a stub.
func TestStubCommandsReturnNotImplemented(t *testing.T) {
	commands := [][]string{
		{"init"},
		{"hook", "claude", "statusline"},
		{"hook", "claude", "user-prompt-submit"},
		{"hook", "claude", "stop"},
		{"hook", "claude", "stop-failure"},
		{"evaluate"},
		{"decision", "allow"},
		{"decision", "deny"},
		{"checkpoint", "create"},
		{"progress", "show"},
		{"progress", "complete"},
		{"state", "show"},
		{"pause", "request"},
		{"pause", "cancel"},
		{"resume"},
		{"scheduler", "run-once"},
		{"status"},
		{"doctor"},
	}

	for _, args := range commands {
		t.Run(joinPath(args), func(t *testing.T) {
			root := NewRootCmd()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(args)

			err := root.Execute()
			if err == nil {
				t.Fatalf("auspex %s: expected an error, got nil", joinPath(args))
			}

			var domainErr *domain.Error
			if !errors.As(err, &domainErr) {
				t.Fatalf("auspex %s: error %v is not a *domain.Error", joinPath(args), err)
			}
			if domainErr.Code != domain.ErrCodeUnavailable {
				t.Errorf("auspex %s: Code = %q, want %q", joinPath(args), domainErr.Code, domain.ErrCodeUnavailable)
			}
		})
	}
}
