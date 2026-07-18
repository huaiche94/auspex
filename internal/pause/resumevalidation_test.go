package pause_test

import (
	"context"
	"errors"
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/pause"
	"github.com/huaiche94/auspex/internal/scheduler"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
)

// --- test doubles for ValidateResume's four narrow seams --------------------

type fakeQuotaReader struct {
	fn func(ctx context.Context, sessionID domain.SessionID, limitID string) (domain.QuotaObservation, error)
}

func (f fakeQuotaReader) ReadCurrentQuota(ctx context.Context, sessionID domain.SessionID, limitID string) (domain.QuotaObservation, error) {
	return f.fn(ctx, sessionID, limitID)
}

type fakeRepoFingerprintReader struct {
	fn func(ctx context.Context, worktreeID domain.WorktreeID) (pause.RepoFingerprint, error)
}

func (f fakeRepoFingerprintReader) ReadCurrentFingerprint(ctx context.Context, worktreeID domain.WorktreeID) (pause.RepoFingerprint, error) {
	return f.fn(ctx, worktreeID)
}

type fakeSessionCapabilityReader struct {
	fn func(ctx context.Context, sessionID domain.SessionID) (pause.SessionCapabilitySnapshot, error)
}

func (f fakeSessionCapabilityReader) ReadSessionCapability(ctx context.Context, sessionID domain.SessionID) (pause.SessionCapabilitySnapshot, error) {
	return f.fn(ctx, sessionID)
}

func ptrFloat(f float64) *float64 { return &f }

func okQuotaBaseline() domain.QuotaObservation {
	return domain.QuotaObservation{LimitID: "limit-1", UsedPercent: ptrFloat(50)}
}

func okQuotaReader() pause.QuotaSnapshotReader {
	return fakeQuotaReader{fn: func(_ context.Context, _ domain.SessionID, _ string) (domain.QuotaObservation, error) {
		return domain.QuotaObservation{LimitID: "limit-1", UsedPercent: ptrFloat(40)}, nil
	}}
}

func okRepoVerifier() *fakes.FakeRepositoryCheckpointService {
	return &fakes.FakeRepositoryCheckpointService{
		VerifyFunc: func(_ context.Context, id domain.RepositoryCheckpointID) (app.RepositoryCheckpointVerification, error) {
			return app.RepositoryCheckpointVerification{ID: id, Valid: true}, nil
		},
	}
}

func okRepoFingerprintReader() pause.RepoFingerprintReader {
	return fakeRepoFingerprintReader{fn: func(_ context.Context, _ domain.WorktreeID) (pause.RepoFingerprint, error) {
		return pause.RepoFingerprint{HeadOID: "head-1"}, nil
	}}
}

func okSessionReader() pause.SessionCapabilityReader {
	return fakeSessionCapabilityReader{fn: func(_ context.Context, _ domain.SessionID) (pause.SessionCapabilitySnapshot, error) {
		return pause.SessionCapabilitySnapshot{Resumable: true, Capabilities: domain.ProviderCapabilities{SessionResume: true}}, nil
	}}
}

func okEvaluations() *fakes.FakeEvaluationService {
	return &fakes.FakeEvaluationService{
		ConsumeAuthorizationFunc: func(_ context.Context, req app.ConsumeAuthorizationRequest) (app.Authorization, error) {
			return app.Authorization{ID: req.AuthorizationID, TurnID: req.TurnID}, nil
		},
	}
}

func okDeps() pause.ResumeValidationDeps {
	return pause.ResumeValidationDeps{
		Quota:                okQuotaReader(),
		RepositoryCheckpoint: okRepoVerifier(),
		RepoFingerprint:      okRepoFingerprintReader(),
		Session:              okSessionReader(),
		Evaluations:          okEvaluations(),
	}
}

func okReq() pause.ResumeValidationRequest {
	return pause.ResumeValidationRequest{
		SessionID:              "sess-1",
		QuotaBaseline:          okQuotaBaseline(),
		RepositoryCheckpointID: "repo-ckpt-1",
		BaselineGitHead:        "head-1",
		WorktreeID:             "wt-1",
		Authorization:          app.ConsumeAuthorizationRequest{AuthorizationID: "auth-1", TurnID: "turn-1"},
	}
}

// --- CheckQuotaSafety --------------------------------------------------------

func TestResumeValidation_CheckQuotaSafety_ImprovedQuotaPasses(t *testing.T) {
	reader := fakeQuotaReader{fn: func(_ context.Context, _ domain.SessionID, _ string) (domain.QuotaObservation, error) {
		return domain.QuotaObservation{LimitID: "limit-1", UsedPercent: ptrFloat(10)}, nil
	}}
	result, err := pause.CheckQuotaSafety(context.Background(), reader, "sess-1", okQuotaBaseline())
	if err != nil {
		t.Fatalf("CheckQuotaSafety: %v", err)
	}
	if !result.Pass {
		t.Fatalf("Pass = false, want true (quota improved): %+v", result)
	}
}

func TestResumeValidation_CheckQuotaSafety_UnchangedQuotaPasses(t *testing.T) {
	reader := fakeQuotaReader{fn: func(_ context.Context, _ domain.SessionID, _ string) (domain.QuotaObservation, error) {
		return domain.QuotaObservation{LimitID: "limit-1", UsedPercent: ptrFloat(50)}, nil
	}}
	result, err := pause.CheckQuotaSafety(context.Background(), reader, "sess-1", okQuotaBaseline())
	if err != nil {
		t.Fatalf("CheckQuotaSafety: %v", err)
	}
	if !result.Pass {
		t.Fatalf("Pass = false, want true (quota unchanged): %+v", result)
	}
}

