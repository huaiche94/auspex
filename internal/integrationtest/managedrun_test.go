// managedrun_test.go: end-to-end coverage for `auspex run` (issue #8's
// managed one-shot MVP, ADD §8.1) — the REAL cli.NewRunCmd command over
// the REAL production stack (evaluation.Service + SQLDataSource + the
// real predictor stages + the real claudetelemetry.EventStore against a
// real on-disk SQLite file — the exact harness shape
// evaluate_privacy_test.go's buildEvaluateCLIRoot established, reusing
// this package's fixedClock/seqIDs and tokenSourceBridge), spawning a
// REAL subprocess: the compiled fake provider from internal/managed/
// testdata/fakeprovider (a Go helper built with `go build`, never a shell
// script — windows-latest CI is hard-blocking; internal/gitx's tests
// exec'ing real git are this repo's precedent for real subprocesses in
// tests). The canned stream fixture's provenance is documented in
// internal/managed/stream_test.go (hand-authored to Claude Code CLI's
// public stream-json format; not a recording of any real session).
package integrationtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/cli"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/evaluation"
	"github.com/huaiche94/auspex/internal/features"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/policy"
	"github.com/huaiche94/auspex/internal/predictor/quota"
	"github.com/huaiche94/auspex/internal/predictor/risk"
	"github.com/huaiche94/auspex/internal/predictor/scope"
	"github.com/huaiche94/auspex/internal/predictor/token"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
)

const (
	managedRunSession  = "sess-managed-run"
	managedRunWorktree = "wt-managed-run"
	managedRunPrompt   = "implement a small fix"
)

// buildFakeProviderBinary compiles internal/managed/testdata/fakeprovider
// into a temp dir (same helper shape as internal/managed's own run_test —
// duplicated per this package's established per-file-duplicate
// convention). The forward-slash package path is deliberate: the go tool
// accepts it on every OS, including Windows.
func buildFakeProviderBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fake-claude")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "../managed/testdata/fakeprovider")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build fakeprovider: %v\n%s", err, out)
	}
	return bin
}

func managedStreamFixture(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "managed", "testdata", name))
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	return abs
}

