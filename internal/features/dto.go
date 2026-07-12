package features

import (
	"time"

	"github.com/huaiche94/preflight/internal/domain"
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
