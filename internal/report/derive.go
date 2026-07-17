// derive.go: per-turn attribution and section aggregation.
//
// The per-turn cost/duration figures here are an attribution MODEL, not
// an observation: statusline usage totals are session-cumulative
// (total_cost_usd only grows), so a turn's figure is a subtraction
// across snapshots that lag the work they measure. The bracketing rules
// below deliberately MIRROR the reference implementation in
// research/calibration/observations.py (derive_turn_actuals) — the
// repository's one previously-existing delta derivation — rather than
// inventing a second attribution model; that file is Python (research/
// owns modeling, the Go bridges only capture), so the logic is restated
// here minimally instead of imported. Keep the two in sync:
//
//   - a turn window opens at provider.turn.started and is closed by the
//     first terminal turn event before the next turn.started (or end of
//     series); an unclosed turn gets NO derived figures;
//   - usage samples between a turn's terminal event and the next
//     turn.started are attributed to the finished turn (snapshots lag);
//   - delta = (last cumulative sample inside the window) - (last sample
//     at or before turn start); no pre-turn baseline -> underivable —
//     NEVER a 0 baseline (resumed sessions and retention-trimmed heads
//     carry prior totals; unknown is not zero);
//   - a negative cost delta means the input series is suspect and is
//     surfaced via a report note, never silently dropped or clamped.
//
// Managed-run outcomes need none of this: `auspex run` persists a
// provider.usage.observed event that is already per-turn and already
// turn-stamped (internal/telemetry/claude/managedrun.go), consumed here
// directly.
package report

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/huaiche94/auspex/internal/pricing"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// turnRecord is one attributed turn: identity, outcome, and every
// per-turn figure this report could derive for it. nil = underivable.
type turnRecord struct {
	sessionID string
	turnID    string
	provider  string
	// anchor orders and window-filters the turn: turn.started time when
	// one was captured, else the terminal event's time.
	anchor  time.Time
	outcome string // completed | failed | interrupted | "" (unclosed)

	costUSD       *float64
	apiDurationMs *int64

	tokens  tokenSample
	fileOps fileOpsSample

	// Identity observed on the turn's own events (payload model_id /
	// effort), used as fallback labels when no predictions row exists.
	eventModelID string
	eventEffort  string
}

// tokenSample is one turn's token accounting, if any event carried it.
type tokenSample struct {
	freshInput    *int64
	output        *int64
	cacheRead     *int64
	cacheCreation *int64
	reasoning     *int64
}

func (t tokenSample) any() bool {
	return t.freshInput != nil || t.output != nil || t.cacheRead != nil ||
		t.cacheCreation != nil || t.reasoning != nil
}

// fileOpsSample is one turn's file-operation aggregate (counts only; paths
// never persist — ADR-052). nil = that count was not measured.
type fileOpsSample struct {
	total    *int64
	repeated *int64
	distinct *int64
	maxOps   *int64
}

// repeatRate mirrors telemetry/claude toolops.RepeatRate: repeated/total,
// omitted (ok=false) when either is unknown or total is 0 — a rate over no
// operations is not a measurement, never a fabricated 0.
func (f fileOpsSample) repeatRate() (float64, bool) {
	if f.repeated == nil || f.total == nil || *f.total <= 0 {
		return 0, false
	}
	return float64(*f.repeated) / float64(*f.total), true
}

// loadTurnRecords loads the event series and derives one turnRecord per
// observed turn, over the FULL series (window filtering happens after —
// see loadSeriesEvents). Returned notes surface data-quality anomalies.
func loadTurnRecords(ctx context.Context, db *sqlite.DB) ([]turnRecord, []string, error) {
	events, err := loadSeriesEvents(ctx, db)
	if err != nil {
		return nil, nil, err
	}
	turns, notes := deriveTurnRecords(events)
	return turns, notes, nil
}

type sample struct {
	t time.Time
	v float64
}

