// restart_sameDB_test.go implements qa-03 (docs/implementation/vertical-slice/
// EXECUTION_DAG.md's qa-03 row; agents/qa.md deliverable #3: "Restart test
// using the same SQLite DB").
//
// # Relationship to runtime-b10's own restart proof
//
// runtime-b10 (internal/app/wiring/restart_test.go) already built a
// rigorous in-process-restart-same-SQLite-file proof at the wiring/CLI
// layer, including a real OS-process SIGKILL crash test — that node's own
// DAG note names it as the direct gate for both qa-02 and this node. This
// file does NOT duplicate that work; it builds on the SAME core technique
// (open a real on-disk, temp-file-not-:memory: SQLite database; drive real
// work through it; discard every in-process Go value including the *sqlite.DB
// itself; open a BRAND NEW *sqlite.DB against the SAME file path; re-migrate
// and confirm idempotence; then prove every role's real state survived and
// remains both readable AND writable) but at a different, complementary
// scope: runtime-b10 proves restart-safety for the P0 COMMANDS this role
// wires through wiring.App/cobra; this node proves restart-safety for
// MULTIPLE ROLES' OWN STORAGE LAYERS coexisting in one file directly (no
// wiring.App, no CLI/cobra layer at all) — claude-provider's EventStore,
// checkpoint's Progress Tree/State Checkpoint/Repository Checkpoint stores,
// predictor's evaluation/authorization store, and runtime's pause/scheduler
// stores, all opened directly against their own real constructors, exactly
// as this node's own task brief asks ("not just runtime's own tables in
// isolation").
//
// Every fixture in this file is independent from runtime-b10's own
// (different task/session/worktree IDs, a dedicated qa03-prefixed ID
// generator and Git scratch repo, a distinct high-risk DataSource literal)
// so this is a genuinely separate exercise of the same guarantee, not a
// re-run of the same test under a new name.
package integrationtest

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/artifacts"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/evaluation"
	"github.com/huaiche94/auspex/internal/features"
	"github.com/huaiche94/auspex/internal/gitx"
	claudehooks "github.com/huaiche94/auspex/internal/hooks/claude"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/pause"
	"github.com/huaiche94/auspex/internal/policy"
	"github.com/huaiche94/auspex/internal/predictor/quota"
	"github.com/huaiche94/auspex/internal/predictor/risk"
	"github.com/huaiche94/auspex/internal/predictor/scope"
	"github.com/huaiche94/auspex/internal/predictor/token"
	"github.com/huaiche94/auspex/internal/progress"
	"github.com/huaiche94/auspex/internal/repocheckpoint"
	"github.com/huaiche94/auspex/internal/scheduler"
	"github.com/huaiche94/auspex/internal/statecheckpoint"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// --- independent fixtures (qa03-prefixed; distinct from qa-02's own and
// from runtime-b10's own restart_test.go fixtures) -------------------------

type qa03Clock struct{ t time.Time }

func (c qa03Clock) Now() time.Time { return c.t }

type qa03IDs struct {
	n      int
	prefix string
}

func (g *qa03IDs) NewID() string {
	g.n++
	return g.prefix + "-" + qa03Itoa(g.n)
}

func qa03Itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func qa03Fixture(t *testing.T, dir, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "provider-events", "claude", dir, name))
	if err != nil {
		t.Fatalf("reading fixture %s/%s: %v", dir, name, err)
	}
	return b
}

// qa03Repo is a distinct scratch-Git-repo builder from qa-02's own
// (different commit content, different directory) — same minimal-helper
// duplication precedent this codebase's other integration tests already
// establish.
type qa03Repo struct {
	t   *testing.T
	dir string
}

