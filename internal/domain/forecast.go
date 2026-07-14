package domain

// ReasonCode is the closed vocabulary of explanations a prediction or
// policy decision may cite (ADD §16.4). Other roles pattern-match on
// these values, so it is a typed enum rather than free-form string.
type ReasonCode string

const (
	ReasonQuotaNearLimit               ReasonCode = "QUOTA_NEAR_LIMIT"
	ReasonQuotaBurnAccelerating        ReasonCode = "QUOTA_BURN_ACCELERATING"
	ReasonQuotaResetSoon               ReasonCode = "QUOTA_RESET_SOON"
	ReasonQuotaUnknown                 ReasonCode = "QUOTA_UNKNOWN"
	ReasonContextNearLimit             ReasonCode = "CONTEXT_NEAR_LIMIT"
	ReasonContextUnknown               ReasonCode = "CONTEXT_UNKNOWN"
	ReasonLargeFileScope               ReasonCode = "LARGE_FILE_SCOPE"
	ReasonLargeLineScope               ReasonCode = "LARGE_LINE_SCOPE"
	ReasonCrossLayerChange             ReasonCode = "CROSS_LAYER_CHANGE"
	ReasonCrossProjectChange           ReasonCode = "CROSS_PROJECT_CHANGE"
	ReasonIntegrationTestsLikely       ReasonCode = "INTEGRATION_TESTS_LIKELY"
	ReasonMigrationLikely              ReasonCode = "MIGRATION_LIKELY"
	ReasonSecuritySensitive            ReasonCode = "SECURITY_SENSITIVE"
	ReasonPublicAPIChange              ReasonCode = "PUBLIC_API_CHANGE"
	ReasonOpenEndedScope               ReasonCode = "OPEN_ENDED_SCOPE"
	ReasonHighRecentRetryRate          ReasonCode = "HIGH_RECENT_RETRY_RATE"
	ReasonHighRecentTestFailureRate    ReasonCode = "HIGH_RECENT_TEST_FAILURE_RATE"
	ReasonNoRecentRepositoryCheckpoint ReasonCode = "NO_RECENT_REPOSITORY_CHECKPOINT"
	ReasonNoRecentStateCheckpoint      ReasonCode = "NO_RECENT_STATE_CHECKPOINT"
	ReasonLargeDirtyWorktree           ReasonCode = "LARGE_DIRTY_WORKTREE"
	ReasonLongRemainingCriticalPath    ReasonCode = "LONG_REMAINING_CRITICAL_PATH"
	ReasonProgressBlocked              ReasonCode = "PROGRESS_BLOCKED"
	ReasonPredictionColdStart          ReasonCode = "PREDICTION_COLD_START"
	ReasonTelemetrySparse              ReasonCode = "TELEMETRY_SPARSE"
	ReasonProviderSchemaDegraded       ReasonCode = "PROVIDER_SCHEMA_DEGRADED"
	ReasonPauseCapabilityUnavailable   ReasonCode = "PAUSE_CAPABILITY_UNAVAILABLE"
	ReasonRepositoryChangedDuringSleep ReasonCode = "REPOSITORY_CHANGED_DURING_SLEEP"
)

// Context-utilization threshold reason codes (ADR-043 increment 2,
// DECISION_LOG.md D-08). These two values are an ADDITIVE extension to the
// original ADD §16.4 list above, sanctioned by ADR-043's "contract impact
// is additive" rule and recorded in CONTRACT_FREEZE.md's Amendments
// section: they are emitted by internal/policy when the projected P90
// context-window utilization (domain.QuotaForecast.ProjectedContextUsedP90)
// crosses D-08's factory-default WARN (>85%) / CHECKPOINT_AND_RUN (>95%)
// thresholds, so the forecast card and `auspex evaluate` can say WHY a
// context-driven action was taken. Two distinct codes (not one) because a
// stronger already-chosen action can absorb the threshold's action while
// the reason code alone must still convey which tier fired. They describe
// a threshold comparison on a projected utilization percentage — never a
// probability claim (Constitution principle #2); a decision citing them
// always carries probability: null unless some OTHER calibrated input
// independently justified one.
const (
	ReasonContextWarnThresholdExceeded       ReasonCode = "CONTEXT_WARN_THRESHOLD_EXCEEDED"
	ReasonContextCheckpointThresholdExceeded ReasonCode = "CONTEXT_CHECKPOINT_THRESHOLD_EXCEEDED"
)