// TestCheckQuotaSafety_WorseQuotaFails is the required test "unsafe quota
// reschedules"'s validation-level half: a session must not resume into a
// quota state that has gotten WORSE since it paused.
func TestResumeValidation_CheckQuotaSafety_WorseQuotaFails(t *testing.T) {
	reader := fakeQuotaReader{fn: func(_ context.Context, _ domain.SessionID, _ string) (domain.QuotaObservation, error) {
		return domain.QuotaObservation{LimitID: "limit-1", UsedPercent: ptrFloat(90)}, nil
	}}
	result, err := pause.CheckQuotaSafety(context.Background(), reader, "sess-1", okQuotaBaseline())
	if err != nil {
		t.Fatalf("CheckQuotaSafety: %v", err)
	}
	if result.Pass {
		t.Fatalf("Pass = true, want false (quota got worse)")
	}
	if result.Reason != pause.ReasonQuotaWorseSincePause {
		t.Fatalf("Reason = %q, want %q", result.Reason, pause.ReasonQuotaWorseSincePause)
	}
}

func TestResumeValidation_CheckQuotaSafety_ReachedTransitionFails(t *testing.T) {
	reader := fakeQuotaReader{fn: func(_ context.Context, _ domain.SessionID, _ string) (domain.QuotaObservation, error) {
		return domain.QuotaObservation{LimitID: "limit-1", UsedPercent: ptrFloat(50), Reached: true}, nil
	}}
	baseline := domain.QuotaObservation{LimitID: "limit-1", UsedPercent: ptrFloat(50), Reached: false}
	result, err := pause.CheckQuotaSafety(context.Background(), reader, "sess-1", baseline)
	if err != nil {
		t.Fatalf("CheckQuotaSafety: %v", err)
	}
	if result.Pass {
		t.Fatalf("Pass = true, want false (now Reached)")
	}
	if result.Reason != pause.ReasonQuotaWorseSincePause {
		t.Fatalf("Reason = %q, want %q", result.Reason, pause.ReasonQuotaWorseSincePause)
	}
}

func TestResumeValidation_CheckQuotaSafety_UnknownUsedPercentFailsClosed(t *testing.T) {
	reader := fakeQuotaReader{fn: func(_ context.Context, _ domain.SessionID, _ string) (domain.QuotaObservation, error) {
		return domain.QuotaObservation{LimitID: "limit-1", UsedPercent: nil}, nil
	}}
	result, err := pause.CheckQuotaSafety(context.Background(), reader, "sess-1", okQuotaBaseline())
	if err != nil {
		t.Fatalf("CheckQuotaSafety: %v", err)
	}
	if result.Pass {
		t.Fatalf("Pass = true, want false (unknown UsedPercent must fail closed, not be assumed safe)")
	}
}

func TestResumeValidation_CheckQuotaSafety_ReadErrorFailsClosedNotServiceError(t *testing.T) {
	reader := fakeQuotaReader{fn: func(_ context.Context, _ domain.SessionID, _ string) (domain.QuotaObservation, error) {
		return domain.QuotaObservation{}, errors.New("boom")
	}}
	result, err := pause.CheckQuotaSafety(context.Background(), reader, "sess-1", okQuotaBaseline())
	if err != nil {
		t.Fatalf("CheckQuotaSafety should report a failing CheckResult, not a Go error, for a read failure: %v", err)
	}
	if result.Pass || result.Reason != pause.ReasonQuotaReadUnavailable {
		t.Fatalf("result = %+v, want fail with ReasonQuotaReadUnavailable", result)
	}
}

func TestResumeValidation_CheckQuotaSafety_NilReaderFailsClosed(t *testing.T) {
	_, err := pause.CheckQuotaSafety(context.Background(), nil, "sess-1", okQuotaBaseline())
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("err = %v, want ErrCodeUnavailable", err)
	}
}

// --- CheckRepositoryCompatibility --------------------------------------------

func TestResumeValidation_CheckRepositoryCompatibility_NoChangePasses(t *testing.T) {
	result, err := pause.CheckRepositoryCompatibility(context.Background(), okRepoVerifier(), okRepoFingerprintReader(), "ckpt-1", "head-1", "wt-1", nil, pause.RepoChangePolicyAllowUnrelated)
	if err != nil {
		t.Fatalf("CheckRepositoryCompatibility: %v", err)
	}
	if !result.Pass {
		t.Fatalf("Pass = false, want true (no repo change): %+v", result)
	}
}

// TestCheckRepositoryCompatibility_OverlapBlocks is the required test "repo
// overlap blocks" verbatim: a repo change overlapping the paused work's own
// files must block resume regardless of policy.
func TestResumeValidation_CheckRepositoryCompatibility_OverlapBlocks(t *testing.T) {
	reader := fakeRepoFingerprintReader{fn: func(_ context.Context, _ domain.WorktreeID) (pause.RepoFingerprint, error) {
		return pause.RepoFingerprint{HeadOID: "head-2", ChangedPaths: []string{"internal/foo/bar.go"}}, nil
	}}
	for _, policy := range []pause.RepoChangePolicy{pause.RepoChangePolicyAllowUnrelated, pause.RepoChangePolicyBlockAny} {
		result, err := pause.CheckRepositoryCompatibility(context.Background(), okRepoVerifier(), reader, "ckpt-1", "head-1", "wt-1", []string{"internal/foo/bar.go"}, policy)
		if err != nil {
			t.Fatalf("CheckRepositoryCompatibility (policy=%s): %v", policy, err)
		}
		if result.Pass {
			t.Fatalf("policy=%s: Pass = true, want false (overlap always blocks)", policy)
		}
		if result.Reason != pause.ReasonRepositoryOverlapBlocks {
			t.Fatalf("policy=%s: Reason = %q, want %q", policy, result.Reason, pause.ReasonRepositoryOverlapBlocks)
		}
	}
}

