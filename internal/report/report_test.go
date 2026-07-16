// report_test.go: Engine.GenerateReport against real on-disk seeded
// SQLite databases (never :memory:), the same seeding-by-SQL convention
// internal/retention's engine tests established. Fixtures are built from
// the exact payload key vocabulary the telemetry normalizers persist
// (internal/telemetry/claude/normalizer.go, managedrun.go;
// internal/telemetry/codex/normalizer.go) so the attribution under test
// is the one production data exercises.
package report

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// testNow is the fixed report instant; the default 7d window reaches
// back to testNow-168h. All seeded in-window activity happens on
// 2026-07-15/16 UTC.
var testNow = time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)

type fixedClock struct{ t time.Time }

func (f fixedClock) Now() time.Time { return f.t }

func newTestEngine(t *testing.T) (*Engine, *sqlite.DB) {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "auspex.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return &Engine{DB: db, Clock: fixedClock{t: testNow}, Location: time.UTC}, db
}

func exec(t *testing.T, db *sqlite.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Conn().ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

var eventSeq int

// insertEvent seeds one events row with the columns this package reads;
// occurred_at/observed_at share ts (RFC3339Nano UTC, as the stores write).
func insertEvent(t *testing.T, db *sqlite.DB, eventType string, ts time.Time, provider, sessionID, turnID, payload string) {
	t.Helper()
	eventSeq++
	var turn any
	if turnID != "" {
		turn = turnID
	}
	exec(t, db, `INSERT INTO events
			(event_id, schema_version, event_type, occurred_at, observed_at, source, provider, session_id, turn_id, payload_json)
		VALUES (?, 'auspex.event.v1', ?, ?, ?, 'test', ?, ?, ?, ?)`,
		fmt.Sprintf("ev-%04d", eventSeq), eventType,
		ts.UTC().Format(time.RFC3339Nano), ts.UTC().Format(time.RFC3339Nano),
		provider, sessionID, turn, payload)
}

// insertPrediction seeds the #20 Phase 0 label stamp for one turn.
func insertPrediction(t *testing.T, db *sqlite.DB, turnID, provider, modelID, modelFamily, effort string) {
	t.Helper()
	eventSeq++
	exec(t, db, `INSERT INTO predictions
			(id, turn_id, predictor_id, predictor_version, feature_set_version,
			 quota_risk_score, context_risk_score, completion_risk_score,
			 blast_radius_risk_score, overall_risk_score, confidence, calibrated,
			 reason_codes_json, created_at, provider, model_id, model_family, effort)
		VALUES (?, ?, 'test', 'v1', 'v1', 0, 0, 0, 0, 0, 'low', 0, '[]', ?, ?, ?, ?, ?)`,
		fmt.Sprintf("pred-%04d", eventSeq), turnID,
		testNow.Format(time.RFC3339Nano), provider, modelID, modelFamily, effort)
}

func insertFeatureVector(t *testing.T, db *sqlite.DB, turnID, taskClass string) {
	t.Helper()
	exec(t, db, `INSERT INTO feature_vectors (turn_id, feature_set_version, features_json, created_at)
		VALUES (?, 'v1', ?, ?)`,
		turnID, fmt.Sprintf(`{"task_class":%q}`, taskClass), testNow.Format(time.RFC3339Nano))
}

// seedManagedTurn seeds one managed-run style turn: a terminal
// turn.completed plus the turn-stamped per-turn usage event
// (internal/telemetry/claude/managedrun.go's event pair).
func seedManagedTurn(t *testing.T, db *sqlite.DB, ts time.Time, provider, sessionID, turnID string, costUSD float64) {
	t.Helper()
	insertEvent(t, db, "provider.turn.completed", ts, provider, sessionID, turnID, `{"result_subtype":"success"}`)
	insertEvent(t, db, "provider.usage.observed", ts.Add(time.Second), provider, sessionID, turnID,
		fmt.Sprintf(`{"total_cost_usd":%g,"total_duration_ms":30000,"total_api_duration_ms":20000,"input_tokens":50,"output_tokens":60}`, costUSD))
}

func day(hour, minute int) time.Time {
	return time.Date(2026, 7, 16, hour, minute, 0, 0, time.UTC)
}

// seedPopulatedDB builds the all-six-sections fixture:
//
//   - sess-a: a native statusline session — cumulative usage samples
//     bracketing two closed turns (cost deltas 0.60 and 0.30, API-time
//     deltas 70s and 20s) plus one unclosed turn; per-turn tokens on the
//     completed events; opus/xhigh predictions + "question" task class.
//   - sess-b: one managed run (turn-stamped usage: exact $1.25).
//   - sess-c: one codex turn with reasoning tokens.
//   - quota: two five_hour samples (max 77%), one seven_day (8%), one
//     rate_limit.hit.
//   - sess-old: a fully-formed turn 10 days ago — outside the window.
func seedPopulatedDB(t *testing.T, db *sqlite.DB) {
	t.Helper()
	// sess-a cumulative statusline series (no turn_id).
	usage := func(ts time.Time, cost float64, apiMs int64) {
		insertEvent(t, db, "provider.usage.observed", ts, "claude", "sess-a", "",
			fmt.Sprintf(`{"total_cost_usd":%g,"total_api_duration_ms":%d,"model_id":"claude-opus-4-8[1m]","effort":"xhigh"}`, cost, apiMs))
	}
	usage(day(10, 0), 0, 0)
	insertEvent(t, db, "provider.turn.started", day(10, 1), "claude", "sess-a", "turn-a1", `{}`)
	usage(day(10, 1).Add(30*time.Second), 0.50, 60_000)
	insertEvent(t, db, "provider.turn.completed", day(10, 2), "claude", "sess-a", "turn-a1",
		`{"input_tokens":100,"output_tokens":200,"cache_read_input_tokens":5000,"cache_creation_input_tokens":400,"model_id":"claude-opus-4-8[1m]","effort":"xhigh"}`)
	usage(day(10, 2).Add(30*time.Second), 0.60, 70_000) // lags the turn it measures
	insertEvent(t, db, "provider.turn.started", day(10, 3), "claude", "sess-a", "turn-a2", `{}`)
	usage(day(10, 3).Add(30*time.Second), 0.90, 90_000)
	insertEvent(t, db, "provider.turn.completed", day(10, 4), "claude", "sess-a", "turn-a2",
		`{"input_tokens":150,"output_tokens":250,"cache_read_input_tokens":7000,"cache_creation_input_tokens":600,"model_id":"claude-opus-4-8[1m]","effort":"xhigh"}`)
	// Unclosed: started, never terminated.
	insertEvent(t, db, "provider.turn.started", day(10, 30), "claude", "sess-a", "turn-a3", `{}`)

	for _, turnID := range []string{"turn-a1", "turn-a2"} {
		insertPrediction(t, db, turnID, "claude", "claude-opus-4-8[1m]", "opus", "xhigh")
		insertFeatureVector(t, db, turnID, "question")
	}

	// sess-b: managed run.
	seedManagedTurn(t, db, day(11, 0), "claude", "sess-b", "turn-b1", 1.25)

	// sess-c: codex turn with reasoning tokens (fresh-input vocabulary,
	// internal/telemetry/codex/normalizer.go).
	insertEvent(t, db, "provider.turn.completed", day(11, 30), "codex", "sess-c", "turn-c1",
		`{"input_tokens":30,"output_tokens":90,"cache_read_input_tokens":100,"reasoning_output_tokens":40,"model_id":"gpt-5.6-sol"}`)

	// Quota + rate limit.
	insertEvent(t, db, "provider.quota.observed", day(11, 40), "claude", "sess-a", "", `{"limit_id":"five_hour","used_percent":42}`)
	insertEvent(t, db, "provider.quota.observed", day(11, 50), "claude", "sess-a", "", `{"limit_id":"five_hour","used_percent":77}`)
	insertEvent(t, db, "provider.quota.observed", day(11, 55), "claude", "sess-a", "", `{"limit_id":"seven_day","used_percent":8}`)
	insertEvent(t, db, "provider.rate_limit.hit", day(11, 58), "claude", "sess-a", "", `{"failure_class":"provider_rate_limit"}`)

	// sess-old: complete turn with attributable cost, 10 days ago —
	// outside every default-window assertion below.
	old := testNow.AddDate(0, 0, -10)
	insertEvent(t, db, "provider.usage.observed", old, "claude", "sess-old", "", `{"total_cost_usd":0}`)
	insertEvent(t, db, "provider.turn.started", old.Add(time.Minute), "claude", "sess-old", "turn-old", `{}`)
	insertEvent(t, db, "provider.usage.observed", old.Add(90*time.Second), "claude", "sess-old", "", `{"total_cost_usd":9.99}`)
	insertEvent(t, db, "provider.turn.completed", old.Add(2*time.Minute), "claude", "sess-old", "turn-old", `{}`)
}

func TestGenerateReport_PopulatedAllSections(t *testing.T) {
	engine, db := newTestEngine(t)
	seedPopulatedDB(t, db)

	rep, err := engine.GenerateReport(context.Background(), DefaultWindow)
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}

	if rep.SchemaVersion != ReportSchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", rep.SchemaVersion, ReportSchemaVersion)
	}

	// Section 1: totals. 5 in-window turns (a1, a2, a3-unclosed, b1, c1);
	// sess-old's turn is outside the window.
	tot := rep.Totals
	if tot.Turns != 5 || tot.TurnsCompleted != 4 || tot.TurnsUnclosed != 1 {
		t.Errorf("turns = %d (completed %d, unclosed %d), want 5/4/1", tot.Turns, tot.TurnsCompleted, tot.TurnsUnclosed)
	}
	if tot.Sessions != 3 {
		t.Errorf("Sessions = %d, want 3", tot.Sessions)
	}
	if tot.ActiveDays != 1 {
		t.Errorf("ActiveDays = %d, want 1 (all in-window activity is on 2026-07-16 UTC)", tot.ActiveDays)
	}
	// Cost: 0.60 + 0.30 statusline deltas + 1.25 managed = 2.15 over 3 turns.
	if tot.CostUSD == nil || !almostEqual(*tot.CostUSD, 2.15) {
		t.Errorf("CostUSD = %v, want 2.15", tot.CostUSD)
	}
	if tot.CostAttributedTurns != 3 {
		t.Errorf("CostAttributedTurns = %d, want 3", tot.CostAttributedTurns)
	}
	// API time: 70s + 20s statusline deltas + 20s managed = 110s.
	if tot.APIDurationMs == nil || *tot.APIDurationMs != 110_000 {
		t.Errorf("APIDurationMs = %v, want 110000", tot.APIDurationMs)
	}
	// Tokens: fresh 100+150+50+30, cache read 5000+7000+100, creation
	// 400+600, output 200+250+60+90, reasoning 40 (codex only).
	wantTokens := map[string]int64{"fresh": 330, "read": 12100, "create": 1000, "out": 600, "reason": 40}
	if got := derefInt(tot.Tokens.FreshInput); got != wantTokens["fresh"] {
		t.Errorf("FreshInput = %d, want %d", got, wantTokens["fresh"])
	}
	if got := derefInt(tot.Tokens.CacheRead); got != wantTokens["read"] {
		t.Errorf("CacheRead = %d, want %d", got, wantTokens["read"])
	}
	if got := derefInt(tot.Tokens.CacheCreation); got != wantTokens["create"] {
		t.Errorf("CacheCreation = %d, want %d", got, wantTokens["create"])
	}
	if got := derefInt(tot.Tokens.Output); got != wantTokens["out"] {
		t.Errorf("Output = %d, want %d", got, wantTokens["out"])
	}
	if got := derefInt(tot.Tokens.Reasoning); got != wantTokens["reason"] {
		t.Errorf("Reasoning = %d, want %d", got, wantTokens["reason"])
	}
	if tot.TokenReportingTurns != 4 {
		t.Errorf("TokenReportingTurns = %d, want 4", tot.TokenReportingTurns)
	}

	// Section 2: model mix. Highest-cost row first: unlabeled (managed
	// $1.25) then opus/xhigh ($0.90). turn-a3 (no prediction row, no
	// payload identity) must land in the unlabeled row, not in opus.
	if len(rep.ModelMix) != 3 {
		t.Fatalf("ModelMix = %+v, want 3 rows", rep.ModelMix)
	}
	mix := map[string]ModelMixRow{}
	for _, r := range rep.ModelMix {
		mix[r.Provider+"/"+r.Model+"/"+r.Effort] = r
	}
	opus, ok := mix["claude/opus/xhigh"]
	if !ok || opus.Turns != 2 {
		t.Errorf("claude/opus/xhigh row = %+v, want 2 turns", opus)
	}
	if opus.CostUSD == nil || !almostEqual(*opus.CostUSD, 0.90) {
		t.Errorf("opus/xhigh cost = %v, want 0.90", opus.CostUSD)
	}
	if unl, ok := mix["claude/unlabeled/unlabeled"]; !ok || unl.Turns != 2 || unl.CostUSD == nil || !almostEqual(*unl.CostUSD, 1.25) {
		t.Errorf("claude/unlabeled row = %+v, want 2 turns at $1.25", unl)
	}
	// The codex model id is not in the claude-centric price table; it
	// must keep its own name, never be mislabeled "default".
	if cx, ok := mix["codex/gpt-5.6-sol/unlabeled"]; !ok || cx.Turns != 1 {
		t.Errorf("codex row = %+v (rows %+v), want gpt-5.6-sol with 1 turn", cx, rep.ModelMix)
	}

	// Section 3: right-sizing — only 2 attributed "question" turns, far
	// below MinCohortTurns, so the honest thin-data note must appear.
	if rep.RightSizing.Note == "" || len(rep.RightSizing.TaskClasses) != 0 {
		t.Errorf("RightSizing = %+v, want empty cohorts with a not-enough-data note", rep.RightSizing)
	}
	if !strings.Contains(rep.RightSizing.Note, ">=8") {
		t.Errorf("RightSizing.Note = %q, want the >=8 threshold named", rep.RightSizing.Note)
	}

	// Section 4: cache hygiene ratio = 12100/330.
	if rep.CacheHygiene.CacheReadPerFreshInput == nil ||
		!almostEqual(*rep.CacheHygiene.CacheReadPerFreshInput, 12100.0/330.0) {
		t.Errorf("CacheReadPerFreshInput = %v, want %v", rep.CacheHygiene.CacheReadPerFreshInput, 12100.0/330.0)
	}
	if rep.CacheHygiene.FlaggedSessions != 0 {
		t.Errorf("FlaggedSessions = %d, want 0 (all seeded sessions are below the churn threshold)", rep.CacheHygiene.FlaggedSessions)
	}

	// Section 5: quota.
	if rep.Quota.RateLimitHits != 1 {
		t.Errorf("RateLimitHits = %d, want 1", rep.Quota.RateLimitHits)
	}
	approaches := map[string]QuotaApproach{}
	for _, a := range rep.Quota.ClosestApproach {
		approaches[a.Provider+"/"+a.LimitID] = a
	}
	if a, ok := approaches["claude/five_hour"]; !ok || a.MaxUsedPercent != 77 || a.Samples != 2 {
		t.Errorf("five_hour approach = %+v, want max 77%% over 2 samples", a)
	}
	if a, ok := approaches["claude/seven_day"]; !ok || a.MaxUsedPercent != 8 {
		t.Errorf("seven_day approach = %+v, want max 8%%", a)
	}

	// Section 6: top turns, costliest first, ids only.
	if len(rep.TopTurns) != 3 {
		t.Fatalf("TopTurns = %d entries, want 3", len(rep.TopTurns))
	}
	if rep.TopTurns[0].TurnID != "turn-b1" || !almostEqual(rep.TopTurns[0].CostUSD, 1.25) {
		t.Errorf("TopTurns[0] = %+v, want turn-b1 at $1.25", rep.TopTurns[0])
	}
	if rep.TopTurns[1].TurnID != "turn-a1" || rep.TopTurns[2].TurnID != "turn-a2" {
		t.Errorf("TopTurns order = %s, %s, want turn-a1 then turn-a2",
			rep.TopTurns[1].TurnID, rep.TopTurns[2].TurnID)
	}

	// The rendered text must not fabricate data for the old session.
	text := RenderText(rep)
	if strings.Contains(text, "sess-old") {
		t.Error("rendered text mentions the out-of-window session")
	}
}