// deriveTurnRecords groups the series per session and applies the
// bracketing model documented in the file comment. Events without a
// session_id cannot be windowed and are ignored (the same honest
// limitation observations.py documents).
func deriveTurnRecords(events []seriesEvent) ([]turnRecord, []string) {
	bySession := map[string][]seriesEvent{}
	var sessionIDs []string
	for _, ev := range events {
		if ev.sessionID == "" {
			continue
		}
		if _, ok := bySession[ev.sessionID]; !ok {
			sessionIDs = append(sessionIDs, ev.sessionID)
		}
		bySession[ev.sessionID] = append(bySession[ev.sessionID], ev)
	}
	sort.Strings(sessionIDs)

	var turns []turnRecord
	negativeCostDeltas := 0
	for _, sessionID := range sessionIDs {
		series := bySession[sessionID]
		// Re-sort by parsed time: the SQL ordered by the occurred_at TEXT,
		// which is not totally ordered at sub-second granularity
		// (trimmed-zero RFC3339Nano — observations.py re-sorts the same way).
		sort.SliceStable(series, func(i, j int) bool {
			return series[i].occurredAt.Before(series[j].occurredAt)
		})

		sessionTurns := deriveSessionTurns(sessionID, series, &negativeCostDeltas)
		turns = append(turns, sessionTurns...)
	}

	var notes []string
	if negativeCostDeltas > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d turn(s) derived a NEGATIVE cost delta — a cumulative cost series cannot shrink, so those rows are suspect; surfaced as-is, not dropped",
			negativeCostDeltas))
	}
	return turns, notes
}

// deriveSessionTurns derives one session's turn records.
func deriveSessionTurns(sessionID string, series []seriesEvent, negativeCostDeltas *int) []turnRecord {
	// Cumulative statusline samples (no turn_id) vs managed per-turn
	// usage events (turn_id stamped): entirely different semantics —
	// the managed variant must NOT enter the cumulative series.
	var costSamples, apiDurSamples []sample
	managedUsage := map[string]seriesEvent{}
	var startIdx []int
	terminalUsed := make([]bool, len(series))

	for i, ev := range series {
		switch ev.eventType {
		case string(v1.EventProviderUsageObserved):
			if ev.turnID != "" {
				managedUsage[ev.turnID] = ev
				continue
			}
			if ev.totalCostUSD != nil {
				costSamples = append(costSamples, sample{t: ev.occurredAt, v: *ev.totalCostUSD})
			}
			if ev.totalAPIDurationMs != nil {
				apiDurSamples = append(apiDurSamples, sample{t: ev.occurredAt, v: float64(*ev.totalAPIDurationMs)})
			}
		case string(v1.EventProviderTurnStarted):
			startIdx = append(startIdx, i)
		}
	}

	var turns []turnRecord
	for n, i := range startIdx {
		start := series[i]
		var nextStart *time.Time
		if n+1 < len(startIdx) {
			t := series[startIdx[n+1]].occurredAt
			nextStart = &t
		}

		// First terminal event inside (start, nextStart).
		var terminal *seriesEvent
		for j := i + 1; j < len(series); j++ {
			if nextStart != nil && !series[j].occurredAt.Before(*nextStart) {
				break
			}
			if isTerminal(series[j].eventType) && !terminalUsed[j] {
				terminal = &series[j]
				terminalUsed[j] = true
				break
			}
		}

		rec := turnRecord{
			sessionID:    sessionID,
			turnID:       start.turnID,
			provider:     start.provider,
			anchor:       start.occurredAt,
			eventModelID: start.modelID,
			eventEffort:  start.effort,
		}
		if terminal != nil {
			rec.outcome = terminalOutcome(terminal.eventType)
			rec.tokens = tokensFromEvent(*terminal)
			rec.fileOps = fileOpsFromEvent(*terminal)
			if terminal.modelID != "" {
				rec.eventModelID = terminal.modelID
			}
			if terminal.effort != "" {
				rec.eventEffort = terminal.effort
			}
			if terminal.provider != "" {
				rec.provider = terminal.provider
			}

			// Cumulative-delta attribution — only for a CLOSED turn
			// (an unclosed window has no defensible end).
			rec.costUSD = deltaOver(costSamples, start.occurredAt, nextStart)
			if rec.costUSD != nil && *rec.costUSD < 0 {
				*negativeCostDeltas++
			}
			if d := deltaOver(apiDurSamples, start.occurredAt, nextStart); d != nil {
				ms := int64(*d)
				rec.apiDurationMs = &ms
			}
		}
		mergeManagedUsage(&rec, managedUsage)
		turns = append(turns, rec)
	}

	// Terminal events consumed by no started window (e.g. managed runs
	// emit only the terminal event; resumed sessions may have lost the
	// started head): each is still a real turn.
	for j, ev := range series {
		if !isTerminal(ev.eventType) || terminalUsed[j] {
			continue
		}
		rec := turnRecord{
			sessionID:    sessionID,
			turnID:       ev.turnID,
			provider:     ev.provider,
			anchor:       ev.occurredAt,
			outcome:      terminalOutcome(ev.eventType),
			tokens:       tokensFromEvent(ev),
			fileOps:      fileOpsFromEvent(ev),
			eventModelID: ev.modelID,
			eventEffort:  ev.effort,
		}
		mergeManagedUsage(&rec, managedUsage)
		turns = append(turns, rec)
	}
	return turns
}

