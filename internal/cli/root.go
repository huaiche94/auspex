package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/huaiche94/auspex/internal/buildinfo"
)

// NewRootCmd builds the root `auspex` Cobra command with the full P0
// command tree (agents/runtime.md Part B "P0 commands") registered under
// it. Business logic stays out of RunE handlers per Auspex_ADD.md §10.1;
// every handler below is either a direct call to a foundation-owned helper
// (buildinfo.String, for `version`) or an honest stub returning
// errNotImplemented until the service it depends on is wired (see doc.go).
//
// Kept separate from any os.Exit call so it is fully testable — mirrors
// cmd/auspex/main.go's own newRootCmd() convention. This constructor is
// not called from cmd/auspex/main.go yet; that root-wiring integration
// step belongs to contract-integrator/foundation per agents/runtime.md.
//
// # Error-contract wiring (runtime-b09)
//
// Every command in this tree is wrapped, via WithJSONErrorRendering, so
// that a returned error is rendered as SchemaVersionError's typed JSON
// envelope on the command's stderr — in addition to still being returned
// as a Go error exactly as before (see errors.go's own doc comment for why
// the RETURNED VALUE is unchanged: every existing caller's errors.As(err,
// &domain.Error{}) check, e.g. errors_test.go/root_test.go, keeps working
// unmodified). SilenceErrors is true (changed from false by this same
// node): agents/runtime.md's "machine mode never emits decorative text to
// stdout" applies equally to stderr's error path — Cobra's own default
// printer would otherwise ALSO print a second, plain-text "Error: ..."
// line after this package's own JSON envelope, which is exactly the
// decorative/non-machine-readable text the contract forbids. This was
// confirmed as a real bug during this node's own test-writing (an early
// draft of TestErrorContract_NoDecorativeTextOnAnyCommand failed on every
// single command, catching Cobra's plain-text line appended after the
// JSON) — SilenceErrors: true is the fix, not a workaround.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "auspex",
		Short:         "Auspex is a local-first predictive runtime guard for AI coding agents.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(
		newVersionCmd(),
		newInitCmd(),
		newHookCmd(),
		newEvaluateCmd(),
		newDecisionCmd(),
		newCheckpointCmd(),
		newProgressCmd(),
		newStateCmd(),
		newPauseCmd(),
		newResumeCmd(),
		newSchedulerCmd(),
		newDaemonCmd(),
		newStatusCmd(),
		newDoctorCmd(),
		newGCCmd(),
		newExportCmd(),
		newRunCmd(),
		newReportCmd(),
		newWatchCmd(),
	)

	return WithJSONErrorRendering(root)
}

// newVersionCmd builds `auspex version`. Unlike every other command in
// this package, it is fully real — buildinfo.String() has no service
// dependency, so there is nothing to stub.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the Auspex version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), buildinfo.String())
			return err
		},
	}
}

// newInitCmd builds `auspex init` (ADD §10.1 day-one setup flow). Stub:
// workspace/repository registration depends on internal/app/wiring, not
// built this wave.
func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize Auspex for the current repository",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("init")
		},
	}
}

// newEvaluateCmd builds the standalone-stub `auspex evaluate` leaf
// (ADD §9.9 EvaluationService). A stub ONLY on this bare tree —
// internal/app/wiring.App.RootCmd() replaces it with NewEvaluateCmd's
// real handler (evaluate.go, issue #14 deliverable 5), the same
// stub-then-swap pattern `hook`/`progress`/`checkpoint` follow: a caller
// with no wired services still gets an honest "not yet available".
func newEvaluateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "evaluate",
		Short: "Evaluate the current turn and produce a risk decision",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("evaluate")
		},
	}
}

// newDecisionCmd builds `auspex decision {allow,deny}`. Stub: both
// subcommands depend on EvaluationService.Decide/ConsumeAuthorization,
// not wired this wave.
func newDecisionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "decision",
		Short: "Act on a pending evaluation decision",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "allow",
			Short: "Issue a one-time authorization allowing the turn to proceed",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return notImplemented("decision allow")
			},
		},
		&cobra.Command{
			Use:   "deny",
			Short: "Deny the pending turn",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return notImplemented("decision deny")
			},
		},
	)
	return cmd
}