// ReasonTurnCostBudget* are ADR-043 increment 3's user-declared per-turn
// cost-budget threshold states (internal/policy/costbudget.go), additive
// to the taxonomy under the same sanction as the CONTEXT_* pair above:
// WARN-tier when the projected worst-case turn cost exceeds the declared
// budget, CHECKPOINT-tier when even the optimistic projection does. They
// describe a comparison between an ESTIMATED cost range and a
// user-declared number — never a probability claim (Constitution
// principle #2).
const (
	ReasonTurnCostBudgetWarnExceeded       ReasonCode = "TURN_COST_BUDGET_WARN_EXCEEDED"
	ReasonTurnCostBudgetCheckpointExceeded ReasonCode = "TURN_COST_BUDGET_CHECKPOINT_EXCEEDED"
)

// ScopeEstimate is the Predictor pipeline's Stage 1 output: what work a
// turn is expected to require, not how many tokens it will cost (ADD §14.1).
//
// Field set mirrors ADD §14.1 exactly. Unlike the ADD's own pseudocode
// (which used plain int), numeric quantile fields are pointer-typed so a
// genuinely-unknown estimate is never silently reported as zero (ADD
// principle 1, "Unknown is not zero"; Constitution §7). A Wave 2
// implementation of ScopeEstimator MAY populate only a subset of these
// fields (e.g. files/lines) and leave the rest nil — that is an explicit,
// visible degradation, not a contract violation.
type ScopeEstimate struct {
	FilesReadP50        *int64
	FilesReadP80        *int64
	FilesReadP90        *int64
	FilesChangedP50     *int64
	FilesChangedP80     *int64
	FilesChangedP90     *int64
	LinesChangedP50     *int64
	LinesChangedP80     *int64
	LinesChangedP90     *int64
	ToolCallsP50        *int64
	ToolCallsP90        *int64
	VerificationP50     *int64
	VerificationP90     *int64
	RetryLoopsP50       *int64
	RetryLoopsP90       *int64
	DurationP50         *int64 // nanoseconds; pointer-typed like every other quantile field
	DurationP90         *int64
	RequiresUnitTests   bool
	RequiresIntegration bool
	CrossProject        bool
	MigrationLikely     bool
	SecuritySensitive   bool
	Confidence          Confidence
	Calibrated          bool
	ReasonCodes         []ReasonCode
}

// TokenForecast is the Predictor pipeline's Stage 2 output: predicted
// total token cost of the upcoming turn, per the decomposition and
// multiplier model in ADD §15.1-15.2. Distinct from RunwayForecast, which
// forecasts imminent quota-exhaustion probability across a live session
// rather than the cost of a single upcoming turn, and remains independent
// of this forecast (ADR-041).
type TokenForecast struct {
	TokensP50   int64
	TokensP80   int64
	TokensP90   int64
	Calibrated  bool
	Confidence  Confidence
	ReasonCodes []ReasonCode
}

// QuotaForecast is the Predictor pipeline's Stage 3 output: projected
// provider-quota and context-window position after the upcoming turn, per
// ADD §15.3 (quota delta model) and §15.9 (context projection). Both
// projections are produced together because they use the same
// delta-projection technique and both feed RiskCombiner's quota_risk and
// context_risk terms (ADD §16.2). A nil pointer means unknown, not zero.
type QuotaForecast struct {
	ProjectedQuotaUsedP90   *float64 // percent, 0-100
	ProjectedContextUsedP90 *float64 // percent, 0-100
	Calibrated              bool
	Confidence              Confidence
	ReasonCodes             []ReasonCode
}

// RiskComponent is a single named risk term (quota, context, completion,
// or blast-radius) as produced by RiskCombiner (ADD §16.1-16.2). Score is
// a 0-1 value; per ADD principle 2 ("Score is not probability"), it MUST
// NOT be presented as a probability unless Calibrated is true.
type RiskComponent struct {
	Score       float64
	Calibrated  bool
	Confidence  Confidence
	ReasonCodes []ReasonCode
}

// DataQuality summarizes how much an overall evaluation should be
// trusted, independent of any single component's own confidence — e.g. a
// high-confidence risk score computed from stale quota data is still a
// data-quality problem the caller needs to see.
type DataQuality struct {
	Confidence    Confidence
	StaleInputs   []string // names of measurement sources past StaleAfter
	MissingInputs []string // names of measurement sources entirely absent
}
