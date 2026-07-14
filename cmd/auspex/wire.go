// wire.go is cmd/auspex's composition root — the one place, per every
// role's own agents/*.md exclusive-path declarations and
// internal/app/wiring's own doc comment ("Root wiring (cmd/auspex/
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
	"net/http"
	"os"
	"path/filepath"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/app/wiring"
	"github.com/huaiche94/auspex/internal/artifacts"
	"github.com/huaiche94/auspex/internal/buildinfo"
	"github.com/huaiche94/auspex/internal/clock"
	"github.com/huaiche94/auspex/internal/daemon"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/evaluation"
	"github.com/huaiche94/auspex/internal/gitx"
	"github.com/huaiche94/auspex/internal/httpapi"
	"github.com/huaiche94/auspex/internal/idgen"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/paths"
	"github.com/huaiche94/auspex/internal/pause"
	"github.com/huaiche94/auspex/internal/policy"
	"github.com/huaiche94/auspex/internal/predictor/quota"
	"github.com/huaiche94/auspex/internal/predictor/risk"
	"github.com/huaiche94/auspex/internal/predictor/scope"
	"github.com/huaiche94/auspex/internal/predictor/token"
	"github.com/huaiche94/auspex/internal/progress"
	"github.com/huaiche94/auspex/internal/repocheckpoint"
	"github.com/huaiche94/auspex/internal/retention"
	"github.com/huaiche94/auspex/internal/scheduler"
	"github.com/huaiche94/auspex/internal/statecheckpoint"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"

	"github.com/spf13/cobra"
)

// buildRootCmd opens (creating if needed) Auspex's SQLite database
// under the OS-appropriate user data directory, migrates it, composes one
// real implementation of every frozen app.* service interface plus the
// hook/diagnostics/pause-lifecycle support wiring.App.RootCmd needs, and
// returns the resulting fully-wired Cobra command tree. The returned
// closeFn must be deferred by the caller to release the DB connection.
func buildRootCmd(ctx context.Context) (root *cobra.Command, closeFn func() error, err error) {
	dirs, err := paths.ResolveHost(paths.NewOSEnv())
	if err != nil {
		return nil, nil, fmt.Errorf("cmd/auspex: resolve user data directory: %w", err)
	}

	if err := os.MkdirAll(dirs.Data, 0o755); err != nil {
		return nil, nil, fmt.Errorf("cmd/auspex: create data directory %s: %w", dirs.Data, err)
	}

	dbPath := filepath.Join(dirs.Data, "auspex.db")
	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("cmd/auspex: open database %s: %w", dbPath, err)
	}
	closeFn = db.Close

	migrations, err := sqlite.AllMigrations()
	if err != nil {
		_ = closeFn()
		return nil, nil, fmt.Errorf("cmd/auspex: load migrations: %w", err)
	}
	if err := db.Migrate(ctx, migrations); err != nil {
		_ = closeFn()
		return nil, nil, fmt.Errorf("cmd/auspex: migrate database: %w", err)
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
		return nil, nil, fmt.Errorf("cmd/auspex: create artifact staging directory %s: %w", stagingDir, err)
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
	// evaluationService.Policy is deliberately left zero: policy.Decide
	// normalizes it to policy.DefaultConfig(), which ships the ADR-043
	// increment-2 / D-08 context-utilization thresholds ACTIVE
	// (owner-approved factory posture). Adjusting or disabling them from
	// the YAML config chain (internal/config) is a documented follow-up —
	// this composition root does not load internal/config at all yet (no
	// production consumer of Config.Raw exists), the same recorded gap as
	// the pricing override above; until then, evaluationService.Policy is
	// the programmatic seam (see evaluation.Service.Policy's doc comment).

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
			// The SAME SQLDataSource the evaluation pipeline uses doubles
			// as the hook event correlator's session -> task resolver
			// (issue #1; it satisfies orchestrator.SessionResolver's
			// narrow view of the frozen app.FeatureDataSource port), so
			// persisted hook events carry TaskID/ProgressNodeID whenever
			// they resolve unambiguously.
			SessionResolver: dataSource,
			// Lazy in-hook session bootstrap (issue #17): every hook
			// invocation registers its session's repositories/worktrees/
			// provider_sessions chain from the payload's reported
			// directory — over the SAME db and gitx client composed above
			// — so SessionResolver/Resolve above (and the whole evaluation
			// pipeline behind Evaluation/Forecast) has rows to find in
			// real native-hook sessions, not just test-seeded databases.
			Bootstrapper: &orchestrator.SessionBootstrapper{
				DB:    db,
				Git:   gitClient,
				Clock: clk,
				IDs:   ids,
			},
			// The REAL evaluation.Service doubles as the issue-#14
			// forecast-card source (it satisfies orchestrator.
			// ForecastCardSource — ForecastCard/LatestForecastCard read
			// back the prediction/policy rows EvaluateTurn persists), so
			// the UserPromptSubmit hook's additionalContext, the
			// statusline --emit-line display, and `auspex evaluate`
			// all render the same persisted forecast. Cost estimates use
			// pricing.DefaultTable() (evaluation.Service.Pricing nil —
			// ADR-043's config override is a documented follow-up, see
			// internal/pricing's package comment).
			Forecast: evaluationService,
		},
		Diagnostics: wiring.DiagnosticsSupport{
			DB: db,
		},
		PauseLifecycle: orchestrator.PauseLifecycleDeps{
			Store:    pauseStore,
			WakeJobs: wakeJobStore,
		},
		// `auspex gc` (ADR-046 tiered telemetry retention): the engine
		// runs against the same db (it doubles as the app.TxRunner for
		// the archive-verified delete transaction) and archives under
		// dirs.Data/archive/. Clock/IDs are the same injected ports as
		// every other service — retention never calls time.Now directly.
		GC: orchestrator.GCDeps{
			Runner: &retention.Engine{DB: db, Clock: clk, IDs: ids, DataDir: dirs.Data},
		},
		// `auspex daemon` (issue #7, M6): the resident worker drives the
		// SAME wakeJobStore/pauseStore/gracefulPauseService composed above
		// — the whole point is that a pause created by a short-lived hook
		// process resumes from the daemon against the same SQLite file.
		Daemon: composeDaemon(dirs, clk, ids, wakeJobStore, pauseStore, gracefulPauseService),
	}

	app, err := wiring.New(services)
	if err != nil {
		_ = closeFn()
		return nil, nil, fmt.Errorf("cmd/auspex: wire services: %w", err)
	}
	return app.RootCmd(), closeFn, nil
}

