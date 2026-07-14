// daemon.go implements the orchestrator-layer wiring for `auspex daemon
// run|status|stop|install|uninstall` (issue #7, M6; ADD §24.2). Mirrors
// pauselifecycle.go's structure: one Deps bundle, one function per command,
// fail-closed nil checks, all output data returned as structs for the CLI
// layer to render.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/huaiche94/auspex/internal/daemon"
	"github.com/huaiche94/auspex/internal/domain"
)

// DaemonDeps bundles the daemon command family's collaborators. Daemon is
// required only by DaemonRun; the metadata-reading commands (status/stop)
// need only RuntimeDir, and install/uninstall only LaunchAgentDir — the
// same required-only-by-its-command bundling PauseLifecycleDeps uses.
type DaemonDeps struct {
	Daemon     *daemon.Daemon
	RuntimeDir string
	// LaunchAgentDir hosts the generated plist (production:
	// ~/Library/LaunchAgents; tests: a temp dir).
	LaunchAgentDir string
	// ExecutablePath is the binary the plist points at (production:
	// os.Executable()).
	ExecutablePath string
}

// --- auspex daemon run -----------------------------------------------------

// DaemonRun runs the daemon until ctx is cancelled (the CLI wraps ctx with
// signal.NotifyContext, keeping process-signal concerns out of both the
// daemon package and this one).
func DaemonRun(ctx context.Context, deps DaemonDeps, onReady func(daemon.RunInfo)) error {
	if deps.Daemon == nil {
		return &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "orchestrator: DaemonRun requires a composed daemon (wiring absent)", Retryable: false,
		}
	}
	return deps.Daemon.Run(ctx, onReady)
}

// --- auspex daemon status ----------------------------------------------------

// DaemonStatusResult reports the probe outcome. Running=false with no
// error is the ordinary cold state, not a failure.
type DaemonStatusResult struct {
	Running bool
	PID     int
	Address string
	Version string
	// Health is the /v1/health probe outcome when metadata was found:
	// "ok", or the failure mode ("unreachable", "unauthorized", …) — a
	// metadata file pointing at a dead process yields Running=false with
	// Health "unreachable" so the caller sees WHY.
	Health string
}

// healthProbeTimeout bounds the status probe (§23.3 names a short timeout
// so callers never hang on a wedged daemon).
const healthProbeTimeout = 2 * time.Second

// DaemonStatus reads the runtime metadata and, when present, probes
// GET /v1/health with the bearer token from the metadata's token file.
func DaemonStatus(ctx context.Context, deps DaemonDeps) (DaemonStatusResult, error) {
	if deps.RuntimeDir == "" {
		return DaemonStatusResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "orchestrator: DaemonStatus requires RuntimeDir", Retryable: false,
		}
	}
	meta, found, err := daemon.ReadMetadata(deps.RuntimeDir)
	if err != nil {
		return DaemonStatusResult{}, err
	}
	if !found {
		return DaemonStatusResult{Running: false, Health: "not_running"}, nil
	}

	result := DaemonStatusResult{PID: meta.PID, Address: meta.Address, Version: meta.Version}
	token, err := os.ReadFile(meta.TokenFile)
	if err != nil {
		result.Health = "token_unreadable"
		return result, nil
	}

	probeCtx, cancel := context.WithTimeout(ctx, healthProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, "http://"+meta.Address+"/v1/health", nil)
	if err != nil {
		result.Health = "unreachable"
		return result, nil
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		result.Health = "unreachable"
		return result, nil
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
		result.Running = true
		result.Health = "ok"
	case http.StatusUnauthorized, http.StatusForbidden:
		result.Health = "unauthorized"
	default:
		result.Health = fmt.Sprintf("http_%d", resp.StatusCode)
	}
	return result, nil
}

// --- auspex daemon stop ------------------------------------------------------

// DaemonStopResult reports the stop signal outcome.
type DaemonStopResult struct {
	Signaled bool
	PID      int
}

