// Package wiring is the in-process application composition layer
// (agents/runtime.md Part B: "wire the frozen ports into an
// in-process-first application"; ADD §13). It owns exactly one concern:
// collecting one implementation of each frozen cross-component service
// interface (internal/app/ports.go) into a validated container that the
// rest of Part B — the CLI command handlers (internal/cli, runtime-b03+),
// the orchestrator (internal/orchestrator), and eventually the daemon —
// consumes without knowing which concrete implementation it got.
//
// This node (runtime-b02) built the wiring SHAPE first and proved it
// against internal/testutil/fakes before any real service existed
// (EXECUTION_DAG.md runtime-b02: "can start against
// claude-provider/checkpoint/predictor fakes"). The real implementations
// have since landed — checkpoint's Progress Tree/State Checkpoint
// services, predictor's evaluation service, this role's own pause service
// — and cmd/auspex/wire.go now populates Services with them for the binary.
// The design promise held: because every field is a frozen interface type,
// no signature here moved, so the same container still accepts a fake for a
// test or a real implementation for production (see this package's
// *_swap_test.go, which exercise both against one wiring).
//
// Root wiring (cmd/auspex/main.go) is NOT this package's job: the
// contract-integrator/foundation roles own composing this container into
// the binary (agents/runtime.md "Exclusive paths").
package wiring

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/cli"
	"github.com/huaiche94/auspex/internal/clock"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/idgen"
	"github.com/huaiche94/auspex/internal/orchestrator"
)

// Services carries one implementation of each frozen service interface.
// All five fields are required; New rejects a partially-populated struct
// (see New). Fields are the interface types themselves — real
// implementations, internal/testutil/fakes doubles, and future
// composites are all equally valid values.
type Services struct {
	Evaluation           app.EvaluationService
	ProgressTree         app.ProgressTreeService
	StateCheckpoint      app.StateCheckpointService
	GracefulPause        app.GracefulPauseService
	RepositoryCheckpoint app.RepositoryCheckpointService

	// Hooks configures the Claude Code hook command handlers
	// (runtime-b04, internal/orchestrator.HookDeps). Unlike the five
	// service fields above, this is NOT required: a caller that only
	// needs the other P0 commands (e.g. most tests) may leave it at its
	// zero value, and RootCmd falls back to real domain.Clock/
	// domain.IDGenerator implementations with no event persistence or
	// evaluation wiring (matching HookDeps' own documented nil-safe
	// defaults — see internal/orchestrator/hooks.go). A caller that wants
	// hook telemetry actually persisted, or UserPromptSubmit actually
	// evaluated, sets Hooks explicitly.
	Hooks HookSupport

	// Diagnostics configures `auspex doctor`'s optional checks
	// (runtime-b08, internal/orchestrator.DoctorDeps). Also not required:
	// omitting it renders every doctor check CheckSkipped rather than
	// failing container construction — doctor is meant to run even in a
	// minimal environment (e.g. before `auspex init` has created a
	// database at all), and reporting "skipped, not configured" for each
	// missing piece IS doctor's correct behavior in that case, not a
	// construction-time error.
	Diagnostics DiagnosticsSupport

	// PauseLifecycle configures `pause request`/`pause cancel`/`resume`/
	// `scheduler run-once` (runtime-b07, internal/orchestrator.
	// PauseLifecycleDeps). Not required, same reasoning as Diagnostics: a
	// caller that hasn't wired Part A's real stores yet still gets a
	// working container, with these four commands left as RootCmd's
	// original stub tree rather than swapped for real handlers — see
	// RootCmd's own replaceSubcommand call for exactly which condition
	// triggers the swap.
	PauseLifecycle orchestrator.PauseLifecycleDeps

	// GC configures `auspex gc` (ADR-046 tiered telemetry retention).
	// Not required, same reasoning as PauseLifecycle: the retention
	// engine needs a real *sqlite.DB and data directory (no fake-able
	// frozen interface stands in for it — gc is an internal maintenance
	// concern, deliberately NOT a frozen app.* port), so a caller that
	// hasn't wired one keeps RootCmd's original `gc` stub rather than a
	// handler that would immediately fail closed on every call. See
	// RootCmd's own condition for exactly which field gates the swap.
	GC orchestrator.GCDeps

	// Decision configures `decision allow`/`decision deny` (runtime-b06,
	// internal/orchestrator.DecisionDeps). Not required: a caller that
	// hasn't wired the real internal/evaluation.Service's
	// AuthorizationIssuer seam yet (Decision.Issuer nil) keeps RootCmd's
	// original `decision` stub tree — see RootCmd's own condition for
	// exactly which field gates the swap. Decision.Evaluation, unlike
	// Issuer, is NOT independently sufficient to trigger the swap: the
	// issue flow's AuthorizationIssuer is this command's whole reason for
	// requiring the REAL service (a fake can implement
	// app.EvaluationService alone perfectly well, but only the real
	// *internal/evaluation.Service also satisfies AuthorizationIssuer),
	// so gating on Issuer alone is the correct, minimal signal that real
	// wiring is actually in place.
	Decision orchestrator.DecisionDeps

	// Daemon configures `auspex daemon run|status|stop|install|uninstall`
	// (issue #7, M6; internal/orchestrator.DaemonDeps). Not required, same
	// reasoning as GC: the daemon needs real stores, real dirs, and a
	// composed internal/daemon.Daemon — a caller that hasn't wired them
	// keeps RootCmd's original `daemon` stub tree. The swap gates on
	// RuntimeDir (every subcommand needs it) rather than Daemon (only
	// `run` does).
	Daemon orchestrator.DaemonDeps
}