func newQA03Repo(t *testing.T) *qa03Repo {
	t.Helper()
	runner := gitx.ExecRunner{}
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", dir, err)
	}
	rb := &qa03Repo{t: t, dir: resolved}
	if _, err := runner.Run(context.Background(), rb.dir, "git", "--version"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	rb.git("init", "-q", "-b", "main")
	rb.git("config", "user.name", "Auspex QA Restart")
	rb.git("config", "user.email", "qa-restart@auspex.invalid")
	rb.git("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(rb.dir, "restart-fixture.txt"), []byte("restart-same-db scratch repo\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	rb.git("add", "restart-fixture.txt")
	rb.git("commit", "-q", "-m", "initial")
	return rb
}

func (rb *qa03Repo) git(args ...string) {
	rb.t.Helper()
	runner := gitx.ExecRunner{}
	res, err := runner.Run(context.Background(), rb.dir, "git", args...)
	if err != nil {
		rb.t.Fatalf("git %s: %v", args, err)
	}
	if res.ExitCode != 0 {
		rb.t.Fatalf("git %s: exit %d: %s", args, res.ExitCode, res.Stderr)
	}
}

// qa03SeedChain seeds a minimal repositories -> worktrees ->
// provider_sessions -> tasks chain, independent from qa-02's/runtime-b10's
// own seed helpers (different table literal, kept local to this file per
// this codebase's established per-file duplication precedent).
func qa03SeedChain(t *testing.T, db *sqlite.DB, repoID, worktreePath string, worktreeID domain.WorktreeID, sessionID domain.SessionID, taskID domain.TaskID) {
	t.Helper()
	now := "2026-07-12T08:00:00Z"
	stmts := []string{
		`INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
		 VALUES ('` + repoID + `', '` + worktreePath + `', '` + worktreePath + `/.git', '` + now + `', '` + now + `')`,
		`INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
		 VALUES ('` + string(worktreeID) + `', '` + repoID + `', '` + worktreePath + `', '` + worktreePath + `/.git', '` + now + `', '` + now + `')`,
		`INSERT INTO provider_sessions (id, worktree_id, provider, invocation_mode, started_at, metadata_json)
		 VALUES ('` + string(sessionID) + `', '` + string(worktreeID) + `', 'claude-code', 'interactive', '` + now + `', '{}')`,
		`INSERT INTO tasks (id, session_id, worktree_id, objective_hash, status, created_at, updated_at)
		 VALUES ('` + string(taskID) + `', '` + string(sessionID) + `', '` + string(worktreeID) + `', 'hash-restart', 'in_progress', '` + now + `', '` + now + `')`,
	}
	for _, stmt := range stmts {
		if _, err := db.Conn().ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
}

// qa03DataSource is a distinct low-risk-tuned evaluation.DataSource literal
// (this node's own point is restart durability, not re-proving the
// risk-scoring formula, which decision_realauth_test.go/qa-02 already
// exercise exhaustively) — a plain fixture that reliably reaches a
// decidable, non-cold-start action.
type qa03DataSource struct{ taskID domain.TaskID }

func (f qa03DataSource) Resolve(_ context.Context, _ domain.SessionID) (evaluation.ResolvedSession, error) {
	tid := f.taskID
	return evaluation.ResolvedSession{RepositoryID: "repo-qa03", TaskID: &tid}, nil
}
func (f qa03DataSource) Classification(_ context.Context, _ domain.SessionID, _ *domain.TaskID) (features.Classification, features.PromptFeatures, error) {
	return features.Classification{Class: features.TaskClassBugfixLocal, Confidence: domain.ConfidenceMedium},
		features.PromptFeatures{ExplicitPathCount: 3}, nil
}
func (f qa03DataSource) Repository(_ context.Context, _ domain.RepositoryID) (features.RepositoryFeatures, bool, error) {
	return features.RepositoryFeatures{TrackedFileCount: 200, DirtyFileCount: 2}, true, nil
}
func (f qa03DataSource) Session(_ context.Context, _ domain.SessionID) (features.SessionFeatures, bool, error) {
	p50, p90 := 2.0, 5.0
	return features.SessionFeatures{ChangedFilesRecentP50: &p50, ChangedFilesRecentP90: &p90}, true, nil
}
func (f qa03DataSource) Progress(_ context.Context, _ *domain.TaskID) (features.ProgressFeatures, bool, error) {
	return features.ProgressFeatures{CriticalPathLength: 3}, true, nil
}
func (f qa03DataSource) RecentSimilarTurnTokens(_ context.Context, _ domain.SessionID, _ features.TaskClass) (features.SimilarTurnTokens, error) {
	return features.SimilarTurnTokens{Rung: features.CohortRungSession}, nil
}
func (f qa03DataSource) Quota(_ context.Context, _ domain.SessionID) ([]domain.QuotaObservation, error) {
	used := 30.0
	return []domain.QuotaObservation{{ID: "q1", SessionID: "sess-qa03", Provider: "claude-code", LimitID: "five_hour", UsedPercent: &used, ObservedAt: time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)}}, nil
}
func (f qa03DataSource) Context(_ context.Context, _ domain.SessionID) (domain.ContextObservation, error) {
	used := 25.0
	return domain.ContextObservation{ID: "c1", SessionID: "sess-qa03", UsedPercent: &used, ObservedAt: time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)}, nil
}
func (f qa03DataSource) RunwayForecast(_ context.Context, _ domain.SessionID) (domain.RunwayForecast, bool, error) {
	return domain.RunwayForecast{}, false, nil
}
func (f qa03DataSource) PriorRunwayHitConfirmed(_ context.Context, _ domain.SessionID) (bool, error) {
	return false, nil
}

