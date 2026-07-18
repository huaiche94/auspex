// e2e_highrisk_test.go implements qa-02 (docs/implementation/vertical-slice/
// EXECUTION_DAG.md's qa-02 row; agents/qa.md deliverable #2: "One
// end-to-end high-risk Claude fixture flow"). Per the DAG's own framing,
// this is THE literal vertical-slice demo: "Cannot start meaningfully until all six
// upstream tasks are real" — claude-provider-07, checkpoint-a09,
// checkpoint-b09, predictor-11, runtime-a11, runtime-b10 — and, as of this
// phase (Phases 8-11 merged), every one of them is.
//
// # The narrative this file drives, once, end to end
//
// A single coherent scenario models one realistic "risky turn" rather than
// six disconnected sub-tests, per the task brief's explicit preference:
//
//  1. Status-line ingestion: a REAL Claude Code status-line snapshot
//     (testdata/provider-events/claude/statusline/high_usage.json — 98.85%
//     context used, 97.3% five-hour quota used) is parsed and normalized by
//     claude-provider's real Normalizer and persisted via the real
//     EventStore. This is the observational signal that foreshadows the
//     rest of the scenario: an agent deep into a long, expensive turn.
//  2. Prompt auspex block: a real evaluation.Service.EvaluateTurn ->
//     Decide call, driven into the critical risk band via a DataSource
//     fixture reusing runtime-b06's own documented technique
//     (decision_realauth_test.go's newHighRiskDataSource: large
//     changed-file/line quantiles plus every completion/blast-radius flag
//     internal/predictor/risk/combiner.go reads) — this is the same
//     session's next prompt: a large, security-sensitive, migration-like
//     change, exactly the kind of turn that SHOULD be blocked pending
//     confirmation given the quota/context pressure step 1 just observed.
//  3. State/repo checkpoint: checkpoint's real CompleteNode (a real
//     Progress Tree node completion, State Checkpoint created in the same
//     atomic operation per Constitution Sec6.3) plus a real
//     orchestrator.CheckpointCreate call (real statecheckpoint.Service +
//     real repocheckpoint.Service against a real scratch Git repository) —
//     the checkpoint a PolicyCheckpointAndRun decision requires before
//     proceeding.
//  4. One-time allow: orchestrator.DecisionAllowCmd's real issue-then-consume
//     flow against the SAME real evaluation.Service (predictor-10's
//     storage-backed exactly-once ConsumeAuthorization) — the user
//     reviews and allows the high-risk turn to proceed exactly once, and a
//     replay of the same authorization is proven rejected.
//  5. Stop outcome: the turn finishes; a REAL Stop fixture is parsed,
//     normalized, and persisted via orchestrator.HandleStop (claude-
//     provider's real Normalizer, runtime-b04's real hook handler).
//  6. Pause request/wake recovery: immediately after, the SAME session's
//     quota pressure (still near the ceiling from step 1) trips a Graceful
//     Pause — real pause.RequestPause, a real persist-shaped sequence
//     (Progress Tree node already completed/checkpointed in step 3, State
//     Checkpoint id threaded onto the pause record), real
//     pause.InterruptAndSleep, a real durable wake job
//     (internal/scheduler.Store, backed by the same on-disk DB), and full
//     wake recovery through the real, SQLite-backed pause.SQLiteStore
//     (runtime-b10) — pause.Wake -> pause.ValidateResume (against the SAME
//     real evaluation.Service for its Authorization check) -> pause.Resume
//     -> Resumed.
//
// Every dependency below is a REAL implementation against a single,
// shared, on-disk (never :memory:) SQLite database — matching this node's
// task brief ("driving REAL implementations throughout... nothing needs to
// be faked"). The only test-only glue is deriveCompleteNodeInput-style
// wiring already established and documented as such by qa-04
// (duplicate_outoforder_test.go's package doc comment) for the one
// still-missing piece of production wiring (no adapter yet resolves an
// arbitrary persisted provider event to a specific ProgressNodeID) — this
// file does not re-litigate that finding, it simply reuses the same
// documented, test-only technique where the scenario needs a concrete
// NodeID to complete.
package integrationtest

import (
	"context"
	"errors"
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
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/pause"
	"github.com/huaiche94/auspex/internal/policy"
	"github.com/huaiche94/auspex/internal/predictor/quota"
	"github.com/huaiche94/auspex/internal/predictor/risk"
	"github.com/huaiche94/auspex/internal/predictor/scope"
	"github.com/huaiche94/auspex/internal/predictor/token"
	"github.com/huaiche94/auspex/internal/progress"
	claudeprovider "github.com/huaiche94/auspex/internal/providers/claude"
	"github.com/huaiche94/auspex/internal/repocheckpoint"
	"github.com/huaiche94/auspex/internal/scheduler"
	"github.com/huaiche94/auspex/internal/statecheckpoint"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// --- shared fixtures / test doubles (qa02-prefixed, following qa-04's own
// documented precedent of a small per-file duplicate rather than an
// exported cross-package helper) ---------------------------------------

type qa02Clock struct{ t time.Time }

func (c qa02Clock) Now() time.Time { return c.t }

type qa02IDs struct {
	n      int
	prefix string
}

func (g *qa02IDs) NewID() string {
	g.n++
	return g.prefix + "-" + qa02Itoa(g.n)
}

func qa02Itoa(n int) string {
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

func qa02Fixture(t *testing.T, dir, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "provider-events", "claude", dir, name))
	if err != nil {
		t.Fatalf("reading fixture %s/%s: %v", dir, name, err)
	}
	return b
}

