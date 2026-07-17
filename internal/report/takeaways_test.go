// takeaways_test.go: the actionable layer (issue #100). Two kinds of
// coverage — (1) buildTakeaways over synthetic sections/turns, exercising
// every firing and non-firing branch of each case without a DB; (2) one
// end-to-end GenerateReport seed proving file-op payload keys decode into
// the agent-thrash case. Every non-fired case must still carry its
// lesson/action and an honest analysis (never a fabricated signal).
package report

import (
	"context"
	"strings"
	"testing"
	"time"
)

func i64p(v int64) *int64     { return &v }
func f64p(v float64) *float64 { return &v }

// takeawayByCase indexes a []Takeaway for assertions.
func takeawayByCase(ts []Takeaway) map[TakeawayCase]Takeaway {
	out := map[TakeawayCase]Takeaway{}
	for _, t := range ts {
		out[t.Case] = t
	}
	return out
}

func TestBuildTakeaways_AllFiveAlwaysPresentWithLessonAndAction(t *testing.T) {
	// Empty everything: no turns, no sections. All five cases must still
	// appear, none fired, each with a non-empty analysis/lesson/action.
	got := buildTakeaways(nil, turnLabels{}, Report{RightSizing: RightSizing{Note: "not enough data yet"}})

	wantOrder := []TakeawayCase{
		CaseExpensiveTurns, CaseModelRightSizing, CaseSessionCacheChurn,
		CaseQuotaPressure, CaseAgentThrash,
	}
	if len(got) != len(wantOrder) {
		t.Fatalf("got %d takeaways, want %d", len(got), len(wantOrder))
	}
	for i, c := range wantOrder {
		if got[i].Case != c {
			t.Errorf("takeaway[%d].Case = %q, want %q (order must be fixed)", i, got[i].Case, c)
		}
	}
	for _, tk := range got {
		if tk.Fired {
			t.Errorf("%s: fired on empty input, want dormant", tk.Case)
		}
		if tk.Title == "" || tk.Analysis == "" || tk.Lesson == "" || tk.Action == "" {
			t.Errorf("%s: empty field — every case needs title/analysis/lesson/action even when dormant: %+v", tk.Case, tk)
		}
		if len(tk.Evidence) != 0 {
			t.Errorf("%s: non-fired case must carry no evidence, got %v", tk.Case, tk.Evidence)
		}
	}
}

func TestTakeaway_ExpensiveTurns_FiresAndGroundsInTopTurn(t *testing.T) {
	turns := []turnRecord{
		{sessionID: "s1", turnID: "t1", costUSD: f64p(43.94)},
		{sessionID: "s1", turnID: "t2", costUSD: f64p(2.0)},
	}
	rep := Report{
		Totals:   Totals{CostUSD: f64p(45.94), CostAttributedTurns: 2},
		TopTurns: []TopTurn{{SessionID: "s1", TurnID: "t1", Model: "opus", Effort: "xhigh", CostUSD: 43.94}},
	}
	tk := takeawayByCase(buildTakeaways(turns, turnLabels{}, rep))[CaseExpensiveTurns]
	if !tk.Fired {
		t.Fatalf("expensive_turns did not fire with a cost-attributed top turn: %+v", tk)
	}
	if !strings.Contains(tk.Analysis, "$43.94") {
		t.Errorf("analysis should name the costliest turn: %q", tk.Analysis)
	}
	if !strings.Contains(tk.Analysis, "median") {
		t.Errorf("analysis should relate the top turn to the median: %q", tk.Analysis)
	}
	if len(tk.Evidence) == 0 || !strings.Contains(strings.Join(tk.Evidence, " "), "t1") {
		t.Errorf("evidence should reference the top turn: %v", tk.Evidence)
	}
}

func TestTakeaway_ExpensiveTurns_DormantWithoutCost(t *testing.T) {
	tk := takeawayByCase(buildTakeaways(nil, turnLabels{}, Report{}))[CaseExpensiveTurns]
	if tk.Fired {
		t.Errorf("expensive_turns fired with no cost-attributed turn")
	}
	if !strings.Contains(tk.Analysis, "unknown") {
		t.Errorf("analysis must say cost is unknown (not $0): %q", tk.Analysis)
	}
}

func TestTakeaway_ModelRightSizing_FiresOnTwoCohorts(t *testing.T) {
	rs := RightSizing{
		MinCohortTurns: MinCohortTurns,
		TaskClasses: []TaskClassComparison{{
			TaskClass: "question",
			Cohorts: []CohortStat{
				{Model: "opus", Effort: "xhigh", Turns: 40, MedianCostUSD: 3.49},
				{Model: "fable", Effort: "xhigh", Turns: 77, MedianCostUSD: 1.97},
			},
		}},
	}
	tk := takeawayByCase(buildTakeaways(nil, turnLabels{}, Report{RightSizing: rs}))[CaseModelRightSizing]
	if !tk.Fired {
		t.Fatalf("model_right_sizing did not fire with two cohorts: %+v", tk)
	}
	for _, want := range []string{"question", "opus", "fable", "$3.49", "$1.97"} {
		if !strings.Contains(tk.Analysis, want) {
			t.Errorf("analysis missing %q: %q", want, tk.Analysis)
		}
	}
}