var _ evaluation.DataSource = qa03DataSource{}

type qa03ScopeAdapter struct{ src evaluation.DataSource }

func (a qa03ScopeAdapter) Classification(ctx context.Context, s domain.SessionID, tid *domain.TaskID) (features.Classification, features.PromptFeatures, error) {
	return a.src.Classification(ctx, s, tid)
}
func (a qa03ScopeAdapter) Repository(ctx context.Context, r domain.RepositoryID) (features.RepositoryFeatures, bool, error) {
	return a.src.Repository(ctx, r)
}
func (a qa03ScopeAdapter) Session(ctx context.Context, s domain.SessionID) (features.SessionFeatures, bool, error) {
	return a.src.Session(ctx, s)
}
func (a qa03ScopeAdapter) Progress(ctx context.Context, tid *domain.TaskID) (features.ProgressFeatures, bool, error) {
	return a.src.Progress(ctx, tid)
}

type qa03TokenAdapter struct {
	src    evaluation.DataSource
	taskID domain.TaskID
}

func (a qa03TokenAdapter) Classification(ctx context.Context, s domain.SessionID) (features.Classification, features.PromptFeatures, error) {
	return a.src.Classification(ctx, s, nil)
}
func (a qa03TokenAdapter) Session(ctx context.Context, s domain.SessionID) (features.SessionFeatures, bool, error) {
	return a.src.Session(ctx, s)
}
func (a qa03TokenAdapter) Progress(ctx context.Context, s domain.SessionID) (features.ProgressFeatures, bool, error) {
	return a.src.Progress(ctx, &a.taskID)
}
func (a qa03TokenAdapter) RecentSimilarTurnTokens(ctx context.Context, s domain.SessionID, c features.TaskClass) (features.SimilarTurnTokens, error) {
	return a.src.RecentSimilarTurnTokens(ctx, s, c)
}

func qa03NewEvaluationService(db *sqlite.DB, clk domain.Clock, ids domain.IDGenerator, taskID domain.TaskID) *evaluation.Service {
	src := qa03DataSource{taskID: taskID}
	return evaluation.New(
		db, src,
		scope.NewRuleScopeEstimator(qa03ScopeAdapter{src: src}),
		token.NewRuleTokenForecaster(qa03TokenAdapter{src: src, taskID: taskID}),
		quota.NewRuleQuotaForecaster(),
		risk.NewRuleRiskCombiner(),
		policy.NewDecider(),
		clk, ids,
	)
}

// qa03TreeReader adapts real progress stores to statecheckpoint.TreeReader,
// scoped to one node (this file's own scenario has exactly one), mirroring
// qa-02's own qa02TreeReader but declared separately per this file's
// independence from that one.
type qa03TreeReader struct {
	nodes     *progress.NodeStore
	artifacts *progress.ArtifactStore
	nodeID    domain.ProgressNodeID
}