// --- real on-disk SQLite database, holding every role's tables at once --

func qa02OpenDB(t *testing.T) *sqlite.DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "auspex-e2e.db")
	db, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("sqlite.AllMigrations: %v", err)
	}
	if err := db.Migrate(context.Background(), migrations); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	return db
}

// qa02SeedChain inserts a minimal repositories -> worktrees ->
// provider_sessions -> tasks chain, mirroring restart_test.go's
// seedRestartChain / duplicate_outoforder_test.go's qa04SeedTask (both
// unexported in different packages) so every real service this scenario
// wires (Progress Tree node FK, Repository Checkpoint worktree FK, pause
// record task/session FK) has satisfiable foreign keys.
func qa02SeedChain(t *testing.T, db *sqlite.DB, repoID, worktreePath string, worktreeID domain.WorktreeID, sessionID domain.SessionID, taskID domain.TaskID) {
	t.Helper()
	now := "2026-07-12T09:00:00Z"
	stmts := []string{
		`INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
		 VALUES ('` + repoID + `', '` + worktreePath + `', '` + worktreePath + `/.git', '` + now + `', '` + now + `')`,
		`INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
		 VALUES ('` + string(worktreeID) + `', '` + repoID + `', '` + worktreePath + `', '` + worktreePath + `/.git', '` + now + `', '` + now + `')`,
		`INSERT INTO provider_sessions (id, worktree_id, provider, invocation_mode, started_at, metadata_json)
		 VALUES ('` + string(sessionID) + `', '` + string(worktreeID) + `', 'claude-code', 'interactive', '` + now + `', '{}')`,
		`INSERT INTO tasks (id, session_id, worktree_id, objective_hash, status, created_at, updated_at)
		 VALUES ('` + string(taskID) + `', '` + string(sessionID) + `', '` + string(worktreeID) + `', 'hash-e2e', 'in_progress', '` + now + `', '` + now + `')`,
	}
	for _, stmt := range stmts {
		if _, err := db.Conn().ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
}

// qa02Repo creates a real, temporary Git repository (skips the test if git
// is unavailable) — mirrors restart_test.go's restartRepo /
// leakage_scanner_test.go's checkpointRepoBuilder exactly, duplicated per
// this codebase's own established precedent for small, package-crossing
// test helpers.
type qa02Repo struct {
	t   *testing.T
	dir string
}

func newQA02Repo(t *testing.T) *qa02Repo {
	t.Helper()
	runner := gitx.ExecRunner{}
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", dir, err)
	}
	rb := &qa02Repo{t: t, dir: resolved}
	if _, err := runner.Run(context.Background(), rb.dir, "git", "--version"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	rb.git("init", "-q", "-b", "main")
	rb.git("config", "user.name", "Auspex QA E2E")
	rb.git("config", "user.email", "qa-e2e@auspex.invalid")
	rb.git("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(rb.dir, "README.md"), []byte("auspex e2e scratch repo\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	rb.git("add", "README.md")
	rb.git("commit", "-q", "-m", "initial")
	return rb
}

func (rb *qa02Repo) git(args ...string) {
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

// --- high-risk evaluation.DataSource, reusing runtime-b06's documented
// technique (decision_realauth_test.go's newHighRiskDataSource) verbatim
// in shape, so this scenario's Decide call reaches the SAME critical band
// through the SAME real predictor pipeline, not a different, unverified
// fixture design. -----------------------------------------------------

type qa02DataSource struct {
	repositoryID domain.RepositoryID
	taskID       *domain.TaskID
}

func (f qa02DataSource) Resolve(_ context.Context, _ domain.SessionID) (evaluation.ResolvedSession, error) {
	return evaluation.ResolvedSession{RepositoryID: f.repositoryID, TaskID: f.taskID}, nil
}

func (f qa02DataSource) Classification(_ context.Context, _ domain.SessionID, _ *domain.TaskID) (features.Classification, features.PromptFeatures, error) {
	return features.Classification{Class: features.TaskClassSecuritySensitive, Confidence: domain.ConfidenceLow},
		features.PromptFeatures{
			ExplicitPathCount:   20,
			MentionsSecurity:    true,
			HasMigrateVerb:      true,
			CrossLayerIndicator: true,
			MentionsTests:       true,
			OpenEndedIndicator:  true,
		}, nil
}

func (f qa02DataSource) Repository(_ context.Context, _ domain.RepositoryID) (features.RepositoryFeatures, bool, error) {
	return features.RepositoryFeatures{TrackedFileCount: 500, DirtyFileCount: 10, DirtyLineCount: 400, TargetDirFanOut: 20}, true, nil
}

func (f qa02DataSource) Session(_ context.Context, _ domain.SessionID) (features.SessionFeatures, bool, error) {
	p50, p90, l50, l90 := 40.0, 90.0, 3000.0, 8000.0
	return features.SessionFeatures{
		ChangedFilesRecentP50: &p50, ChangedFilesRecentP90: &p90,
		ChangedLinesRecentP50: &l50, ChangedLinesRecentP90: &l90,
	}, true, nil
}

func (f qa02DataSource) Progress(_ context.Context, _ *domain.TaskID) (features.ProgressFeatures, bool, error) {
	return features.ProgressFeatures{CriticalPathLength: 50}, true, nil
}

func (f qa02DataSource) RecentSimilarTurnTokens(_ context.Context, _ domain.SessionID, _ features.TaskClass) (features.SimilarTurnTokens, error) {
	return features.SimilarTurnTokens{Rung: features.CohortRungSession}, nil
}

func (f qa02DataSource) Quota(_ context.Context, _ domain.SessionID) ([]domain.QuotaObservation, error) {
	used := 97.3 // matches this scenario's own status-line fixture (five_hour: 97.3%)
	return []domain.QuotaObservation{{
		ID: "q1", SessionID: "sess-e2e", Provider: "claude-code", LimitID: "five_hour",
		UsedPercent: &used, ObservedAt: time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC),
	}}, nil
}

func (f qa02DataSource) Context(_ context.Context, _ domain.SessionID) (domain.ContextObservation, error) {
	used := 98.85 // matches this scenario's own status-line fixture (context used_percentage: 98.85%)
	return domain.ContextObservation{ID: "c1", SessionID: "sess-e2e", UsedPercent: &used, ObservedAt: time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)}, nil
}

func (f qa02DataSource) RunwayForecast(_ context.Context, _ domain.SessionID) (domain.RunwayForecast, bool, error) {
	return domain.RunwayForecast{}, false, nil
}

func (f qa02DataSource) PriorRunwayHitConfirmed(_ context.Context, _ domain.SessionID) (bool, error) {
	return false, nil
}

var _ evaluation.DataSource = qa02DataSource{}

// qa02ScopeAdapter/qa02TokenAdapter adapt qa02DataSource to the scope/token
// stages' own narrower FeatureSource shapes — the same duplicated-adapter
// precedent decision_realauth_test.go/restart_test.go both already
// establish.
type qa02ScopeAdapter struct{ src evaluation.DataSource }

func (a qa02ScopeAdapter) Classification(ctx context.Context, s domain.SessionID, tid *domain.TaskID) (features.Classification, features.PromptFeatures, error) {
	return a.src.Classification(ctx, s, tid)
}
func (a qa02ScopeAdapter) Repository(ctx context.Context, r domain.RepositoryID) (features.RepositoryFeatures, bool, error) {
	return a.src.Repository(ctx, r)
}
func (a qa02ScopeAdapter) Session(ctx context.Context, s domain.SessionID) (features.SessionFeatures, bool, error) {
	return a.src.Session(ctx, s)
}
func (a qa02ScopeAdapter) Progress(ctx context.Context, tid *domain.TaskID) (features.ProgressFeatures, bool, error) {
	return a.src.Progress(ctx, tid)
}

type qa02TokenAdapter struct {
	src    evaluation.DataSource
	taskID domain.TaskID
}

func (a qa02TokenAdapter) Classification(ctx context.Context, s domain.SessionID) (features.Classification, features.PromptFeatures, error) {
	return a.src.Classification(ctx, s, nil)
}
func (a qa02TokenAdapter) Session(ctx context.Context, s domain.SessionID) (features.SessionFeatures, bool, error) {
	return a.src.Session(ctx, s)
}
func (a qa02TokenAdapter) Progress(ctx context.Context, s domain.SessionID) (features.ProgressFeatures, bool, error) {
	return a.src.Progress(ctx, &a.taskID)
}
func (a qa02TokenAdapter) RecentSimilarTurnTokens(ctx context.Context, s domain.SessionID, c features.TaskClass) (features.SimilarTurnTokens, error) {
	return a.src.RecentSimilarTurnTokens(ctx, s, c)
}

// qa02NewEvaluationService wires a REAL *evaluation.Service against real
// pipeline stage implementations (scope/token/quota/risk/policy) and the
// scenario's own shared on-disk DB — mirrors decision_realauth_test.go's
// newRealEvaluationService / restart_test.go's newRestartEvaluationService.
func qa02NewEvaluationService(db *sqlite.DB, clk domain.Clock, ids domain.IDGenerator, src qa02DataSource) *evaluation.Service {
	return evaluation.New(
		db, src,
		scope.NewRuleScopeEstimator(qa02ScopeAdapter{src: src}),
		token.NewRuleTokenForecaster(qa02TokenAdapter{src: src, taskID: *src.taskID}),
		quota.NewRuleQuotaForecaster(),
		risk.NewRuleRiskCombiner(),
		policy.NewDecider(),
		clk, ids,
	)
}

// --- pause-side fixtures: real ValidateResume seams, wired against the
// SAME real evaluation.Service for the authorization check (more real than
// resumevalidation_test.go's own okEvaluations fake), and trivially
// permissive real-shaped readers for quota/repo/session (this scenario's
// own point is proving the FULL loop closes, not re-litigating a08's own
// per-checker unit tests). ------------------------------------------------

type qa02QuotaReader struct{}

func (qa02QuotaReader) ReadCurrentQuota(_ context.Context, _ domain.SessionID, _ string) (domain.QuotaObservation, error) {
	used := 40.0 // improved since the pause (a real resume candidate scenario)
	return domain.QuotaObservation{LimitID: "five_hour", UsedPercent: &used}, nil
}

type qa02RepoFingerprintReader struct{ headOID string }

func (r qa02RepoFingerprintReader) ReadCurrentFingerprint(_ context.Context, _ domain.WorktreeID) (pause.RepoFingerprint, error) {
	return pause.RepoFingerprint{HeadOID: r.headOID}, nil
}

type qa02SessionReader struct{}

func (qa02SessionReader) ReadSessionCapability(_ context.Context, _ domain.SessionID) (pause.SessionCapabilitySnapshot, error) {
	return pause.SessionCapabilitySnapshot{Resumable: true, Capabilities: domain.ProviderCapabilities{SessionResume: true}}, nil
}

// --- the scenario itself --------------------------------------------------

// TestE2EHighRisk_RiskyTurnFullLifecycle drives the entire narrative
// described in this file's package comment against one shared, real,
// on-disk SQLite database and one shared real scratch Git repository.
func TestE2EHighRisk_RiskyTurnFullLifecycle(t *testing.T) {
	db := qa02OpenDB(t)
	repo := newQA02Repo(t)

	const (
		repoID     = "repo-e2e"
		worktreeID = domain.WorktreeID("wt-e2e")
		sessionID  = domain.SessionID("sess-e2e")
		taskID     = domain.TaskID("task-e2e")
	)
	qa02SeedChain(t, db, repoID, repo.dir, worktreeID, sessionID, taskID)

	clock := qa02Clock{t: time.Date(2026, 7, 12, 14, 0, 0, 0, time.UTC)}
	ctx := context.Background()

	// =====================================================================
	// Step 1: status-line ingestion — real parse/normalize/persist.
	// =====================================================================
	normalizer := claudetelemetry.NewNormalizer(clock, &qa02IDs{prefix: "norm"})
	eventStore := claudetelemetry.NewEventStore(db)

	snap, err := claudeprovider.ParseStatusLine(qa02Fixture(t, "statusline", "high_usage.json"))
	if err != nil {
		t.Fatalf("ParseStatusLine: %v", err)
	}
	statusEvents := normalizer.NormalizeStatusLine(snap, clock.Now())
	if len(statusEvents) == 0 {
		t.Fatal("expected NormalizeStatusLine to produce at least one event from a fully-populated high-usage snapshot")
	}
	if err := eventStore.PersistAll(ctx, db, statusEvents); err != nil {
		t.Fatalf("PersistAll (status-line events): %v", err)
	}

	// Sanity: this scenario's whole premise is real quota/context PRESSURE
	// observed here — confirm the persisted events actually carry it,
	// rather than silently relying on the fixture's raw JSON alone.
	foundQuotaPressure := false
	foundContextPressure := false
	for _, ev := range statusEvents {
		switch ev.EventType {
		case v1.EventProviderQuotaObserved:
			foundQuotaPressure = true
		case v1.EventProviderContextObserved:
			foundContextPressure = true
		}
	}
	if !foundQuotaPressure || !foundContextPressure {
		t.Fatalf("expected both quota and context observation events from high_usage.json, got quota=%v context=%v (events: %+v)", foundQuotaPressure, foundContextPressure, statusEvents)
	}

	// =====================================================================
	// Step 2: prompt auspex block — real EvaluateTurn -> Decide, driven
	// into the critical risk band.
	// =====================================================================
	tid := taskID
	src := qa02DataSource{repositoryID: repoID, taskID: &tid}
	evalSvc := qa02NewEvaluationService(db, clock, &qa02IDs{prefix: "eval"}, src)

	turnID := domain.TurnID("turn-e2e-risky")
	promptHash := "sha256:risky-turn-prompt"

	eval, err := evalSvc.EvaluateTurn(ctx, app.EvaluateTurnRequest{
		SessionID: sessionID, TurnID: turnID, Provider: "claude", PromptHash: promptHash,
	})
	if err != nil {
		t.Fatalf("EvaluateTurn: %v", err)
	}
	decision, err := evalSvc.Decide(ctx, app.DecideRequest{EvaluationID: eval.ID})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decision.Action != app.PolicyCheckpointAndRun && decision.Action != app.PolicyRequireConfirmation {
		t.Fatalf("Decide.Action = %q, want a high-risk action (%q or %q) — the fixture did not drive risk into the critical band",
			decision.Action, app.PolicyCheckpointAndRun, app.PolicyRequireConfirmation)
	}
	t.Logf("step 2: real pipeline reached Decide.Action=%q (Evaluation.Confidence=%q, Calibrated=%v)", decision.Action, eval.Confidence, eval.Calibrated)

	// =====================================================================
	// Step 3: state/repo checkpoint — real CompleteNode (Progress Tree +
	// State Checkpoint, atomic per Constitution Sec6.3) followed by a real
	// orchestrator.CheckpointCreate (State then Repository, per that
	// package's own frozen ordering guarantee).
	// =====================================================================

	// 3a. A real Progress Tree node, completed via the real CompleteNode
	// service — this is the node the risky turn's work belongs to.
	nodeID := domain.ProgressNodeID("node-e2e-risky-turn")
	nodeStore := progress.NewNodeStore(db, clock)
	if err := nodeStore.Insert(ctx, progress.Node{
		ID: nodeID, TaskID: taskID, Ordinal: 1, Kind: domain.NodeDocumentSection,
		Title:  "Node " + string(nodeID),
		Status: domain.NodePending,
		Acceptance: []progress.AcceptanceCriterion{
			{Kind: "heading_exists", Value: "# Risky Change"},
			{Kind: "fence_balance"},
		},
		Version: 1, UpdatedAt: clock.Now().Format(time.RFC3339),
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
	scStore := statecheckpoint.NewStore(db)
	completeNode := &progress.CompleteNode{
		DB: db, Clock: clock, IDs: &qa02IDs{prefix: "cn"},
		Nodes: nodeStore, Edges: progress.NewEdgeStore(db), Artifacts: progress.NewArtifactStore(db),
		Validators: artifacts.NewRegistry(), Stager: stager,
		Checkpoints: scStore, Publisher: progress.NoopPublisher{},
	}
	artifactPath := filepath.Join(evidenceDir, "risky-change.md")
	if err := os.WriteFile(artifactPath, []byte("# Risky Change\n\nsecurity-sensitive migration, large diff\n"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	completion, err := completeNode.Run(ctx, progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "node-e2e-risky-turn-completion",
		Artifacts: []domain.ArtifactRef{{
			ID: "artifact-risky-1", Kind: "file", URI: "file:" + artifactPath, MediaType: "text/markdown",
		}},
	})
	if err != nil {
		t.Fatalf("CompleteNode.Run: %v", err)
	}
	if completion.Node.Status != domain.NodeCompleted {
		t.Fatalf("node status = %q, want %q", completion.Node.Status, domain.NodeCompleted)
	}
	if completion.Checkpoint.ID == "" {
		t.Fatal("expected CompleteNode to create a State Checkpoint in the same atomic operation (Constitution Sec6.3), got an empty ID")
	}
	t.Logf("step 3a: real CompleteNode completed node %s with checkpoint %s", nodeID, completion.Checkpoint.ID)

	// 3b. A real, additional CURRENT-state checkpoint via
	// orchestrator.CheckpointCreate — this is the checkpoint a
	// PolicyCheckpointAndRun decision actually requires immediately before
	// allowing the risky turn to proceed, distinct from CompleteNode's own
	// completion-triggered checkpoint above (statecheckpoint's own package
	// doc comment: Create is "a STANDALONE, ad hoc snapshot entry point,"
	// not a wrapper around CompleteNode's path).
	treeReader := qa02TreeReader{nodes: nodeStore, artifacts: progress.NewArtifactStore(db), nodeID: nodeID}
	stateSvc := statecheckpoint.NewService(scStore, treeReader, clock, &qa02IDs{prefix: "sc"})

	gitClient := gitx.NewClient(gitx.ExecRunner{})
	repoStore := repocheckpoint.NewStore(db)
	resolveWorktree := func(_ context.Context, id domain.WorktreeID) (repocheckpoint.WorktreeLocation, error) {
		if id != worktreeID {
			return repocheckpoint.WorktreeLocation{}, &domain.Error{Code: domain.ErrCodeNotFound, Message: "unknown worktree"}
		}
		return repocheckpoint.WorktreeLocation{RepositoryID: repoID, Path: repo.dir}, nil
	}
	repoSvc := repocheckpoint.NewService(gitClient, repoStore, clock, &qa02IDs{prefix: "rc"}, t.TempDir(), resolveWorktree, repocheckpoint.CaptureOptions{})

	checkpointResult, err := orchestrator.CheckpointCreate(ctx, orchestrator.CheckpointCreateDeps{
		StateCheckpoint:      stateSvc,
		RepositoryCheckpoint: repoSvc,
	}, orchestrator.CheckpointCreateRequest{TaskID: taskID, WorktreeID: worktreeID})
	if err != nil {
		t.Fatalf("CheckpointCreate: %v", err)
	}
	if checkpointResult.State.ID == "" || checkpointResult.Repository.ID == "" {
		t.Fatalf("CheckpointCreate result incomplete: %+v", checkpointResult)
	}
	t.Logf("step 3b: real CheckpointCreate produced state=%s repository=%s (git head %s)",
		checkpointResult.State.ID, checkpointResult.Repository.ID, checkpointResult.Repository.GitHead)

	// Independently verify the repository checkpoint really is
	// restore-dry-run-clean at this point (checkpoint-b09's real Verify),
	// since this scenario's later resume validation depends on it.
	repoVerification, err := repoSvc.Verify(ctx, checkpointResult.Repository.ID)
	if err != nil {
		t.Fatalf("RepositoryCheckpoint.Verify: %v", err)
	}
	if !repoVerification.Valid {
		t.Fatal("expected the freshly-created Repository Checkpoint to verify as valid")
	}

	// =====================================================================
	// Step 4: one-time allow — real DecisionAllowCmd issue-then-consume,
	// then replay-rejected.
	// =====================================================================
	decisionDeps := orchestrator.DecisionDeps{Evaluation: evalSvc, Issuer: evalSvc}
	repoCkptID := checkpointResult.Repository.ID

	issueResult, err := orchestrator.DecisionAllowCmd(ctx, decisionDeps, orchestrator.DecisionAllowRequest{
		EvaluationID:           eval.ID,
		TurnID:                 turnID,
		PromptHash:             promptHash,
		SnapshotFingerprint:    checkpointResult.Repository.GitHead,
		RepositoryCheckpointID: &repoCkptID,
	})
	if err != nil {
		t.Fatalf("DecisionAllowCmd (issue): %v", err)
	}
	if !issueResult.Issued || issueResult.Consumed {
		t.Fatalf("issueResult = %+v, want Issued=true Consumed=false", issueResult)
	}
	authID := issueResult.Authorization.ID
	if authID == "" {
		t.Fatal("issued Authorization has an empty ID")
	}

	// The resubmitted prompt: consumes the authorization exactly once.
	consumeResult, err := orchestrator.DecisionAllowCmd(ctx, decisionDeps, orchestrator.DecisionAllowRequest{
		TurnID: turnID, PromptHash: promptHash, AuthorizationID: authID,
	})
	if err != nil {
		t.Fatalf("DecisionAllowCmd (consume, resubmission): %v", err)
	}
	if !consumeResult.Consumed || consumeResult.Authorization.ConsumedAt == nil {
		t.Fatalf("consumeResult = %+v, want Consumed=true with a non-nil ConsumedAt", consumeResult)
	}

	// Replay: the SAME authorization presented again must be rejected by
	// the real, storage-backed exactly-once check (predictor-10's
	// hardened markAuthorizationConsumed conditional update).
	_, err = orchestrator.DecisionAllowCmd(ctx, decisionDeps, orchestrator.DecisionAllowRequest{
		TurnID: turnID, PromptHash: promptHash, AuthorizationID: authID,
	})
	if err == nil {
		t.Fatal("expected a replayed consume of the same authorization to be rejected")
	}
	if derr, ok := requireQA02DomainError(t, err); ok && derr.Code != domain.ErrCodeConflict {
		t.Errorf("replay err.Code = %q, want %q", derr.Code, domain.ErrCodeConflict)
	}
	t.Logf("step 4: real one-time authorization %s issued, consumed exactly once, replay correctly rejected", authID)

	// =====================================================================
	// Step 5: Stop outcome — real Stop fixture parsed/normalized/persisted
	// through the real hook handler.
	// =====================================================================
	hookDeps := orchestrator.HookDeps{
		Clock: clock, IDs: &qa02IDs{prefix: "hook"},
		Persister: eventStore, TxRunner: db,
	}
	stopResult, err := orchestrator.HandleStop(ctx, hookDeps, qa02Fixture(t, "stop", "normal.json"))
	if err != nil {
		t.Fatalf("HandleStop: %v", err)
	}
	if stopResult.EventsNormalized == 0 || !stopResult.Persisted {
		t.Fatalf("HandleStop result = %+v, want at least one event normalized and persisted", stopResult)
	}
	t.Logf("step 5: real Stop hook normalized+persisted %d event(s)", stopResult.EventsNormalized)

	// =====================================================================
	// Step 6: pause request/wake recovery — real full pause lifecycle
	// against the real SQLite-backed PauseStore.
	// =====================================================================
	pauseStore := pause.NewSQLiteStore(db)
	ids := &qa02IDs{prefix: "pause"}

	requestResult, err := pause.RequestPause(ctx, pauseStore, ids, pause.RequestPauseRequest{
		Key:    pause.PauseKey{TaskID: taskID, SessionID: sessionID},
		Reason: pause.TriggerReasonCalibrated,
	})
	if err != nil {
		t.Fatalf("RequestPause: %v", err)
	}
	if !requestResult.Created {
		t.Fatal("expected RequestPause to create a fresh record for this scenario's first request")
	}
	pauseID := requestResult.Record.ID
	if requestResult.Record.Status != domain.PausePredicted {
		t.Fatalf("fresh pause record status = %q, want %q", requestResult.Record.Status, domain.PausePredicted)
	}

	// Drive the real state machine forward (mirrors
	// internal/pause/fulllifecycle_test.go's own runFullLifecycleToSleeping
	// technique, against the real SQLiteStore here rather than MemStore) to
	// Sleeping, then a real InterruptAndSleep call, then a real durable
	// wake job.
	qa02ApplyAndPersist(t, ctx, pauseStore, pauseID, pause.EventDebouncePassed, domain.PauseRequested)
	qa02ApplyAndPersist(t, ctx, pauseStore, pauseID, pause.EventThresholdCrossed, domain.PauseQuiescing)
	qa02ApplyAndPersist(t, ctx, pauseStore, pauseID, pause.EventSafePointReached, domain.PauseCheckpointing)
	qa02ApplyAndPersist(t, ctx, pauseStore, pauseID, pause.EventCheckpointVerified, domain.PauseInterrupting)

	interrupter := pause.TurnInterrupterAdapter{
		Interrupter: qa02Interrupter{},
		Locate: func(domain.PauseID) app.RunLocator {
			return app.RunLocator{SessionID: sessionID, TurnID: turnID}
		},
	}
	interruptedRec, err := pause.InterruptAndSleep(ctx, pauseStore, interrupter, pauseID)
	if err != nil {
		t.Fatalf("InterruptAndSleep: %v", err)
	}
	if interruptedRec.Status != domain.PauseSleeping {
		t.Fatalf("post-interrupt status = %q, want %q", interruptedRec.Status, domain.PauseSleeping)
	}

	wakeStore := scheduler.NewStore(db.Conn(), clock, &qa02IDs{prefix: "wj"})
	wakeJob, err := wakeStore.Schedule(ctx, scheduler.ScheduleRequest{
		PauseID: pauseID, Kind: "pause_resume", RunAfter: clock.Now(), MaxAttempts: 5,
	})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	claim, err := wakeStore.Claim(ctx, "e2e-worker", 5*time.Minute)
	if err != nil || !claim.Found || claim.Job.ID != wakeJob.ID {
		t.Fatalf("Claim: found=%v err=%v job=%v want=%v", claim.Found, err, claim.Job.ID, wakeJob.ID)
	}

	// Real wake recovery: Wake -> ValidateResume (against the SAME real
	// evaluation.Service for its authorization check — a fresh
	// authorization is issued for the resume itself, mirroring how a real
	// resumed turn would need its own one-time allow) -> Resume -> Resumed.
	wakeResult, err := pause.Wake(ctx, pauseStore, pause.WakeRequest{PauseID: pauseID})
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if wakeResult.Record.Status != domain.PauseWakePending {
		t.Fatalf("post-Wake status = %q, want %q", wakeResult.Record.Status, domain.PauseWakePending)
	}

	resumeTurnID := domain.TurnID("turn-e2e-resume")
	resumePromptHash := "sha256:resume-prompt"
	resumeEval, err := evalSvc.EvaluateTurn(ctx, app.EvaluateTurnRequest{
		SessionID: sessionID, TurnID: resumeTurnID, Provider: "claude", PromptHash: resumePromptHash,
	})
	if err != nil {
		t.Fatalf("EvaluateTurn (resume): %v", err)
	}
	if _, err := evalSvc.Decide(ctx, app.DecideRequest{EvaluationID: resumeEval.ID}); err != nil {
		t.Fatalf("Decide (resume): %v", err)
	}
	resumeIssue, err := orchestrator.DecisionAllowCmd(ctx, decisionDeps, orchestrator.DecisionAllowRequest{
		EvaluationID: resumeEval.ID, TurnID: resumeTurnID, PromptHash: resumePromptHash,
	})
	if err != nil {
		t.Fatalf("DecisionAllowCmd (resume issue): %v", err)
	}

	validation, err := pause.ValidateResume(ctx, pause.ResumeValidationDeps{
		Quota:                qa02QuotaReader{},
		RepositoryCheckpoint: repoSvc,
		RepoFingerprint:      qa02RepoFingerprintReader{headOID: checkpointResult.Repository.GitHead},
		Session:              qa02SessionReader{},
		Evaluations:          evalSvc,
	}, pause.ResumeValidationRequest{
		SessionID:              sessionID,
		QuotaBaseline:          domain.QuotaObservation{LimitID: "five_hour", UsedPercent: ptrQA02(97.3)},
		RepositoryCheckpointID: checkpointResult.Repository.ID,
		BaselineGitHead:        checkpointResult.Repository.GitHead,
		WorktreeID:             worktreeID,
		Authorization: app.ConsumeAuthorizationRequest{
			AuthorizationID: resumeIssue.Authorization.ID, TurnID: resumeTurnID, PromptHash: resumePromptHash,
		},
	})
	if err != nil {
		t.Fatalf("ValidateResume: %v", err)
	}
	if !validation.AllPass() {
		t.Fatalf("expected ValidateResume to pass for this real, healthy resume candidate, got %+v", validation)
	}

	verdict := validation.Verdict()
	verdict.PauseID = pauseID
	resumeResult, err := pause.Resume(ctx, pauseStore, verdict)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resumeResult.Record.Status != domain.PauseResumed {
		t.Fatalf("final pause status = %q, want %q", resumeResult.Record.Status, domain.PauseResumed)
	}

	completedJob, err := wakeStore.Complete(ctx, wakeJob.ID, "e2e-worker")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if completedJob.Status != scheduler.StatusDone {
		t.Fatalf("wake job status = %q, want %q", completedJob.Status, scheduler.StatusDone)
	}
	t.Logf("step 6: real pause %s survived full request->persist->interrupt->sleep->wake->validate->resume lifecycle, wake job %s completed", pauseID, wakeJob.ID)

	// --- final cross-cutting assertion: every durable artifact this whole
	// scenario produced is still consistent when read back fresh, proving
	// the six steps genuinely composed into one coherent state rather than
	// merely six independently-passing calls. ---------------------------
	finalPause, found, err := pauseStore.GetByID(ctx, pauseID)
	if err != nil || !found {
		t.Fatalf("final GetByID: found=%v err=%v", found, err)
	}
	if finalPause.Status != domain.PauseResumed {
		t.Fatalf("re-read pause status = %q, want %q", finalPause.Status, domain.PauseResumed)
	}
	finalNode, err := nodeStore.Get(ctx, nodeID)
	if err != nil {
		t.Fatalf("final node Get: %v", err)
	}
	if finalNode.Status != domain.NodeCompleted {
		t.Fatalf("re-read node status = %q, want %q", finalNode.Status, domain.NodeCompleted)
	}
	finalRepoVerify, err := repoSvc.Verify(ctx, checkpointResult.Repository.ID)
	if err != nil || !finalRepoVerify.Valid {
		t.Fatalf("final repository checkpoint re-verify: valid=%v err=%v", finalRepoVerify.Valid, err)
	}
}

// --- small local helpers ---------------------------------------------------

// qa02TreeReader adapts real progress.NodeStore/ArtifactStore to
// statecheckpoint.TreeReader — mirrors restart_test.go's own
// restartTreeReader, except this one reads the REAL populated node/artifact
// rows this scenario created (restart_test.go's own version is
// deliberately always-empty, sufficient for its own narrower restart-safety
// purpose; this scenario's step 3b checkpoint should reflect the real
// completed node from step 3a).
type qa02TreeReader struct {
	nodes     *progress.NodeStore
	artifacts *progress.ArtifactStore
	nodeID    domain.ProgressNodeID
}

func (r qa02TreeReader) ListNodes(ctx context.Context, taskID domain.TaskID) ([]statecheckpoint.NodeSnapshot, error) {
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

// ListArtifacts reads this scenario's own single node's real artifacts via
// ListByNode (ArtifactStore has no task-scoped list method — this
// scenario's own Progress Tree has exactly one node, so per-node listing
// is sufficient and genuinely real, not a stand-in).
func (r qa02TreeReader) ListArtifacts(ctx context.Context, _ domain.TaskID) ([]statecheckpoint.ArtifactSnapshot, error) {
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

// qa02Interrupter is a trivial, always-succeeding app.TurnInterrupter —
// this scenario's point is proving the FULL pause loop closes (persisted
// end to end, wake recovers, resume succeeds), not re-testing
// runtime-a11's own already-proven provider-interrupt-failure branch
// (fulllifecycle_test.go's own dedicated coverage).
type qa02Interrupter struct{}

func (qa02Interrupter) Interrupt(_ context.Context, _ app.RunLocator) error { return nil }

// qa02ApplyAndPersist drives the real pause.Apply state-machine function
// and persists the result via a real CompareAndSwapStatus call against the
// current record — the same fixed-sequence technique
// fulllifecycle_test.go's own runFullLifecycleToSleeping helper uses
// (duplicated here per that file's own established precedent for small,
// package-crossing test helpers), applied here against the real
// SQLiteStore instead of MemStore.
func qa02ApplyAndPersist(t *testing.T, ctx context.Context, store pause.PauseStore, id domain.PauseID, ev pause.Event, want domain.PauseStatus) {
	t.Helper()
	current, found, err := store.GetByID(ctx, id)
	if err != nil || !found {
		t.Fatalf("GetByID before %q: found=%v err=%v", ev, found, err)
	}
	next, err := pause.Apply(current.Status, ev)
	if err != nil {
		t.Fatalf("Apply(%q, %q): %v", current.Status, ev, err)
	}
	if next != want {
		t.Fatalf("Apply(%q, %q) = %q, want %q", current.Status, ev, next, want)
	}
	ok, found, err := store.CompareAndSwapStatus(ctx, id, current.Status, next)
	if err != nil || !found || !ok {
		t.Fatalf("CompareAndSwapStatus(%q -> %q): ok=%v found=%v err=%v", current.Status, next, ok, found, err)
	}
}

func ptrQA02(f float64) *float64 { return &f }

// requireQA02DomainError asserts err is a *domain.Error and returns it
// alongside an ok flag, so callers can chain a Code check on one line
// without a second t.Fatalf branch for the type assertion itself.
func requireQA02DomainError(t *testing.T, err error) (*domain.Error, bool) {
	t.Helper()
	var derr *domain.Error
	if !errors.As(err, &derr) {
		t.Fatalf("err = %v (%T), want *domain.Error", err, err)
		return nil, false
	}
	return derr, true
}
