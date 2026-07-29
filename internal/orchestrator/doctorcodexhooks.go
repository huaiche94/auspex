// doctorcodexhooks.go — the M8 doctor check for the codex hook install
// (issue #9; ADD §21.12 + §31 M8 "doctor checks"): reports whether the
// three Auspex command hooks are wired in the Codex CLI's hooks.json.
// Diagnostic only, read-only, and never a hard failure: hooks are one of
// two capture paths (the rollout watcher covers surfaces hooks cannot),
// so an uninstalled state is a warn with the fix command, not a fail.
package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/huaiche94/auspex/internal/hookinstall"
)

func checkCodexHooks(configPath string) CheckResult {
	const name = "codex hooks"
	if configPath == "" {
		// Not wired (or no home was resolvable at composition time) —
		// skipped like every other omitted dependency, so bare-deps
		// Doctor calls stay environment-independent.
		return CheckResult{Name: name, Status: CheckSkipped, Detail: "no hooks.json path configured"}
	}

	present, absent, err := hookinstall.InstalledEntries(configPath, hookinstall.CodexEntries())
	if err != nil {
		return CheckResult{Name: name, Status: CheckWarn, Detail: "unreadable " + configPath + ": " + err.Error()}
	}
	switch {
	case len(absent) == 0:
		return CheckResult{Name: name, Status: CheckOK, Detail: fmt.Sprintf("all %d auspex hooks wired in %s", len(present), configPath)}
	case len(present) == 0:
		return CheckResult{Name: name, Status: CheckWarn, Detail: "not installed (run `auspex hook codex install`); the rollout watcher remains the only codex capture path"}
	default:
		return CheckResult{Name: name, Status: CheckWarn, Detail: fmt.Sprintf("partial: %d of %d auspex hooks wired in %s (run `auspex hook codex install` to complete)", len(present), len(present)+len(absent), configPath)}
	}
}

// DefaultCodexHooksPath mirrors the CLI's resolution (CODEX_HOME, else
// ~/.codex) for the composition root wiring DoctorDeps; ok=false when no
// home is resolvable (the check then renders skipped).
func DefaultCodexHooksPath() (string, bool) {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return filepath.Join(home, "hooks.json"), true
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, ".codex", "hooks.json"), true
}
