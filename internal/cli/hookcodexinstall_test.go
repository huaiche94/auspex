package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/huaiche94/auspex/internal/orchestrator"
)

// TestHookCodexInstallUninstall_RoundTrip drives the real cobra leaves
// end to end against a temp hooks.json + receipt: install wires the
// three hooks and reports them, a second install is idempotent, and
// uninstall removes exactly what was owned. (The merge engine's own edge
// cases live in internal/hookinstall's suite; this pins the CLI plumbing
// and the two JSON output schemas.)
func TestHookCodexInstallUninstall_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "hooks.json")
	receipt := filepath.Join(dir, "receipt.json")

	run := func(args ...string) map[string]any {
		t.Helper()
		cmd := NewHookCodexCmd(orchestrator.HookDeps{})
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute %v: %v", args, err)
		}
		var out map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
			t.Fatalf("output of %v is not JSON: %v\n%s", args, err, stdout.String())
		}
		return out
	}

	install := run("install", "--codex-hooks-file", config, "--receipt-file", receipt)
	if install["schema_version"] != "auspex.codex-hook-install.v1" ||
		len(install["added"].([]any)) != 3 || install["config_created"] != true {
		t.Fatalf("install output = %v", install)
	}

	again := run("install", "--codex-hooks-file", config, "--receipt-file", receipt)
	if len(again["added"].([]any)) != 0 || len(again["already_wired"].([]any)) != 3 {
		t.Fatalf("second install output = %v, want idempotent", again)
	}

	uninstall := run("uninstall", "--codex-hooks-file", config, "--receipt-file", receipt)
	if uninstall["schema_version"] != "auspex.codex-hook-uninstall.v1" ||
		len(uninstall["removed"].([]any)) != 3 || uninstall["no_receipt"] != false {
		t.Fatalf("uninstall output = %v", uninstall)
	}
}