// mergeManagedUsage overlays a turn-stamped managed usage event's exact
// per-turn figures onto rec. The provider's own result-line accounting
// beats any cumulative-delta estimate (it is an observation, not a
// model), so it overwrites.
func mergeManagedUsage(rec *turnRecord, managedUsage map[string]seriesEvent) {
	if rec.turnID == "" {
		return
	}
	usage, ok := managedUsage[rec.turnID]
	if !ok {
		return
	}
	if usage.totalCostUSD != nil {
		v := *usage.totalCostUSD
		rec.costUSD = &v
	}
	if usage.totalAPIDurationMs != nil {
		v := *usage.totalAPIDurationMs
		rec.apiDurationMs = &v
	} else if usage.totalDurationMs != nil && rec.apiDurationMs == nil {
		// Managed usage is per-turn, so its wall duration IS
		// turn-attributable — an acceptable stand-in when the result
		// line carried no API split.
		v := *usage.totalDurationMs
		rec.apiDurationMs = &v
	}
	if t := tokensFromEvent(usage); t.any() {
		rec.tokens = t
	}
	if usage.modelID != "" {
		rec.eventModelID = usage.modelID
	}
	if usage.effort != "" {
		rec.eventEffort = usage.effort
	}
}

func isTerminal(eventType string) bool {
	switch eventType {
	case string(v1.EventProviderTurnCompleted),
		string(v1.EventProviderTurnFailed),
		string(v1.EventProviderTurnInterrupted):
		return true
	}
	return false
}

func terminalOutcome(eventType string) string {
	return eventType[len("provider.turn."):]
}

func tokensFromEvent(ev seriesEvent) tokenSample {
	return tokenSample{
		freshInput:    ev.inputTokens,
		output:        ev.outputTokens,
		cacheRead:     ev.cacheReadInputTokens,
		cacheCreation: ev.cacheCreationInputTokens,
		reasoning:     ev.reasoningOutputTokens,
	}
}

func fileOpsFromEvent(ev seriesEvent) fileOpsSample {
	return fileOpsSample{
		total:    ev.totalFileOps,
		repeated: ev.repeatedOps,
		distinct: ev.distinctFiles,
		maxOps:   ev.maxOpsOnOneFile,
	}
}

// deltaOver applies the bracketing rule: (last sample in (start, end)) -
// (last sample at or before start). nil when either side is missing.
func deltaOver(samples []sample, start time.Time, end *time.Time) *float64 {
	var base *float64
	var last *float64
	for _, s := range samples {
		switch {
		case !s.t.After(start):
			v := s.v
			base = &v
		case end == nil || s.t.Before(*end):
			v := s.v
			last = &v
		default:
			// past the window; samples are time-ordered
		}
		if end != nil && !s.t.Before(*end) {
			break
		}
	}
	if base == nil || last == nil {
		return nil
	}
	d := *last - *base
	return &d
}

// filterWindow keeps turns anchored inside [from, to).
func filterWindow(turns []turnRecord, from, to time.Time) []turnRecord {
	var out []turnRecord
	for _, t := range turns {
		if !t.anchor.Before(from) && t.anchor.Before(to) {
			out = append(out, t)
		}
	}
	return out
}