// TestCheckRepositoryCompatibility_UnrelatedChangeFollowsPolicy is the
// required test "unrelated repo change follows configured policy" verbatim:
// a non-overlapping change is allowed under RepoChangePolicyAllowUnrelated
// and blocked under RepoChangePolicyBlockAny.
func TestResumeValidation_CheckRepositoryCompatibility_UnrelatedChangeFollowsPolicy(t *testing.T) {
	reader := fakeRepoFingerprintReader{fn: func(_ context.Context, _ domain.WorktreeID) (pause.RepoFingerprint, error) {
		return pause.RepoFingerprint{HeadOID: "head-2", ChangedPaths: []string{"docs/unrelated.md"}}, nil
	}}
	pausedWorkPaths := []string{"internal/foo/bar.go"}

	allow, err := pause.CheckRepositoryCompatibility(context.Background(), okRepoVerifier(), reader, "ckpt-1", "head-1", "wt-1", pausedWorkPaths, pause.RepoChangePolicyAllowUnrelated)
	if err != nil {
		t.Fatalf("CheckRepositoryCompatibility (allow): %v", err)
	}
	if !allow.Pass {
		t.Fatalf("policy=allow_unrelated: Pass = false, want true: %+v", allow)
	}

	block, err := pause.CheckRepositoryCompatibility(context.Background(), okRepoVerifier(), reader, "ckpt-1", "head-1", "wt-1", pausedWorkPaths, pause.RepoChangePolicyBlockAny)
	if err != nil {
		t.Fatalf("CheckRepositoryCompatibility (block): %v", err)
	}
	if block.Pass {
		t.Fatalf("policy=block_any: Pass = true, want false")
	}
	if block.Reason != pause.ReasonRepositoryUnrelatedChangeBlocked {
		t.Fatalf("Reason = %q, want %q", block.Reason, pause.ReasonRepositoryUnrelatedChangeBlocked)
	}
}

func TestResumeValidation_CheckRepositoryCompatibility_DefaultPolicyIsAllowUnrelated(t *testing.T) {
	reader := fakeRepoFingerprintReader{fn: func(_ context.Context, _ domain.WorktreeID) (pause.RepoFingerprint, error) {
		return pause.RepoFingerprint{HeadOID: "head-2", ChangedPaths: []string{"docs/unrelated.md"}}, nil
	}}
	result, err := pause.CheckRepositoryCompatibility(context.Background(), okRepoVerifier(), reader, "ckpt-1", "head-1", "wt-1", []string{"internal/foo/bar.go"}, "")
	if err != nil {
		t.Fatalf("CheckRepositoryCompatibility: %v", err)
	}
	if !result.Pass {
		t.Fatalf("Pass = false, want true (zero-value policy defaults to allow_unrelated)")
	}
}

func TestResumeValidation_CheckRepositoryCompatibility_InvalidCheckpointFailsClosed(t *testing.T) {
	verifier := &fakes.FakeRepositoryCheckpointService{
		VerifyFunc: func(_ context.Context, id domain.RepositoryCheckpointID) (app.RepositoryCheckpointVerification, error) {
			return app.RepositoryCheckpointVerification{ID: id, Valid: false}, nil
		},
	}
	result, err := pause.CheckRepositoryCompatibility(context.Background(), verifier, okRepoFingerprintReader(), "ckpt-1", "head-1", "wt-1", nil, pause.RepoChangePolicyAllowUnrelated)
	if err != nil {
		t.Fatalf("CheckRepositoryCompatibility: %v", err)
	}
	if result.Pass || result.Reason != pause.ReasonRepositoryCheckpointInvalid {
		t.Fatalf("result = %+v, want fail with ReasonRepositoryCheckpointInvalid", result)
	}
}

func TestResumeValidation_CheckRepositoryCompatibility_VerifyErrorFailsClosed(t *testing.T) {
	verifier := &fakes.FakeRepositoryCheckpointService{
		VerifyFunc: func(_ context.Context, _ domain.RepositoryCheckpointID) (app.RepositoryCheckpointVerification, error) {
			return app.RepositoryCheckpointVerification{}, errors.New("boom")
		},
	}
	result, err := pause.CheckRepositoryCompatibility(context.Background(), verifier, okRepoFingerprintReader(), "ckpt-1", "head-1", "wt-1", nil, pause.RepoChangePolicyAllowUnrelated)
	if err != nil {
		t.Fatalf("CheckRepositoryCompatibility should report a failing CheckResult, not a Go error: %v", err)
	}
	if result.Pass || result.Reason != pause.ReasonRepositoryCheckpointInvalid {
		t.Fatalf("result = %+v, want fail with ReasonRepositoryCheckpointInvalid", result)
	}
}

