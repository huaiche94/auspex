package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/huaiche94/preflight/internal/buildinfo"
)

// NewRootCmd builds the root `preflight` Cobra command with the full P0
// command tree (agents/runtime.md Part B "P0 commands") registered under
// it. Business logic stays out of RunE handlers per Preflight_ADD.md §10.1;
// every handler below is either a direct call to a foundation-owned helper
// (buildinfo.String, for `version`) or an honest stub returning
// errNotImplemented until the service it depends on is wired (see doc.go).
//
// Kept separate from any os.Exit call so it is fully testable — mirrors
// cmd/preflight/main.go's own newRootCmd() convention. This constructor is
// not called from cmd/preflight/main.go yet; that root-wiring integration
// step belongs to contract-integrator/foundation per agents/runtime.md.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "preflight",
		Short:         "Preflight is a local-first predictive runtime guard for AI coding agents.",
		SilenceUsage:  true,
		SilenceErrors: false,
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
		newStatusCmd(),
		newDoctorCmd(),
	)

	return root
}

// newVersionCmd builds `preflight version`. Unlike every other command in
// this package, it is fully real — buildinfo.String() has no service
// dependency, so there is nothing to stub.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the Preflight version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), buildinfo.String())
			return err
		},
	}
}

// newInitCmd builds `preflight init` (ADD §10.1 day-one setup flow). Stub:
// workspace/repository registration depends on internal/app/wiring, not
// built this wave.
func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize Preflight for the current repository",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("init")
		},
	}
}

// newEvaluateCmd builds `preflight evaluate` (ADD §9.9 EvaluationService).
// Stub: depends on internal/orchestrator wiring the predictor pipeline,
// not built this wave.
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

// newDecisionCmd builds `preflight decision {allow,deny}`. Stub: both
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

// newCheckpointCmd builds `preflight checkpoint create`. Stub: depends on
// sequencing checkpoint role Part A (state) then Part B (repository) per
// the frozen transaction/orchestration contract (CONTRACT_FREEZE.md
// "Transaction boundaries"), not wired this wave.
func newCheckpointCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "checkpoint",
		Short: "Manage Preflight checkpoints",
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

// newProgressCmd builds `preflight progress show` (ProgressTreeService.Snapshot).
// Stub: depends on internal/app/wiring, not built this wave.
func newProgressCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "progress",
		Short: "Inspect the Progress Tree",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Show the current Progress Tree snapshot",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("progress show")
		},
	})
	return cmd
}

// newStateCmd builds `preflight state show` (StateCheckpointService.LoadLatest).
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

// newPauseCmd builds `preflight pause {request,cancel}` (GracefulPauseService).
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

// newResumeCmd builds `preflight resume` (GracefulPauseService.Resume).
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

// newSchedulerCmd builds `preflight scheduler run-once` (durable wake
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

// newStatusCmd builds `preflight status`. Stub: depends on
// internal/app/wiring to summarize session/pause/quota state, not built
// this wave.
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show a summary of the current Preflight-managed session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("status")
		},
	}
}

// newDoctorCmd builds `preflight doctor`. Stub: environment/provider
// capability diagnostics depend on ProviderDetector/ProviderCapabilityReader
// wiring, not built this wave.
func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose the local Preflight installation and provider setup",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("doctor")
		},
	}
}