func TestTakeaway_ModelRightSizing_DormantUsesNote(t *testing.T) {
	rs := RightSizing{Note: "not enough data yet (need >=8 cost-attributed turns per cohort)"}
	tk := takeawayByCase(buildTakeaways(nil, turnLabels{}, Report{RightSizing: rs}))[CaseModelRightSizing]
	if tk.Fired {
		t.Errorf("model_right_sizing fired with only a note")
	}
	if !strings.Contains(tk.Analysis, "not enough data yet") {
		t.Errorf("analysis should carry the right-sizing note: %q", tk.Analysis)
	}
}

func TestTakeaway_SessionCacheChurn_FiresOnFlaggedSession(t *testing.T) {
	h := CacheHygiene{
		FlagMeanTokensPerTurn:  CacheChurnMeanTokensPerTurn,
		FlagMinReportingTurns:  CacheChurnMinTurns,
		FlaggedSessions:        1,
		TokenReportingSessions: 2,
		Sessions: []SessionChurn{
			{SessionID: "sess-hot", ReportingTurns: 5, CacheCreationTokens: 650_000, MeanTokensPerTurn: 130_000, Flagged: true},
			{SessionID: "sess-cool", ReportingTurns: 4, CacheCreationTokens: 40_000, MeanTokensPerTurn: 10_000, Flagged: false},
		},
	}
	tk := takeawayByCase(buildTakeaways(nil, turnLabels{}, Report{CacheHygiene: h}))[CaseSessionCacheChurn]
	if !tk.Fired {
		t.Fatalf("session_cache_churn did not fire with a flagged session: %+v", tk)
	}
	if !strings.Contains(tk.Analysis, "130,000") {
		t.Errorf("analysis should name the worst session's mean: %q", tk.Analysis)
	}
	if len(tk.Evidence) != 1 || !strings.Contains(tk.Evidence[0], "sess-hot") {
		t.Errorf("evidence should list only the flagged session: %v", tk.Evidence)
	}
}

func TestTakeaway_SessionCacheChurn_DormantWhenClear(t *testing.T) {
	h := CacheHygiene{
		FlagMeanTokensPerTurn:  CacheChurnMeanTokensPerTurn,
		TokenReportingSessions: 2,
		FlaggedSessions:        0,
	}
	tk := takeawayByCase(buildTakeaways(nil, turnLabels{}, Report{CacheHygiene: h}))[CaseSessionCacheChurn]
	if tk.Fired {
		t.Errorf("session_cache_churn fired with no flagged session")
	}
	if !strings.Contains(tk.Analysis, "healthy") {
		t.Errorf("clear analysis should read as healthy: %q", tk.Analysis)
	}
}

func TestTakeaway_QuotaPressure_FiresOnRateLimit(t *testing.T) {
	q := QuotaSection{RateLimitHits: 1, ClosestApproach: []QuotaApproach{
		{Provider: "claude", LimitID: "five_hour", MaxUsedPercent: 84, ObservedAt: "2026-07-16T11:58:00Z", Samples: 3},
	}}
	tk := takeawayByCase(buildTakeaways(nil, turnLabels{}, Report{Quota: q}))[CaseQuotaPressure]
	if !tk.Fired {
		t.Fatalf("quota_pressure did not fire on a rate-limit hit: %+v", tk)
	}
	if !strings.Contains(tk.Analysis, "rate-limit") || !strings.Contains(tk.Analysis, "84%") {
		t.Errorf("analysis should name the hit and the approach: %q", tk.Analysis)
	}
}

func TestTakeaway_QuotaPressure_FiresOnCloseApproachWithoutHit(t *testing.T) {
	q := QuotaSection{RateLimitHits: 0, ClosestApproach: []QuotaApproach{
		{Provider: "claude", LimitID: "five_hour", MaxUsedPercent: QuotaNoticePercent + 2, ObservedAt: "2026-07-16T11:00:00Z", Samples: 5},
	}}
	tk := takeawayByCase(buildTakeaways(nil, turnLabels{}, Report{Quota: q}))[CaseQuotaPressure]
	if !tk.Fired {
		t.Fatalf("quota_pressure did not fire on a close approach: %+v", tk)
	}
}

func TestTakeaway_QuotaPressure_DormantWhenClear(t *testing.T) {
	q := QuotaSection{RateLimitHits: 0, ClosestApproach: []QuotaApproach{
		{Provider: "claude", LimitID: "five_hour", MaxUsedPercent: 42, ObservedAt: "2026-07-16T11:00:00Z", Samples: 2},
	}}
	tk := takeawayByCase(buildTakeaways(nil, turnLabels{}, Report{Quota: q}))[CaseQuotaPressure]
	if tk.Fired {
		t.Errorf("quota_pressure fired well below the notice line")
	}
	if !strings.Contains(tk.Analysis, "clear") {
		t.Errorf("clear analysis should say so: %q", tk.Analysis)
	}
}

