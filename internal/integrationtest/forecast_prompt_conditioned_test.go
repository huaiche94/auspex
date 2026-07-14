// forecast_prompt_conditioned_test.go: issue #42's acceptance proof that
// the token forecast now responds to the prompt. Before #42 the cold-start
// forecast was a constant (P50 3210 for essentially every prompt) because
// the persisted provider.turn.started payload carried only
// hash/length/approx-tokens, so evaluation read-back reconstructed a
// PromptFeatures whose every verb/domain boolean was false and the
// classifier collapsed to TaskClassUnknown. This test drives the REAL
// production path twice — orchestrator.EvaluatePrompt (the exact
// normalize -> persist -> EvaluateTurn -> Decide path the UserPromptSubmit
// hook shares) over the real evaluation.Service + SQLDataSource + real
// predictor stages against a real migrated SQLite DB — with the issue's
// own two acceptance prompts, and asserts the forecast card's P50 now
// DIFFERS between them, in the direction the ADD §14.6 table's own
// designed per-class multipliers imply (bugfix-local 1.0 < refactor-local
// 1.7, internal/predictor/token/coldstart.go). It asserts inequality and
// direction only — never exact token values, which are bootstrap
// constants, not calibrated measurements.
package integrationtest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/evaluation"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/policy"
	"github.com/huaiche94/auspex/internal/predictor/quota"
	"github.com/huaiche94/auspex/internal/predictor/risk"
	"github.com/huaiche94/auspex/internal/predictor/scope"
	"github.com/huaiche94/auspex/internal/predictor/token"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"
)

// buildPromptConditionedStack assembles the same production-shaped stack
// buildEvaluateCLIRoot wires (real Service, SQLDataSource, predictor
// stages, EventStore), but seeded with one provider_sessions row per given
// session ID and returning the HookDeps directly — this test calls
// orchestrator.EvaluatePrompt itself rather than going through cobra,
// because its assertions are on the returned forecast card, not CLI
// rendering.
func buildPromptConditionedStack(t *testing.T, sessionIDs ...string) (orchestrator.HookDeps, *sqlite.DB) {
	t.Helper()

	ctx := context.Background()
	db, err := sqlite.Open(ctx, t.TempDir()+"/forecast-prompt-conditioned.db")
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

	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	seed := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at) VALUES (?, ?, ?, ?, ?)`,
			[]any{"repo-1", "/tmp/repo", "/tmp/repo/.git", now, now}},
		{`INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at) VALUES (?, ?, ?, ?, ?, ?)`,
			[]any{"wt-1", "repo-1", "/tmp/repo", "/tmp/repo/.git", now, now}},
	}
	for _, sid := range sessionIDs {
		seed = append(seed, struct {
			q    string
			args []any
		}{`INSERT INTO provider_sessions (id, worktree_id, provider, invocation_mode, started_at) VALUES (?, ?, ?, ?, ?)`,
			[]any{sid, "wt-1", "claude", "hook", now}})
	}
	for _, s := range seed {
		if _, err := db.Conn().ExecContext(ctx, s.q, s.args...); err != nil {
			t.Fatalf("seed %q: %v", s.q, err)
		}
	}

	clock := fixedClock{t: time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)}
	ids := &seqIDs{}
	dataSource := evaluation.NewSQLDataSource(db)
	svc := evaluation.New(
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

	return orchestrator.HookDeps{
		Clock:      clock,
		IDs:        ids,
		Persister:  claudetelemetry.NewEventStore(db),
		TxRunner:   db,
		Evaluation: svc,
		Forecast:   svc,
	}, db
}

// TestForecastTokens_P50RespondsToPromptClass is the issue-#42 acceptance
// test: the two example prompts of clearly different scope must produce
// different P50s through the real extract -> persist -> classify ->
// forecast path, with the refactor prompt forecast above the local-docs
// fix (its ADD §14.6 relative multiplier and scope bands are strictly
// larger).
func TestForecastTokens_P50RespondsToPromptClass(t *testing.T) {
	deps, db := buildPromptConditionedStack(t, "sess-fix", "sess-refactor")
	ctx := context.Background()

	evalCard := func(sessionID, prompt string) (orchestrator.EvaluatePromptResult, int64) {
		t.Helper()
		res, err := orchestrator.EvaluatePrompt(ctx, deps, orchestrator.EvaluatePromptRequest{
			SessionID: domain.SessionID(sessionID),
			Prompt:    prompt,
		})
		if err != nil {
			t.Fatalf("EvaluatePrompt(%q): %v", prompt, err)
		}
		if res.Card == nil {
			t.Fatalf("EvaluatePrompt(%q): Card = nil, want a forecast card", prompt)
		}
		if res.Card.TokensP50 == nil {
			t.Fatalf("EvaluatePrompt(%q): TokensP50 = nil, want a value", prompt)
		}
		return res, *res.Card.TokensP50
	}

	resFix, p50Fix := evalCard("sess-fix", "fix typo in README")
	resRefactor, p50Refactor := evalCard("sess-refactor", "refactor the policy engine")

	// Inequality and direction only — the values themselves are ADD §14.6
	// bootstrap constants, not calibrated measurements, so pinning them
	// would freeze a number this test has no business freezing.
	if p50Fix == p50Refactor {
		t.Fatalf("P50 identical for prompts of clearly different scope (%d) — the forecast is still prompt-blind (issue #42)", p50Fix)
	}
	if p50Fix >= p50Refactor {
		t.Fatalf("P50(fix typo in README) = %d >= P50(refactor the policy engine) = %d, want the refactor forecast strictly larger (ADD §14.6: bugfix-local 1.0 < refactor-local 1.7)", p50Fix, p50Refactor)
	}

	// The difference must come from real classification, not an accident:
	// the persisted feature snapshot carries the task class each pipeline
	// run actually used (feature_vectors.features_json's task_class).
	assertPersistedTaskClass(t, db, string(resFix.Evaluation.TurnID), "bugfix-local")
	assertPersistedTaskClass(t, db, string(resRefactor.Evaluation.TurnID), "refactor-local")
}

func assertPersistedTaskClass(t *testing.T, db *sqlite.DB, turnID, wantClass string) {
	t.Helper()
	var featuresJSON string
	if err := db.Conn().QueryRowContext(context.Background(),
		`SELECT features_json FROM feature_vectors WHERE turn_id = ?`, turnID,
	).Scan(&featuresJSON); err != nil {
		t.Fatalf("query feature_vectors for turn %s: %v", turnID, err)
	}
	if !strings.Contains(featuresJSON, `"task_class":"`+wantClass+`"`) {
		t.Errorf("feature_vectors.features_json for turn %s does not carry task_class %q: %s", turnID, wantClass, featuresJSON)
	}
}
