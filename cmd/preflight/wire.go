// wire.go is cmd/preflight's composition root — the one place, per every
// role's own agents/*.md exclusive-path declarations and
// internal/app/wiring's own doc comment ("Root wiring (cmd/preflight/
// main.go) is NOT this package's job: the contract-integrator/foundation
// roles own composing this container into the binary"), that is
// authorized to import every role's concrete implementation and assemble
// them into the frozen internal/app/wiring.Services container.
//
// This file adds no new business logic: every real implementation it
// constructs already exists, already has its own package's tests, and is
// composed here exactly as each role's own "Final integration gate
// corrective addition" wave built it to be composed (internal/progress.
// Service, internal/statecheckpoint.Service, internal/repocheckpoint.
// Service, internal/pause.Service, internal/evaluation.Service +
// SQLDataSource). The only new code in this file is DTO-shape translation
// (adapters.go) and directory/path resolution.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/huaiche94/preflight/internal/app"
	"github.com/huaiche94/preflight/internal/app/wiring"
	"github.com/huaiche94/preflight/internal/artifacts"
	"github.com/huaiche94/preflight/internal/clock"
	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/evaluation"
	"github.com/huaiche94/preflight/internal/gitx"
	"github.com/huaiche94/preflight/internal/idgen"
	"github.com/huaiche94/preflight/internal/orchestrator"
	"github.com/huaiche94/preflight/internal/paths"
	"github.com/huaiche94/preflight/internal/pause"
	"github.com/huaiche94/preflight/internal/policy"
	"github.com/huaiche94/preflight/internal/predictor/quota"
	"github.com/huaiche94/preflight/internal/predictor/risk"
	"github.com/huaiche94/preflight/internal/predictor/scope"
	"github.com/huaiche94/preflight/internal/predictor/token"
	"github.com/huaiche94/preflight/internal/progress"
	"github.com/huaiche94/preflight/internal/repocheckpoint"
	"github.com/huaiche94/preflight/internal/scheduler"
	"github.com/huaiche94/preflight/internal/statecheckpoint"
	"github.com/huaiche94/preflight/internal/storage/sqlite"
	claudetelemetry "github.com/huaiche94/preflight/internal/telemetry/claude"

	"github.com/spf13/cobra"
)