// newCheckpointCmd builds `auspex checkpoint create`. Stub: depends on
// sequencing checkpoint role Part A (state) then Part B (repository) per
// the frozen transaction/orchestration contract (CONTRACT_FREEZE.md
// "Transaction boundaries"), not wired this wave.
func newCheckpointCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "checkpoint",
		Short: "Manage Auspex checkpoints",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "create",
		Short: "Create a state checkpoint followed by a repository checkpoint",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("checkpoint create")
		},
	})
	return cmd
}

// newProgressCmd builds the standalone-stub `auspex progress
// {show,complete}` subtree. `show` (ProgressTreeService.Snapshot) remains
// a stub: depends on internal/app/wiring, not built this wave. `complete`
// is a stub ONLY on this bare tree — internal/app/wiring.App.RootCmd()
// replaces the whole `progress` subtree with NewProgressCmd's real
// handlers (progress.go), the same stub-then-swap pattern `hook`/
// `checkpoint`/`status` already follow.
func newProgressCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "progress",
		Short: "Inspect the Progress Tree",
	}
	cmd.AddCommand(
		newProgressShowStubCmd(),
		&cobra.Command{
			Use:   "complete",
			Short: "Complete a Progress Tree node with artifact evidence",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return notImplemented("progress complete")
			},
		},
	)
	return cmd
}

// newProgressShowStubCmd builds the `progress show` stub leaf. Factored
// out of newProgressCmd because the REAL progress subtree (NewProgressCmd,
// progress.go) keeps this same stub for `show` — only `complete` has a
// real implementation as of issue #1 — and the two trees must not drift.
func newProgressShowStubCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the current Progress Tree snapshot",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("progress show")
		},
	}
}

// newStateCmd builds `auspex state show` (StateCheckpointService.LoadLatest).
// Stub: depends on internal/app/wiring, not built this wave.
func newStateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "state",
		Short: "Inspect State Checkpoints",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Show the latest state checkpoint",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("state show")
		},
	})
	return cmd
}

// newPauseCmd builds `auspex pause {request,cancel}` (GracefulPauseService).
// Stub: Part A (internal/pause/**) is not this role's Wave-3 scope; this
// command surface exists so the CLI tree shape is complete, but every
// handler defers to a service that does not exist yet.
func newPauseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pause",
		Short: "Request or cancel a Graceful Pause",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "request",
			Short: "Request a Graceful Pause for the current session",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return notImplemented("pause request")
			},
		},
		&cobra.Command{
			Use:   "cancel",
			Short: "Cancel a pending or in-flight pause",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return notImplemented("pause cancel")
			},
		},
	)
	return cmd
}

// newResumeCmd builds `auspex resume` (GracefulPauseService.Resume).
// Stub: depends on Part A's pause state machine, not built this wave.
func newResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Resume a paused session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("resume")
		},
	}
}

// newSchedulerCmd builds `auspex scheduler run-once` (durable wake
// scheduler, Part A). Stub: depends on internal/scheduler, not this
// role's Wave-3 scope.
func newSchedulerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scheduler",
		Short: "Operate the durable wake scheduler",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "run-once",
		Short: "Run a single scheduler sweep and exit",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("scheduler run-once")
		},
	})
	return cmd
}

// newDaemonCmd builds the `auspex daemon` stub tree (issue #7, M6; ADD
// §24.2). Stub ONLY on this bare tree — internal/app/wiring.App.RootCmd()
// replaces it with NewDaemonCmd (daemon.go) when the daemon is composed.
func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run and manage the Auspex background daemon",
	}
	for _, sub := range []struct{ use, short string }{
		{"run", "Run the daemon in the foreground (Ctrl-C or `auspex daemon stop` to exit)"},
		{"status", "Report whether the daemon is running (metadata + health probe)"},
		{"stop", "Signal the running daemon to shut down gracefully"},
		{"install", "Write a macOS LaunchAgent so the daemon starts at login and stays alive"},
		{"uninstall", "Remove the macOS LaunchAgent (run `launchctl unload` first if loaded)"},
	} {
		use := sub.use
		cmd.AddCommand(&cobra.Command{
			Use:   use,
			Short: sub.short,
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return notImplemented("daemon " + use)
			},
		})
	}
	return cmd
}

