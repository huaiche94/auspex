// Package wiring is the in-process application composition layer
// (agents/runtime.md Part B: "wire the frozen ports into an
// in-process-first application"; ADD §13). It owns exactly one concern:
// collecting one implementation of each frozen cross-component service
// interface (internal/app/ports.go) into a validated container that the
// rest of Part B — the CLI command handlers (internal/cli, runtime-b03+),
// the orchestrator (internal/orchestrator), and eventually the daemon —
// consumes without knowing which concrete implementation it got.
//
// As of runtime-b02, no real implementation of any of these services
// exists (checkpoint's Progress Tree/State Checkpoint services,
// predictor's evaluation service, this same role's own pause service are
// all later nodes). That is by design, not a gap: this node builds the
// wiring SHAPE and proves it against internal/testutil/fakes
// (EXECUTION_DAG.md runtime-b02: "can start against
// claude-provider/checkpoint/predictor fakes"). When the real
// constructors land, populating Services with them instead of fakes is
// the only change — no signature here moves, because every field is a
// frozen interface type.
//
// Root wiring (cmd/preflight/main.go) is NOT this package's job: the
// contract-integrator/foundation roles own composing this container into
// the binary (agents/runtime.md "Exclusive paths").
package wiring

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/huaiche94/preflight/internal/app"
	"github.com/huaiche94/preflight/internal/cli"
	"github.com/huaiche94/preflight/internal/clock"
	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/idgen"
	"github.com/huaiche94/preflight/internal/orchestrator"
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

	// Diagnostics configures `preflight doctor`'s optional checks
	// (runtime-b08, internal/orchestrator.DoctorDeps). Also not required:
	// omitting it renders every doctor check CheckSkipped rather than
	// failing container construction — doctor is meant to run even in a
	// minimal environment (e.g. before `preflight init` has created a
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

// RootCmd builds the Preflight CLI command tree for this container. This
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
		Clock:      a.services.Hooks.Clock,
		IDs:        a.services.Hooks.IDs,
		Persister:  a.services.Hooks.Persister,
		TxRunner:   a.services.Hooks.TxRunner,
		Evaluation: a.services.Evaluation,
	}
	if hookDeps.Clock == nil {
		hookDeps.Clock = clock.New()
	}
	if hookDeps.IDs == nil {
		hookDeps.IDs = idgen.New()
	}

	replaceSubcommand(root, "hook", func(short string) *cobra.Command {
		newHook := &cobra.Command{Use: "hook", Short: short}
		newHook.AddCommand(cli.NewHookClaudeCmd(hookDeps))
		return newHook
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

	doctorDeps := orchestrator.DoctorDeps{
		DB:           a.services.Diagnostics.DB,
		Config:       a.services.Diagnostics.Config,
		RequiredDirs: a.services.Diagnostics.RequiredDirs,
	}
	replaceSubcommand(root, "doctor", func(_ string) *cobra.Command {
		return cli.NewDoctorCmd(doctorDeps)
	})

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