func TestGenerateReport_EmptyDB_HonestEmpties(t *testing.T) {
	engine, _ := newTestEngine(t)

	rep, err := engine.GenerateReport(context.Background(), DefaultWindow)
	if err != nil {
		t.Fatalf("GenerateReport on empty DB: %v", err)
	}

	if rep.Totals.Turns != 0 || rep.Totals.Sessions != 0 || rep.Totals.ActiveDays != 0 {
		t.Errorf("Totals = %+v, want all-zero counts", rep.Totals)
	}
	if rep.Totals.CostUSD != nil {
		t.Errorf("CostUSD = %v, want nil (unknown is not zero)", *rep.Totals.CostUSD)
	}
	if rep.Totals.Tokens.FreshInput != nil || rep.Totals.Tokens.Output != nil {
		t.Error("token totals must stay nil on an empty DB")
	}
	if len(rep.ModelMix) != 0 || len(rep.TopTurns) != 0 {
		t.Error("ModelMix/TopTurns must be empty on an empty DB")
	}
	if rep.RightSizing.Note == "" {
		t.Error("RightSizing.Note must state not-enough-data on an empty DB")
	}
	if rep.Quota.RateLimitHits != 0 || len(rep.Quota.ClosestApproach) != 0 {
		t.Errorf("Quota = %+v, want empty", rep.Quota)
	}

	// The text rendering says so explicitly, section by section.
	text := RenderText(rep)
	for _, want := range []string{
		"no turns observed in this window",
		"not enough data yet",
		"no quota observations in window",
		"no cost-attributed turns in this window",
		"no session reported cache-creation tokens in window",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered empty report missing %q:\n%s", want, text)
		}
	}
}

