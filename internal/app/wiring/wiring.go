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

	for _, sub := range root.Commands() {
		if sub.Name() != "hook" {
			continue
		}
		root.RemoveCommand(sub)
		newHook := &cobra.Command{Use: "hook", Short: sub.Short}
		newHook.AddCommand(cli.NewHookClaudeCmd(hookDeps))
		root.AddCommand(newHook)
		break
	}

	return root
}
