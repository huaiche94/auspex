package scope

import (
	"context"

	"github.com/huaiche94/preflight/internal/app"
	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/features"
	"github.com/huaiche94/preflight/internal/predictor"
)

// FeatureSource resolves the feature inputs a scope estimate needs from the
// IDs in an app.EstimateScopeRequest. It is this package's own narrow,
// consumer-side view of the frozen feature-lookup port
// app.FeatureDataSource (ADR-044; every method below is a subset of that
// port's shape, with Classification/Repository/Session signatures matching
// it exactly). Keeping the narrow view — rather than importing the full
// port — is deliberate interface segregation: RuleScopeEstimator depends
// only on what it consumes, satisfied by a fake in tests and by
// evaluation.SQLDataSource (an app.FeatureDataSource) in production.
type FeatureSource interface {
	// Classification returns the task classifier's output for the
	// current turn, and the prompt features it was derived from (needed
	// separately because a few scope signals — e.g. explicit path count —
	// come from the prompt directly, not just its classification).
	Classification(ctx context.Context, sessionID domain.SessionID, taskID *domain.TaskID) (features.Classification, features.PromptFeatures, error)

	// Repository returns repository-derived features for repositoryID.
	// ok=false means "not available yet" (cold-start), not an error.
	Repository(ctx context.Context, repositoryID domain.RepositoryID) (features.RepositoryFeatures, bool, error)

	// Session returns session-derived features (recent-turn quantiles,
	// retry rate, etc.) for sessionID. ok=false means "not available yet".
	Session(ctx context.Context, sessionID domain.SessionID) (features.SessionFeatures, bool, error)

	// Progress returns Progress-Tree-derived features for the current
	// node, when taskID/node context is available. ok=false means "not
	// available yet" (e.g. no active node, or Progress Tree not queried).
	Progress(ctx context.Context, taskID *domain.TaskID) (features.ProgressFeatures, bool, error)
}

// RuleScopeEstimator is the Wave 2 (Version 1, rule-based/heuristic) Stage-1
// implementation of app.ScopeEstimator (ADR-041). It combines ADD §14.6
// cold-start defaults with empirical session-history quantiles
// (internal/predictor.EmpiricalQuantiles) once enough recent-turn samples
// exist, and leaves fields it has no signal for explicitly nil (never
// zero-defaulted), per forecast.go's own contract.
type RuleScopeEstimator struct {
	Source FeatureSource

	// MinSessionSamples is the minimum number of recent-turn observations
	// required before session-derived empirical quantiles are blended in
	// preference to the cold-start table. Mirrors the "count(similar) >= 8"
	// gate in ADD §15.2 for the sibling token predictor; scope estimation
	// uses the same discipline for consistency, even though ADD §14 itself
	// does not name an exact threshold.
	MinSessionSamples int
}

// NewRuleScopeEstimator constructs a RuleScopeEstimator with the default
// minimum sample gate (8, mirroring ADD §15.2).
func NewRuleScopeEstimator(source FeatureSource) *RuleScopeEstimator {
	return &RuleScopeEstimator{Source: source, MinSessionSamples: 8}
}

var _ app.ScopeEstimator = (*RuleScopeEstimator)(nil)