// TestGenerateReport_RightSizingCohorts seeds two >=8-turn cohorts on one
// task class (plus one 7-turn cohort that must be suppressed) and checks
// the side-by-side medians.
func TestGenerateReport_RightSizingCohorts(t *testing.T) {
	engine, db := newTestEngine(t)

	seedCohort := func(session, turnPrefix, family, effort string, costs []float64) {
		for i, cost := range costs {
			turnID := fmt.Sprintf("%s-%02d", turnPrefix, i)
			seedManagedTurn(t, db, day(9, 0).Add(time.Duration(i)*time.Minute), "claude", session, turnID, cost)
			insertPrediction(t, db, turnID, "claude", "model-"+family, family, effort)
			insertFeatureVector(t, db, turnID, "migration")
		}
	}
	// 8 opus turns, median (0.4+0.5)/2 = 0.45.
	seedCohort("sess-opus", "turn-op", "opus", "xhigh", []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8})
	// 9 haiku turns, median 0.05.
	seedCohort("sess-haiku", "turn-hk", "haiku", "low", []float64{0.01, 0.02, 0.03, 0.04, 0.05, 0.06, 0.07, 0.08, 0.09})
	// 7 fable turns: one short of MinCohortTurns — must be suppressed.
	seedCohort("sess-fable", "turn-fb", "fable", "xhigh", []float64{1, 1, 1, 1, 1, 1, 1})

	rep, err := engine.GenerateReport(context.Background(), DefaultWindow)
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}

	if rep.RightSizing.Note != "" {
		t.Fatalf("RightSizing.Note = %q, want no note (qualifying cohorts exist)", rep.RightSizing.Note)
	}
	if len(rep.RightSizing.TaskClasses) != 1 || rep.RightSizing.TaskClasses[0].TaskClass != "migration" {
		t.Fatalf("TaskClasses = %+v, want exactly [migration]", rep.RightSizing.TaskClasses)
	}
	cohorts := rep.RightSizing.TaskClasses[0].Cohorts
	if len(cohorts) != 2 {
		t.Fatalf("cohorts = %+v, want 2 (fable's 7-turn cohort suppressed)", cohorts)
	}
	byModel := map[string]CohortStat{}
	for _, c := range cohorts {
		byModel[c.Model] = c
	}
	if c := byModel["opus"]; c.Turns != 8 || !almostEqual(c.MedianCostUSD, 0.45) {
		t.Errorf("opus cohort = %+v, want n=8 median 0.45", c)
	}
	if c := byModel["haiku"]; c.Turns != 9 || !almostEqual(c.MedianCostUSD, 0.05) {
		t.Errorf("haiku cohort = %+v, want n=9 median 0.05", c)
	}
	if _, ok := byModel["fable"]; ok {
		t.Error("fable cohort (n=7) must be suppressed below MinCohortTurns")
	}

	// Rendered side by side, phrased with n= counts.
	text := RenderText(rep)
	if !strings.Contains(text, "migration-class turns:") || !strings.Contains(text, " vs ") {
		t.Errorf("rendered right-sizing lacks a side-by-side comparison:\n%s", text)
	}
	if !strings.Contains(text, "(n=8)") || !strings.Contains(text, "(n=9)") {
		t.Errorf("rendered right-sizing lacks cohort sizes:\n%s", text)
	}
}