// buildManagedRunRoot assembles the production-shaped stack (mirroring
// buildEvaluateCLIRoot: migrated on-disk DB, seeded foundation chain,
// real evaluation.Service wired the way cmd/auspex/wire.go wires it) and
// returns a root carrying the REAL `run` command. evalOverride, when
// non-nil, replaces the real evaluation service (the BLOCK scenario needs
// a decision the real pipeline will not render for an empty-history
// session — the same fakes-for-unreachable-decisions technique
// runtime-b06's decision tests use).
func buildManagedRunRoot(t *testing.T, evalOverride app.EvaluationService) (*cobra.Command, *sqlite.DB) {
	t.Helper()
	ctx := context.Background()

	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "managed-run.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}
	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	now := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	seed := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at) VALUES (?, ?, ?, ?, ?)`,
			[]any{"repo-mr", "/tmp/repo-mr", "/tmp/repo-mr/.git", now, now}},
		{`INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at) VALUES (?, ?, ?, ?, ?, ?)`,
			[]any{managedRunWorktree, "repo-mr", "/tmp/repo-mr", "/tmp/repo-mr/.git", now, now}},
		{`INSERT INTO provider_sessions (id, worktree_id, provider, invocation_mode, started_at) VALUES (?, ?, ?, ?, ?)`,
			[]any{managedRunSession, managedRunWorktree, "claude", "managed_stream_json", now}},
	}
	for _, s := range seed {
		if _, err := db.Conn().ExecContext(ctx, s.q, s.args...); err != nil {
			t.Fatalf("seed %q: %v", s.q, err)
		}
	}

	clock := fixedClock{t: time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)}
	ids := &seqIDs{}
	dataSource := evaluation.NewSQLDataSource(db)
	var evalSvc app.EvaluationService = evaluation.New(
		db,
		dataSource,
		scope.NewRuleScopeEstimator(dataSource),
		token.NewRuleTokenForecaster(tokenSourceBridge{source: dataSource}),
		quota.NewRuleQuotaForecaster(),
		risk.NewRuleRiskCombiner(),
		policy.NewDecider(),
		clock,
		ids,
	)
	if evalOverride != nil {
		evalSvc = evalOverride
	}

	deps := orchestrator.HookDeps{
		Clock:      clock,
		IDs:        ids,
		Persister:  claudetelemetry.NewEventStore(db),
		TxRunner:   db,
		Evaluation: evalSvc,
	}

	root := &cobra.Command{Use: "auspex", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(cli.NewRunCmd(deps))
	return cli.WithJSONErrorRendering(root), db
}

// managedRunAttribution mirrors cli/run.go's runOutput wire shape for
// decoding assertions.
type managedRunAttribution struct {
	SchemaVersion   string   `json:"schema_version"`
	SessionID       string   `json:"session_id"`
	TurnID          string   `json:"turn_id"`
	EvaluationID    *string  `json:"evaluation_id"`
	Decision        *string  `json:"decision"`
	ExitCode        int      `json:"exit_code"`
	IsError         *bool    `json:"is_error"`
	TotalCostUSD    *float64 `json:"total_cost_usd"`
	DurationMs      *int64   `json:"duration_ms"`
	EventsPersisted int      `json:"events_persisted"`
}

func TestManagedRun_EndToEnd_GatePersistedEventsAttribution(t *testing.T) {
	bin := buildFakeProviderBinary(t)
	argvFile := filepath.Join(t.TempDir(), "argv.json")
	t.Setenv("AUSPEX_FAKE_STREAM_FILE", managedStreamFixture(t, "stream_success.jsonl"))
	t.Setenv("AUSPEX_FAKE_ARGV_FILE", argvFile)

	root, db := buildManagedRunRoot(t, nil)
	ctx := context.Background()

	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{
		"run",
		"--session-id", managedRunSession,
		"--worktree-id", managedRunWorktree,
		"--provider-bin", bin,
		"--", managedRunPrompt,
	})

	if err := root.Execute(); err != nil {
		t.Fatalf("auspex run over the real stack: %v\nstderr: %s", err, stderr.String())
	}

	// --- attribution JSON: stdout is EXACTLY one machine line ------------
	var out managedRunAttribution
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not one attribution JSON document: %v\n%s", err, stdout.String())
	}
	if out.SchemaVersion != "auspex.run.v1" {
		t.Errorf("schema_version = %q, want auspex.run.v1", out.SchemaVersion)
	}
	if out.SessionID != managedRunSession || out.TurnID == "" {
		t.Errorf("attribution session/turn = %q/%q, want %s/non-empty", out.SessionID, out.TurnID, managedRunSession)
	}
	if out.EvaluationID == nil || *out.EvaluationID == "" {
		t.Error("evaluation_id is null/empty — the gate did not run")
	}
	if out.Decision == nil || *out.Decision == string(app.PolicyBlock) || *out.Decision == "" {
		t.Errorf("decision = %v, want a real non-BLOCK policy action", out.Decision)
	}
	if out.ExitCode != 0 {
		t.Errorf("exit_code = %d, want 0", out.ExitCode)
	}
	if out.IsError == nil || *out.IsError {
		t.Errorf("is_error = %v, want false", out.IsError)
	}
	if out.TotalCostUSD == nil || *out.TotalCostUSD != 0.0417 {
		t.Errorf("total_cost_usd = %v, want 0.0417 (the fixture result line's exact figure)", out.TotalCostUSD)
	}
	if out.DurationMs == nil || *out.DurationMs != 2385 {
		t.Errorf("duration_ms = %v, want 2385", out.DurationMs)
	}
	if out.EventsPersisted != 3 {
		t.Errorf("events_persisted = %d, want 3 (turn.started + turn.completed + usage.observed)", out.EventsPersisted)
	}

	// --- events durably persisted, all joined on the attribution TurnID --
	countEvents := func(query string, args ...any) int {
		t.Helper()
		var n int
		if err := db.Conn().QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
			t.Fatalf("count %q: %v", query, err)
		}
		return n
	}
	if n := countEvents(`SELECT COUNT(*) FROM events WHERE event_type = 'provider.turn.started' AND session_id = ? AND turn_id = ?`, managedRunSession, out.TurnID); n != 1 {
		t.Errorf("turn.started rows = %d, want 1", n)
	}
	if n := countEvents(`SELECT COUNT(*) FROM events WHERE event_type = 'provider.turn.completed' AND session_id = ? AND turn_id = ? AND worktree_id = ?`, managedRunSession, out.TurnID, managedRunWorktree); n != 1 {
		t.Errorf("turn.completed rows (turn+worktree stamped) = %d, want 1", n)
	}
	if n := countEvents(`SELECT COUNT(*) FROM events WHERE event_type = 'provider.usage.observed' AND turn_id = ? AND payload_json LIKE '%total_cost_usd%'`, out.TurnID); n != 1 {
		t.Errorf("turn-stamped usage rows = %d, want 1", n)
	}
	// Issue #11: the persisted usage event carries the per-turn token
	// actuals — total_tokens (input + output) joined to the turn by
	// turn_id, plus the init line's model label for the cohort ladder.
	var usagePayloadJSON string
	if err := db.Conn().QueryRowContext(ctx,
		`SELECT payload_json FROM events WHERE event_type = 'provider.usage.observed' AND turn_id = ?`, out.TurnID,
	).Scan(&usagePayloadJSON); err != nil {
		t.Fatalf("read usage payload: %v", err)
	}
	var usagePayload map[string]any
	if err := json.Unmarshal([]byte(usagePayloadJSON), &usagePayload); err != nil {
		t.Fatalf("decode usage payload: %v", err)
	}
	if usagePayload["total_tokens"] != 2450.0 || usagePayload["input_tokens"] != 2100.0 || usagePayload["output_tokens"] != 350.0 {
		t.Errorf("usage payload = %v, want total/input/output tokens 2450/2100/350 (the fixture result line's token block)", usagePayload)
	}
	if usagePayload["model_id"] != "claude-sonnet-4-5" {
		t.Errorf("usage payload model_id = %v, want claude-sonnet-4-5", usagePayload["model_id"])
	}
	// A real prediction row proves the gate ran the REAL pipeline, not a
	// fake (the same positive-control discipline evaluate_privacy_test
	// uses).
	if n := countEvents(`SELECT COUNT(*) FROM predictions`); n == 0 {
		t.Error("no prediction row persisted — the gate did not run the real evaluation pipeline")
	}
	// Privacy: the raw prompt reaches the provider argv and the gate's
	// hasher, never the database (Constitution §7 rule 2).
	if n := countEvents(`SELECT COUNT(*) FROM events WHERE payload_json LIKE ?`, "%"+managedRunPrompt+"%"); n != 0 {
		t.Errorf("%d event payloads contain the raw prompt", n)
	}

	// --- the provider was spawned argv-only with the exact ADD §22.1 shape
	argvJSON, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("argv file: %v", err)
	}
	var argv []string
	if err := json.Unmarshal(argvJSON, &argv); err != nil {
		t.Fatalf("decoding argv: %v", err)
	}
	want := []string{"-p", managedRunPrompt, "--output-format", "stream-json", "--verbose"}
	if len(argv) != len(want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q", i, argv[i], want[i])
		}
	}

	// --- human surface: the decision line and relayed stream went to
	// stderr, keeping stdout machine-pure.
	if !bytes.Contains(stderr.Bytes(), []byte("auspex run: decision")) {
		t.Errorf("stderr lacks the decision line:\n%s", stderr.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte(`"type":"result"`)) {
		t.Errorf("stderr lacks the relayed stream lines:\n%s", stderr.String())
	}
}

func TestManagedRun_EndToEnd_BlockRefusesToSpawn(t *testing.T) {
	argvFile := filepath.Join(t.TempDir(), "argv.json")
	t.Setenv("AUSPEX_FAKE_ARGV_FILE", argvFile)

	blocker := &fakes.FakeEvaluationService{
		EvaluateTurnFunc: func(_ context.Context, req app.EvaluateTurnRequest) (app.Evaluation, error) {
			return app.Evaluation{ID: "eval-e2e-block", TurnID: req.TurnID}, nil
		},
		DecideFunc: func(_ context.Context, _ app.DecideRequest) (app.DecisionResult, error) {
			return app.DecisionResult{ID: "dec-e2e-block", Action: app.PolicyBlock}, nil
		},
	}
	root, db := buildManagedRunRoot(t, blocker)
	ctx := context.Background()

	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{
		"run",
		"--session-id", managedRunSession,
		"--worktree-id", managedRunWorktree,
		// A path that cannot exist: reaching the spawn would surface as
		// ErrCodeUnavailable, so the unauthorized assertion below also
		// proves no spawn was attempted.
		"--provider-bin", filepath.Join(t.TempDir(), "never-a-binary"),
		"--", managedRunPrompt,
	})

	err := root.Execute()
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnauthorized {
		t.Fatalf("err = %v, want *domain.Error with code %q (the typed BLOCK refusal)", err, domain.ErrCodeUnauthorized)
	}
	if bytes.Contains(stdout.Bytes(), []byte("auspex.run.v1")) {
		t.Errorf("stdout carries attribution JSON on a blocked run:\n%s", stdout.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte(`"schema_version":"auspex.error.v1"`)) {
		t.Errorf("stderr lacks the typed error envelope:\n%s", stderr.String())
	}

	// The provider process never existed: no argv file, and no terminal
	// turn events — only the gate's turn.started.
	if _, statErr := os.Stat(argvFile); !os.IsNotExist(statErr) {
		t.Errorf("argv file exists (stat err %v) — the provider was spawned despite BLOCK", statErr)
	}
	var terminal int
	if err := db.Conn().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE session_id = ? AND event_type IN ('provider.turn.completed', 'provider.turn.failed', 'provider.usage.observed')`,
		managedRunSession,
	).Scan(&terminal); err != nil {
		t.Fatalf("count terminal events: %v", err)
	}
	if terminal != 0 {
		t.Errorf("terminal events = %d, want 0 on a blocked run", terminal)
	}
	var started int
	if err := db.Conn().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE session_id = ? AND event_type = 'provider.turn.started'`,
		managedRunSession,
	).Scan(&started); err != nil {
		t.Fatalf("count started events: %v", err)
	}
	if started != 1 {
		t.Errorf("turn.started events = %d, want 1 (the gate's own record of the blocked attempt)", started)
	}
}

// TestManagedRun_TokenActuals_CohortLadderWakesUp is issue #11's closing
// assertion: ADR-047's cohort fallback ladder
// (evaluation.SQLDataSource.RecentSimilarTurnTokens) shipped dormant —
// documented as "activates for free when a payload carries total_tokens"
// — and managed runs are that payload's first producer. Eight real
// `auspex run` invocations over the real stack (eight, because that is
// the ADD §15.2 sample gate the ladder's rungs answer at) must leave the
// ladder returning a NON-empty per-turn token sample set, mirroring
// datasource_sql_test.go's seeded ladder tests but with samples produced
// by the production capture path end to end instead of hand-inserted
// rows.
func TestManagedRun_TokenActuals_CohortLadderWakesUp(t *testing.T) {
	bin := buildFakeProviderBinary(t)
	t.Setenv("AUSPEX_FAKE_STREAM_FILE", managedStreamFixture(t, "stream_success.jsonl"))

	root, db := buildManagedRunRoot(t, nil)
	ctx := context.Background()

	for i := 0; i < 8; i++ {
		var stdout, stderr bytes.Buffer
		root.SetOut(&stdout)
		root.SetErr(&stderr)
		root.SetArgs([]string{
			"run",
			"--session-id", managedRunSession,
			"--worktree-id", managedRunWorktree,
			"--provider-bin", bin,
			"--", managedRunPrompt,
		})
		if err := root.Execute(); err != nil {
			t.Fatalf("run %d: %v\nstderr: %s", i, err, stderr.String())
		}
	}

	// Eight distinct turns, each with its own turn-stamped total_tokens
	// sample (the turn-scoped idempotency key dedupes within a run, never
	// across runs).
	var turns int
	if err := db.Conn().QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT turn_id) FROM events
			WHERE event_type = 'provider.usage.observed'
			AND turn_id IS NOT NULL AND payload_json LIKE '%"total_tokens":2450%'`,
	).Scan(&turns); err != nil {
		t.Fatalf("count turn-stamped usage events: %v", err)
	}
	if turns != 8 {
		t.Fatalf("turn-stamped total_tokens usage events = %d distinct turns, want 8", turns)
	}

	src := evaluation.NewSQLDataSource(db)
	similar, err := src.RecentSimilarTurnTokens(ctx, domain.SessionID(managedRunSession), features.TaskClassBugfixLocal)
	if err != nil {
		t.Fatalf("RecentSimilarTurnTokens: %v", err)
	}
	// The seeded provider_sessions row carries no model/effort labels, so
	// the identity rungs are honestly skipped and the provider rung
	// answers — with the eight real samples, not the empty slice this
	// method returned on every database before this capture existed.
	if similar.Rung != features.CohortRungProvider {
		t.Errorf("rung = %q, want %q", similar.Rung, features.CohortRungProvider)
	}
	if len(similar.Samples) != 8 {
		t.Fatalf("len(samples) = %d, want 8 — the ladder did not wake up", len(similar.Samples))
	}
	for _, s := range similar.Samples {
		if s != 2450 {
			t.Fatalf("samples = %v: want every sample = 2450 (the fixture's input 2100 + output 350)", similar.Samples)
		}
	}
}