func TestTakeaway_AgentThrash_FiresOnHighRepeatRate(t *testing.T) {
	turns := []turnRecord{{
		sessionID: "s1", turnID: "t1",
		fileOps: fileOpsSample{total: i64p(20), repeated: i64p(15), distinct: i64p(5), maxOps: i64p(12)},
	}}
	tk := takeawayByCase(buildTakeaways(turns, turnLabels{}, Report{}))[CaseAgentThrash]
	if !tk.Fired {
		t.Fatalf("agent_thrash did not fire at repeat-rate 0.75: %+v", tk)
	}
	if !strings.Contains(tk.Analysis, "75%") {
		t.Errorf("analysis should state the repeat rate: %q", tk.Analysis)
	}
	if !strings.Contains(tk.Analysis, "one file") {
		t.Errorf("analysis should surface max-ops-on-one-file: %q", tk.Analysis)
	}
}

func TestTakeaway_AgentThrash_DormantWithoutTelemetry(t *testing.T) {
	// A turn with no file-op counts at all (e.g. a managed run) — the
	// case must read as "accumulating", not fabricate a zero.
	turns := []turnRecord{{sessionID: "s1", turnID: "t1"}}
	tk := takeawayByCase(buildTakeaways(turns, turnLabels{}, Report{}))[CaseAgentThrash]
	if tk.Fired {
		t.Errorf("agent_thrash fired without any file-op telemetry")
	}
	if !strings.Contains(tk.Analysis, "accumulating") {
		t.Errorf("dormant analysis should say telemetry is accumulating: %q", tk.Analysis)
	}
}

func TestTakeaway_AgentThrash_DormantBelowThreshold(t *testing.T) {
	// Measured, but a low rate and too few ops: converged, not thrashing.
	turns := []turnRecord{
		{sessionID: "s1", turnID: "t1", fileOps: fileOpsSample{total: i64p(10), repeated: i64p(1), distinct: i64p(9), maxOps: i64p(2)}},
		{sessionID: "s1", turnID: "t2", fileOps: fileOpsSample{total: i64p(4), repeated: i64p(3), distinct: i64p(1), maxOps: i64p(4)}}, // high rate but only 4 ops
	}
	tk := takeawayByCase(buildTakeaways(turns, turnLabels{}, Report{}))[CaseAgentThrash]
	if tk.Fired {
		t.Errorf("agent_thrash fired below the op floor / rate line: %+v", tk)
	}
	if !strings.Contains(tk.Analysis, "converged") {
		t.Errorf("below-threshold analysis should read as converged: %q", tk.Analysis)
	}
}

// TestGenerateReport_Takeaways_FileOpsDecodeIntoAgentThrash proves the
// end-to-end path: file-op payload keys on provider.turn.completed decode
// (load.go), attach to the turn (derive.go), and drive the agent-thrash
// case through the real engine.
func TestGenerateReport_Takeaways_FileOpsDecodeIntoAgentThrash(t *testing.T) {
	engine, db := newTestEngine(t)
	ts := time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC)
	insertEvent(t, db, "provider.turn.started", ts, "claude", "sess-thrash", "turn-x", `{}`)
	insertEvent(t, db, "provider.turn.completed", ts.Add(time.Minute), "claude", "sess-thrash", "turn-x",
		`{"total_file_ops":20,"repeated_ops":15,"distinct_files_touched":5,"max_ops_on_one_file":12}`)

	rep, err := engine.GenerateReport(context.Background(), DefaultWindow)
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}
	tk := takeawayByCase(rep.Takeaways)[CaseAgentThrash]
	if !tk.Fired {
		t.Fatalf("agent_thrash did not fire from seeded file-op telemetry: %+v", tk)
	}
	if !strings.Contains(tk.Analysis, "75%") {
		t.Errorf("analysis should carry the derived repeat rate: %q", tk.Analysis)
	}

	// And it renders, with FIRED cases surfaced ahead of dormant ones.
	text := RenderText(rep)
	if !strings.Contains(text, "Actionable takeaways") {
		t.Errorf("rendered report missing the takeaways section:\n%s", text)
	}
	fired := strings.Index(text, "When the agent goes in circles")
	dormant := strings.Index(text, "Where the money went")
	if fired == -1 || dormant == -1 || fired > dormant {
		t.Errorf("FIRED case should render before dormant cases (fired=%d dormant=%d)", fired, dormant)
	}
}

func TestRenderTakeaways_ShowsAnalysisLessonAction(t *testing.T) {
	takeaways := []Takeaway{{
		Case: CaseExpensiveTurns, Title: "Where the money went", Fired: true,
		Analysis: "Costliest turn was $43.94.", Lesson: "Plan first.", Action: "Scope it.",
		Evidence: []string{"turn t1"},
	}}
	var b strings.Builder
	renderTakeaways(&b, takeaways)
	out := b.String()
	for _, want := range []string{"FIRED", "analysis:", "lesson:", "action:", "evidence:", "$43.94"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered takeaway missing %q:\n%s", want, out)
		}
	}
}