func TestResumeValidation_CheckRepositoryCompatibility_FingerprintReadErrorFailsClosed(t *testing.T) {
	reader := fakeRepoFingerprintReader{fn: func(_ context.Context, _ domain.WorktreeID) (pause.RepoFingerprint, error) {
		return pause.RepoFingerprint{}, errors.New("git unavailable")
	}}
	result, err := pause.CheckRepositoryCompatibility(context.Background(), okRepoVerifier(), reader, "ckpt-1", "head-1", "wt-1", nil, pause.RepoChangePolicyAllowUnrelated)
	if err != nil {
		t.Fatalf("CheckRepositoryCompatibility should report a failing CheckResult, not a Go error: %v", err)
	}
	if result.Pass || result.Reason != pause.ReasonRepositoryFingerprintUnavailable {
		t.Fatalf("result = %+v, want fail with ReasonRepositoryFingerprintUnavailable", result)
	}
}

func TestResumeValidation_CheckRepositoryCompatibility_NilDepsFailClosed(t *testing.T) {
	_, err := pause.CheckRepositoryCompatibility(context.Background(), nil, okRepoFingerprintReader(), "ckpt-1", "head-1", "wt-1", nil, pause.RepoChangePolicyAllowUnrelated)
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("nil verifier: err = %v, want ErrCodeUnavailable", err)
	}
	_, err = pause.CheckRepositoryCompatibility(context.Background(), okRepoVerifier(), nil, "ckpt-1", "head-1", "wt-1", nil, pause.RepoChangePolicyAllowUnrelated)
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("nil reader: err = %v, want ErrCodeUnavailable", err)
	}
}

// --- CheckSessionCapability ---------------------------------------------------

func TestResumeValidation_CheckSessionCapability_ResumableSessionPasses(t *testing.T) {
	result, err := pause.CheckSessionCapability(context.Background(), okSessionReader(), "sess-1", false)
	if err != nil {
		t.Fatalf("CheckSessionCapability: %v", err)
	}
	if !result.Pass {
		t.Fatalf("Pass = false, want true: %+v", result)
	}
}

func TestResumeValidation_CheckSessionCapability_NotResumableFails(t *testing.T) {
	reader := fakeSessionCapabilityReader{fn: func(_ context.Context, _ domain.SessionID) (pause.SessionCapabilitySnapshot, error) {
		return pause.SessionCapabilitySnapshot{Resumable: false}, nil
	}}
	result, err := pause.CheckSessionCapability(context.Background(), reader, "sess-1", false)
	if err != nil {
		t.Fatalf("CheckSessionCapability: %v", err)
	}
	if result.Pass || result.Reason != pause.ReasonSessionCapabilityInvalid {
		t.Fatalf("result = %+v, want fail with ReasonSessionCapabilityInvalid", result)
	}
}

func TestResumeValidation_CheckSessionCapability_MissingRequiredCapabilityFails(t *testing.T) {
	reader := fakeSessionCapabilityReader{fn: func(_ context.Context, _ domain.SessionID) (pause.SessionCapabilitySnapshot, error) {
		return pause.SessionCapabilitySnapshot{Resumable: true, Capabilities: domain.ProviderCapabilities{SessionResume: false}}, nil
	}}
	result, err := pause.CheckSessionCapability(context.Background(), reader, "sess-1", true)
	if err != nil {
		t.Fatalf("CheckSessionCapability: %v", err)
	}
	if result.Pass {
		t.Fatalf("Pass = true, want false (SessionResume capability confirmed absent)")
	}
}

func TestResumeValidation_CheckSessionCapability_ReadErrorFailsClosed(t *testing.T) {
	reader := fakeSessionCapabilityReader{fn: func(_ context.Context, _ domain.SessionID) (pause.SessionCapabilitySnapshot, error) {
		return pause.SessionCapabilitySnapshot{}, errors.New("boom")
	}}
	result, err := pause.CheckSessionCapability(context.Background(), reader, "sess-1", false)
	if err != nil {
		t.Fatalf("CheckSessionCapability should report a failing CheckResult, not a Go error: %v", err)
	}
	if result.Pass || result.Reason != pause.ReasonSessionCapabilityUnavailable {
		t.Fatalf("result = %+v, want fail with ReasonSessionCapabilityUnavailable", result)
	}
}

func TestResumeValidation_CheckSessionCapability_NilReaderFailsClosed(t *testing.T) {
	_, err := pause.CheckSessionCapability(context.Background(), nil, "sess-1", false)
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("err = %v, want ErrCodeUnavailable", err)
	}
}

// --- CheckAuthorization: predictor-09/10 fake ------------------------------
//
// predictor-10's authorization-hardening pass is a concurrent sibling this
// same phase, not yet mergeable (per the task brief) — fakes.
// FakeEvaluationService (already populated by an earlier runtime phase) is
// used here for app.EvaluationService.ConsumeAuthorization, consistent with
// the established fake-then-swap pattern (e.g. runtime-a05's State
// Checkpoint step against checkpoint-a05).

func TestResumeValidation_CheckAuthorization_ValidUnconsumedAuthorizationPasses(t *testing.T) {
	result, err := pause.CheckAuthorization(context.Background(), okEvaluations(), app.ConsumeAuthorizationRequest{AuthorizationID: "auth-1", TurnID: "turn-1"})
	if err != nil {
		t.Fatalf("CheckAuthorization: %v", err)
	}
	if !result.Pass {
		t.Fatalf("Pass = false, want true: %+v", result)
	}
}