// HookSupport bundles the optional collaborators
// internal/orchestrator.HookDeps needs beyond the five core services
// above. See Services.Hooks' doc comment for the zero-value fallback
// behavior.
type HookSupport struct {
	Clock     domain.Clock
	IDs       domain.IDGenerator
	Persister orchestrator.EventPersister
	TxRunner  app.TxRunner

	// SessionResolver optionally enables hook event correlation (issue #1,
	// internal/orchestrator/correlate.go): when non-nil, RootCmd wires an
	// orchestrator.EventCorrelator over it (session -> task) and the
	// container's own ProgressTree service (task -> single in-progress
	// node), so persisted hook events carry TaskID/ProgressNodeID whenever
	// they resolve unambiguously. The real value is the same
	// internal/evaluation.SQLDataSource cmd/auspex/wire.go already
	// constructs for the evaluation pipeline (it satisfies the narrow
	// orchestrator.SessionResolver view of the frozen app.FeatureDataSource
	// port). nil disables correlation entirely — events persist with
	// SessionID only, exactly the pre-issue-#1 behavior — which is the
	// right degrade for callers with no session registry to resolve
	// against (most tests, minimal compositions).
	SessionResolver orchestrator.SessionResolver

	// OpenTurns optionally enables turn correlation for terminal hook
	// events (issue #11, orchestrator.HookDeps.OpenTurns): when non-nil,
	// Stop/StopFailure events are stamped with the session's latest
	// started turn's ID, activating the prediction↔actual outcome join
	// (ADR-046). The real value is cmd/auspex's events-table adapter;
	// nil disables stamping — terminal events persist with SessionID
	// only, the pre-#11 behavior, which is the right degrade for
	// compositions without an events store to resolve against.
	OpenTurns orchestrator.OpenTurnResolver

	// Bootstrapper optionally enables the issue-#17 lazy session
	// bootstrap (internal/orchestrator/sessionbootstrap.go): when
	// non-nil, every hook handler registers the session's repositories/
	// worktrees/provider_sessions chain from the payload's reported
	// directory before persisting/evaluating, which is what lets
	// SQLDataSource.Resolve — and therefore the whole evaluation
	// pipeline, the issue-#14 forecast card, and issue #1's TaskID
	// correlation — actually work in real native-hook sessions instead
	// of only in test-seeded databases. cmd/auspex/wire.go constructs
	// the real value over the same *sqlite.DB and gitx.Client it already
	// composes. nil disables registration (most tests, minimal
	// compositions), degrading to the pre-issue-#17 behavior per
	// orchestrator.HookDeps.Bootstrapper's own documented contract.
	Bootstrapper *orchestrator.SessionBootstrapper

	// Forecast optionally enables the issue-#14 forecast surfaces: the
	// UserPromptSubmit hook's additionalContext card, the statusline
	// --emit-line display, and `auspex evaluate`'s card output. Like
	// Decision.Issuer, only the REAL *internal/evaluation.Service
	// satisfies orchestrator.ForecastCardSource (a card is a read-back of
	// the persisted prediction/policy rows only the real service owns) —
	// cmd/auspex/wire.go passes its evaluation.Service here. nil
	// degrades every surface to its pre-issue-#14 output (no card block,
	// model-only status line, `evaluate` without card numbers), per
	// orchestrator.HookDeps.Forecast's own documented contract.
	Forecast orchestrator.ForecastCardSource
}