// newStatusCmd builds `auspex status`. Stub: depends on
// internal/app/wiring to summarize session/pause/quota state, not built
// this wave.
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show a summary of the current Auspex-managed session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("status")
		},
	}
}

// newGCCmd builds the standalone-stub `auspex gc` leaf (ADR-046 tiered
// telemetry retention). A stub ONLY on this bare tree —
// internal/app/wiring.App.RootCmd() replaces it with NewGCCmd's real
// handler (gc.go) once a retention engine is wired, the same
// stub-then-swap pattern `status`/`doctor` follow: a caller with no
// wired database still gets an honest "not yet available" instead of a
// handler that would immediately fail on a nil engine.
func newGCCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "gc",
		Short: "Archive and delete telemetry older than the retention window",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("gc")
		},
	}
}

// newExportCmd builds `auspex export`. Stub: the real exports
// (FR-170/171, issue #11) need the retention engine's *sqlite.DB wiring,
// same gating as gc — internal/app/wiring swaps in the real handlers
// (both leaves at once; the family is one seam, cli.Exporter).
func newExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export de-identified datasets for offline analysis",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "calibration",
		Short: "Export prediction-vs-actual calibration pairs as JSONL",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("export calibration")
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "observations",
		Short: "Export raw usage/context/quota series with turn boundaries as JSONL",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("export observations")
		},
	})
	return cmd
}

// newRunCmd builds the standalone-stub `auspex run` leaf (ADD §8.1
// managed one-shot mode, issue #8). A stub ONLY on this bare tree —
// internal/app/wiring.App.RootCmd() replaces it with NewRunCmd's real
// handler (run.go), the same stub-then-swap pattern `evaluate`/`gc`
// follow: a caller with no wired evaluation/telemetry services still gets
// an honest "not yet available" instead of a runner that would spawn a
// provider with no gate behind it. ArbitraryArgs (not NoArgs, unlike the
// other stubs) because the real command's shape is `run [flags] --
// <prompt>` — the stub must accept the same invocation and answer with
// the stub error, not a usage error. `auspex shell` (ADD §8.2) is a later
// issue-#8 increment and deliberately has no command here at all.
func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run [flags] -- <prompt>",
		Short: "Run a provider one-shot prompt under Auspex's managed gate",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("run")
		},
	}
}

// newReportCmd builds the standalone-stub `auspex report` leaf (issue
// #91, personal usage report). A stub ONLY on this bare tree —
// internal/app/wiring.App.RootCmd() replaces it with NewReportCmd's real
// handler (report.go) once a report engine is wired, the same
// stub-then-swap pattern `gc`/`export` follow: a caller with no wired
// database still gets an honest "not yet available" instead of a handler
// that would immediately fail on a nil engine.
func newReportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "report",
		Short: "Show a personal usage report (cost, tokens, model mix, cache hygiene, quota) for a recent window",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("report")
		},
	}
}

// newWatchCmd builds the standalone-stub `auspex watch codex` subtree
// (issue #92, the rollout-tailing watcher). A stub ONLY on this bare tree
// — internal/app/wiring.App.RootCmd() replaces it with NewWatchCmd's real
// handlers (watch.go) once the event store is wired, the same
// stub-then-swap pattern `gc`/`report` follow: a caller with no wired
// database still gets an honest "not yet available" instead of a watcher
// that would scan and then drop everything it found.
func newWatchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Watch provider session logs and capture usage telemetry",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "codex",
		Short: "Tail Codex session rollouts and capture usage from any surface (CLI, IDE, subagents)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("watch codex")
		},
	})
	return cmd
}

// newDoctorCmd builds `auspex doctor`. Stub: environment/provider
// capability diagnostics depend on ProviderDetector/ProviderCapabilityReader
// wiring, not built this wave.
func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose the local Auspex installation and provider setup",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("doctor")
		},
	}
}
