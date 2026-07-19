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
// corrective addition" phase built it to be composed (internal/progress.
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
	"github.com/huaiche94/auspex/internal/pace"
	"github.com/huaiche94/auspex/internal/paths"
	"github.com/huaiche94/auspex/internal/pause"
	"github.com/huaiche94/auspex/internal/policy"
	"github.com/huaiche94/auspex/internal/predictor/quota"
	"github.com/huaiche94/auspex/internal/predictor/risk"
	"github.com/huaiche94/auspex/internal/predictor/scope"
	"github.com/huaiche94/auspex/internal/predictor/token"
	"github.com/huaiche94/auspex/internal/progress"
	"github.com/huaiche94/auspex/internal/repocheckpoint"
	"github.com/huaiche94/auspex/internal/report"
	"github.com/huaiche94/auspex/internal/retention"
	"github.com/huaiche94/auspex/internal/rolloutwatch"
	"github.com/huaiche94/auspex/internal/scheduler"
	"github.com/huaiche94/auspex/internal/sessionstatus"
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

	// FR-162 daemon status surface (issue #10): a read-only assembler over
	// the SAME stores composed above (dataSource for risk/runway/quota/
	// resolve, the two progress stores, both checkpoint stores, the wake-job
	// store, and db for its own latest-session/latest-pause SELECTs). Feeds
	// the /v1/session/status endpoints below.
	//
	// FLAG (composition-root reconciliation): this block and the
	// composeDaemon signature/call gain one appended argument; another agent
	// may also touch this file — the change is additive and reuses only
	// already-constructed instances, so it should merge cleanly.
	sessionStatusReader := sessionstatus.NewReader(sessionstatus.Deps{
		DB:               db,
		Evaluation:       dataSource,
		Nodes:            nodeStore,
		Edges:            edgeStore,
		StateCheckpoints: checkpointStore,
		RepoCheckpoints:  repoCheckpointStore,
		Jobs:             wakeJobStore,
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
			// Turn correlation for terminal events (issue #11): the
			// Stop/StopFailure hooks resolve the session's latest
			// started turn from the SAME events table the
			// UserPromptSubmit hook stamped it into, activating the
			// prediction↔actual outcome join (ADR-046's documented
			// upgrade point).
			OpenTurns: &orchestrator.OpenTurnStore{DB: db},
			// `auspex hook codex status` (issue #9 Phase 1b) reads the
			// latest codex session's persisted context/quota observations
			// back out of the same DB the codex hook subtree writes into —
			// the stdin-less render path tmux polls.
			CodexStatus: &orchestrator.CodexStatusStore{DB: db},
			// M10: drive the independent Runway Predictor from each Stop's
			// per-turn quota telemetry, over the SAME db + injected clock —
			// recomputing and persisting a runway_forecasts row so the next
			// turn's evaluation gate (and the statusline) read a live runway
			// signal. Native-hook mode records only; it never forces a pause
			// (§8.8). The stateless runway.Scorer/DefaultHorizon defaults
			// apply (nil Scorer, zero Horizon).
			Runway: &orchestrator.RunwayForecastStore{DB: db, Clock: clk},
			// #90 Phase A: today's spend + pace, aggregated read-only from
			// the SAME events table the hooks write, over the same injected
			// clock (which defines "today" in the process's local zone).
			//
			// FLAG (composition-root reconciliation): appended line only —
			// reuses already-constructed db/clk instances; merges cleanly
			// with any other agent's additive edit to this literal.
			Pace: &pace.Store{DB: db, Clock: clk},
			// #67 slice 3a (ADR-052): the PostToolUse per-turn scratch
			// counter, over the SAME db + injected clock. `hook claude
			// post-tool-use` accumulates into toolop_scratch (migration
			// 0011 — counters/ids only, never a path, raw or hashed) and
			// the Stop hook folds + clears it into provider.turn.completed's
			// five aggregate fields.
			//
			// FLAG (composition-root reconciliation): appended line only —
			// reuses already-constructed db/clk instances; merges cleanly
			// with any other agent's additive edit to this literal.
			ToolOps: &orchestrator.ToolOpScratchStore{DB: db, Clock: clk},
			// #114 (M4/M10): the pre-compaction auto State Checkpoint.
			// `hook claude pre-compact` (and the codex twin) resolve the
			// session through the SAME dataSource the evaluation pipeline
			// uses (session -> task) plus a worktree lookup over the same
			// db, then run the frozen CheckpointCreate ordering over the
			// SAME state/repository checkpoint services `auspex checkpoint
			// create` uses — capture happens BEFORE the provider's
			// compaction proceeds, and every failure is recorded on the
			// event, never a blocked compaction (fail-open).
			//
			// FLAG (composition-root reconciliation, #114): appended block
			// only — reuses already-constructed instances; merges cleanly
			// with any other agent's additive edit to this literal.
			CompactCheckpoint: &orchestrator.CompactCheckpointer{
				Sessions:   dataSource,
				Worktrees:  &orchestrator.SessionWorktreeStore{DB: db},
				State:      stateCheckpointService,
				Repository: repositoryCheckpointService,
			},
			// Issue #115 (M4): the post-turn Stop reconciliation +
			// evidence-gate outcome labeling, composed from the SAME
			// already-constructed pieces — dataSource (session→task), the
			// Progress Tree service (snapshot), the progress Reconciler
			// (the M4 crash-window scan, REUSED not rebuilt), and the gitx
			// client (worktree fingerprint). Flag-not-block by design: it
			// records one progress.tree.reconciled event per turn and
			// never mutates the tree or fails the hook.
			//
			// FLAG (composition-root reconciliation, #115): appended field
			// only — reuses already-constructed instances; merges cleanly
			// with any other agent's additive edit to this literal.
			StopReconcile: &orchestrator.TurnReconcileService{
				Sessions:   dataSource,
				Progress:   progressTreeService,
				Reconciler: reconciler,
				Git:        gitClient,
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
		// `auspex report` (issue #91): the read-only personal usage
		// report over the same db. Location nil -> time.Local (the
		// report is personal; the user's own wall clock frames "active
		// days" and the window).
		Report: &report.Engine{DB: db, Clock: clk},
		// `auspex daemon` (issue #7, M6): the resident worker drives the
		// SAME wakeJobStore/pauseStore/gracefulPauseService composed above
		// — the whole point is that a pause created by a short-lived hook
		// process resumes from the daemon against the same SQLite file.
		Daemon: composeDaemon(dirs, clk, ids, wakeJobStore, pauseStore, gracefulPauseService, sessionStatusReader),
		// `auspex watch codex` (issue #92): the rollout-tailing watcher
		// persists through the SAME provider-agnostic EventStore + db the
		// hook subtree writes into — shared UNIQUE idempotency keys are
		// what make hook+watcher double-capture dedupe by construction.
		//
		// FLAG (composition-root reconciliation, #92): appended field only
		// — reuses already-constructed clk/ids/db instances; merges cleanly
		// with any other agent's additive edit to this literal.
		Watch: &rolloutwatch.Deps{
			Clock:     clk,
			IDs:       ids,
			Persister: claudetelemetry.NewEventStore(db),
			TxRunner:  db,
		},
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
	sessionStatusReader httpapi.SessionStatusReader,
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
				// Cancel is the FR-163 mutation (#10): the VS Code
				// companion's "cancel scheduled resume" POSTs against the
				// SAME store the worker claims from, so the conditional
				// UPDATE in scheduler.Cancel is what arbitrates the race.
				Cancel: wakeJobStore,
				Events: eventBroker,
				// SessionStatus is the FR-162 read-model (#10): the same
				// endpoints the extension renders risk/runway/quota/progress/
				// checkpoint/pause from.
				SessionStatus: sessionStatusReader,
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