// TestGenerateReport_CacheChurnFlagging seeds one thrashing session
// (mean 150k cache-creation tokens/turn over 3 turns), one healthy
// session, and one heavy-but-short session (2 turns — below the minimum
// reporting turns), asserting exactly the first is flagged.
func TestGenerateReport_CacheChurnFlagging(t *testing.T) {
	engine, db := newTestEngine(t)

	seedChurnTurn := func(session, turnID string, ts time.Time, cacheCreation int64) {
		insertEvent(t, db, "provider.turn.completed", ts, "claude", session, turnID,
			fmt.Sprintf(`{"input_tokens":10,"output_tokens":10,"cache_creation_input_tokens":%d}`, cacheCreation))
	}
	for i := range 3 {
		seedChurnTurn("sess-thrash", fmt.Sprintf("turn-t%d", i), day(8, i), 150_000)
	}
	for i := range 4 {
		seedChurnTurn("sess-healthy", fmt.Sprintf("turn-h%d", i), day(8, 10+i), 2_000)
	}
	for i := range 2 {
		seedChurnTurn("sess-short", fmt.Sprintf("turn-s%d", i), day(8, 20+i), 500_000)
	}

	rep, err := engine.GenerateReport(context.Background(), DefaultWindow)
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}

	h := rep.CacheHygiene
	if h.TokenReportingSessions != 3 {
		t.Errorf("TokenReportingSessions = %d, want 3", h.TokenReportingSessions)
	}
	if h.FlaggedSessions != 1 {
		t.Errorf("FlaggedSessions = %d, want 1", h.FlaggedSessions)
	}
	flags := map[string]bool{}
	for _, s := range h.Sessions {
		flags[s.SessionID] = s.Flagged
	}
	if !flags["sess-thrash"] {
		t.Error("sess-thrash (150k/turn x3) must be flagged")
	}
	if flags["sess-healthy"] {
		t.Error("sess-healthy (2k/turn) must not be flagged")
	}
	if flags["sess-short"] {
		t.Error("sess-short (2 reporting turns) must not be flagged below the minimum turn count")
	}

	text := RenderText(rep)
	if !strings.Contains(text, "FLAG session sess-thrash") {
		t.Errorf("rendered cache hygiene lacks the thrash flag:\n%s", text)
	}
	if strings.Contains(text, "FLAG session sess-healthy") {
		t.Errorf("rendered cache hygiene flags a healthy session:\n%s", text)
	}
}