// composeDaemon assembles the M6 daemon command family's deps (issue #7):
// broker → worker → daemon → HTTP handler, all over the same stores the
// rest of the container uses. The LaunchAgent dir and executable path are
// best-effort (empty on failure): only install/uninstall need them, and
// those commands fail closed with a clear message when unresolved.
func composeDaemon(
	dirs paths.Dirs,
	clk domain.Clock,
	ids domain.IDGenerator,
	wakeJobStore *scheduler.Store,
	pauseStore pause.PauseStore,
	gracefulPauseService app.GracefulPauseService,
) orchestrator.DaemonDeps {
	eventBroker := daemon.NewBroker()
	worker := daemon.NewWorker(daemon.WorkerDeps{
		Jobs:       wakeJobStore,
		Pause:      gracefulPauseService,
		PauseStore: pauseStore,
		Clock:      clk,
		IDs:        ids,
		Events:     eventBroker,
	})
	d := daemon.New(daemon.Config{
		DataDir:    dirs.Data,
		RuntimeDir: dirs.Runtime,
		Version:    buildinfo.Version,
		Clock:      clk,
		Worker:     worker,
		NewHandler: func(token string) http.Handler {
			return httpapi.NewHandler(httpapi.Deps{
				Version:   buildinfo.Version,
				StartedAt: clk.Now(),
				Clock:     clk,
				Jobs:      wakeJobStore,
				Events:    eventBroker,
			}, token)
		},
	})
	exe, _ := os.Executable()
	launchAgents := ""
	if home, err := os.UserHomeDir(); err == nil {
		launchAgents = filepath.Join(home, "Library", "LaunchAgents")
	}
	return orchestrator.DaemonDeps{
		Daemon:         d,
		RuntimeDir:     dirs.Runtime,
		LaunchAgentDir: launchAgents,
		ExecutablePath: exe,
	}
}
