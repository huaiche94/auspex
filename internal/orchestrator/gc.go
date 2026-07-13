// gc.go implements `auspex gc` (ADR-046 tiered telemetry retention) as
// an orchestration function internal/cli's command constructor calls
// into — the same deps-struct pattern diagnostics.go established for
// status/doctor. The actual retention protocol (select → rollup →
// archive → verify → delete → record) lives in internal/retention; this
// layer owns request validation and the CLI-facing shape only.
package orchestrator

import (
	"context"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/retention"
)

// RetentionRunner is the narrow interface GC needs from the retention
// engine — declared locally (not the concrete *retention.Engine) so the
// command handler can be tested against a fake without a real SQLite
// file, matching DBPinger/ConfigLoader's convention in diagnostics.go.
type RetentionRunner interface {
	Run(ctx context.Context, req retention.RunRequest) (retention.RunResult, error)
}

// GCDeps bundles GC's collaborators. Runner nil is the not-wired case: a
// bare CLI tree (or a composition without a database) keeps root.go's
// stub, and GC itself fails closed with ErrCodeUnavailable rather than
// pretending an empty pass ran.
type GCDeps struct {
	Runner RetentionRunner
}

// GCRequest is `auspex gc`'s input, one field per flag.
type GCRequest struct {
	// DryRun reports what would be archived/deleted with zero side
	// effects (no archive files, no rollup rows, no retention_runs row —
	// ADR-046 "Dry run").
	DryRun bool
	// RetentionDays is the hot-window override; 0 means the ADR-046
	// default (90). Negative values are rejected.
	RetentionDays int
	// Vacuum runs a full VACUUM after a deleting pass (ADR-046 "Space
	// reclamation": today's databases run auto_vacuum=NONE, so this is
	// the only way to shrink the file — opt-in because it rewrites the
	// whole database under an exclusive lock).
	Vacuum bool
}

// GC implements `auspex gc`: run one tiered-retention pass (or a dry
// run) and return its full accounting. Unlike status/doctor this command
// MUTATES the database (that is its purpose), so a missing dependency is
// an error, never a silent skip.
func GC(ctx context.Context, deps GCDeps, req GCRequest) (retention.RunResult, error) {
	if req.RetentionDays < 0 {
		return retention.RunResult{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "orchestrator: GC requires a positive --retention-days",
			Retryable: false,
		}
	}
	if deps.Runner == nil {
		return retention.RunResult{}, &domain.Error{
			Code:      domain.ErrCodeUnavailable,
			Message:   "orchestrator: no retention engine wired",
			Retryable: false,
		}
	}
	return deps.Runner.Run(ctx, retention.RunRequest{
		Policy: retention.Policy{RetentionDays: req.RetentionDays},
		DryRun: req.DryRun,
		Vacuum: req.Vacuum,
	})
}