func TestResumeValidation_CheckAuthorization_RejectedAuthorizationFails(t *testing.T) {
	evaluations := &fakes.FakeEvaluationService{
		ConsumeAuthorizationFunc: func(_ context.Context, _ app.ConsumeAuthorizationRequest) (app.Authorization, error) {
			return app.Authorization{}, &domain.Error{Code: domain.ErrCodeConflict, Message: "authorization already consumed", Retryable: false}
		},
	}
	result, err := pause.CheckAuthorization(context.Background(), evaluations, app.ConsumeAuthorizationRequest{AuthorizationID: "auth-1", TurnID: "turn-1"})
	if err != nil {
		t.Fatalf("CheckAuthorization should report a failing CheckResult, not a Go error: %v", err)
	}
	if result.Pass || result.Reason != pause.ReasonAuthorizationInvalid {
		t.Fatalf("result = %+v, want fail with ReasonAuthorizationInvalid", result)
	}
}

func TestResumeValidation_CheckAuthorization_ServiceUnavailableDistinctReason(t *testing.T) {
	evaluations := &fakes.FakeEvaluationService{
		ConsumeAuthorizationFunc: func(_ context.Context, _ app.ConsumeAuthorizationRequest) (app.Authorization, error) {
			return app.Authorization{}, &domain.Error{Code: domain.ErrCodeUnavailable, Message: "storage unreachable", Retryable: true}
		},
	}
	result, err := pause.CheckAuthorization(context.Background(), evaluations, app.ConsumeAuthorizationRequest{AuthorizationID: "auth-1", TurnID: "turn-1"})
	if err != nil {
		t.Fatalf("CheckAuthorization should report a failing CheckResult, not a Go error: %v", err)
	}
	if result.Pass || result.Reason != pause.ReasonAuthorizationServiceUnavailable {
		t.Fatalf("result = %+v, want fail with ReasonAuthorizationServiceUnavailable (distinct from a rejected authorization)", result)
	}
}

func TestResumeValidation_CheckAuthorization_NilServiceFailsClosed(t *testing.T) {
	_, err := pause.CheckAuthorization(context.Background(), nil, app.ConsumeAuthorizationRequest{})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("err = %v, want ErrCodeUnavailable", err)
	}
}

// --- ValidateResume orchestration -------------------------------------------

func TestResumeValidation_ValidateResume_AllPassYieldsValidVerdict(t *testing.T) {
	result, err := pause.ValidateResume(context.Background(), okDeps(), okReq())
	if err != nil {
		t.Fatalf("ValidateResume: %v", err)
	}
	if !result.AllPass() {
		t.Fatalf("AllPass() = false, want true: %+v", result)
	}
	verdict := result.Verdict()
	if !verdict.Valid || verdict.QuotaUnsafe || verdict.Conflict {
		t.Fatalf("Verdict() = %+v, want {Valid: true}", verdict)
	}
}

// TestValidateResume_QuotaFailureAloneReschedules is the required test
// "unsafe quota reschedules" at the orchestration level: a quota failure
// with every other check passing must map to QuotaUnsafe (reschedule), not
// Conflict (block).
func TestResumeValidation_ValidateResume_QuotaFailureAloneReschedules(t *testing.T) {
	deps := okDeps()
	deps.Quota = fakeQuotaReader{fn: func(_ context.Context, _ domain.SessionID, _ string) (domain.QuotaObservation, error) {
		return domain.QuotaObservation{LimitID: "limit-1", UsedPercent: ptrFloat(95)}, nil
	}}
	result, err := pause.ValidateResume(context.Background(), deps, okReq())
	if err != nil {
		t.Fatalf("ValidateResume: %v", err)
	}
	if result.AllPass() {
		t.Fatalf("AllPass() = true, want false (quota check should fail)")
	}
	verdict := result.Verdict()
	if !verdict.QuotaUnsafe || verdict.Valid || verdict.Conflict {
		t.Fatalf("Verdict() = %+v, want {QuotaUnsafe: true} (quota-only failure reschedules, does not block)", verdict)
	}
}

// TestValidateResume_RepositoryOverlapYieldsConflictVerdict is the required
// test "repo overlap blocks" at the orchestration level: an overlapping
// repository change must map to Conflict, never Valid or QuotaUnsafe.
func TestResumeValidation_ValidateResume_RepositoryOverlapYieldsConflictVerdict(t *testing.T) {
	deps := okDeps()
	deps.RepoFingerprint = fakeRepoFingerprintReader{fn: func(_ context.Context, _ domain.WorktreeID) (pause.RepoFingerprint, error) {
		return pause.RepoFingerprint{HeadOID: "head-2", ChangedPaths: []string{"internal/foo/bar.go"}}, nil
	}}
	req := okReq()
	req.PausedWorkPaths = []string{"internal/foo/bar.go"}
	result, err := pause.ValidateResume(context.Background(), deps, req)
	if err != nil {
		t.Fatalf("ValidateResume: %v", err)
	}
	verdict := result.Verdict()
	if !verdict.Conflict || verdict.Valid || verdict.QuotaUnsafe {
		t.Fatalf("Verdict() = %+v, want {Conflict: true}", verdict)
	}
}