// DiagnosticsSupport bundles the optional collaborators
// internal/orchestrator.DoctorDeps needs. See Services.Diagnostics' doc
// comment for the zero-value (all-skipped) fallback behavior.
type DiagnosticsSupport struct {
	DB           orchestrator.DBPinger
	Config       orchestrator.ConfigLoader
	RequiredDirs []string
}

// App is the validated, immutable-after-construction service container.
// Accessors never return nil for a container built by New.
type App struct {
	services Services
}

// New validates that every service in s is present and returns the
// container. A missing service is a construction-time bug in the caller's
// composition root, so this fails closed with the frozen domain.Error
// shape (ErrCodeValidation, Retryable: false, Details naming every
// missing field) rather than deferring the nil-pointer panic to whichever
// command handler first touches the hole at runtime (CONTRACT_FREEZE.md
// "Error contract": state/composition integrity failures fail closed).
func New(s Services) (*App, error) {
	var missing []string
	if s.Evaluation == nil {
		missing = append(missing, "Evaluation")
	}
	if s.ProgressTree == nil {
		missing = append(missing, "ProgressTree")
	}
	if s.StateCheckpoint == nil {
		missing = append(missing, "StateCheckpoint")
	}
	if s.GracefulPause == nil {
		missing = append(missing, "GracefulPause")
	}
	if s.RepositoryCheckpoint == nil {
		missing = append(missing, "RepositoryCheckpoint")
	}
	if len(missing) > 0 {
		return nil, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "wiring: missing required services: " + strings.Join(missing, ", "),
			Retryable: false,
			Details: map[string]string{
				"missing_services": strings.Join(missing, ","),
			},
		}
	}
	return &App{services: s}, nil
}

// Evaluation returns the wired app.EvaluationService.
func (a *App) Evaluation() app.EvaluationService { return a.services.Evaluation }

// ProgressTree returns the wired app.ProgressTreeService.
func (a *App) ProgressTree() app.ProgressTreeService { return a.services.ProgressTree }

// StateCheckpoint returns the wired app.StateCheckpointService.
func (a *App) StateCheckpoint() app.StateCheckpointService { return a.services.StateCheckpoint }

// GracefulPause returns the wired app.GracefulPauseService.
func (a *App) GracefulPause() app.GracefulPauseService { return a.services.GracefulPause }

// RepositoryCheckpoint returns the wired app.RepositoryCheckpointService.
func (a *App) RepositoryCheckpoint() app.RepositoryCheckpointService {
	return a.services.RepositoryCheckpoint
}