// --- section builders ---------------------------------------------------

func buildTotals(turns []turnRecord, loc *time.Location) Totals {
	totals := Totals{}
	sessions := map[string]bool{}
	days := map[string]bool{}
	tokens := tokenAccumulator{}
	var costSum float64
	var durSum int64

	for _, t := range turns {
		totals.Turns++
		sessions[t.sessionID] = true
		days[t.anchor.In(loc).Format("2006-01-02")] = true
		switch t.outcome {
		case "completed":
			totals.TurnsCompleted++
		case "failed":
			totals.TurnsFailed++
		case "interrupted":
			totals.TurnsInterrupted++
		default:
			totals.TurnsUnclosed++
		}
		if t.costUSD != nil {
			costSum += *t.costUSD
			totals.CostAttributedTurns++
		}
		if t.apiDurationMs != nil {
			durSum += *t.apiDurationMs
			totals.DurationAttributedTurns++
		}
		if t.tokens.any() {
			totals.TokenReportingTurns++
			tokens.add(t.tokens)
		}
	}
	totals.Sessions = len(sessions)
	totals.ActiveDays = len(days)
	if totals.CostAttributedTurns > 0 {
		totals.CostUSD = &costSum
	}
	if totals.DurationAttributedTurns > 0 {
		totals.APIDurationMs = &durSum
	}
	totals.Tokens = tokens.totals()
	return totals
}

// tokenAccumulator sums per-class token counts, tracking whether each
// class was ever reported at all (a class no turn reported stays nil in
// the output — unknown is not zero).
type tokenAccumulator struct {
	freshInput, output, cacheRead, cacheCreation, reasoning           int64
	sawFresh, sawOutput, sawCacheRead, sawCacheCreation, sawReasoning bool
}

func (a *tokenAccumulator) add(t tokenSample) {
	if t.freshInput != nil {
		a.freshInput += *t.freshInput
		a.sawFresh = true
	}
	if t.output != nil {
		a.output += *t.output
		a.sawOutput = true
	}
	if t.cacheRead != nil {
		a.cacheRead += *t.cacheRead
		a.sawCacheRead = true
	}
	if t.cacheCreation != nil {
		a.cacheCreation += *t.cacheCreation
		a.sawCacheCreation = true
	}
	if t.reasoning != nil {
		a.reasoning += *t.reasoning
		a.sawReasoning = true
	}
}

func (a *tokenAccumulator) totals() TokenTotals {
	out := TokenTotals{}
	if a.sawFresh {
		v := a.freshInput
		out.FreshInput = &v
	}
	if a.sawOutput {
		v := a.output
		out.Output = &v
	}
	if a.sawCacheRead {
		v := a.cacheRead
		out.CacheRead = &v
	}
	if a.sawCacheCreation {
		v := a.cacheCreation
		out.CacheCreation = &v
	}
	if a.sawReasoning {
		v := a.reasoning
		out.Reasoning = &v
	}
	return out
}

// unlabeledValue is the display value for a turn with no model or effort
// label from any source. Distinct from the classifier's own "unknown"
// task class, which is a real, first-class label (ADD §14.3).
const unlabeledValue = "unlabeled"

// modelEffortLabel resolves a turn's display labels, preferring the
// prediction row's stamp (#20 Phase 0 — resolved at evaluate time), then
// the identity observed on the turn's own events, family-resolved via
// the default pricing table when possible.
func modelEffortLabel(t turnRecord, labels turnLabels) (model, effort string) {
	model = labels.modelFamily[t.turnID]
	if model == "" {
		if id := labels.modelID[t.turnID]; id != "" {
			model = familyOrID(id)
		}
	}
	if model == "" && t.eventModelID != "" {
		model = familyOrID(t.eventModelID)
	}
	if model == "" {
		model = unlabeledValue
	}

	effort = labels.effort[t.turnID]
	if effort == "" {
		effort = t.eventEffort
	}
	if effort == "" {
		effort = unlabeledValue
	}
	return model, effort
}

