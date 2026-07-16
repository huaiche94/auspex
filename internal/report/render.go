// render.go: the human-readable rendering of a Report — clean aligned
// text via text/tabwriter, one block per section. Every section renders
// SOMETHING: when a section has no data it says so explicitly (unknown
// is not zero, and an empty report must still be an honest report).
package report

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"
)

// RenderText renders rep as the `auspex report` default (non---json)
// output.
func RenderText(rep Report) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Auspex usage report  %s -> %s  (window %s, local time)\n",
		rep.WindowFrom, rep.WindowTo, rep.WindowLabel)

	renderTotals(&b, rep.Totals)
	renderModelMix(&b, rep.ModelMix)
	renderRightSizing(&b, rep.RightSizing)
	renderCacheHygiene(&b, rep.CacheHygiene)
	renderQuota(&b, rep.Quota)
	renderTopTurns(&b, rep.TopTurns)

	if len(rep.Notes) > 0 {
		b.WriteString("\nNotes\n")
		for _, note := range rep.Notes {
			fmt.Fprintf(&b, "  - %s\n", note)
		}
	}
	return b.String()
}

func renderTotals(b *strings.Builder, t Totals) {
	b.WriteString("\nTotals\n")
	if t.Turns == 0 {
		b.WriteString("  no turns observed in this window\n")
		return
	}
	w := newTab(b)
	tabf(w, "  turns\t%d\t(completed %d, failed %d, interrupted %d, unclosed %d)\n",
		t.Turns, t.TurnsCompleted, t.TurnsFailed, t.TurnsInterrupted, t.TurnsUnclosed)
	tabf(w, "  sessions\t%d\t\n", t.Sessions)
	tabf(w, "  active days\t%d\t\n", t.ActiveDays)
	if t.CostUSD != nil {
		tabf(w, "  cost\t%s\tattributed on %d of %d turns (the rest are unknown, not $0)\n",
			formatUSD(*t.CostUSD), t.CostAttributedTurns, t.Turns)
	} else {
		tabf(w, "  cost\tunknown\tno turn in this window has an attributable cost\n")
	}
	if t.APIDurationMs != nil {
		tabf(w, "  API time\t%s\tattributed on %d of %d turns\n",
			formatDurationMs(*t.APIDurationMs), t.DurationAttributedTurns, t.Turns)
	} else {
		tabf(w, "  API time\tunknown\tno turn in this window has an attributable duration\n")
	}
	_ = w.Flush()

	if t.TokenReportingTurns == 0 {
		b.WriteString("  tokens: no turn in this window reported per-turn token usage\n")
		return
	}
	fmt.Fprintf(b, "  tokens (%d token-reporting turns):\n", t.TokenReportingTurns)
	w = newTab(b)
	writeTokenLine(w, "    fresh input", t.Tokens.FreshInput, "")
	writeTokenLine(w, "    cache read", t.Tokens.CacheRead, "")
	writeTokenLine(w, "    cache creation", t.Tokens.CacheCreation, "")
	writeTokenLine(w, "    output", t.Tokens.Output, "")
	if t.Tokens.Reasoning != nil {
		writeTokenLine(w, "    reasoning", t.Tokens.Reasoning, "(codex; already included in output)")
	}
	_ = w.Flush()
}

func writeTokenLine(w *tabwriter.Writer, label string, v *int64, note string) {
	if v == nil {
		tabf(w, "%s\tnot reported\t%s\n", label, note)
		return
	}
	tabf(w, "%s\t%s\t%s\n", label, formatCount(*v), note)
}

func renderModelMix(b *strings.Builder, rows []ModelMixRow) {
	b.WriteString("\nBy provider / model / effort\n")
	if len(rows) == 0 {
		b.WriteString("  no turns observed in this window\n")
		return
	}
	w := newTab(b)
	tabf(w, "  provider\tmodel/effort\tturns\tcost\tfresh in\tcache read\tcache create\toutput\t\n")
	for _, r := range rows {
		cost := "unknown"
		if r.CostUSD != nil {
			cost = fmt.Sprintf("%s (n=%d)", formatUSD(*r.CostUSD), r.CostAttributedTurns)
		}
		tabf(w, "  %s\t%s/%s\t%d\t%s\t%s\t%s\t%s\t%s\t\n",
			r.Provider, r.Model, r.Effort, r.Turns, cost,
			formatMaybeCount(r.Tokens.FreshInput),
			formatMaybeCount(r.Tokens.CacheRead),
			formatMaybeCount(r.Tokens.CacheCreation),
			formatMaybeCount(r.Tokens.Output))
	}
	_ = w.Flush()
	b.WriteString("  cost n = turns the cost could be attributed to; token columns cover token-reporting turns only\n")
}

func renderRightSizing(b *strings.Builder, rs RightSizing) {
	b.WriteString("\nRight-sizing observations (descriptive, not prescriptive)\n")
	if rs.Note != "" {
		fmt.Fprintf(b, "  %s\n", rs.Note)
		return
	}
	for _, tc := range rs.TaskClasses {
		parts := make([]string, 0, len(tc.Cohorts))
		for _, c := range tc.Cohorts {
			parts = append(parts, fmt.Sprintf("%s/%s median %s/turn (n=%d)",
				c.Model, c.Effort, formatUSD(c.MedianCostUSD), c.Turns))
		}
		fmt.Fprintf(b, "  %s-class turns: %s\n", tc.TaskClass, strings.Join(parts, " vs "))
		if len(tc.Cohorts) == 1 {
			fmt.Fprintf(b, "    (no second cohort with >=%d attributed turns to set beside it)\n", rs.MinCohortTurns)
		}
	}
	b.WriteString("  These cohorts ran different work; medians are observations, not a controlled comparison.\n")
}

