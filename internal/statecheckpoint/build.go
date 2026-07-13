// build.go: assembles a Manifest from the Progress Tree state a caller
// already has in hand (BuildInput), independent of how that state was
// gathered. internal/progress's CompleteNode protocol (checkpoint-a04) is
// this function's primary caller — it reads node/edge/artifact rows
// inside its own transaction and hands the resulting snapshot here rather
// than this package reaching into internal/progress's stores directly,
// keeping the Part A internal boundary (stores vs. checkpoint manifest)
// as real as the Part A/Part B boundary agents/checkpoint.md documents.
package statecheckpoint

import (
	"time"

	"github.com/huaiche94/auspex/internal/domain"
)

// BuildInput is everything Build needs to assemble a Manifest. Every slice
// field is defensively copied by Build's callers' responsibility, not by
// Build itself (Build does not mutate its input).
type BuildInput struct {
	CheckpointID   domain.StateCheckpointID
	TaskID         domain.TaskID
	CreatedAt      time.Time
	ProgressTree   ProgressTreeSummary
	Artifacts      []ArtifactSummary
	Repository     RepositoryInfo
	Provider       ProviderInfo
	Quota          []QuotaObservationRef
	ContextUsedPct *float64
	NextAction     NextActionInfo
	Resume         ResumeInfo
}

// Build assembles an unsealed Manifest (IntegritySHA256 empty) from in.
// Callers must call Seal (or rely on their own store's Insert path calling
// it) before persisting — Build itself never computes the digest, keeping
// "assemble the document" and "seal it" as two distinct, individually
// testable steps.
func Build(in BuildInput) Manifest {
	return Manifest{
		SchemaVersion: SchemaVersion,
		CheckpointID:  in.CheckpointID,
		TaskID:        in.TaskID,
		CreatedAt:     in.CreatedAt,
		ProgressTree:  in.ProgressTree,
		Artifacts:     in.Artifacts,
		Repository:    in.Repository,
		Provider:      in.Provider,
		Quota:         in.Quota,
		Context:       ContextInfo{UsedPercent: in.ContextUsedPct},
		NextAction:    in.NextAction,
		Resume:        in.Resume,
	}
}
