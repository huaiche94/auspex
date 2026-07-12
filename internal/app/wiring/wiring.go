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
	"github.com/huaiche94/preflight/internal/domain"
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
// is the seam between the wiring layer and internal/cli: as of
// runtime-b02 every handler below `preflight version` is still
// runtime-b01's honest ErrCodeUnavailable stub, so the container's
// services are not yet threaded into individual handlers — runtime-b03+
// replaces those stubs by passing the relevant service into each command
// constructor, and this method is where that threading starts. Callers
// that want the CLI wired to *this* App must obtain the tree here rather
// than calling cli.NewRootCmd directly, so the b03+ change is invisible
// to them.
func (a *App) RootCmd() *cobra.Command {
	return cli.NewRootCmd()
}