// RootCmd builds the Auspex CLI command tree for this container. This
// is the seam between the wiring layer and internal/cli: runtime-b02
// started from cli.NewRootCmd()'s all-stub tree; runtime-b04 (this
// change) is the first node to actually thread a service into a command
// constructor — the `hook claude ...` subtree is now replaced with
// internal/cli.NewHookClaudeCmd's real handlers, wired against an
// internal/orchestrator.HookDeps built from a.services.Hooks (falling
// back to real domain.Clock/domain.IDGenerator implementations when the
// caller left HookSupport at its zero value — see Services.Hooks' doc
// comment). Every other P0 command remains cli.NewRootCmd's stub as of
// this node; later nodes (runtime-b05, b08, ...) replace them the same
// way. Callers that want the CLI wired to *this* App must obtain the tree
// here rather than calling cli.NewRootCmd directly, so this replacement
// is invisible to them.
func (a *App) RootCmd() *cobra.Command {
	root := cli.NewRootCmd()

	hookDeps := orchestrator.HookDeps{
		Clock:        a.services.Hooks.Clock,
		IDs:          a.services.Hooks.IDs,
		Persister:    a.services.Hooks.Persister,
		TxRunner:     a.services.Hooks.TxRunner,
		Evaluation:   a.services.Evaluation,
		Forecast:     a.services.Hooks.Forecast,
		Bootstrapper: a.services.Hooks.Bootstrapper,
		OpenTurns:    a.services.Hooks.OpenTurns,
	}
	if hookDeps.Clock == nil {
		hookDeps.Clock = clock.New()
	}
	if hookDeps.IDs == nil {
		hookDeps.IDs = idgen.New()
	}
	// Event correlation (issue #1) is gated on a SessionResolver being
	// wired: the correlator's task half has nothing to resolve against
	// without one, and orchestrator.EventCorrelator's own contract treats
	// nil as a documented no-op — see Services.Hooks.SessionResolver's doc
	// comment. The node half always uses this container's own required
	// ProgressTree service, real or fake alike (Snapshot is the only
	// method consumed, via the narrow orchestrator.ProgressSnapshotReader
	// view).
	if a.services.Hooks.SessionResolver != nil {
		hookDeps.Correlator = &orchestrator.EventCorrelator{
			Sessions: a.services.Hooks.SessionResolver,
			Progress: a.services.ProgressTree,
		}
	}

	replaceSubcommand(root, "hook", func(short string) *cobra.Command {
		newHook := &cobra.Command{Use: "hook", Short: short}
		newHook.AddCommand(cli.NewHookClaudeCmd(hookDeps))
		return newHook
	})

	// evaluate (issue #14): swapped unconditionally, like progress/
	// checkpoint/status, because its one required dependency (Evaluation)
	// is a required service this container cannot exist without. It
	// shares hookDeps with the hook subtree deliberately — `auspex
	// evaluate` runs the SAME production evaluation path the
	// UserPromptSubmit hook runs (orchestrator.EvaluatePrompt over the
	// same normalizer/persister/evaluation/forecast collaborators), which
	// is the whole point of issue #14 deliverable 5's "share code, don't
	// duplicate". A nil Hooks.Forecast merely degrades the card output,
	// per cli.NewEvaluateCmd's own contract.
	replaceSubcommand(root, "evaluate", func(_ string) *cobra.Command {
		return cli.NewEvaluateCmd(hookDeps)
	})

	// run (issue #8, ADD §8.1 managed one-shot MVP): swapped
	// unconditionally, like evaluate, because its one required service
	// (Evaluation, for the pre-prompt gate) is a required field this
	// container cannot exist without, and it shares the SAME hookDeps —
	// deliberately, so the gate `auspex run` enforces before spawning the
	// provider is the exact evaluation path the UserPromptSubmit hook
	// runs (internal/managed's doc). Nil Persister/Bootstrapper/Forecast
	// degrade per HookDeps' own documented contracts (no telemetry
	// persisted, no lazy session registration, no forecast card) — the
	// run itself still gates and executes.
	replaceSubcommand(root, "run", func(_ string) *cobra.Command {
		return cli.NewRunCmd(hookDeps)
	})

	checkpointDeps := orchestrator.CheckpointCreateDeps{
		StateCheckpoint:      a.services.StateCheckpoint,
		RepositoryCheckpoint: a.services.RepositoryCheckpoint,
	}
	replaceSubcommand(root, "checkpoint", func(_ string) *cobra.Command {
		return cli.NewCheckpointCmd(checkpointDeps)
	})

	statusDeps := orchestrator.StatusDeps{ProgressTree: a.services.ProgressTree}
	replaceSubcommand(root, "status", func(_ string) *cobra.Command {
		return cli.NewStatusCmd(statusDeps)
	})

	// progress (issue #1): swapped unconditionally, like checkpoint/status,
	// because its one dependency (ProgressTree) is a required service this
	// container cannot exist without. Inside the replacement, `complete` is
	// real and `show` remains root.go's stub — see cli.NewProgressCmd.
	progressDeps := orchestrator.ProgressCompleteDeps{ProgressTree: a.services.ProgressTree}
	replaceSubcommand(root, "progress", func(_ string) *cobra.Command {
		return cli.NewProgressCmd(progressDeps)
	})

	doctorDeps := orchestrator.DoctorDeps{
		DB:           a.services.Diagnostics.DB,
		Config:       a.services.Diagnostics.Config,
		RequiredDirs: a.services.Diagnostics.RequiredDirs,
	}
	replaceSubcommand(root, "doctor", func(_ string) *cobra.Command {
		return cli.NewDoctorCmd(doctorDeps)
	})

	// gc (ADR-046) only swaps to the real handler when a retention
	// engine has actually been wired — same gating convention as
	// pause/decision below, and for the same reason: no fake-able frozen
	// interface stands in for the engine's real *sqlite.DB + data-dir
	// dependencies (see Services.GC's own doc comment).
	if a.services.GC.Runner != nil {
		gcDeps := a.services.GC
		replaceSubcommand(root, "gc", func(_ string) *cobra.Command {
			return cli.NewGCCmd(gcDeps)
		})
	}

	// export (FR-170/171, issue #11) rides the same retention-engine
	// wiring gc does — the exporter is the engine itself, so the same
	// nil-gate applies for the same reason. The assertion is on the full
	// cli.Exporter union (calibration + observations): NewExportCmd swaps
	// the WHOLE export family at once, so a runner serving only one
	// dataset keeps the stubs rather than half-wiring the family.
	if exporter, ok := a.services.GC.Runner.(cli.Exporter); ok && a.services.GC.Runner != nil {
		replaceSubcommand(root, "export", func(_ string) *cobra.Command {
			return cli.NewExportCmd(exporter)
		})
	}

	// pause/resume/scheduler (runtime-b07) only swap to the real handlers
	// when a Store has actually been wired — unlike the other command
	// families above, Part A's stores have no fake-able frozen interface
	// standing in for them (this role's own real internal/pause,
	// internal/scheduler, same-branch dependency per the DAG), so a
	// caller that hasn't wired PauseLifecycle yet keeps RootCmd's original
	// stub tree rather than swapping to a handler that would immediately
	// fail closed on every call.
	if a.services.PauseLifecycle.Store != nil {
		pauseDeps := a.services.PauseLifecycle
		replaceSubcommand(root, "pause", func(_ string) *cobra.Command {
			return cli.NewPauseCmd(pauseDeps)
		})
		replaceSubcommand(root, "resume", func(_ string) *cobra.Command {
			return cli.NewResumeCmd(pauseDeps)
		})
	}
	if a.services.PauseLifecycle.WakeJobs != nil {
		schedulerDeps := a.services.PauseLifecycle
		replaceSubcommand(root, "scheduler", func(_ string) *cobra.Command {
			return cli.NewSchedulerCmd(schedulerDeps)
		})
	}

	// daemon (issue #7, M6) swaps when the runtime directory is wired —
	// see Services.Daemon's own doc comment for why that field gates.
	if a.services.Daemon.RuntimeDir != "" {
		daemonDeps := a.services.Daemon
		replaceSubcommand(root, "daemon", func(_ string) *cobra.Command {
			return cli.NewDaemonCmd(daemonDeps)
		})
	}

	// decision (runtime-b06) only swaps to the real handlers once the
	// REAL internal/evaluation.Service's AuthorizationIssuer seam has been
	// wired — see Services.Decision's own doc comment for why Issuer,
	// specifically, is the gating field rather than Evaluation.
	if a.services.Decision.Issuer != nil {
		decisionDeps := a.services.Decision
		if decisionDeps.Evaluation == nil {
			decisionDeps.Evaluation = a.services.Evaluation
		}
		replaceSubcommand(root, "decision", func(_ string) *cobra.Command {
			return cli.NewDecisionCmd(decisionDeps)
		})
	}

	// runtime-b09: cli.NewRootCmd() already wrapped its own stub tree with
	// cli.WithJSONErrorRendering, but every replaceSubcommand call above
	// swaps in a FRESH subtree (cli.NewHookClaudeCmd, cli.NewCheckpointCmd,
	// ...) built after that wrapping already ran — those fresh RunE
	// closures are unwrapped. Re-applying the wrap here, once, after every
	// replacement, ensures every command reachable from THIS App's actual
	// command tree (real or still-stub) uniformly renders the typed JSON
	// error envelope, not just whichever subset happened to still be a
	// stub when cli.NewRootCmd() ran. wrapCommandTree recurses the whole
	// tree and is idempotent per leaf (each RunE is wrapped exactly once
	// here, since this is the only wrap call that runs after every
	// replacement), so calling it again on already-wrapped leaves that
	// were NOT replaced is harmless — see errors.go's own doc comment.
	return cli.WithJSONErrorRendering(root)
}

// replaceSubcommand removes root's top-level subcommand named name (a
// no-op if none matches) and adds the command built builds returns in its
// place. build receives the removed command's Short text so a replacement
// can preserve it without repeating the string in two files
// (internal/cli's stub and this wiring). Centralizes the
// find-remove-rebuild-add pattern every runtime-b0N node that swaps a stub
// subtree for a real one needs, so each node's RootCmd change is a single
// call rather than a hand-rolled loop.
func replaceSubcommand(root *cobra.Command, name string, build func(short string) *cobra.Command) {
	for _, sub := range root.Commands() {
		if sub.Name() != name {
			continue
		}
		root.RemoveCommand(sub)
		root.AddCommand(build(sub.Short))
		return
	}
}
