// daemon.go: the REAL `auspex daemon` command family (issue #7, M6;
// ADD §24.2), wired by internal/app/wiring in place of root.go's stub.
// `run` is the manual-first foreground core and `install`/`uninstall`
// manage the optional LaunchAgent around it (D-16 hybrid decision);
// `status`/`stop` operate on the runtime metadata a running daemon
// publishes. Signal handling lives HERE (signal.NotifyContext) — the
// daemon and orchestrator layers only ever see a context.
package cli

import (
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/huaiche94/auspex/internal/daemon"
	"github.com/huaiche94/auspex/internal/orchestrator"
)

// NewDaemonCmd builds the real `auspex daemon` tree against deps.
func NewDaemonCmd(deps orchestrator.DaemonDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run and manage the Auspex background daemon",
	}
	cmd.AddCommand(
		newDaemonRunCmd(deps),
		newDaemonStatusCmd(deps),
		newDaemonStopCmd(deps),
		newDaemonInstallCmd(deps),
		newDaemonUninstallCmd(deps),
	)
	return cmd
}

type daemonReadyOutput struct {
	SchemaVersion string `json:"schema_version"`
	Address       string `json:"address"`
	PID           int    `json:"pid"`
	TokenFile     string `json:"token_file"`
}

func newDaemonRunCmd(deps orchestrator.DaemonDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run the daemon in the foreground (Ctrl-C or `auspex daemon stop` to exit)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			return orchestrator.DaemonRun(ctx, deps, func(info daemon.RunInfo) {
				body, err := marshalOrError("daemon run", daemonReadyOutput{
					SchemaVersion: "auspex.daemon-ready.v1",
					Address:       info.Address,
					PID:           info.PID,
					TokenFile:     info.TokenFile,
				})
				if err == nil {
					_ = writeJSON(cmd, body)
				}
			})
		},
	}
}

type daemonStatusOutput struct {
	SchemaVersion string `json:"schema_version"`
	Running       bool   `json:"running"`
	Health        string `json:"health"`
	PID           int    `json:"pid,omitempty"`
	Address       string `json:"address,omitempty"`
	Version       string `json:"version,omitempty"`
}

func newDaemonStatusCmd(deps orchestrator.DaemonDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report whether the daemon is running (metadata + health probe)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := orchestrator.DaemonStatus(cmd.Context(), deps)
			if err != nil {
				return err
			}
			body, err := marshalOrError("daemon status", daemonStatusOutput{
				SchemaVersion: "auspex.daemon-status.v1",
				Running:       result.Running,
				Health:        result.Health,
				PID:           result.PID,
				Address:       result.Address,
				Version:       result.Version,
			})
			if err != nil {
				return err
			}
			return writeJSON(cmd, body)
		},
	}
}

type daemonStopOutput struct {
	SchemaVersion string `json:"schema_version"`
	Signaled      bool   `json:"signaled"`
	PID           int    `json:"pid,omitempty"`
}

func newDaemonStopCmd(deps orchestrator.DaemonDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Signal the running daemon to shut down gracefully",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := orchestrator.DaemonStop(cmd.Context(), deps)
			if err != nil {
				return err
			}
			body, err := marshalOrError("daemon stop", daemonStopOutput{
				SchemaVersion: "auspex.daemon-stop.v1",
				Signaled:      result.Signaled,
				PID:           result.PID,
			})
			if err != nil {
				return err
			}
			return writeJSON(cmd, body)
		},
	}
}

type daemonInstallOutput struct {
	SchemaVersion string `json:"schema_version"`
	PlistPath     string `json:"plist_path"`
	LoadCommand   string `json:"load_command"`
}

func newDaemonInstallCmd(deps orchestrator.DaemonDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Write a macOS LaunchAgent so the daemon starts at login and stays alive",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := orchestrator.DaemonInstall(deps)
			if err != nil {
				return err
			}
			body, err := marshalOrError("daemon install", daemonInstallOutput{
				SchemaVersion: "auspex.daemon-install.v1",
				PlistPath:     result.PlistPath,
				LoadCommand:   "launchctl load " + result.PlistPath,
			})
			if err != nil {
				return err
			}
			return writeJSON(cmd, body)
		},
	}
}

type daemonUninstallOutput struct {
	SchemaVersion string `json:"schema_version"`
	Removed       bool   `json:"removed"`
	PlistPath     string `json:"plist_path"`
	UnloadCommand string `json:"unload_command,omitempty"`
}

func newDaemonUninstallCmd(deps orchestrator.DaemonDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the macOS LaunchAgent (run `launchctl unload` first if loaded)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := orchestrator.DaemonUninstall(deps)
			if err != nil {
				return err
			}
			out := daemonUninstallOutput{
				SchemaVersion: "auspex.daemon-uninstall.v1",
				Removed:       result.Removed,
				PlistPath:     result.PlistPath,
			}
			if result.Removed {
				out.UnloadCommand = "launchctl unload " + result.PlistPath
			}
			body, err := marshalOrError("daemon uninstall", out)
			if err != nil {
				return err
			}
			return writeJSON(cmd, body)
		},
	}
}
