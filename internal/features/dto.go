package features

import (
	"time"

	"github.com/huaiche94/auspex/internal/domain"
)

// RepositoryFeatures holds repository-derived signals (ADD §14.2
// "Repository-derived"). All fields are already-derived scalars/flags —
// no raw file contents or paths beyond simple counts.
type RepositoryFeatures struct {
	WorktreeID domain.WorktreeID

	TrackedFileCount  int
	LanguageCount     int
	GoModuleCount     int
	GoPackageCount    int
	DotNetProjectRefs int

	DirtyFileCount int
	DirtyLineCount int

	TargetDirFanOut  int
	TestProjectCount int

	IsMonorepo             bool
	IsWorktree             bool
	RecentChangedPathCount int

	Confidence domain.Confidence
}

// SessionFeatures holds session-derived signals (ADD §14.2
// "Session-derived") — empirical quantiles over recent turns plus rolling
// counters. Quantile fields use pointer semantics: nil means unknown, never
// a substituted zero (CONTRACT_FREEZE.md "Unknown/null semantics").
type SessionFeatures struct {
	SessionID domain.SessionID

	RecentTurnUsageP50 *float64
	RecentTurnUsageP80 *float64
	RecentTurnUsageP90 *float64

	ChangedFilesRecentP50 *float64
	ChangedFilesRecentP90 *float64
	ChangedLinesRecentP50 *float64
	ChangedLinesRecentP90 *float64

	RetryRate          *float64
	TestFailureRate    *float64
	ToolOutputBytesP50 *int64

	ContextGrowthRateP50 *float64
	CompactionCount      int
	CheckpointAge        *time.Duration

	Confidence domain.Confidence
}

// ProgressFeatures holds Progress-Tree-derived signals (ADD §14.2
// "Progress Tree-derived").
type ProgressFeatures struct {
	TaskID domain.TaskID
	NodeID *domain.ProgressNodeID

	CurrentNodeKind      domain.ProgressNodeKind
	DescendantsRemaining int
	CriticalPathLength   int
	CompletedRatio       *float64

	RemainingArtifactSizeBytes *int64
	NodeHistoricalCostP50      *float64
	NodeHistoricalCostP90      *float64

	IsDocumentSection  bool
	UnresolvedBlockers int

	Confidence domain.Confidence
}

// ClassifierInput bundles every feature source ClassifyTask may draw on.
// Prompt is required; the others are optional (zero value = "not
// available yet") and only sharpen — never replace — the prompt signal.
type ClassifierInput struct {
	Prompt   PromptFeatures
	Progress *ProgressFeatures
}

// SimilarTurnCohortRung identifies which rung of the ADD §15.2 cohort
// fallback ladder answered a RecentSimilarTurnTokens lookup (#20 Phase 1;
// docs/backlog/provider-model-effort-features.md §3.4). Rungs are ordered
// most- to least-specific; the data source picks the highest rung whose
// sample count meets the §15.2 gate and reports which one it was, so the
// forecaster can attach an honest reason code instead of presenting a
// provider-wide sample set as a model-exact one.
type SimilarTurnCohortRung string

const (
	// CohortRungModelEffort: samples matched provider + model family +
	// effort — the full normalized triple.
	CohortRungModelEffort SimilarTurnCohortRung = "provider_model_effort"
	// CohortRungModelFamily: effort dropped; samples matched provider +
	// model family.
	CohortRungModelFamily SimilarTurnCohortRung = "provider_model_family"
	// CohortRungProvider: model dropped too; samples matched provider
	// only.
	CohortRungProvider SimilarTurnCohortRung = "provider"
	// CohortRungSession: the pre-ladder behavior — recent observations
	// for this exact session, regardless of identity labels. Also the
	// terminal rung when no wider rung meets the sample gate (its
	// samples may then be fewer than the gate; the forecaster's own
	// >= MinSimilarSamples check decides cold-start, unchanged).
	CohortRungSession SimilarTurnCohortRung = "session"
)

// SimilarTurnTokens is RecentSimilarTurnTokens' result: the raw
// total-token samples of the selected cohort rung, plus which rung
// selected them. Samples may be empty (no observations carry a
// total-token field yet — honest cold-start); Rung is always set.
type SimilarTurnTokens struct {
	Samples []float64
	Rung    SimilarTurnCohortRung
}
