// takeaways.go: the actionable layer (issue #100). The descriptive
// sections above answer "what happened"; this answers "so what / now
// what" — for each of the five weekly-reflection cases the pilot guide
// frames, it emits analysis -> lesson -> action so the user is not left to
// judge alone.
//
// # Rule-based by design, not LLM (no ADR needed)
//
// Every lesson and action below is a CANNED template mapped from an
// observed pattern — the deterministic, no-outbound-LLM path the issue
// specifies. Synthesizing lessons with an LLM would cross the ADR-gated
// outbound-LLM boundary (#98); this stays entirely local.
//
// # Grounding discipline (issue #100)
//
// A case fires from the most STRUCTURAL signal available, reusing cutoffs
// already justified elsewhere rather than inventing calibrated numbers:
//
//   - expensive_turns fires on the mere existence of a cost-attributed
//     turn (the weekly "was it worth it?" question) — no dollar threshold;
//   - model_right_sizing fires when a task class already has >=2 cohorts
//     past the existing MinCohortTurns gate — a structural comparison;
//   - session_cache_churn reuses CacheChurnMeanTokensPerTurn verbatim;
//   - quota_pressure fires on a real rate-limit hit (structural) or a
//     close approach — the one genuinely new cutoff below;
//   - agent_thrash needs a repeat-rate line — the other new cutoff.
//
// The two genuinely new cutoffs (QuotaNoticePercent, HighRepeatRate) are
// CONSERVATIVE PROVISIONAL defaults, not calibrated values: they wait on
// real windowed data (backlog convention) and are documented as
// heuristics here, exactly as CacheChurnMeanTokensPerTurn documents its
// own basis. A non-fired case never fabricates a signal — it says why it
// stayed quiet.
package report

import (
	"fmt"
	"strings"
)

// QuotaNoticePercent flags a quota window the user came close to
// exhausting even without a hard rate-limit hit. PROVISIONAL: 80% is a
// conservative "getting close" line pending calibration against real
// windowed approach distributions (#100 grounding discipline).
const QuotaNoticePercent = 80.0

// HighRepeatRate flags a turn whose file operations mostly revisited
// already-touched files (agent thrash / churn). HighRepeatMinOps guards it
// from firing on a tiny handful of ops where a high rate is just noise.
// PROVISIONAL: 0.6 ("well over half the ops were re-visits") and a floor
// of 8 ops are conservative lines pending calibration on real repeat-rate
// distributions — the pilot guide's alarming example sat at 0.7.
const (
	HighRepeatRate   = 0.6
	HighRepeatMinOps = 8
)

// buildTakeaways derives the five actionable cases from the already-built
// report sections plus the in-window turns (medians and repeat rates the
// sections do not surface). Order is fixed so the output — and any golden
// — is deterministic.
func buildTakeaways(turns []turnRecord, labels turnLabels, rep Report) []Takeaway {
	return []Takeaway{
		takeawayExpensiveTurns(turns, rep),
		takeawayModelRightSizing(rep.RightSizing),
		takeawaySessionCacheChurn(rep.CacheHygiene),
		takeawayQuotaPressure(rep.Quota),
		takeawayAgentThrash(turns, labels),
	}
}

// takeawayExpensiveTurns — case 1, "where did the money go". Fires
// whenever there is a cost-attributed turn to reflect on; the analysis
// grounds it in the costliest turn and its multiple of the median.
func takeawayExpensiveTurns(turns []turnRecord, rep Report) Takeaway {
	t := Takeaway{
		Case:   CaseExpensiveTurns,
		Title:  "Where the money went",
		Lesson: "One vague, broad instruction makes the agent read half the repo — a single turn can cost an order of magnitude more than a scoped one.",
		Action: "Before a big task, ask for a plan first, then execute it in scoped steps; replace \"refactor this module\" with an explicit spec of the files and the outcome you want.",
	}
	if len(rep.TopTurns) == 0 {
		t.Analysis = "No cost-attributed turn in this window yet — turn costs are unknown here, not $0, so there is nothing to rank."
		return t
	}

	top := rep.TopTurns[0]
	var costs []float64
	for _, tr := range turns {
		if tr.costUSD != nil {
			costs = append(costs, *tr.costUSD)
		}
	}
	med := median(costs)

	var sb strings.Builder
	fmt.Fprintf(&sb, "Costliest turn was %s (%s/%s)", formatUSD(top.CostUSD), top.Model, top.Effort)
	if med > 0 && len(costs) > 1 {
		fmt.Fprintf(&sb, ", %.1fx the median attributed turn (%s)", top.CostUSD/med, formatUSD(med))
	}
	if rep.Totals.CostUSD != nil && *rep.Totals.CostUSD > 0 {
		var topSum float64
		for _, tt := range rep.TopTurns {
			topSum += tt.CostUSD
		}
		fmt.Fprintf(&sb, "; the top %d turns are %.0f%% of attributed window cost", len(rep.TopTurns), 100*topSum/(*rep.Totals.CostUSD))
	}
	sb.WriteString(". Ask whether each was worth its price and what you asked for at the time.")

	t.Fired = true
	t.Analysis = sb.String()
	t.Evidence = []string{"session " + top.SessionID, "turn " + orQuestion(top.TurnID)}
	return t
}