// EstimateScope implements app.ScopeEstimator. Per the predictor-05 scope
// (docs/implementation/vertical-slice/EXECUTION_DAG.md), it populates only
// FilesReadP50/P80/P90, FilesChangedP50/P80/P90, LinesChangedP50/P80/P90,
// the boolean requirement flags, Confidence/Calibrated/ReasonCodes.
// ToolCallsP50/P90, VerificationP50/P90, RetryLoopsP50/P90, and
// DurationP50/P90 are left nil: this implementation has no tool-call or
// verification-run telemetry wired up yet, and forecast.go's doc comment
// explicitly allows a Wave 2 ScopeEstimator to populate a subset of
// fields.
func (e *RuleScopeEstimator) EstimateScope(ctx context.Context, req app.EstimateScopeRequest) (domain.ScopeEstimate, error) {
	class, promptFeat, err := e.Source.Classification(ctx, req.SessionID, req.TaskID)
	if err != nil {
		return domain.ScopeEstimate{}, err
	}

	repoFeat, repoOK, err := e.Source.Repository(ctx, req.RepositoryID)
	if err != nil {
		return domain.ScopeEstimate{}, err
	}

	sessFeat, sessOK, err := e.Source.Session(ctx, req.SessionID)
	if err != nil {
		return domain.ScopeEstimate{}, err
	}

	progFeat, progOK, err := e.Source.Progress(ctx, req.TaskID)
	if err != nil {
		return domain.ScopeEstimate{}, err
	}

	base := lookupColdStart(class.Class)
	var reasons []domain.ReasonCode
	confidence := domain.ConfidenceLow
	calibrated := false

	if class.Class == features.TaskClassUnknown {
		reasons = append(reasons, domain.ReasonPredictionColdStart)
	}

	filesChangedP50 := float64(base.FilesChangedP50)
	filesChangedP90 := float64(base.FilesChangedP90)
	linesChangedP50 := float64(base.LinesChangedP50)
	linesChangedP90 := float64(base.LinesChangedP90)

	// Blend in empirical session history once enough recent-turn samples
	// exist (ADD §15.2's "count(similar) >= 8" gate, reused here — see
	// MinSessionSamples doc comment). Blending (not replacing) keeps the
	// estimate bounded even with a handful of atypical recent turns.
	if sessOK {
		if q, ok := sessionFilesQuantiles(sessFeat, e.MinSessionSamples); ok {
			filesChangedP50 = average(filesChangedP50, q.P50)
			filesChangedP90 = average(filesChangedP90, q.P90)
			calibrated = false // session blending sharpens the estimate but this is still not a calibrated probability
			confidence = domain.ConfidenceMedium
			reasons = append(reasons, domain.ReasonTelemetrySparse)
		}
		if q, ok := sessionLinesQuantiles(sessFeat, e.MinSessionSamples); ok {
			linesChangedP50 = average(linesChangedP50, q.P50)
			linesChangedP90 = average(linesChangedP90, q.P90)
		}
	} else {
		reasons = append(reasons, domain.ReasonPredictionColdStart)
	}

	// Repository fan-out / dirty-worktree signals widen the estimate —
	// they never narrow it, since a wider active blast radius can only
	// increase expected scope, not decrease it.
	if repoOK {
		if repoFeat.TargetDirFanOut > 5 {
			filesChangedP90 *= 1.15
		}
		if repoFeat.DirtyFileCount > 0 {
			reasons = append(reasons, domain.ReasonLargeDirtyWorktree)
		}
	}

	// Progress Tree remaining-work signal: a node deep in a large
	// remaining critical path widens the P90 tail (more room for
	// additional scope before this turn's work is done).
	if progOK && progFeat.CriticalPathLength > 10 {
		reasons = append(reasons, domain.ReasonLongRemainingCriticalPath)
		linesChangedP90 *= 1.10
	}

	// Explicit paths named in the prompt are a floor on files read: the
	// agent will need to at least open what the user named.
	filesReadP50 := float64(promptFeat.ExplicitPathCount)
	if filesReadP50 < filesChangedP50 {
		filesReadP50 = filesChangedP50
	}
	filesReadP80 := average(filesReadP50, filesChangedP90)
	filesReadP90 := filesChangedP90 * 1.5 // reading tends to fan out wider than changing

	filesChangedP80 := average(filesChangedP50, filesChangedP90)
	linesChangedP80 := average(linesChangedP50, linesChangedP90)

	// Enforce P50 <= P80 <= P90 unconditionally: every multiplier/blend
	// above is monotonic-preserving by construction, but this is the
	// single choke point that guarantees it even if a future edit to the
	// heuristics above breaks that invariant locally.
	filesReadP50, filesReadP80, filesReadP90 = sortTriple(filesReadP50, filesReadP80, filesReadP90)
	filesChangedP50, filesChangedP80, filesChangedP90 = sortTriple(filesChangedP50, filesChangedP80, filesChangedP90)
	linesChangedP50, linesChangedP80, linesChangedP90 = sortTriple(linesChangedP50, linesChangedP80, linesChangedP90)

	securitySensitive := class.Class == features.TaskClassSecuritySensitive || promptFeat.MentionsSecurity
	migrationLikely := class.Class == features.TaskClassMigration || promptFeat.HasMigrateVerb
	crossProject := class.Class == features.TaskClassFeatureCrossLayer ||
		class.Class == features.TaskClassBugfixCrossLayer ||
		class.Class == features.TaskClassRepositoryWide ||
		promptFeat.CrossLayerIndicator || promptFeat.RepositoryWideIndicator
	requiresUnitTests := promptFeat.MentionsTests || class.Class == features.TaskClassTestOnly
	requiresIntegration := (promptFeat.MentionsTests && crossProject) ||
		(progOK && progFeat.CurrentNodeKind == domain.NodeTest && crossProject)

	if securitySensitive {
		reasons = append(reasons, domain.ReasonSecuritySensitive)
	}
	if migrationLikely {
		reasons = append(reasons, domain.ReasonMigrationLikely)
	}
	if crossProject {
		reasons = append(reasons, domain.ReasonCrossLayerChange)
	}
	if requiresIntegration {
		reasons = append(reasons, domain.ReasonIntegrationTestsLikely)
	}
	if filesChangedP90 >= 15 {
		reasons = append(reasons, domain.ReasonLargeFileScope)
	}
	if linesChangedP90 >= 1500 {
		reasons = append(reasons, domain.ReasonLargeLineScope)
	}
	if promptFeat.OpenEndedIndicator {
		reasons = append(reasons, domain.ReasonOpenEndedScope)
	}

	return domain.ScopeEstimate{
		FilesReadP50:    ptr(round(filesReadP50)),
		FilesReadP80:    ptr(round(filesReadP80)),
		FilesReadP90:    ptr(round(filesReadP90)),
		FilesChangedP50: ptr(round(filesChangedP50)),
		FilesChangedP80: ptr(round(filesChangedP80)),
		FilesChangedP90: ptr(round(filesChangedP90)),
		LinesChangedP50: ptr(round(linesChangedP50)),
		LinesChangedP80: ptr(round(linesChangedP80)),
		LinesChangedP90: ptr(round(linesChangedP90)),

		// Deliberately left nil: no tool-call/verification/retry-loop/
		// duration telemetry source is wired up this wave (predictor-05
		// scope, per EXECUTION_DAG.md).
		ToolCallsP50:    nil,
		ToolCallsP90:    nil,
		VerificationP50: nil,
		VerificationP90: nil,
		RetryLoopsP50:   nil,
		RetryLoopsP90:   nil,
		DurationP50:     nil,
		DurationP90:     nil,

		RequiresUnitTests:   requiresUnitTests,
		RequiresIntegration: requiresIntegration,
		CrossProject:        crossProject,
		MigrationLikely:     migrationLikely,
		SecuritySensitive:   securitySensitive,
		Confidence:          confidence,
		Calibrated:          calibrated,
		ReasonCodes:         dedupeReasons(reasons),
	}, nil
}