// TestValidateResume_UnrelatedChangeFollowsPolicy is the required test
// "unrelated repo change follows configured policy" at the orchestration
// level.
func TestResumeValidation_ValidateResume_UnrelatedChangeFollowsPolicy(t *testing.T) {
	deps := okDeps()
	deps.RepoFingerprint = fakeRepoFingerprintReader{fn: func(_ context.Context, _ domain.WorktreeID) (pause.RepoFingerprint, error) {
		return pause.RepoFingerprint{HeadOID: "head-2", ChangedPaths: []string{"docs/unrelated.md"}}, nil
	}}
	req := okReq()
	req.PausedWorkPaths = []string{"internal/foo/bar.go"}

	req.RepoPolicy = pause.RepoChangePolicyAllowUnrelated
	allowResult, err := pause.ValidateResume(context.Background(), deps, req)
	if err != nil {
		t.Fatalf("ValidateResume (allow): %v", err)
	}
	if !allowResult.Verdict().Valid {
		t.Fatalf("policy=allow_unrelated: Verdict() = %+v, want Valid", allowResult.Verdict())
	}

	req.RepoPolicy = pause.RepoChangePolicyBlockAny
	blockResult, err := pause.ValidateResume(context.Background(), deps, req)
	if err != nil {
		t.Fatalf("ValidateResume (block): %v", err)
	}
	if !blockResult.Verdict().Conflict {
		t.Fatalf("policy=block_any: Verdict() = %+v, want Conflict", blockResult.Verdict())
	}
}

func TestResumeValidation_ValidateResume_SessionInvalidYieldsConflictVerdict(t *testing.T) {
	deps := okDeps()
	deps.Session = fakeSessionCapabilityReader{fn: func(_ context.Context, _ domain.SessionID) (pause.SessionCapabilitySnapshot, error) {
		return pause.SessionCapabilitySnapshot{Resumable: false}, nil
	}}
	result, err := pause.ValidateResume(context.Background(), deps, okReq())
	if err != nil {
		t.Fatalf("ValidateResume: %v", err)
	}
	if !result.Verdict().Conflict {
		t.Fatalf("Verdict() = %+v, want Conflict", result.Verdict())
	}
}

func TestResumeValidation_ValidateResume_AuthorizationInvalidYieldsConflictVerdict(t *testing.T) {
	deps := okDeps()
	deps.Evaluations = &fakes.FakeEvaluationService{
		ConsumeAuthorizationFunc: func(_ context.Context, _ app.ConsumeAuthorizationRequest) (app.Authorization, error) {
			return app.Authorization{}, &domain.Error{Code: domain.ErrCodeConflict, Message: "already consumed"}
		},
	}
	result, err := pause.ValidateResume(context.Background(), deps, okReq())
	if err != nil {
		t.Fatalf("ValidateResume: %v", err)
	}
	if !result.Verdict().Conflict {
		t.Fatalf("Verdict() = %+v, want Conflict", result.Verdict())
	}
}

// TestValidateResume_QuotaPlusOtherFailureYieldsConflictNotReschedule proves
// Verdict()'s mapping requires quota to be the SOLE failure to reschedule —
// a simultaneous quota + repository failure must still block, not silently
// reschedule past an unresolved conflict.
func TestResumeValidation_ValidateResume_QuotaPlusOtherFailureYieldsConflictNotReschedule(t *testing.T) {
	deps := okDeps()
	deps.Quota = fakeQuotaReader{fn: func(_ context.Context, _ domain.SessionID, _ string) (domain.QuotaObservation, error) {
		return domain.QuotaObservation{LimitID: "limit-1", UsedPercent: ptrFloat(95)}, nil
	}}
	deps.Session = fakeSessionCapabilityReader{fn: func(_ context.Context, _ domain.SessionID) (pause.SessionCapabilitySnapshot, error) {
		return pause.SessionCapabilitySnapshot{Resumable: false}, nil
	}}
	result, err := pause.ValidateResume(context.Background(), deps, okReq())
	if err != nil {
		t.Fatalf("ValidateResume: %v", err)
	}
	verdict := result.Verdict()
	if !verdict.Conflict || verdict.QuotaUnsafe {
		t.Fatalf("Verdict() = %+v, want Conflict (quota is not the SOLE failure)", verdict)
	}
}

// TestValidateResume_EveryCheckRunsEvenAfterAnEarlierFailure proves
// ValidateResume does not short-circuit on the first FAILING check (as
// opposed to an erroring dependency) — a caller building a full audit
// trail needs every check's own result.
func TestResumeValidation_ValidateResume_EveryCheckRunsEvenAfterAnEarlierFailure(t *testing.T) {
	deps := okDeps()
	deps.Quota = fakeQuotaReader{fn: func(_ context.Context, _ domain.SessionID, _ string) (domain.QuotaObservation, error) {
		return domain.QuotaObservation{LimitID: "limit-1", UsedPercent: ptrFloat(95)}, nil
	}}
	result, err := pause.ValidateResume(context.Background(), deps, okReq())
	if err != nil {
		t.Fatalf("ValidateResume: %v", err)
	}
	if result.Quota.Pass {
		t.Fatalf("Quota.Pass = true, want false")
	}
	if !result.Repository.Pass || !result.Session.Pass || !result.Authorization.Pass {
		t.Fatalf("later checks did not run/pass despite an earlier check's own FAILURE (not an error): %+v", result)
	}
}

// --- ValidateResume fail-closed on dependency errors ------------------------

func TestResumeValidation_ValidateResume_NilDepsFailClosed(t *testing.T) {
	cases := []struct {
		name string
		deps pause.ResumeValidationDeps
	}{
		{"Quota", func() pause.ResumeValidationDeps { d := okDeps(); d.Quota = nil; return d }()},
		{"RepositoryCheckpoint", func() pause.ResumeValidationDeps { d := okDeps(); d.RepositoryCheckpoint = nil; return d }()},
		{"RepoFingerprint", func() pause.ResumeValidationDeps { d := okDeps(); d.RepoFingerprint = nil; return d }()},
		{"Session", func() pause.ResumeValidationDeps { d := okDeps(); d.Session = nil; return d }()},
		{"Evaluations", func() pause.ResumeValidationDeps { d := okDeps(); d.Evaluations = nil; return d }()},
	}
	for _, tc := range cases {
		_, err := pause.ValidateResume(context.Background(), tc.deps, okReq())
		var derr *domain.Error
		if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
			t.Errorf("missing %s: err = %v, want ErrCodeUnavailable", tc.name, err)
		}
	}
}

