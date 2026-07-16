package codex

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
)

func statDirOK(string) (os.FileInfo, error) {
	// A real directory FileInfo without touching this repo's layout.
	return os.Stat(os.TempDir())
}

func statMissing(string) (os.FileInfo, error) {
	return nil, os.ErrNotExist
}

func codexInstallation(version string) app.ProviderInstallation {
	return app.ProviderInstallation{Provider: Provider, Version: version, Path: "/usr/local/bin/codex"}
}

func TestCapabilities_HookCapableInstallWithSessions(t *testing.T) {
	r := &CapabilityReader{SessionsDir: "/probe/sessions", Stat: statDirOK}
	caps, err := r.Capabilities(context.Background(), codexInstallation("0.144.4"))
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}

	want := domain.ProviderCapabilities{
		PrePromptGate:         true,
		HookAdditionalContext: true,
		ExactTurnUsage:        true,
		ContextWindowUsage:    true,
		RollingQuotaUsage:     true,
		QuotaResetTimestamp:   true,
		// Everything else is deliberately false in Phase 1 — see the
		// Capabilities doc comment for the per-field reasoning (notably
		// SessionResume/SessionFork: present in the CLI but not driven by
		// Auspex, i.e. degraded, recorded conservatively as false).
	}
	if caps != want {
		t.Errorf("caps = %+v, want %+v", caps, want)
	}
}

func TestCapabilities_NoSessionsDir_RolloutCapsAbsent(t *testing.T) {
	r := &CapabilityReader{SessionsDir: "/probe/sessions", Stat: statMissing}
	caps, err := r.Capabilities(context.Background(), codexInstallation("0.144.4"))
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !caps.PrePromptGate || !caps.HookAdditionalContext {
		t.Error("hook capabilities must not depend on the sessions dir")
	}
	if caps.ExactTurnUsage || caps.ContextWindowUsage || caps.RollingQuotaUsage || caps.QuotaResetTimestamp {
		t.Errorf("rollout-derived capabilities declared without a sessions dir: %+v", caps)
	}
}

func TestCapabilities_PreHookVersion_HookCapsAbsent(t *testing.T) {
	for _, version := range []string{"0.143.0", "0.99.9", "", "garbage", "0", "x.y.z"} {
		r := &CapabilityReader{SessionsDir: "/probe/sessions", Stat: statDirOK}
		caps, err := r.Capabilities(context.Background(), codexInstallation(version))
		if err != nil {
			t.Fatalf("Capabilities(%q): %v", version, err)
		}
		if caps.PrePromptGate || caps.HookAdditionalContext {
			t.Errorf("version %q: hook capabilities declared for a pre-hook/unparseable version", version)
		}
	}
}

func TestCapabilities_VersionVariantsAccepted(t *testing.T) {
	for _, version := range []string{"0.144.0", "0.144.0-alpha.4", "v0.145.2", "1.0.0"} {
		r := &CapabilityReader{SessionsDir: "/probe/sessions", Stat: statDirOK}
		caps, err := r.Capabilities(context.Background(), codexInstallation(version))
		if err != nil {
			t.Fatalf("Capabilities(%q): %v", version, err)
		}
		if !caps.PrePromptGate {
			t.Errorf("version %q: want PrePromptGate detected", version)
		}
	}
}

func TestCapabilities_WrongProviderRejected(t *testing.T) {
	r := &CapabilityReader{SessionsDir: "/probe/sessions", Stat: statDirOK}
	_, err := r.Capabilities(context.Background(), app.ProviderInstallation{Provider: "claude", Version: "2.1.0"})
	if err == nil {
		t.Fatal("expected an error for a non-codex installation")
	}
	var de *domain.Error
	if !errors.As(err, &de) || de.Code != domain.ErrCodeValidation {
		t.Errorf("error = %v, want a validation domain.Error", err)
	}
}