// buildRootCmd opens (creating if needed) Preflight's SQLite database
// under the OS-appropriate user data directory, migrates it, composes one
// real implementation of every frozen app.* service interface plus the
// hook/diagnostics/pause-lifecycle support wiring.App.RootCmd needs, and
// returns the resulting fully-wired Cobra command tree. The returned
// closeFn must be deferred by the caller to release the DB connection.
func buildRootCmd(ctx context.Context) (root *cobra.Command, closeFn func() error, err error) {
	dirs, err := paths.ResolveHost(paths.NewOSEnv())
	if err != nil {
		return nil, nil, fmt.Errorf("cmd/preflight: resolve user data directory: %w", err)
	}

	if err := os.MkdirAll(dirs.Data, 0o755); err != nil {
		return nil, nil, fmt.Errorf("cmd/preflight: create data directory %s: %w", dirs.Data, err)
	}

	dbPath := filepath.Join(dirs.Data, "preflight.db")
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("cmd/preflight: open database %s: %w", dbPath, err)
	}
	closeFn = db.Close

	migrations, err := sqlite.AllMigrations()
	if err != nil {
		_ = closeFn()
		return nil, nil, fmt.Errorf("cmd/preflight: load migrations: %w", err)
	}
	if err := db.Migrate(ctx, migrations); err != nil {
		_ = closeFn()
		return nil, nil, fmt.Errorf("cmd/preflight: migrate database: %w", err)
	}

	clk := clock.New()
	ids := idgen.New()
	gitClient := gitx.NewClient(gitx.ExecRunner{})

	// --- checkpoint Part A: Progress Tree -----------------------------
	taskStore := progress.NewTaskStore(db, clk)
	nodeStore := progress.NewNodeStore(db, clk)
	edgeStore := progress.NewEdgeStore(db)
	artifactStore := progress.NewArtifactStore(db)
	validators := artifacts.NewRegistry()
	stagingDir := filepath.Join(dirs.Data, "staging")
	stager, err := progress.NewFileStager(stagingDir)
	if err != nil {
		_ = closeFn()
		return nil, nil, fmt.Errorf("cmd/preflight: create artifact staging directory %s: %w", stagingDir, err)
	}
	checkpointStore := statecheckpoint.NewStore(db)
	completeOp := &progress.CompleteNode{
		DB:          db,
		Clock:       clk,
		IDs:         ids,
		Nodes:       nodeStore,
		Edges:       edgeStore,
		Artifacts:   artifactStore,
		Validators:  validators,
		Stager:      stager,
		Checkpoints: checkpointStore,
		Publisher:   progress.NoopPublisher{},
	}
	reconciler := &progress.Reconciler{
		Nodes:       nodeStore,
		Checkpoints: checkpointStore,
		EvidenceDir: stagingDir,
	}
	progressTreeService := progress.NewService(taskStore, nodeStore, completeOp, reconciler, clk, ids)

	// --- checkpoint Part A: State Checkpointing -----------------------
	treeReader := treeReaderAdapter{nodes: nodeStore, artifacts: artifactStore}
	stateCheckpointService := statecheckpoint.NewService(checkpointStore, treeReader, clk, ids)

	// --- checkpoint Part B: Repository Checkpoint ---------------------
	repoCheckpointStore := repocheckpoint.NewStore(db)
	artifactsRoot := filepath.Join(dirs.Data, "checkpoints")
	resolveWorktree := func(ctx context.Context, worktreeID domain.WorktreeID) (repocheckpoint.WorktreeLocation, error) {
		root, repositoryID, err := resolveWorktreeLocation(ctx, db, worktreeID)
		if err != nil {
			return repocheckpoint.WorktreeLocation{}, err
		}
		return repocheckpoint.WorktreeLocation{RepositoryID: repositoryID, Path: root}, nil
	}
	repositoryCheckpointService := repocheckpoint.NewService(
		gitClient, repoCheckpointStore, clk, ids, artifactsRoot, resolveWorktree,
		// ADD §19.5's default untracked-archive policy (5 MiB/file,
		// 100 MiB total); file-count cap is this composition's own
		// operational default, since the ADD does not name one.
		repocheckpoint.CaptureOptions{
			MaxUntrackedFileBytes:  5 * 1024 * 1024,
			MaxUntrackedTotalBytes: 100 * 1024 * 1024,
			MaxUntrackedFileCount:  10000,
		},
	)

	// --- predictor pipeline + evaluation persistence ------------------
	dataSource := evaluation.NewSQLDataSource(db)
	scopeEstimator := scope.NewRuleScopeEstimator(dataSource)
	tokenForecaster := token.NewRuleTokenForecaster(tokenFeatureSourceAdapter{source: dataSource})
	quotaForecaster := quota.NewRuleQuotaForecaster()
	riskCombiner := risk.NewRuleRiskCombiner()
	decider := policy.NewDecider()
	evaluationService := evaluation.New(db, dataSource, scopeEstimator, tokenForecaster, quotaForecaster, riskCombiner, decider, clk, ids)

	// --- runtime Part A: Graceful Pause / Scheduler -------------------
	pauseStore := pause.NewSQLiteStore(db)
	wakeJobStore := scheduler.NewStore(db.Conn(), clk, ids)
	gracefulPauseService := pause.NewService(pause.ServiceDeps{
		Store:                pauseStore,
		Clock:                clk,
		IDs:                  ids,
		Sessions:             sessionContextResolverAdapter{db: db, source: dataSource},
		ProgressTree:         progressTreeService,
		StateCheckpoint:      stateCheckpointService,
		RepositoryCheckpoint: repositoryCheckpointService,
		WakeJobs:             wakeJobStore,
		// Interrupter/Session: managed provider interrupt and resume are
		// explicit stretch goals never built in this vertical slice (see
		// adapters.go's stubTurnInterrupter/sessionCapabilityReaderStub
		// doc comments) — both fail closed rather than fabricating a
		// capability that does not exist yet.
		Interrupter: stubTurnInterrupter{},
		Locate: func(pauseID domain.PauseID) app.RunLocator {
			// No real run-locator registry exists yet (it would need to
			// track which live SessionID/TurnID a PauseID's interrupt
			// call should target, itself only meaningful once a real
			// TurnInterrupter exists) -- this returns a zero-value
			// locator, which stubTurnInterrupter's own fail-closed
			// Interrupt call reports as an unavailable capability
			// regardless of its contents.
			return app.RunLocator{}
		},
		Quota:           quotaSnapshotReaderAdapter{source: dataSource},
		RepoFingerprint: repoFingerprintReaderAdapter{db: db, git: gitClient},
		Session:         sessionCapabilityReaderStub{},
		Evaluations:     evaluationService,
	})

	services := wiring.Services{
		Evaluation:           evaluationService,
		ProgressTree:         progressTreeService,
		StateCheckpoint:      stateCheckpointService,
		GracefulPause:        gracefulPauseService,
		RepositoryCheckpoint: repositoryCheckpointService,
		Hooks: wiring.HookSupport{
			Clock:     clk,
			IDs:       ids,
			Persister: claudetelemetry.NewEventStore(db),
			TxRunner:  db,
		},
		Diagnostics: wiring.DiagnosticsSupport{
			DB: db,
		},
		PauseLifecycle: orchestrator.PauseLifecycleDeps{
			Store:    pauseStore,
			WakeJobs: wakeJobStore,
		},
	}

	app, err := wiring.New(services)
	if err != nil {
		_ = closeFn()
		return nil, nil, fmt.Errorf("cmd/preflight: wire services: %w", err)
	}
	return app.RootCmd(), closeFn, nil
}