// takeawayModelRightSizing — case 2, "did I pick the wrong model". Fires
// when a task class has >=2 qualifying cohorts (already past
// MinCohortTurns), i.e. a cheaper alternative sits beside a pricier one
// for the same kind of work.
func takeawayModelRightSizing(rs RightSizing) Takeaway {
	t := Takeaway{
		Case:   CaseModelRightSizing,
		Title:  "Right-sizing your models",
		Lesson: "Everyday tasks (fix a test, write a docstring, format code) often run fine a tier down; paying the top model/effort for them is pure overhead.",
		Action: "Default routine task classes to one tier down for a week and keep the top tier for architecture-level work, then re-check this same right-sizing table to confirm.",
	}

	// Find the class with the widest cheaper-vs-pricier spread to name.
	var (
		bestClass                string
		bestCheap, bestExpensive CohortStat
		bestSpread               float64
		found                    bool
	)
	for _, tc := range rs.TaskClasses {
		if len(tc.Cohorts) < 2 {
			continue
		}
		cheap, expensive := tc.Cohorts[0], tc.Cohorts[0]
		for _, c := range tc.Cohorts {
			if c.MedianCostUSD < cheap.MedianCostUSD {
				cheap = c
			}
			if c.MedianCostUSD > expensive.MedianCostUSD {
				expensive = c
			}
		}
		if spread := expensive.MedianCostUSD - cheap.MedianCostUSD; !found || spread > bestSpread {
			found, bestSpread, bestClass, bestCheap, bestExpensive = true, spread, tc.TaskClass, cheap, expensive
		}
	}

	if !found {
		if rs.Note != "" {
			t.Analysis = rs.Note + " — no two cohorts to compare, so nothing to right-size against yet."
		} else {
			t.Analysis = "No task class has two comparable cohorts in this window, so there is no cheaper alternative to weigh."
		}
		return t
	}

	t.Fired = true
	t.Analysis = fmt.Sprintf(
		"For %s-class work, %s/%s ran %s/turn (n=%d) beside %s/%s at %s/turn (n=%d) — the same kind of task at different prices. Routine work may not need the pricier tier.",
		bestClass,
		bestExpensive.Model, bestExpensive.Effort, formatUSD(bestExpensive.MedianCostUSD), bestExpensive.Turns,
		bestCheap.Model, bestCheap.Effort, formatUSD(bestCheap.MedianCostUSD), bestCheap.Turns)
	t.Evidence = []string{
		fmt.Sprintf("%s: %s/%s vs %s/%s", bestClass, bestExpensive.Model, bestExpensive.Effort, bestCheap.Model, bestCheap.Effort),
	}
	return t
}

// takeawaySessionCacheChurn — case 3, "the session that got more expensive
// as it ran". Reuses the existing, already-justified churn flag.
func takeawaySessionCacheChurn(h CacheHygiene) Takeaway {
	t := Takeaway{
		Case:   CaseSessionCacheChurn,
		Title:  "Sessions that get more expensive as they run",
		Lesson: "A day-long session that keeps rebuilding its context (compaction thrash) pays for its whole history every turn, re-billed at the 1.25x cache-write rate.",
		Action: "Start a fresh session when you switch tasks, and write conclusions you still need into files instead of leaving them in the conversation.",
	}

	if h.TokenReportingSessions == 0 {
		t.Analysis = "No session reported cache-creation tokens in this window, so churn cannot be assessed yet."
		return t
	}
	if h.FlaggedSessions == 0 {
		t.Analysis = fmt.Sprintf("No session crossed the churn flag (mean > %s cache-creation tokens/turn) across %d reporting session(s) — context reuse looks healthy.",
			formatCount(h.FlagMeanTokensPerTurn), h.TokenReportingSessions)
		return t
	}

	// Sessions are sorted heaviest-churn first (buildCacheHygiene).
	var worst *SessionChurn
	var evidence []string
	for i := range h.Sessions {
		if !h.Sessions[i].Flagged {
			continue
		}
		if worst == nil {
			worst = &h.Sessions[i]
		}
		evidence = append(evidence, "session "+h.Sessions[i].SessionID)
	}

	t.Fired = true
	t.Analysis = fmt.Sprintf(
		"%d of %d reporting session(s) flagged for cache-creation churn; the heaviest averaged %s tokens/turn across %d turns — context is being rebuilt every turn.",
		h.FlaggedSessions, h.TokenReportingSessions, formatCount(worst.MeanTokensPerTurn), worst.ReportingTurns)
	t.Evidence = evidence
	return t
}

