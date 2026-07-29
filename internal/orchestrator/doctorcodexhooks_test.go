package orchestrator

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/hookinstall"
)

func TestCheckCodexHooks(t *testing.T) {
	t.Run("unconfigured skips", func(t *testing.T) {
		c := checkCodexHooks("")
		if c.Status != CheckSkipped {
			t.Errorf("status = %s, want skipped", c.Status)
		}
	})

	t.Run("absent config warns with the fix command", func(t *testing.T) {
		c := checkCodexHooks(filepath.Join(t.TempDir(), "hooks.json"))
		if c.Status != CheckWarn {
			t.Fatalf("status = %s, want warn (uninstalled is degraded capture, not a failure)", c.Status)
		}
	})

	t.Run("installed config is ok", func(t *testing.T) {
		dir := t.TempDir()
		config := filepath.Join(dir, "hooks.json")
		receipt := filepath.Join(dir, "receipt.json")
		if _, err := hookinstall.Install(config, receipt, "codex", "", "test", hookinstall.CodexEntries(), time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)); err != nil {
			t.Fatalf("Install: %v", err)
		}
		c := checkCodexHooks(config)
		if c.Status != CheckOK {
			t.Errorf("status = %s (%s), want ok", c.Status, c.Detail)
		}
	})
}