func (r qa03TreeReader) ListNodes(ctx context.Context, taskID domain.TaskID) ([]statecheckpoint.NodeSnapshot, error) {
	rows, err := r.nodes.ListByTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	out := make([]statecheckpoint.NodeSnapshot, 0, len(rows))
	for _, n := range rows {
		out = append(out, statecheckpoint.NodeSnapshot{ID: n.ID, Status: n.Status})
	}
	return out, nil
}

func (r qa03TreeReader) ListArtifacts(ctx context.Context, _ domain.TaskID) ([]statecheckpoint.ArtifactSnapshot, error) {
	rows, err := r.artifacts.ListByNode(ctx, r.nodeID)
	if err != nil {
		return nil, err
	}
	out := make([]statecheckpoint.ArtifactSnapshot, 0, len(rows))
	for _, a := range rows {
		mediaType := ""
		if a.MediaType != nil {
			mediaType = *a.MediaType
		}
		out = append(out, statecheckpoint.ArtifactSnapshot{
			ID: a.ID, URI: a.URI, MediaType: mediaType, Bytes: a.Bytes, SHA256: a.SHA256, ValidationStatus: string(a.ValidationStatus),
		})
	}
	return out, nil
}

// --- the restart test itself ------------------------------------------

// TestRestartSameDB_MultiRoleStateSurvivesProcessRestart proves restart-
// same-DB holds when claude-provider's events, checkpoint's Progress
// Tree/State/Repository checkpoints, predictor's evaluations/
// authorizations, and runtime's pause records ALL coexist in the same
// database file across a genuine restart (the *sqlite.DB and every
// in-process Go value built on it are entirely discarded and rebuilt fresh
// against the same file path) — not just runtime's own tables in
// isolation, per this node's own task brief.
func TestRestartSameDB_MultiRoleStateSurvivesProcessRestart(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "qa03-restart.db")
	repo := newQA03Repo(t)

	const (
		repoID     = "repo-qa03"
		worktreeID = domain.WorktreeID("wt-qa03")
		sessionID  = domain.SessionID("sess-qa03")
		taskID     = domain.TaskID("task-qa03")
		nodeID     = domain.ProgressNodeID("node-qa03")
	)
	clock := qa03Clock{t: time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)}

	// ===================================================================
	// "Process 1": open+migrate, seed, drive real work from every role.
	// ===================================================================
	db1, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}
	if err := db1.Migrate(context.Background(), migrations); err != nil {
		t.Fatalf("Migrate (process 1): %v", err)
	}
	versionAfterFirstMigrate, err := db1.CurrentVersion(context.Background())
	if err != nil {
		t.Fatalf("CurrentVersion (process 1): %v", err)
	}
	if versionAfterFirstMigrate == 0 {
		t.Fatal("CurrentVersion = 0 after Migrate, want > 0")
	}
	qa03SeedChain(t, db1, repoID, repo.dir, worktreeID, sessionID, taskID)

	ctx := context.Background()

	// --- claude-provider: real Normalizer + EventStore ---
	normalizer := claudetelemetry.NewNormalizer(clock, &qa03IDs{prefix: "norm1"})
	eventStore := claudetelemetry.NewEventStore(db1)
	parsedStop, err := claudehooks.ParseStop(qa03Fixture(t, "stop", "normal.json"))
	if err != nil {
		t.Fatalf("ParseStop: %v", err)
	}
	stopEvent := normalizer.NormalizeStop(parsedStop, clock.Now())
	if err := eventStore.PersistAll(ctx, db1, []v1.Event{stopEvent}); err != nil {
		t.Fatalf("PersistAll: %v", err)
	}

	// --- checkpoint: real Progress Tree node completion (CompleteNode) ---
	nodeStore := progress.NewNodeStore(db1, clock)
	if err := nodeStore.Insert(ctx, progress.Node{
		ID: nodeID, TaskID: taskID, Ordinal: 1, Kind: domain.NodeDocumentSection,
		Title: "Node " + string(nodeID), Status: domain.NodePending,
		Acceptance: []progress.AcceptanceCriterion{{Kind: "heading_exists", Value: "# Restart"}, {Kind: "fence_balance"}},
		Version:    1, UpdatedAt: clock.Now().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	if err := nodeStore.TransitionStatus(ctx, nodeID, domain.NodePending, domain.NodeReady, 1); err != nil {
		t.Fatalf("transition to ready: %v", err)
	}
	if err := nodeStore.TransitionStatus(ctx, nodeID, domain.NodeReady, domain.NodeInProgress, 2); err != nil {
		t.Fatalf("transition to in_progress: %v", err)
	}
	evidenceDir := t.TempDir()
	stager, err := progress.NewFileStager(evidenceDir)
	if err != nil {
		t.Fatalf("NewFileStager: %v", err)
	}
	scStore := statecheckpoint.NewStore(db1)
	artifactStore := progress.NewArtifactStore(db1)
	completeNode := &progress.CompleteNode{
		DB: db1, Clock: clock, IDs: &qa03IDs{prefix: "cn1"},
		Nodes: nodeStore, Edges: progress.NewEdgeStore(db1), Artifacts: artifactStore,
		Validators: artifacts.NewRegistry(), Stager: stager,
		Checkpoints: scStore, Publisher: progress.NoopPublisher{},
	}
	artifactPath := filepath.Join(evidenceDir, "restart-node.md")
	if err := os.WriteFile(artifactPath, []byte("# Restart\n\nprose\n"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	completion, err := completeNode.Run(ctx, progress.CompleteNodeInput{
		NodeID: nodeID, IdempotencyKey: "qa03-node-completion",
		Artifacts: []domain.ArtifactRef{{ID: "artifact-qa03-1", Kind: "file", URI: "file:" + artifactPath, MediaType: "text/markdown"}},
	})
	if err != nil {
		t.Fatalf("CompleteNode.Run: %v", err)
	}
	nodeCompletionCheckpointID := completion.Checkpoint.ID
	if nodeCompletionCheckpointID == "" {
		t.Fatal("expected a State Checkpoint from CompleteNode")
	}

	// --- checkpoint: real standalone State + Repository checkpoint ---
	treeReader := qa03TreeReader{nodes: nodeStore, artifacts: artifactStore, nodeID: nodeID}
	stateSvc := statecheckpoint.NewService(scStore, treeReader, clock, &qa03IDs{prefix: "sc1"})
	gitClient := gitx.NewClient(gitx.ExecRunner{})
	repoStore := repocheckpoint.NewStore(db1)
	resolveWorktree := func(_ context.Context, id domain.WorktreeID) (repocheckpoint.WorktreeLocation, error) {
		if id != worktreeID {
			return repocheckpoint.WorktreeLocation{}, &domain.Error{Code: domain.ErrCodeNotFound, Message: "unknown worktree"}
		}
		return repocheckpoint.WorktreeLocation{RepositoryID: repoID, Path: repo.dir}, nil
	}
	repoSvc := repocheckpoint.NewService(gitClient, repoStore, clock, &qa03IDs{prefix: "rc1"}, t.TempDir(), resolveWorktree, repocheckpoint.CaptureOptions{})

	checkpointResult, err := orchestrator.CheckpointCreate(ctx, orchestrator.CheckpointCreateDeps{
		StateCheckpoint: stateSvc, RepositoryCheckpoint: repoSvc,
	}, orchestrator.CheckpointCreateRequest{TaskID: taskID, WorktreeID: worktreeID})
	if err != nil {
		t.Fatalf("CheckpointCreate: %v", err)
	}

	// --- predictor: real EvaluateTurn/Decide/authorization ---
	evalSvc1 := qa03NewEvaluationService(db1, clock, &qa03IDs{prefix: "ev1"}, taskID)
	turnID := domain.TurnID("turn-qa03-1")
	promptHash := "sha256:qa03-restart"
	eval1, err := evalSvc1.EvaluateTurn(ctx, app.EvaluateTurnRequest{SessionID: sessionID, TurnID: turnID, Provider: "claude", PromptHash: promptHash})
	if err != nil {
		t.Fatalf("EvaluateTurn: %v", err)
	}
	if _, err := evalSvc1.Decide(ctx, app.DecideRequest{EvaluationID: eval1.ID}); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	authResult, err := evalSvc1.IssueAuthorization(ctx, turnID, promptHash, checkpointResult.Repository.GitHead, "some-decision", &checkpointResult.Repository.ID)
	if err != nil {
		t.Fatalf("IssueAuthorization: %v", err)
	}
	authID := authResult.ID
	if authID == "" {
		t.Fatal("empty Authorization.ID")
	}

	// --- runtime: real pause record via the real SQLite-backed PauseStore ---
	pauseStore := pause.NewSQLiteStore(db1)
	pauseResult, err := pause.RequestPause(ctx, pauseStore, &qa03IDs{prefix: "pause1"}, pause.RequestPauseRequest{
		Key: pause.PauseKey{TaskID: taskID, SessionID: sessionID}, Reason: pause.TriggerReasonCalibrated,
	})
	if err != nil {
		t.Fatalf("RequestPause: %v", err)
	}
	pauseID := pauseResult.Record.ID

	// --- runtime: real durable wake job via scheduler.Store ---
	wakeStore1 := scheduler.NewStore(db1.Conn(), clock, &qa03IDs{prefix: "wj1"})
	job1, err := wakeStore1.Schedule(ctx, scheduler.ScheduleRequest{
		PauseID: pauseID, Kind: "pause_resume", RunAfter: clock.Now(), MaxAttempts: 5,
	})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	// --- doctor-style reachability check, pre-restart ---
	if err := db1.Conn().PingContext(ctx); err != nil {
		t.Fatalf("ping (pre-restart): %v", err)
	}

	// ===================================================================
	// Simulate a restart: discard db1 and every Go value built on it.
	// Nothing below this line reads db1/nodeStore/evalSvc1/... again.
	// ===================================================================
	if err := db1.Close(); err != nil {
		t.Fatalf("db1.Close: %v", err)
	}

	// ===================================================================
	// "Process 2": brand-new *sqlite.DB, same file, fresh migrate.
	// ===================================================================
	db2, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open (process 2): %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	if err := db2.Migrate(context.Background(), migrations); err != nil {
		t.Fatalf("Migrate (process 2): %v", err)
	}
	versionAfterReopen, err := db2.CurrentVersion(context.Background())
	if err != nil {
		t.Fatalf("CurrentVersion (process 2): %v", err)
	}
	if versionAfterReopen != versionAfterFirstMigrate {
		t.Fatalf("CurrentVersion after reopen+re-migrate = %d, want unchanged %d (no double-migration)", versionAfterReopen, versionAfterFirstMigrate)
	}

	// --- claude-provider: the persisted Stop event is readable post-restart ---
	eventStore2 := claudetelemetry.NewEventStore(db2)
	storedEvent, err := eventStore2.GetByEventID(ctx, stopEvent.EventID)
	if err != nil {
		t.Fatalf("GetByEventID (post-restart): %v", err)
	}
	if storedEvent.EventType != string(v1.EventProviderTurnCompleted) {
		t.Fatalf("post-restart stored event type = %q, want %q", storedEvent.EventType, v1.EventProviderTurnCompleted)
	}

	// --- checkpoint: node completion + State Checkpoint readable, and the
	// node store can still WRITE (not just read) against the reopened DB ---
	nodeStore2 := progress.NewNodeStore(db2, clock)
	reopenedNode, err := nodeStore2.Get(ctx, nodeID)
	if err != nil {
		t.Fatalf("nodeStore2.Get (post-restart): %v", err)
	}
	if reopenedNode.Status != domain.NodeCompleted {
		t.Fatalf("post-restart node status = %q, want %q", reopenedNode.Status, domain.NodeCompleted)
	}
	scStore2 := statecheckpoint.NewStore(db2)
	reopenedStateCkpt, err := scStore2.Get(ctx, nodeCompletionCheckpointID)
	if err != nil {
		t.Fatalf("scStore2.Get (post-restart, completion checkpoint): %v", err)
	}
	if reopenedStateCkpt.ID != nodeCompletionCheckpointID {
		t.Fatalf("post-restart state checkpoint id = %q, want %q", reopenedStateCkpt.ID, nodeCompletionCheckpointID)
	}
	stateSvc2 := statecheckpoint.NewService(scStore2, qa03TreeReader{nodes: nodeStore2, artifacts: progress.NewArtifactStore(db2), nodeID: nodeID}, clock, &qa03IDs{prefix: "sc2"})
	reopenedStandaloneCkpt, err := stateSvc2.Snapshot(ctx, checkpointResult.State.ID)
	if err != nil {
		t.Fatalf("stateSvc2.Snapshot (post-restart, standalone checkpoint): %v", err)
	}
	if reopenedStandaloneCkpt.ID != checkpointResult.State.ID {
		t.Fatalf("post-restart standalone state checkpoint id = %q, want %q", reopenedStandaloneCkpt.ID, checkpointResult.State.ID)
	}
	verification, err := stateSvc2.Verify(ctx, checkpointResult.State.ID)
	if err != nil {
		t.Fatalf("stateSvc2.Verify (post-restart): %v", err)
	}
	if !verification.Valid {
		t.Fatal("expected the pre-restart State Checkpoint to still verify as valid post-restart")
	}

	// --- checkpoint: Repository Checkpoint readable and still Verify-able
	// through a brand new Service instance, against the same real Git repo ---
	repoStore2 := repocheckpoint.NewStore(db2)
	repoSvc2 := repocheckpoint.NewService(gitClient, repoStore2, clock, &qa03IDs{prefix: "rc2"}, t.TempDir(), resolveWorktree, repocheckpoint.CaptureOptions{})
	repoVerify2, err := repoSvc2.Verify(ctx, checkpointResult.Repository.ID)
	if err != nil {
		t.Fatalf("repoSvc2.Verify (post-restart): %v", err)
	}
	if !repoVerify2.Valid {
		t.Fatal("expected the pre-restart Repository Checkpoint to still verify as valid post-restart")
	}
	// WRITE proof: a brand new checkpoint can be captured post-restart too
	// (not just old ones read) — proves no orphaned lock on this table.
	checkpointResult2, err := orchestrator.CheckpointCreate(ctx, orchestrator.CheckpointCreateDeps{
		StateCheckpoint: stateSvc2, RepositoryCheckpoint: repoSvc2,
	}, orchestrator.CheckpointCreateRequest{TaskID: taskID, WorktreeID: worktreeID})
	if err != nil {
		t.Fatalf("CheckpointCreate (post-restart, fresh): %v", err)
	}
	if checkpointResult2.State.ID == "" || checkpointResult2.State.ID == checkpointResult.State.ID {
		t.Fatalf("post-restart fresh state checkpoint id = %q, want a new, distinct id", checkpointResult2.State.ID)
	}

	// --- predictor: the pre-restart authorization is readable AND its
	// exactly-once consumption guarantee is durable across the restart ---
	evalSvc2 := qa03NewEvaluationService(db2, clock, &qa03IDs{prefix: "ev2"}, taskID)
	consumed, err := evalSvc2.ConsumeAuthorization(ctx, app.ConsumeAuthorizationRequest{
		AuthorizationID: authID, TurnID: turnID, PromptHash: promptHash,
	})
	if err != nil {
		t.Fatalf("ConsumeAuthorization (post-restart, first consume): %v", err)
	}
	if consumed.ConsumedAt == nil {
		t.Fatal("expected ConsumedAt to be set after a successful post-restart consume")
	}
	// Replay, post-restart: must still be rejected — the exactly-once
	// guarantee is durable, not merely an in-process invariant that a
	// restart silently resets.
	_, err = evalSvc2.ConsumeAuthorization(ctx, app.ConsumeAuthorizationRequest{
		AuthorizationID: authID, TurnID: turnID, PromptHash: promptHash,
	})
	if err == nil {
		t.Fatal("expected a post-restart replay of the pre-restart authorization to be rejected")
	}

	// A fresh evaluation + authorization also succeeds post-restart (write
	// path fully live, not just reads of pre-existing rows).
	turnID2 := domain.TurnID("turn-qa03-2")
	promptHash2 := "sha256:qa03-restart-2"
	eval2, err := evalSvc2.EvaluateTurn(ctx, app.EvaluateTurnRequest{SessionID: sessionID, TurnID: turnID2, Provider: "claude", PromptHash: promptHash2})
	if err != nil {
		t.Fatalf("EvaluateTurn (post-restart, fresh): %v", err)
	}
	if _, err := evalSvc2.Decide(ctx, app.DecideRequest{EvaluationID: eval2.ID}); err != nil {
		t.Fatalf("Decide (post-restart, fresh): %v", err)
	}

	// --- runtime: the pre-restart pause record is readable AND mutable
	// (CompareAndSwapStatus) post-restart ---
	pauseStore2 := pause.NewSQLiteStore(db2)
	reopenedPause, found, err := pauseStore2.GetByID(ctx, pauseID)
	if err != nil || !found {
		t.Fatalf("pauseStore2.GetByID (post-restart): found=%v err=%v", found, err)
	}
	if reopenedPause.Status != domain.PausePredicted {
		t.Fatalf("post-restart pause status = %q, want %q", reopenedPause.Status, domain.PausePredicted)
	}
	cancelResult, err := pause.Cancel(ctx, pauseStore2, pause.CancelRequest{PauseID: pauseID})
	if err != nil {
		t.Fatalf("Cancel (post-restart): %v", err)
	}
	if cancelResult.Record.Status != domain.PauseCancelled {
		t.Fatalf("post-restart cancel status = %q, want %q", cancelResult.Record.Status, domain.PauseCancelled)
	}

	// --- runtime: the pre-restart wake job's lease survives, AND the
	// scheduler can still WRITE (schedule + claim a fresh job) post-restart ---
	wakeStore2 := scheduler.NewStore(db2.Conn(), clock, &qa03IDs{prefix: "wj2"})
	claim, err := wakeStore2.Claim(ctx, "qa03-worker-post-restart", 5*time.Minute)
	if err != nil || !claim.Found || claim.Job.ID != job1.ID {
		t.Fatalf("Claim (post-restart, pre-existing job): found=%v err=%v job=%v want=%v", claim.Found, err, claim.Job.ID, job1.ID)
	}
	completedJob, err := wakeStore2.Complete(ctx, job1.ID, "qa03-worker-post-restart")
	if err != nil {
		t.Fatalf("Complete (post-restart): %v", err)
	}
	if completedJob.Status != scheduler.StatusDone {
		t.Fatalf("post-restart completed job status = %q, want %q", completedJob.Status, scheduler.StatusDone)
	}
	// A brand new pause + wake job, scheduled and claimed entirely
	// post-restart, proves the write path (not just reads of old rows).
	freshPause, err := pause.RequestPause(ctx, pauseStore2, &qa03IDs{prefix: "pause2"}, pause.RequestPauseRequest{
		Key: pause.PauseKey{TaskID: taskID, SessionID: sessionID}, Reason: pause.TriggerReasonCalibrated,
	})
	if err != nil {
		t.Fatalf("RequestPause (post-restart, fresh): %v", err)
	}
	if !freshPause.Created {
		t.Fatal("expected a fresh pause request post-restart to create a new record (the prior one was cancelled above)")
	}
	freshJob, err := wakeStore2.Schedule(ctx, scheduler.ScheduleRequest{
		PauseID: freshPause.Record.ID, Kind: "pause_resume", RunAfter: clock.Now(), MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("Schedule (post-restart, fresh): %v", err)
	}
	freshClaim, err := wakeStore2.Claim(ctx, "qa03-worker-post-restart-2", time.Minute)
	if err != nil || !freshClaim.Found || freshClaim.Job.ID != freshJob.ID {
		t.Fatalf("Claim (post-restart, fresh job): found=%v err=%v", freshClaim.Found, err)
	}

	t.Logf("qa-03: multi-role state (claude-provider event, checkpoint Progress-Tree/State/Repository checkpoints, predictor evaluation/authorization, runtime pause/scheduler) all survived a full restart against the same SQLite file %s, and every role's real service could still write fresh state afterward", dbPath)
}