// TestGenerateReport_NoBaselineNoDelta pins the unknown-is-not-zero rule:
// a closed turn whose session series has no usage sample at or before the
// turn start (resumed session / truncated head) derives NO cost — never a
// delta against an assumed 0 baseline.
func TestGenerateReport_NoBaselineNoDelta(t *testing.T) {
	engine, db := newTestEngine(t)

	insertEvent(t, db, "provider.turn.started", day(9, 0), "claude", "sess-r", "turn-r1", `{}`)
	// Only IN-window samples exist; the pre-turn baseline is missing.
	insertEvent(t, db, "provider.usage.observed", day(9, 1), "claude", "sess-r", "", `{"total_cost_usd":123.45}`)
	insertEvent(t, db, "provider.turn.completed", day(9, 2), "claude", "sess-r", "turn-r1", `{}`)

	rep, err := engine.GenerateReport(context.Background(), DefaultWindow)
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}
	if rep.Totals.Turns != 1 || rep.Totals.TurnsCompleted != 1 {
		t.Fatalf("Totals = %+v, want the one completed turn counted", rep.Totals)
	}
	if rep.Totals.CostUSD != nil {
		t.Errorf("CostUSD = %v, want nil — no pre-turn baseline sample, no delta", *rep.Totals.CostUSD)
	}
	if len(rep.TopTurns) != 0 {
		t.Errorf("TopTurns = %+v, want empty (no attributable cost)", rep.TopTurns)
	}
}

// TestGenerateReport_JSONShape locks the --json wire contract: schema
// version present, and a round-trip through encoding/json preserves the
// section keys.
func TestGenerateReport_JSONShape(t *testing.T) {
	engine, db := newTestEngine(t)
	seedPopulatedDB(t, db)

	rep, err := engine.GenerateReport(context.Background(), DefaultWindow)
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}
	body, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["schema_version"] != "auspex.report.v1" {
		t.Errorf("schema_version = %v, want auspex.report.v1", decoded["schema_version"])
	}
	for _, key := range []string{"totals", "model_mix", "right_sizing", "cache_hygiene", "quota", "top_turns", "window_from", "window_to", "window_label", "generated_at"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("JSON output missing key %q", key)
		}
	}
}

func almostEqual(a, b float64) bool {
	d := a - b
	return d < 1e-9 && d > -1e-9
}

func derefInt(v *int64) int64 {
	if v == nil {
		return -1
	}
	return *v
}
