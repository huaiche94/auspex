// Package report builds the read-only personal usage report behind
// `auspex report` (issue #91 items 1-3): what did my agent sessions
// actually cost, in which model/effort mix, with what cache hygiene and
// quota pressure, over a recent local-time window.
//
// Everything here is a SELECT over tables other roles own (events,
// predictions, feature_vectors) plus in-memory aggregation — no writes,
// no schema, no new event types. Where a per-turn figure is not an
// observation but an attribution MODEL (statusline usage totals are
// session-cumulative, so "this turn cost $0.12" is a subtraction across
// snapshots), the derivation deliberately mirrors the reference
// implementation in research/calibration/observations.py (see derive.go)
// and every underivable value stays nil: unknown is not zero, and no
// section of this report ever substitutes a fabricated 0 for a missing
// measurement (CONTRACT_FREEZE.md).
package report

import (
	"context"
	"fmt"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// ReportSchemaVersion stamps the `auspex report --json` wire shape,
// following the repo's schema-version convention (auspex.gc.v1,
// auspex.evaluate.v1, ...).
const ReportSchemaVersion = "auspex.report.v1"

// DefaultWindow is the report's default lookback: the last 7 days
// (issue #91's "personal weekly usage report").
const DefaultWindow = 7 * 24 * time.Hour

// MinCohortTurns is the right-sizing floor: a task-class x model/effort
// cohort needs at least this many COST-ATTRIBUTED turns before its median
// is shown at all. Below it the report says "not enough data yet" rather
// than printing a median of three noisy points as if it meant something.
const MinCohortTurns = 8

// Cache-churn flag threshold (section 4). A session whose token-reporting
// turns average more than CacheChurnMeanTokensPerTurn cache-creation
// tokens per turn, across at least CacheChurnMinTurns such turns, is
// flagged as likely context churn / compaction thrash: every one of those
// writes re-bills the written tokens at the cache-write rate (1.25x the
// fresh-input price — internal/pricing's CacheCreation multiplier), so
// repeatedly re-creating a large context is pure overhead. The 100k
// figure is roughly half a Claude context window re-written EVERY turn —
// far above the incremental append a well-behaved session shows.
const (
	CacheChurnMeanTokensPerTurn int64 = 100_000
	CacheChurnMinTurns          int   = 3
)

// Engine generates reports over one Auspex database. Read-only: no
// method of this type writes anything.
type Engine struct {
	// DB is the SQLite handle to report over.
	DB *sqlite.DB
	// Clock is the frozen determinism port (never time.Now directly).
	Clock domain.Clock
	// Location is the timezone for "active days" bucketing and window
	// arithmetic display; nil means time.Local (the report is personal —
	// the user's own wall clock is the honest frame).
	Location *time.Location
}

// Report is the full auspex.report.v1 wire shape. Pointer fields follow
// the repository-wide rule: nil = honestly unknown / no data, never a
// substituted zero.
type Report struct {
	SchemaVersion string `json:"schema_version"`
	GeneratedAt   string `json:"generated_at"`
	WindowFrom    string `json:"window_from"`
	WindowTo      string `json:"window_to"`
	WindowLabel   string `json:"window_label"`

	Totals       Totals        `json:"totals"`
	ModelMix     []ModelMixRow `json:"model_mix"`
	RightSizing  RightSizing   `json:"right_sizing"`
	CacheHygiene CacheHygiene  `json:"cache_hygiene"`
	Quota        QuotaSection  `json:"quota"`
	TopTurns     []TopTurn     `json:"top_turns"`

	// Notes surfaces data-quality observations (e.g. a negative cost
	// delta, which a cumulative series should never produce) instead of
	// silently absorbing them.
	Notes []string `json:"notes,omitempty"`
}

// TokenTotals is the by-class token accounting (ADR-051's four classes
// plus codex's reasoning counter). fresh_input is the provider's own
// non-cached input count (both providers' normalizers persist
// input_tokens as fresh input, cache traffic carried separately);
// reasoning is codex-specific and already INCLUDED in output — it is
// never added into any total here.
type TokenTotals struct {
	FreshInput    *int64 `json:"fresh_input,omitempty"`
	CacheRead     *int64 `json:"cache_read,omitempty"`
	CacheCreation *int64 `json:"cache_creation,omitempty"`
	Output        *int64 `json:"output,omitempty"`
	Reasoning     *int64 `json:"reasoning,omitempty"`
}

// Totals is section 1.
type Totals struct {
	Turns            int `json:"turns"`
	TurnsCompleted   int `json:"turns_completed"`
	TurnsFailed      int `json:"turns_failed"`
	TurnsInterrupted int `json:"turns_interrupted"`
	// TurnsUnclosed counts turn.started windows with no terminal event
	// captured before the next turn (or end of series) — real turns whose
	// outcome (and cost) is honestly unknown.
	TurnsUnclosed int `json:"turns_unclosed"`
	Sessions      int `json:"sessions"`
	ActiveDays    int `json:"active_days"`

	// CostUSD is the sum of per-turn attributed costs: statusline
	// cumulative-delta derivations plus managed-run outcomes (derive.go).
	// nil when no turn in the window has an attributable cost.
	CostUSD *float64 `json:"cost_usd,omitempty"`
	// CostAttributedTurns / Turns is the honest coverage of CostUSD:
	// turns outside it have UNKNOWN cost, not zero cost.
	CostAttributedTurns int `json:"cost_attributed_turns"`

	Tokens TokenTotals `json:"tokens"`
	// TokenReportingTurns counts turns that carried any per-turn token
	// accounting at all (transcript-enriched or managed) — the
	// denominator disclosure for the token sums above.
	TokenReportingTurns int `json:"token_reporting_turns"`

	// APIDurationMs sums per-turn API-time deltas (total_api_duration_ms
	// bracketing + managed outcomes). API time, not wall time: the
	// session-cumulative wall clock keeps running while the user is idle,
	// so its deltas are not turn-attributable.
	APIDurationMs           *int64 `json:"api_duration_ms,omitempty"`
	DurationAttributedTurns int    `json:"duration_attributed_turns"`
}

// ModelMixRow is one provider x model x effort line of section 2.
type ModelMixRow struct {
	Provider string `json:"provider"`
	// Model is the model family when a label exists (predictions row or
	// pricing-table resolution), else the raw model id, else "unlabeled".
	Model  string `json:"model"`
	Effort string `json:"effort"`
	Turns  int    `json:"turns"`

	CostUSD             *float64    `json:"cost_usd,omitempty"`
	CostAttributedTurns int         `json:"cost_attributed_turns"`
	Tokens              TokenTotals `json:"tokens"`
	TokenReportingTurns int         `json:"token_reporting_turns"`
}

// RightSizing is section 3: cost-per-turn medians for task-class x
// model/effort cohorts, shown side by side. Strictly descriptive — the
// cohorts ran DIFFERENT work, so a lower median is an observation about
// what happened, never a controlled comparison or a recommendation.
type RightSizing struct {
	MinCohortTurns int                   `json:"min_cohort_turns"`
	TaskClasses    []TaskClassComparison `json:"task_classes,omitempty"`
	// Note is set when no cohort reached MinCohortTurns.
	Note string `json:"note,omitempty"`
}

// TaskClassComparison groups one task class's qualifying cohorts.
type TaskClassComparison struct {
	TaskClass string       `json:"task_class"`
	Cohorts   []CohortStat `json:"cohorts"`
}

// CohortStat is one model/effort cohort's cost-per-turn summary.
type CohortStat struct {
	Model         string  `json:"model"`
	Effort        string  `json:"effort"`
	Turns         int     `json:"turns"`
	MedianCostUSD float64 `json:"median_cost_usd"`
}

// CacheHygiene is section 4.
type CacheHygiene struct {
	FreshInputTokens *int64 `json:"fresh_input_tokens,omitempty"`
	CacheReadTokens  *int64 `json:"cache_read_tokens,omitempty"`
	// CacheReadPerFreshInput = cache_read / fresh_input over the window's
	// token-reporting turns. High is GOOD: context re-reads served from
	// cache at ~1/10th the fresh-input price instead of re-billed fresh.
	CacheReadPerFreshInput *float64 `json:"cache_read_per_fresh_input,omitempty"`

	// Sessions lists every session with at least one cache-creation-
	// reporting turn in the window; Flagged marks the churn threshold
	// breach documented on CacheChurnMeanTokensPerTurn.
	Sessions               []SessionChurn `json:"sessions,omitempty"`
	FlagMeanTokensPerTurn  int64          `json:"flag_mean_tokens_per_turn"`
	FlagMinReportingTurns  int            `json:"flag_min_reporting_turns"`
	FlaggedSessions        int            `json:"flagged_sessions"`
	TokenReportingSessions int            `json:"token_reporting_sessions"`
}

// SessionChurn is one session's cache-creation churn accounting.
type SessionChurn struct {
	SessionID           string `json:"session_id"`
	ReportingTurns      int    `json:"reporting_turns"`
	CacheCreationTokens int64  `json:"cache_creation_tokens"`
	MeanTokensPerTurn   int64  `json:"mean_tokens_per_turn"`
	Flagged             bool   `json:"flagged"`
}

// QuotaSection is section 5.
type QuotaSection struct {
	RateLimitHits   int             `json:"rate_limit_hits"`
	ClosestApproach []QuotaApproach `json:"closest_approach,omitempty"`
}

// QuotaApproach is one provider quota window's closest approach: the
// maximum used_percent observed inside the report window.
type QuotaApproach struct {
	Provider       string  `json:"provider"`
	LimitID        string  `json:"limit_id"`
	MaxUsedPercent float64 `json:"max_used_percent"`
	ObservedAt     string  `json:"observed_at"`
	Samples        int     `json:"samples"`
}

// TopTurn is one section-6 row: ids and numbers only — the events table
// stores no prompt text, and this report surfaces none.
type TopTurn struct {
	SessionID     string      `json:"session_id"`
	TurnID        string      `json:"turn_id,omitempty"`
	StartedAt     string      `json:"started_at"`
	Provider      string      `json:"provider"`
	Model         string      `json:"model"`
	Effort        string      `json:"effort"`
	CostUSD       float64     `json:"cost_usd"`
	Tokens        TokenTotals `json:"tokens"`
	APIDurationMs *int64      `json:"api_duration_ms,omitempty"`
}

// GenerateReport builds the report for the trailing window ending at
// Clock.Now(). Read-only and safe to run at any time; an empty database
// yields a valid report whose sections say "no data" honestly.
func (e *Engine) GenerateReport(ctx context.Context, window time.Duration) (Report, error) {
	if e.DB == nil {
		return Report{}, fmt.Errorf("report: Engine.DB is required")
	}
	if e.Clock == nil {
		return Report{}, fmt.Errorf("report: Engine.Clock is required")
	}
	if window <= 0 {
		window = DefaultWindow
	}
	loc := e.Location
	if loc == nil {
		loc = time.Local
	}

	now := e.Clock.Now()
	to := now
	from := now.Add(-window)

	turns, notes, err := loadTurnRecords(ctx, e.DB)
	if err != nil {
		return Report{}, err
	}
	labels, err := loadTurnLabels(ctx, e.DB)
	if err != nil {
		return Report{}, err
	}
	quota, err := loadQuotaSection(ctx, e.DB, from, to)
	if err != nil {
		return Report{}, err
	}

	inWindow := filterWindow(turns, from, to)

	rep := Report{
		SchemaVersion: ReportSchemaVersion,
		GeneratedAt:   now.In(loc).Format(time.RFC3339),
		WindowFrom:    from.In(loc).Format(time.RFC3339),
		WindowTo:      to.In(loc).Format(time.RFC3339),
		WindowLabel:   formatWindowLabel(window),
		Totals:        buildTotals(inWindow, loc),
		ModelMix:      buildModelMix(inWindow, labels),
		RightSizing:   buildRightSizing(inWindow, labels),
		CacheHygiene:  buildCacheHygiene(inWindow),
		Quota:         quota,
		TopTurns:      buildTopTurns(inWindow, labels),
		Notes:         notes,
	}
	return rep, nil
}

// formatWindowLabel renders a duration the way the --window flag accepts
// it: whole days as "Nd", anything else as Go duration syntax.
func formatWindowLabel(window time.Duration) string {
	if window%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", int(window/(24*time.Hour)))
	}
	return window.String()
}