func TestResumeValidation_ValidateResume_ValidatesSessionID(t *testing.T) {
	req := okReq()
	req.SessionID = ""
	_, err := pause.ValidateResume(context.Background(), okDeps(), req)
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("err = %v, want ErrCodeValidation", err)
	}
}

// TestResumeValidation_ValidateResume_DownstreamReadFailureSurfacesAsCheckResult
// proves a downstream service ERROR (as opposed to a nil/missing
// dependency, a composition bug) is reported through the normal
// ResumeValidationResult channel — a failing CheckResult with an
// "_UNAVAILABLE" reason code — not a Go error, and does not prevent later
// checks from still running. This is fail-closed (the check does not
// pass) without collapsing "we could not ask" and "definitely broken" into
// an opaque panic-shaped error a caller/audit trail cannot label.
func TestResumeValidation_ValidateResume_DownstreamReadFailureSurfacesAsCheckResult(t *testing.T) {
	deps := okDeps()
	deps.RepositoryCheckpoint = &fakes.FakeRepositoryCheckpointService{
		// VerifyFunc left unconfigured: FakeRepositoryCheckpointService
		// fails loud (ErrCodeUnavailable) on an unconfigured method,
		// mirroring internal/testutil/fakes' documented pattern (see
		// wiring_test.go's own use of the same convention) rather than
		// this test hand-rolling a distinct error type — this is exactly
		// the shape a real Verify-call-fails scenario takes.
	}
	result, err := pause.ValidateResume(context.Background(), deps, okReq())
	if err != nil {
		t.Fatalf("ValidateResume: want a nil Go error (the failure belongs in the CheckResult), got %v", err)
	}
	if result.Repository.Pass {
		t.Fatal("Repository.Pass = true, want false (Verify errored)")
	}
	if result.Repository.Reason != pause.ReasonRepositoryCheckpointInvalid {
		t.Fatalf("Repository.Reason = %q, want %q", result.Repository.Reason, pause.ReasonRepositoryCheckpointInvalid)
	}
	if !result.Session.Pass || !result.Authorization.Pass {
		t.Fatalf("later checks did not still run after an earlier downstream read failure: %+v", result)
	}
	if !result.Verdict().Conflict {
		t.Fatalf("Verdict() = %+v, want Conflict", result.Verdict())
	}
}

// --- RescheduleWakeJobOnQuotaUnsafe ------------------------------------------

type fakeWakeJobRescheduler struct {
	failFunc   func(ctx context.Context, id domain.WakeJobID, owner string, reason string) (scheduler.Job, error)
	called     bool
	lastID     domain.WakeJobID
	lastOwner  string
	lastReason string
}

func (f *fakeWakeJobRescheduler) Fail(ctx context.Context, id domain.WakeJobID, owner string, reason string) (scheduler.Job, error) {
	f.called = true
	f.lastID = id
	f.lastOwner = owner
	f.lastReason = reason
	return f.failFunc(ctx, id, owner, reason)
}

// TestRescheduleWakeJobOnQuotaUnsafe_QuotaUnsafeCallsFail is the required
// test "unsafe quota reschedules" proven directly against the scheduler
// integration: a quota-unsafe ValidateResume result must drive the wake
// job's own retry/backoff machinery (scheduler.Store.Fail), not merely
// leave the pause record in Sleeping with a stale wake job.
func TestResumeValidation_RescheduleWakeJobOnQuotaUnsafe_QuotaUnsafeCallsFail(t *testing.T) {
	rescheduler := &fakeWakeJobRescheduler{
		failFunc: func(_ context.Context, id domain.WakeJobID, owner string, _ string) (scheduler.Job, error) {
			return scheduler.Job{ID: id, LeaseOwner: &owner, Status: scheduler.StatusScheduled}, nil
		},
	}
	deps := okDeps()
	deps.Quota = fakeQuotaReader{fn: func(_ context.Context, _ domain.SessionID, _ string) (domain.QuotaObservation, error) {
		return domain.QuotaObservation{LimitID: "limit-1", UsedPercent: ptrFloat(95)}, nil
	}}
	result, err := pause.ValidateResume(context.Background(), deps, okReq())
	if err != nil {
		t.Fatalf("ValidateResume: %v", err)
	}

	job, rescheduled, err := pause.RescheduleWakeJobOnQuotaUnsafe(context.Background(), rescheduler, "job-1", "owner-1", result)
	if err != nil {
		t.Fatalf("RescheduleWakeJobOnQuotaUnsafe: %v", err)
	}
	if !rescheduled {
		t.Fatal("rescheduled = false, want true")
	}
	if !rescheduler.called {
		t.Fatal("scheduler.Store.Fail was not called for a quota-unsafe verdict")
	}
	if rescheduler.lastID != "job-1" || rescheduler.lastOwner != "owner-1" {
		t.Fatalf("Fail called with (%q, %q), want (job-1, owner-1)", rescheduler.lastID, rescheduler.lastOwner)
	}
	if job.ID != "job-1" {
		t.Fatalf("job.ID = %q, want job-1", job.ID)
	}
}

