package scope

import (
	"context"
	"errors"
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/features"
)

// fakeSource is a minimal, fully-controllable FeatureSource for tests.
type fakeSource struct {
	class    features.Classification
	prompt   features.PromptFeatures
	classErr error
	repo     features.RepositoryFeatures
	repoOK   bool
	repoErr  error
	sess     features.SessionFeatures
	sessOK   bool
	sessErr  error
	prog     features.ProgressFeatures
	progOK   bool
	progErr  error
}

func (f fakeSource) Classification(ctx context.Context, sessionID domain.SessionID, taskID *domain.TaskID) (features.Classification, features.PromptFeatures, error) {
	return f.class, f.prompt, f.classErr
}

func (f fakeSource) Repository(ctx context.Context, repositoryID domain.RepositoryID) (features.RepositoryFeatures, bool, error) {
	return f.repo, f.repoOK, f.repoErr
}

func (f fakeSource) Session(ctx context.Context, sessionID domain.SessionID) (features.SessionFeatures, bool, error) {
	return f.sess, f.sessOK, f.sessErr
}

func (f fakeSource) Progress(ctx context.Context, taskID *domain.TaskID) (features.ProgressFeatures, bool, error) {
	return f.prog, f.progOK, f.progErr
}

var _ FeatureSource = fakeSource{}

func baseReq() app.EstimateScopeRequest {
	return app.EstimateScopeRequest{
		SessionID:    domain.SessionID("sess-1"),
		RepositoryID: domain.RepositoryID("repo-1"),
	}
}

// assertScopeMonotonic checks P50 <= P80 <= P90 across all three
// quantile-triples the estimator populates, reusing the discipline from
// predictor-04's quantile monotonicity tests.
func assertScopeMonotonic(t *testing.T, label string, est domain.ScopeEstimate) {
	t.Helper()
	check := func(name string, p50, p80, p90 *int64) {
		t.Helper()
		if p50 == nil || p80 == nil || p90 == nil {
			t.Fatalf("%s: %s: expected all three quantiles populated, got p50=%v p80=%v p90=%v", label, name, p50, p80, p90)
		}
		if *p50 > *p80 {
			t.Fatalf("%s: %s: monotonicity violated: P50=%d > P80=%d", label, name, *p50, *p80)
		}
		if *p80 > *p90 {
			t.Fatalf("%s: %s: monotonicity violated: P80=%d > P90=%d", label, name, *p80, *p90)
		}
	}
	check("FilesRead", est.FilesReadP50, est.FilesReadP80, est.FilesReadP90)
	check("FilesChanged", est.FilesChangedP50, est.FilesChangedP80, est.FilesChangedP90)
	check("LinesChanged", est.LinesChangedP50, est.LinesChangedP80, est.LinesChangedP90)
}

func TestEstimateScopeMonotonicity(t *testing.T) {
	cases := []struct {
		name   string
		source fakeSource
	}{
		{
			name: "cold start, unknown class, no repo/session/progress",
			source: fakeSource{
				class: features.Classification{Class: features.TaskClassUnknown, Confidence: domain.ConfidenceUnavailable},
			},
		},
		{
			name: "documentation-short, cold start",
			source: fakeSource{
				class: features.Classification{Class: features.TaskClassDocumentationShort, Confidence: domain.ConfidenceLow},
			},
		},
		{
			name: "repository-wide, cold start",
			source: fakeSource{
				class: features.Classification{Class: features.TaskClassRepositoryWide, Confidence: domain.ConfidenceLow},
			},
		},
		{
			name: "feature-cross-layer with repo fan-out and dirty worktree",
			source: fakeSource{
				class:  features.Classification{Class: features.TaskClassFeatureCrossLayer, Confidence: domain.ConfidenceLow},
				repo:   features.RepositoryFeatures{TargetDirFanOut: 12, DirtyFileCount: 3, DirtyLineCount: 40},
				repoOK: true,
			},
		},
		{
			name: "bugfix-local with session history blended in",
			source: fakeSource{
				class:  features.Classification{Class: features.TaskClassBugfixLocal, Confidence: domain.ConfidenceLow},
				sess:   sessionWithQuantiles(1, 9, 40, 320),
				sessOK: true,
			},
		},
		{
			name: "migration with long remaining critical path",
			source: fakeSource{
				class:  features.Classification{Class: features.TaskClassMigration, Confidence: domain.ConfidenceLow},
				prog:   features.ProgressFeatures{CriticalPathLength: 25},
				progOK: true,
			},
		},
		{
			name: "explicit paths named in prompt exceed cold-start files-changed floor",
			source: fakeSource{
				class:  features.Classification{Class: features.TaskClassDocumentationShort, Confidence: domain.ConfidenceLow},
				prompt: features.PromptFeatures{ExplicitPathCount: 25},
			},
		},
		{
			name: "zero-sample session pointers absent (nil) treated as no session data",
			source: fakeSource{
				class:  features.Classification{Class: features.TaskClassFeatureLocal, Confidence: domain.ConfidenceLow},
				sess:   features.SessionFeatures{}, // all pointers nil
				sessOK: true,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			est := NewRuleScopeEstimator(tc.source)
			got, err := est.EstimateScope(context.Background(), baseReq())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertScopeMonotonic(t, tc.name, got)
		})
	}
}