// familyOrID resolves a raw model id to its pricing family for display
// ("claude-opus-4-8[1m]" -> "opus"). Price's DefaultFamily fallback is an
// explicit non-match, not a resolution — an unrecognized id (e.g. a codex
// model the claude-centric table doesn't know) keeps its own name rather
// than being mislabeled "default".
func familyOrID(modelID string) string {
	if _, family := pricing.DefaultTable().Price(modelID); family != "" && family != pricing.DefaultFamily {
		return family
	}
	return modelID
}

func providerLabel(t turnRecord, labels turnLabels) string {
	if t.provider != "" {
		return t.provider
	}
	if p := labels.provider[t.turnID]; p != "" {
		return p
	}
	return unlabeledValue
}

func buildModelMix(turns []turnRecord, labels turnLabels) []ModelMixRow {
	type key struct{ provider, model, effort string }
	rows := map[key]*ModelMixRow{}
	costs := map[key]float64{}
	tokens := map[key]*tokenAccumulator{}
	var keys []key

	for _, t := range turns {
		model, effort := modelEffortLabel(t, labels)
		k := key{provider: providerLabel(t, labels), model: model, effort: effort}
		row, ok := rows[k]
		if !ok {
			row = &ModelMixRow{Provider: k.provider, Model: k.model, Effort: k.effort}
			rows[k] = row
			tokens[k] = &tokenAccumulator{}
			keys = append(keys, k)
		}
		row.Turns++
		if t.costUSD != nil {
			costs[k] += *t.costUSD
			row.CostAttributedTurns++
		}
		if t.tokens.any() {
			row.TokenReportingTurns++
			tokens[k].add(t.tokens)
		}
	}

	for k, row := range rows {
		if row.CostAttributedTurns > 0 {
			v := costs[k]
			row.CostUSD = &v
		}
		row.Tokens = tokens[k].totals()
	}

	// Costliest first; label order breaks ties deterministically.
	sort.Slice(keys, func(i, j int) bool {
		ci, cj := costs[keys[i]], costs[keys[j]]
		if ci != cj {
			return ci > cj
		}
		if rows[keys[i]].Turns != rows[keys[j]].Turns {
			return rows[keys[i]].Turns > rows[keys[j]].Turns
		}
		a, b := keys[i], keys[j]
		if a.provider != b.provider {
			return a.provider < b.provider
		}
		if a.model != b.model {
			return a.model < b.model
		}
		return a.effort < b.effort
	})
	out := make([]ModelMixRow, 0, len(keys))
	for _, k := range keys {
		out = append(out, *rows[k])
	}
	return out
}

func buildRightSizing(turns []turnRecord, labels turnLabels) RightSizing {
	rs := RightSizing{MinCohortTurns: MinCohortTurns}

	type cohortKey struct{ taskClass, model, effort string }
	costsByCohort := map[cohortKey][]float64{}
	for _, t := range turns {
		if t.costUSD == nil {
			continue // only cost-attributed turns can enter a cost median
		}
		taskClass, ok := labels.taskClass[t.turnID]
		if !ok || taskClass == "" {
			continue // no feature vector -> no class label to cohort under
		}
		model, effort := modelEffortLabel(t, labels)
		if model == unlabeledValue {
			continue // a model-less cohort compares nothing
		}
		k := cohortKey{taskClass: taskClass, model: model, effort: effort}
		costsByCohort[k] = append(costsByCohort[k], *t.costUSD)
	}

	byClass := map[string][]CohortStat{}
	for k, costs := range costsByCohort {
		if len(costs) < MinCohortTurns {
			continue
		}
		byClass[k.taskClass] = append(byClass[k.taskClass], CohortStat{
			Model:         k.model,
			Effort:        k.effort,
			Turns:         len(costs),
			MedianCostUSD: median(costs),
		})
	}

	var classes []string
	for class := range byClass {
		classes = append(classes, class)
	}
	sort.Strings(classes)
	for _, class := range classes {
		cohorts := byClass[class]
		sort.Slice(cohorts, func(i, j int) bool {
			if cohorts[i].Turns != cohorts[j].Turns {
				return cohorts[i].Turns > cohorts[j].Turns
			}
			if cohorts[i].Model != cohorts[j].Model {
				return cohorts[i].Model < cohorts[j].Model
			}
			return cohorts[i].Effort < cohorts[j].Effort
		})
		rs.TaskClasses = append(rs.TaskClasses, TaskClassComparison{
			TaskClass: class,
			Cohorts:   cohorts,
		})
	}
	if len(rs.TaskClasses) == 0 {
		rs.Note = fmt.Sprintf(
			"not enough data yet (need >=%d cost-attributed turns per task-class x model/effort cohort)",
			MinCohortTurns)
	}
	return rs
}