func renderCacheHygiene(b *strings.Builder, h CacheHygiene) {
	b.WriteString("\nCache hygiene\n")
	if h.CacheReadPerFreshInput != nil {
		fmt.Fprintf(b, "  cache-read : fresh-input ratio %.1fx (%s cache-read vs %s fresh) — higher is better: cache reads re-serve context at ~1/10th the fresh-input price\n",
			*h.CacheReadPerFreshInput,
			formatCount(*h.CacheReadTokens), formatCount(*h.FreshInputTokens))
	} else {
		b.WriteString("  cache-read : fresh-input ratio unknown (no token-reporting turns in window)\n")
	}

	if h.TokenReportingSessions == 0 {
		b.WriteString("  cache-creation churn: no session reported cache-creation tokens in window\n")
		return
	}
	fmt.Fprintf(b, "  cache-creation churn (flag: mean >%s tokens/turn across >=%d reporting turns — repeated large cache writes suggest compaction/context thrash; every rewrite re-bills at the 1.25x cache-write rate):\n",
		formatCount(h.FlagMeanTokensPerTurn), h.FlagMinReportingTurns)
	flaggedShown := 0
	w := newTab(b)
	for _, s := range h.Sessions {
		if !s.Flagged {
			continue
		}
		flaggedShown++
		tabf(w, "    FLAG session %s\t%d turns\t%s total\t%s/turn\t\n",
			s.SessionID, s.ReportingTurns, formatCount(s.CacheCreationTokens), formatCount(s.MeanTokensPerTurn))
	}
	_ = w.Flush()
	if flaggedShown == 0 {
		fmt.Fprintf(b, "    no session above the churn threshold (%d reporting session(s))\n", h.TokenReportingSessions)
	} else {
		fmt.Fprintf(b, "    %d of %d reporting session(s) flagged\n", flaggedShown, h.TokenReportingSessions)
	}
}

func renderQuota(b *strings.Builder, q QuotaSection) {
	b.WriteString("\nQuota incidents\n")
	fmt.Fprintf(b, "  rate-limit hits: %d\n", q.RateLimitHits)
	if len(q.ClosestApproach) == 0 {
		b.WriteString("  closest approach: no quota observations in window\n")
		return
	}
	b.WriteString("  closest approach per quota window (max used_percent observed):\n")
	w := newTab(b)
	for _, a := range q.ClosestApproach {
		tabf(w, "    %s %s\t%.0f%%\tat %s\t(%d samples)\t\n",
			a.Provider, a.LimitID, a.MaxUsedPercent, formatTimestamp(a.ObservedAt), a.Samples)
	}
	_ = w.Flush()
}

func renderTopTurns(b *strings.Builder, turns []TopTurn) {
	b.WriteString("\nTop turns by attributed cost\n")
	if len(turns) == 0 {
		b.WriteString("  no cost-attributed turns in this window\n")
		return
	}
	w := newTab(b)
	for i, t := range turns {
		turnID := t.TurnID
		if turnID == "" {
			turnID = "?"
		}
		dur := "duration unknown"
		if t.APIDurationMs != nil {
			dur = "api " + formatDurationMs(*t.APIDurationMs)
		}
		tokens := "tokens not reported"
		if t.Tokens.Output != nil || t.Tokens.FreshInput != nil {
			tokens = fmt.Sprintf("in %s / out %s",
				formatMaybeCount(t.Tokens.FreshInput), formatMaybeCount(t.Tokens.Output))
		}
		tabf(w, "  %d.\t%s\t%s/%s\t%s\t%s\t%s\tsession %s\tturn %s\t\n",
			i+1, formatUSD(t.CostUSD), t.Model, t.Effort, dur, tokens,
			formatTimestamp(t.StartedAt), t.SessionID, turnID)
	}
	_ = w.Flush()
}

// --- formatting helpers ---------------------------------------------------

func newTab(b *strings.Builder) *tabwriter.Writer {
	return tabwriter.NewWriter(b, 0, 4, 2, ' ', 0)
}

// tabf writes one formatted row into a tabwriter. Every tabwriter in
// this file wraps a strings.Builder, whose writes are documented never
// to fail, so the write error is deliberately discarded.
func tabf(w *tabwriter.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func formatUSD(v float64) string {
	return fmt.Sprintf("$%.2f", v)
}

// formatCount renders an integer with thousands separators.
func formatCount(v int64) string {
	s := fmt.Sprintf("%d", v)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	out := strings.Join(parts, ",")
	if neg {
		out = "-" + out
	}
	return out
}

func formatMaybeCount(v *int64) string {
	if v == nil {
		return "-"
	}
	return formatCount(*v)
}

func formatDurationMs(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
}

// formatTimestamp compacts a stored RFC3339Nano timestamp to minute
// precision for display; anything unparseable is shown verbatim.
func formatTimestamp(s string) string {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		if t2, err2 := time.Parse(time.RFC3339, s); err2 == nil {
			t = t2
		} else {
			return s
		}
	}
	return t.Format("2006-01-02 15:04")
}