// TestEstimateScopeUnknownFieldsStayNil verifies the fields this
// implementation deliberately does not populate remain nil, not
// zero-defaulted (ADD principle 1 / Constitution §7, "unknown is not
// zero").
func TestEstimateScopeUnknownFieldsStayNil(t *testing.T) {
	source := fakeSource{
		class: features.Classification{Class: features.TaskClassFeatureLocal, Confidence: domain.ConfidenceLow},
	}
	est := NewRuleScopeEstimator(source)
	got, err := est.EstimateScope(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	nilChecks := []struct {
		name string
		val  *int64
	}{
		{"ToolCallsP50", got.ToolCallsP50},
		{"ToolCallsP90", got.ToolCallsP90},
		{"VerificationP50", got.VerificationP50},
		{"VerificationP90", got.VerificationP90},
		{"RetryLoopsP50", got.RetryLoopsP50},
		{"RetryLoopsP90", got.RetryLoopsP90},
	}
	for _, c := range nilChecks {
		if c.val != nil {
			t.Errorf("%s: expected nil (unknown), got %d (zero-defaulted or populated)", c.name, *c.val)
		}
	}

	populatedChecks := []struct {
		name string
		val  *int64
	}{
		{"FilesReadP50", got.FilesReadP50},
		{"FilesReadP80", got.FilesReadP80},
		{"FilesReadP90", got.FilesReadP90},
		{"FilesChangedP50", got.FilesChangedP50},
		{"FilesChangedP80", got.FilesChangedP80},
		{"FilesChangedP90", got.FilesChangedP90},
		{"LinesChangedP50", got.LinesChangedP50},
		{"LinesChangedP80", got.LinesChangedP80},
		{"LinesChangedP90", got.LinesChangedP90},
		{"DurationP50", got.DurationP50},
		{"DurationP90", got.DurationP90},
	}
	for _, c := range populatedChecks {
		if c.val == nil {
			t.Errorf("%s: expected populated (non-nil), got nil", c.name)
		}
	}

	// #62 Phase-1: duration is a positive, monotonic P50<=P90 estimate in
	// nanoseconds, and includes the per-turn overhead floor even for a
	// zero-scope class.
	if got.DurationP50 != nil && got.DurationP90 != nil {
		if *got.DurationP50 < int64(durationPerTurnOverhead) {
			t.Errorf("DurationP50 %d below per-turn overhead floor %d", *got.DurationP50, int64(durationPerTurnOverhead))
		}
		if *got.DurationP90 < *got.DurationP50 {
			t.Errorf("DurationP90 %d < DurationP50 %d (must be monotonic)", *got.DurationP90, *got.DurationP50)
		}
	}
}

func TestEstimateScopeColdStartReasonCode(t *testing.T) {
	source := fakeSource{
		class: features.Classification{Class: features.TaskClassUnknown, Confidence: domain.ConfidenceUnavailable},
	}
	est := NewRuleScopeEstimator(source)
	got, err := est.EstimateScope(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Calibrated {
		t.Errorf("expected Calibrated=false for a rule-based cold-start estimate")
	}
	if !containsReason(got.ReasonCodes, domain.ReasonPredictionColdStart) {
		t.Errorf("expected %s in reason codes, got %v", domain.ReasonPredictionColdStart, got.ReasonCodes)
	}
}

func TestEstimateScopeSecurityAndMigrationFlags(t *testing.T) {
	source := fakeSource{
		class:  features.Classification{Class: features.TaskClassSecuritySensitive, Confidence: domain.ConfidenceLow},
		prompt: features.PromptFeatures{MentionsSecurity: true},
	}
	est := NewRuleScopeEstimator(source)
	got, err := est.EstimateScope(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.SecuritySensitive {
		t.Errorf("expected SecuritySensitive=true")
	}
	if !containsReason(got.ReasonCodes, domain.ReasonSecuritySensitive) {
		t.Errorf("expected %s in reason codes, got %v", domain.ReasonSecuritySensitive, got.ReasonCodes)
	}
}

func TestEstimateScopeDeterministic(t *testing.T) {
	source := fakeSource{
		class:  features.Classification{Class: features.TaskClassFeatureCrossLayer, Confidence: domain.ConfidenceLow},
		prompt: features.PromptFeatures{ExplicitPathCount: 3, MentionsTests: true},
		repo:   features.RepositoryFeatures{TargetDirFanOut: 8, DirtyFileCount: 2},
		repoOK: true,
		sess:   sessionWithQuantiles(2, 8, 60, 400),
		sessOK: true,
		prog:   features.ProgressFeatures{CriticalPathLength: 4},
		progOK: true,
	}
	est := NewRuleScopeEstimator(source)

	first, err := est.EstimateScope(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := est.EstimateScope(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if *first.FilesChangedP50 != *second.FilesChangedP50 ||
		*first.FilesChangedP90 != *second.FilesChangedP90 ||
		*first.LinesChangedP50 != *second.LinesChangedP50 ||
		*first.LinesChangedP90 != *second.LinesChangedP90 {
		t.Fatalf("EstimateScope is not deterministic for identical input: first=%+v second=%+v", first, second)
	}
}

func TestEstimateScopePropagatesSourceError(t *testing.T) {
	wantErr := errors.New("boom")
	source := fakeSource{classErr: wantErr}
	est := NewRuleScopeEstimator(source)
	_, err := est.EstimateScope(context.Background(), baseReq())
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected error to propagate, got %v", err)
	}
}

func containsReason(reasons []domain.ReasonCode, target domain.ReasonCode) bool {
	for _, r := range reasons {
		if r == target {
			return true
		}
	}
	return false
}

func sessionWithQuantiles(filesP50, filesP90, linesP50, linesP90 float64) features.SessionFeatures {
	return features.SessionFeatures{
		ChangedFilesRecentP50: &filesP50,
		ChangedFilesRecentP90: &filesP90,
		ChangedLinesRecentP50: &linesP50,
		ChangedLinesRecentP90: &linesP90,
	}
}