// takeawayQuotaPressure — case 4, "cut off by a quota wall". Fires on a
// real rate-limit hit (structural) or a close approach past the
// provisional notice line.
func takeawayQuotaPressure(q QuotaSection) Takeaway {
	t := Takeaway{
		Case:   CaseQuotaPressure,
		Title:  "Getting cut off by a quota wall",
		Lesson: "Hitting a quota wall mid-task with no warning costs you the tail of the work; the risk concentrates near the end of a quota window.",
		Action: "Do the heavy work earlier in a quota window and watch the panel's Runway estimate; defer non-urgent work until after the reset.",
	}

	var worst *QuotaApproach
	for i := range q.ClosestApproach {
		if worst == nil || q.ClosestApproach[i].MaxUsedPercent > worst.MaxUsedPercent {
			worst = &q.ClosestApproach[i]
		}
	}

	closeApproach := worst != nil && worst.MaxUsedPercent >= QuotaNoticePercent
	if q.RateLimitHits == 0 && !closeApproach {
		if worst == nil {
			t.Analysis = "No quota observations in this window, so there is no approach to watch."
		} else {
			t.Analysis = fmt.Sprintf("Closest approach was %s %s at %.0f%% — comfortably clear of the %.0f%% notice line; no rate-limit hits.",
				worst.Provider, worst.LimitID, worst.MaxUsedPercent, QuotaNoticePercent)
		}
		return t
	}

	var sb strings.Builder
	if q.RateLimitHits > 0 {
		fmt.Fprintf(&sb, "%d rate-limit hit(s) this window", q.RateLimitHits)
	} else {
		sb.WriteString("No hard rate-limit hit")
	}
	if worst != nil {
		fmt.Fprintf(&sb, "; closest approach %s %s reached %.0f%% at %s",
			worst.Provider, worst.LimitID, worst.MaxUsedPercent, formatTimestamp(worst.ObservedAt))
		t.Evidence = []string{fmt.Sprintf("%s %s %.0f%%", worst.Provider, worst.LimitID, worst.MaxUsedPercent)}
	}
	sb.WriteString(".")

	t.Fired = true
	t.Analysis = sb.String()
	return t
}

// takeawayAgentThrash — case 5, "the agent going in circles". Grounded in
// the per-turn file-op repeat rate (issue #67/ADR-052; paths never
// persist, only counts). When no turn carried file-op telemetry, the case
// stays honestly dormant — the pilot guide's "accumulating" state.
func takeawayAgentThrash(turns []turnRecord, labels turnLabels) Takeaway {
	t := Takeaway{
		Case:   CaseAgentThrash,
		Title:  "When the agent goes in circles",
		Lesson: "A high file-revisit rate mirrors instruction quality: the agent loops when it was not told how to know it had succeeded.",
		Action: "Give each task an explicit success check (which test, what the passing output looks like); if a task keeps thrashing, rewrite the prompt rather than re-running it.",
	}

	measured := 0
	var worst *turnRecord
	var worstRate float64
	for i := range turns {
		rate, ok := turns[i].fileOps.repeatRate()
		if !ok {
			continue
		}
		measured++
		if turns[i].fileOps.total == nil || *turns[i].fileOps.total < HighRepeatMinOps {
			continue
		}
		if rate >= HighRepeatRate && (worst == nil || rate > worstRate) {
			worst = &turns[i]
			worstRate = rate
		}
	}

	if measured == 0 {
		t.Analysis = "No file-operation telemetry in this window yet — the PostToolUse capture (issue #67/ADR-052) has not stamped these turns, so repeat-rate is still accumulating."
		return t
	}
	if worst == nil {
		t.Analysis = fmt.Sprintf("No turn crossed the repeat-rate flag (>= %.0f%% re-visits over >= %d ops) across %d measured turn(s) — the agent mostly converged.",
			HighRepeatRate*100, HighRepeatMinOps, measured)
		return t
	}

	model, effort := modelEffortLabel(*worst, labels)
	detail := ""
	if worst.fileOps.repeated != nil && worst.fileOps.total != nil {
		detail = fmt.Sprintf(" (%d of %d ops re-visited files", *worst.fileOps.repeated, *worst.fileOps.total)
		if worst.fileOps.maxOps != nil {
			detail += fmt.Sprintf(", up to %d ops on one file", *worst.fileOps.maxOps)
		}
		detail += ")"
	}
	t.Fired = true
	t.Analysis = fmt.Sprintf(
		"A turn revisited files heavily: repeat-rate %.0f%%%s on %s/%s — the agent went back and forth instead of converging.",
		worstRate*100, detail, model, effort)
	t.Evidence = []string{"session " + worst.sessionID, "turn " + orQuestion(worst.turnID)}
	return t
}

// orQuestion renders an empty id as "?" (a turn whose id was never
// captured), matching renderTopTurns.
func orQuestion(s string) string {
	if s == "" {
		return "?"
	}
	return s
}
