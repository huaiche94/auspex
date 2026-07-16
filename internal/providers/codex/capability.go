// Package codex declares what Auspex can actually do against a Codex
// CLI installation running in native-hook mode (issue #9 Phase 1). Per
// Constitution §5, capabilities are DETECTED from the installation and the
// local evidence surfaces they depend on, never hardcoded assumptions:
// each true below is tied to a concrete, checkable precondition, and every
// check that fails degrades that capability to false (absent, not assumed).
package codex

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	codextelemetry "github.com/huaiche94/auspex/internal/telemetry/codex"
)

// Provider is the installation identifier this reader answers for.
const Provider = "codex"

// minHooksVersion is the first Codex CLI minor with the
// Claude-Code-compatible native hook protocol this adapter targets
// (hooks.json config, SessionStart/UserPromptSubmit/Stop events, JSON
// stdout decisions). Verified against the v0.144.4 binary's embedded hook
// schemas; older installations without hooks get every hook-derived
// capability declared false rather than a runtime surprise.
const (
	minHooksMajor = 0
	minHooksMinor = 144
)

// CapabilityReader implements the frozen app.ProviderCapabilityReader port
// for Codex native-hook mode. Zero value is usable: SessionsDir defaults
// to codextelemetry.DefaultSessionsDir() and Stat to os.Stat; both are
// injectable for tests.
type CapabilityReader struct {
	// SessionsDir overrides the rollout sessions root probed for the
	// rollout-derived capabilities. "" means resolve the host default
	// ($CODEX_HOME/sessions, else ~/.codex/sessions).
	SessionsDir string
	// Stat is the filesystem probe; nil means os.Stat.
	Stat func(name string) (os.FileInfo, error)
}

var _ app.ProviderCapabilityReader = (*CapabilityReader)(nil)

// Capabilities reports the detected capability set for installation.
// Detection inputs and what they gate:
//
//   - installation.Version >= 0.144 gates the hook-protocol capabilities:
//     PrePromptGate (UserPromptSubmit block decision) and
//     HookAdditionalContext (hookSpecificOutput.additionalContext on
//     UserPromptSubmit/SessionStart responses). An unparseable or older
//     version declares both false.
//   - a readable rollout sessions directory gates the rollout-derived
//     capabilities: ExactTurnUsage (last_token_usage per turn),
//     ContextWindowUsage (model_context_window), RollingQuotaUsage and
//     QuotaResetTimestamp (rate_limits primary/secondary with resets_at).
//     No sessions directory means no rollout to read, so all four are
//     false.
//
// Deliberate Phase 1 falses (not detection failures — these reflect what
// Auspex can drive today, the honest reading of "capability"):
//
//   - LiveTokenUsage: the rollout is read at Stop, not streamed live.
//   - ManagedExecution/StructuredEventStream: no codex managed runner.
//   - PlanEvents/TaskEvents/FileChangeEvents/ToolEvents: PostToolUse and
//     friends are deferred past Phase 1.
//   - TurnInterrupt/SafePointControl: no interrupt surface is integrated.
//   - SessionResume/SessionFork: `codex resume`/`codex fork` exist, but
//     Auspex does not drive them and cannot validate a resumed
//     session's state — DEGRADED, which the boolean capability contract
//     conservatively records as false (a true here would let pause/resume
//     validation assume a resume path that Phase 1 cannot deliver).
//   - NativeStatusLine: Codex has no statusLine hook (the reason issue
//     #9 Phase 1b adds the DB-backed `auspex hook codex status` line).
//   - NativeInteractiveChoice: no such hook surface.
func (r *CapabilityReader) Capabilities(_ context.Context, installation app.ProviderInstallation) (domain.ProviderCapabilities, error) {
	if installation.Provider != Provider {
		return domain.ProviderCapabilities{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "codex: CapabilityReader asked about provider " + installation.Provider,
			Retryable: false,
		}
	}

	var caps domain.ProviderCapabilities

	if hooksSupported(installation.Version) {
		caps.PrePromptGate = true
		caps.HookAdditionalContext = true
	}

	if r.sessionsDirExists() {
		caps.ExactTurnUsage = true
		caps.ContextWindowUsage = true
		caps.RollingQuotaUsage = true
		caps.QuotaResetTimestamp = true
	}

	return caps, nil
}

// sessionsDirExists probes the rollout sessions root. Any resolution or
// stat failure is false — the capability is declared absent, never assumed.
func (r *CapabilityReader) sessionsDirExists() bool {
	dir := r.SessionsDir
	if dir == "" {
		d, ok := codextelemetry.DefaultSessionsDir()
		if !ok {
			return false
		}
		dir = d
	}
	stat := r.Stat
	if stat == nil {
		stat = os.Stat
	}
	info, err := stat(dir)
	return err == nil && info.IsDir()
}

// hooksSupported parses a Codex version string ("0.144.4",
// "0.144.0-alpha.4", a "v" prefix tolerated) and reports whether it is at
// least the first hook-capable release. Unparseable versions are false:
// a capability that cannot be verified is not declared (Constitution §5).
func hooksSupported(version string) bool {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if v == "" {
		return false
	}
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	minorStr := parts[1]
	if i := strings.IndexAny(minorStr, "-+"); i >= 0 {
		minorStr = minorStr[:i]
	}
	minor, err := strconv.Atoi(minorStr)
	if err != nil {
		return false
	}
	if major != minHooksMajor {
		return major > minHooksMajor
	}
	return minor >= minHooksMinor
}