// DaemonStop signals the daemon named by the runtime metadata with SIGTERM
// (the graceful path — Daemon.Run's signal-wrapped ctx cancels, the worker
// drains, metadata and lock are cleaned up by the daemon itself, which is
// why this function does NOT delete them). No metadata → nothing to stop
// (Signaled=false, not an error — idempotent like RemoveMetadata).
func DaemonStop(ctx context.Context, deps DaemonDeps) (DaemonStopResult, error) {
	if deps.RuntimeDir == "" {
		return DaemonStopResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "orchestrator: DaemonStop requires RuntimeDir", Retryable: false,
		}
	}
	meta, found, err := daemon.ReadMetadata(deps.RuntimeDir)
	if err != nil {
		return DaemonStopResult{}, err
	}
	if !found {
		return DaemonStopResult{Signaled: false}, nil
	}
	proc, err := os.FindProcess(meta.PID)
	if err != nil {
		// Unreachable on Unix (FindProcess always succeeds there), so a
		// real error here is worth surfacing, not mapping to "not running".
		return DaemonStopResult{}, fmt.Errorf("orchestrator: DaemonStop: finding pid %d: %w", meta.PID, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			// Stale metadata from a crash: report not-running rather than
			// erroring — the next `daemon run`'s stale-lock handling and
			// metadata rewrite recover the files.
			return DaemonStopResult{Signaled: false, PID: meta.PID}, nil
		}
		return DaemonStopResult{}, fmt.Errorf("orchestrator: DaemonStop: signaling pid %d: %w", meta.PID, err)
	}
	return DaemonStopResult{Signaled: true, PID: meta.PID}, nil
}

// --- auspex daemon install / uninstall ---------------------------------------

// LaunchAgentLabel is the plist's launchd job label (D-16 decision ①:
// optional LaunchAgent install around the manual-first `daemon run` core).
const LaunchAgentLabel = "com.auspex.daemon"

// DaemonInstallResult reports where the plist landed.
type DaemonInstallResult struct {
	PlistPath string
}

// launchAgentPlist is the generated LaunchAgent document: KeepAlive
// restarts the daemon if it dies, RunAtLoad starts it at login —
// together they are the "unattended" half of D-16's hybrid decision.
const launchAgentPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>daemon</string>
		<string>run</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
</dict>
</plist>
`

// DaemonInstall writes the LaunchAgent plist pointing at ExecutablePath.
// It does NOT launchctl-load it — printing the load command is the CLI's
// job; actually running launchctl inside a Go process that may itself be
// managed by launchd invites recursion and permission surprises.
func DaemonInstall(deps DaemonDeps) (DaemonInstallResult, error) {
	if deps.LaunchAgentDir == "" || deps.ExecutablePath == "" {
		return DaemonInstallResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "orchestrator: DaemonInstall requires LaunchAgentDir and ExecutablePath", Retryable: false,
		}
	}
	if err := os.MkdirAll(deps.LaunchAgentDir, 0o755); err != nil {
		return DaemonInstallResult{}, fmt.Errorf("orchestrator: DaemonInstall: %w", err)
	}
	path := filepath.Join(deps.LaunchAgentDir, LaunchAgentLabel+".plist")
	content := fmt.Sprintf(launchAgentPlist, LaunchAgentLabel, deps.ExecutablePath)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return DaemonInstallResult{}, fmt.Errorf("orchestrator: DaemonInstall: %w", err)
	}
	return DaemonInstallResult{PlistPath: path}, nil
}

// DaemonUninstallResult reports whether a plist existed to remove.
type DaemonUninstallResult struct {
	Removed   bool
	PlistPath string
}

// DaemonUninstall removes the LaunchAgent plist; idempotent.
func DaemonUninstall(deps DaemonDeps) (DaemonUninstallResult, error) {
	if deps.LaunchAgentDir == "" {
		return DaemonUninstallResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "orchestrator: DaemonUninstall requires LaunchAgentDir", Retryable: false,
		}
	}
	path := filepath.Join(deps.LaunchAgentDir, LaunchAgentLabel+".plist")
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return DaemonUninstallResult{Removed: false, PlistPath: path}, nil
	}
	if err != nil {
		return DaemonUninstallResult{}, fmt.Errorf("orchestrator: DaemonUninstall: %w", err)
	}
	return DaemonUninstallResult{Removed: true, PlistPath: path}, nil
}