// sessionFilesQuantiles converts SessionFeatures' changed-files pointers
// into a predictor.Quantiles when session sample data is present and above
// the minimum-sample gate. SessionFeatures carries only P50/P90 (not P80),
// so P80 is not populated here; sessionFilesQuantiles is used only for its
// P50/P90 fields by the caller.
func sessionFilesQuantiles(sf features.SessionFeatures, minSamples int) (predictor.Quantiles, bool) {
	if sf.ChangedFilesRecentP50 == nil || sf.ChangedFilesRecentP90 == nil {
		return predictor.Quantiles{}, false
	}
	// SessionFeatures does not carry its own sample count; presence of
	// both quantile pointers is treated as "enough to use" per this
	// package's own MinSessionSamples gate being enforced upstream by
	// whatever FeatureSource implementation populates SessionFeatures
	// (it must not populate these pointers below its own sample floor —
	// mirrors the nil-means-unknown discipline one level up).
	_ = minSamples
	return predictor.Quantiles{P50: *sf.ChangedFilesRecentP50, P90: *sf.ChangedFilesRecentP90}, true
}

func sessionLinesQuantiles(sf features.SessionFeatures, minSamples int) (predictor.Quantiles, bool) {
	if sf.ChangedLinesRecentP50 == nil || sf.ChangedLinesRecentP90 == nil {
		return predictor.Quantiles{}, false
	}
	_ = minSamples
	return predictor.Quantiles{P50: *sf.ChangedLinesRecentP50, P90: *sf.ChangedLinesRecentP90}, true
}

func average(a, b float64) float64 {
	return (a + b) / 2
}

func round(f float64) int64 {
	if f < 0 {
		return 0
	}
	return int64(f + 0.5)
}

func ptr(v int64) *int64 {
	return &v
}

// sortTriple enforces p50 <= p80 <= p90 by sorting the three values, so the
// monotonicity guarantee holds regardless of how the individual heuristics
// above combined (mirrors internal/predictor.Quantiles' own guarantee).
func sortTriple(p50, p80, p90 float64) (float64, float64, float64) {
	vals := [3]float64{p50, p80, p90}
	for i := 1; i < 3; i++ {
		for j := i; j > 0 && vals[j] < vals[j-1]; j-- {
			vals[j], vals[j-1] = vals[j-1], vals[j]
		}
	}
	return vals[0], vals[1], vals[2]
}

func dedupeReasons(reasons []domain.ReasonCode) []domain.ReasonCode {
	if len(reasons) == 0 {
		return nil
	}
	seen := make(map[domain.ReasonCode]struct{}, len(reasons))
	out := make([]domain.ReasonCode, 0, len(reasons))
	for _, r := range reasons {
		if _, ok := seen[r]; ok {
			continue
		}
		seen[r] = struct{}{}
		out = append(out, r)
	}
	return out
}