func median(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

func buildCacheHygiene(turns []turnRecord) CacheHygiene {
	hygiene := CacheHygiene{
		FlagMeanTokensPerTurn: CacheChurnMeanTokensPerTurn,
		FlagMinReportingTurns: CacheChurnMinTurns,
	}

	var freshSum, cacheReadSum int64
	sawFresh, sawCacheRead := false, false
	type churn struct {
		turns int
		total int64
	}
	bySession := map[string]*churn{}
	var sessionIDs []string

	for _, t := range turns {
		if t.tokens.freshInput != nil {
			freshSum += *t.tokens.freshInput
			sawFresh = true
		}
		if t.tokens.cacheRead != nil {
			cacheReadSum += *t.tokens.cacheRead
			sawCacheRead = true
		}
		if t.tokens.cacheCreation == nil {
			continue
		}
		c, ok := bySession[t.sessionID]
		if !ok {
			c = &churn{}
			bySession[t.sessionID] = c
			sessionIDs = append(sessionIDs, t.sessionID)
		}
		c.turns++
		c.total += *t.tokens.cacheCreation
	}

	if sawFresh {
		v := freshSum
		hygiene.FreshInputTokens = &v
	}
	if sawCacheRead {
		v := cacheReadSum
		hygiene.CacheReadTokens = &v
	}
	if sawFresh && sawCacheRead && freshSum > 0 {
		ratio := float64(cacheReadSum) / float64(freshSum)
		hygiene.CacheReadPerFreshInput = &ratio
	}

	sort.Strings(sessionIDs)
	for _, id := range sessionIDs {
		c := bySession[id]
		mean := c.total / int64(c.turns)
		flagged := c.turns >= CacheChurnMinTurns && mean > CacheChurnMeanTokensPerTurn
		if flagged {
			hygiene.FlaggedSessions++
		}
		hygiene.Sessions = append(hygiene.Sessions, SessionChurn{
			SessionID:           id,
			ReportingTurns:      c.turns,
			CacheCreationTokens: c.total,
			MeanTokensPerTurn:   mean,
			Flagged:             flagged,
		})
	}
	hygiene.TokenReportingSessions = len(sessionIDs)
	// Heaviest churn first.
	sort.SliceStable(hygiene.Sessions, func(i, j int) bool {
		return hygiene.Sessions[i].MeanTokensPerTurn > hygiene.Sessions[j].MeanTokensPerTurn
	})
	return hygiene
}

func buildTopTurns(turns []turnRecord, labels turnLabels) []TopTurn {
	var attributed []turnRecord
	for _, t := range turns {
		if t.costUSD != nil {
			attributed = append(attributed, t)
		}
	}
	sort.SliceStable(attributed, func(i, j int) bool {
		return *attributed[i].costUSD > *attributed[j].costUSD
	})
	if len(attributed) > 5 {
		attributed = attributed[:5]
	}

	out := make([]TopTurn, 0, len(attributed))
	for _, t := range attributed {
		model, effort := modelEffortLabel(t, labels)
		top := TopTurn{
			SessionID: t.sessionID,
			TurnID:    t.turnID,
			StartedAt: t.anchor.UTC().Format(time.RFC3339),
			Provider:  providerLabel(t, labels),
			Model:     model,
			Effort:    effort,
			CostUSD:   *t.costUSD,
		}
		acc := tokenAccumulator{}
		acc.add(t.tokens)
		top.Tokens = acc.totals()
		if t.apiDurationMs != nil {
			v := *t.apiDurationMs
			top.APIDurationMs = &v
		}
		out = append(out, top)
	}
	return out
}