func TestResumeValidation_RescheduleWakeJobOnQuotaUnsafe_ValidVerdictDoesNotReschedule(t *testing.T) {
	rescheduler := &fakeWakeJobRescheduler{
		failFunc: func(_ context.Context, id domain.WakeJobID, owner string, _ string) (scheduler.Job, error) {
			return scheduler.Job{ID: id}, nil
		},
	}
	result, err := pause.ValidateResume(context.Background(), okDeps(), okReq())
	if err != nil {
		t.Fatalf("ValidateResume: %v", err)
	}
	_, rescheduled, err := pause.RescheduleWakeJobOnQuotaUnsafe(context.Background(), rescheduler, "job-1", "owner-1", result)
	if err != nil {
		t.Fatalf("RescheduleWakeJobOnQuotaUnsafe: %v", err)
	}
	if rescheduled {
		t.Fatal("rescheduled = true, want false (verdict was Valid)")
	}
	if rescheduler.called {
		t.Fatal("scheduler.Store.Fail was called despite a Valid verdict")
	}
}

func TestResumeValidation_RescheduleWakeJobOnQuotaUnsafe_ConflictVerdictDoesNotReschedule(t *testing.T) {
	rescheduler := &fakeWakeJobRescheduler{
		failFunc: func(_ context.Context, id domain.WakeJobID, owner string, _ string) (scheduler.Job, error) {
			return scheduler.Job{ID: id}, nil
		},
	}
	deps := okDeps()
	deps.Session = fakeSessionCapabilityReader{fn: func(_ context.Context, _ domain.SessionID) (pause.SessionCapabilitySnapshot, error) {
		return pause.SessionCapabilitySnapshot{Resumable: false}, nil
	}}
	result, err := pause.ValidateResume(context.Background(), deps, okReq())
	if err != nil {
		t.Fatalf("ValidateResume: %v", err)
	}
	_, rescheduled, err := pause.RescheduleWakeJobOnQuotaUnsafe(context.Background(), rescheduler, "job-1", "owner-1", result)
	if err != nil {
		t.Fatalf("RescheduleWakeJobOnQuotaUnsafe: %v", err)
	}
	if rescheduled {
		t.Fatal("rescheduled = true, want false (verdict was Conflict, not QuotaUnsafe)")
	}
	if rescheduler.called {
		t.Fatal("scheduler.Store.Fail was called despite a Conflict verdict")
	}
}

func TestResumeValidation_RescheduleWakeJobOnQuotaUnsafe_NilReschedulerFailsClosed(t *testing.T) {
	result := pause.ResumeValidationResult{}
	_, _, err := pause.RescheduleWakeJobOnQuotaUnsafe(context.Background(), nil, "job-1", "owner-1", result)
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
		t.Fatalf("err = %v, want ErrCodeUnavailable", err)
	}
}

func TestResumeValidation_RescheduleWakeJobOnQuotaUnsafe_EmptyJobIDValidationError(t *testing.T) {
	rescheduler := &fakeWakeJobRescheduler{failFunc: func(_ context.Context, id domain.WakeJobID, _ string, _ string) (scheduler.Job, error) {
		return scheduler.Job{ID: id}, nil
	}}
	deps := okDeps()
	deps.Quota = fakeQuotaReader{fn: func(_ context.Context, _ domain.SessionID, _ string) (domain.QuotaObservation, error) {
		return domain.QuotaObservation{LimitID: "limit-1", UsedPercent: ptrFloat(95)}, nil
	}}
	result, err := pause.ValidateResume(context.Background(), deps, okReq())
	if err != nil {
		t.Fatalf("ValidateResume: %v", err)
	}
	_, _, err = pause.RescheduleWakeJobOnQuotaUnsafe(context.Background(), rescheduler, "", "owner-1", result)
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("err = %v, want ErrCodeValidation", err)
	}
}

// --- FirstFailure -------------------------------------------------------------

func TestResumeValidationResult_FirstFailureReturnsFalseWhenAllPass(t *testing.T) {
	result, err := pause.ValidateResume(context.Background(), okDeps(), okReq())
	if err != nil {
		t.Fatalf("ValidateResume: %v", err)
	}
	if _, ok := result.FirstFailure(); ok {
		t.Fatal("FirstFailure: ok = true, want false when every check passes")
	}
}

func TestResumeValidationResult_FirstFailureReturnsQuotaBeforeLaterChecks(t *testing.T) {
	deps := okDeps()
	deps.Quota = fakeQuotaReader{fn: func(_ context.Context, _ domain.SessionID, _ string) (domain.QuotaObservation, error) {
		return domain.QuotaObservation{LimitID: "limit-1", UsedPercent: ptrFloat(95)}, nil
	}}
	deps.Session = fakeSessionCapabilityReader{fn: func(_ context.Context, _ domain.SessionID) (pause.SessionCapabilitySnapshot, error) {
		return pause.SessionCapabilitySnapshot{Resumable: false}, nil
	}}
	result, err := pause.ValidateResume(context.Background(), deps, okReq())
	if err != nil {
		t.Fatalf("ValidateResume: %v", err)
	}
	failure, ok := result.FirstFailure()
	if !ok {
		t.Fatal("FirstFailure: ok = false, want true")
	}
	if failure.Reason != pause.ReasonQuotaWorseSincePause {
		t.Fatalf("FirstFailure().Reason = %q, want the quota failure (fixed check order)", failure.Reason)
	}
}
