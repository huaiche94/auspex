// resumevalidation.go: runtime-a08 — agents/runtime.md Part A deliverable 8,
// "Resume validation: quota safe; repository fingerprint compatible;
// session/provider capability valid; authorization/consent valid." This is
// the real check lifecycle.go's package comment names as its own explicitly
// documented gap: Resume (lifecycle.go, runtime-b07) drives the state
// machine from a CALLER-SUPPLIED verdict (ResumeRequest.Valid/QuotaUnsafe/
// Conflict); this file COMPUTES that verdict for real, so a caller (a
// future Part B wiring node) can pass ValidateResume's ResumeValidationResult
// straight into ResumeRequest instead of inventing its own answer.
//
// # Why four separate checkers, not one function
//
// Each of the four checks agents/runtime.md names is independently
// swappable and independently faked in tests (mirrors safepoint.go's
// CheckpointPersister/Interrupter split and persistphase.go's PersistDeps
// bundle): quota safety needs a live quota reader, repository compatibility
// needs both the frozen app.RepositoryCheckpointService.Verify AND a
// current-repo-state reader, session capability needs a provider-session
// reader, and authorization needs the frozen
// app.EvaluationService.ConsumeAuthorization. Bundling all four behind one
// interface would force every caller (including every test) to implement
// all four even when only one is under test — exactly the "God interface"
// Constitution §4 and agents/contract-integrator.md warn against.
//
// # Fail-closed discipline (DAG risk note: "the last line before
// # unattended code execution")
//
// Every checker returns (CheckResult, error), and the two failure channels
// mean two different things, deliberately:
//
//   - A downstream READ failure (the quota/repository/session/authorization
//     service itself errors when asked) is reported as a FAILING CheckResult
//     with an "_UNAVAILABLE"-suffixed reason code (e.g.
//     ReasonQuotaReadUnavailable), not a Go error. This is still fail-closed
//     — the check does not pass — but it flows through the same channel a
//     normal rejection does, so a caller building a full audit trail (e.g. a
//     resume_attempts row) can record WHY resume did not proceed, and a
//     human resolving a BlockedConflict via ADD §20.9's UI sees "session
//     capability unavailable" as a concrete reason, not a crash. This
//     mirrors CONTRACT_FREEZE.md's own error contract shape (a distinct
//     reason code, not a bare panic) applied at the validation-gate layer.
//   - A returned Go error is reserved for a COMPOSITION bug: a nil
//     dependency, or a malformed request (missing SessionID, etc.) — the
//     caller wired this wrong, as opposed to "the check ran and found a
//     problem." ValidateResume stops immediately on this class of error
//     without running any further checks, because a mis-wired caller's
//     later checks would be running against an already-invalid setup, not
//     because a downstream read failure aborts anything.
//
// Both channels are equally fail-closed in the sense that neither is ever
// silently treated as a pass (per the DAG's "the last line before
// unattended code execution" framing) — they are simply reported through
// the shape ("this is data for the audit trail" vs. "this is a bug to
// fix") that best matches which kind of failure occurred.
//
// # Order of checks
//
// Quota first (cheapest, most common failure — reschedule is a normal,
// expected outcome, not a conflict), then repository, then session
// capability, then authorization (the most expensive / most consequential
// check, since a consumed authorization cannot be un-consumed). This
// mirrors persistphase.go's own "cheapest/most-idempotent-safe first"
// ordering discipline: an early quota-unsafe result must not spend a
// one-time Authorization on a resume attempt that is about to be
// rescheduled anyway.
package pause

import (
	"context"
	"errors"
	"fmt"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/scheduler"
)

// ResumeValidationReasonCode is this package's own closed vocabulary for
// WHY a resume-validation check failed (mirrors Event/TriggerReason/
// Boundary: package-local, not persisted verbatim, not part of any frozen
// contract). Distinct from domain.ReasonCode (which explains a RISK
// SCORE's composition) for the same reason TriggerReason is distinct from
// it — this explains a VALIDATION GATE's decision, a different concept.
type ResumeValidationReasonCode string

const (
	// ReasonQuotaWorseSincePause: the current quota read is less safe (a
	// higher used-percent, or now Reached) than the observation recorded
	// at pause time — required test "unsafe quota reschedules".
	ReasonQuotaWorseSincePause ResumeValidationReasonCode = "QUOTA_WORSE_SINCE_PAUSE"
	// ReasonQuotaReadUnavailable: the current quota could not be read at
	// all. Fails closed (this is a state-integrity gate, not an
	// operational observation — see package doc comment).
	ReasonQuotaReadUnavailable ResumeValidationReasonCode = "QUOTA_READ_UNAVAILABLE"

	// ReasonRepositoryCheckpointInvalid: app.RepositoryCheckpointService.
	// Verify reported the checkpoint itself (taken at pause time) is no
	// longer intact — cannot compare against a corrupt baseline, so this
	// fails closed rather than skipping the comparison.
	ReasonRepositoryCheckpointInvalid ResumeValidationReasonCode = "REPOSITORY_CHECKPOINT_INVALID"
	// ReasonRepositoryOverlapBlocks: the repository changed since pause in
	// a way that OVERLAPS the paused work's own files — required test
	// "repo overlap blocks". Reuses the frozen domain.ReasonCode this
	// concept already has a name for
	// (domain.ReasonRepositoryChangedDuringSleep) as this reason's
	// Detail, not as a second enum value, per Constitution §6 rule 4 (no
	// ad hoc status/enum invention) applied here to reason vocabularies by
	// analogy: the frozen name is cited, not duplicated.
	ReasonRepositoryOverlapBlocks ResumeValidationReasonCode = "REPOSITORY_OVERLAP_BLOCKS"
	// ReasonRepositoryUnrelatedChangeBlocked: the repository changed since
	// pause, the change does NOT overlap the paused work's own files, but
	// the configured RepoChangePolicy is RepoChangePolicyBlockAny —
	// required test "unrelated repo change follows configured policy",
	// the block-policy branch.
	ReasonRepositoryUnrelatedChangeBlocked ResumeValidationReasonCode = "REPOSITORY_UNRELATED_CHANGE_BLOCKED"
	// ReasonRepositoryFingerprintUnavailable: the current repository
	// fingerprint could not be read at all. Fails closed.
	ReasonRepositoryFingerprintUnavailable ResumeValidationReasonCode = "REPOSITORY_FINGERPRINT_UNAVAILABLE"

	// ReasonSessionCapabilityInvalid: the provider session is no longer
	// resumable (capability reader reported Valid: false, or a capability
	// this resume needs is confirmed absent).
	ReasonSessionCapabilityInvalid ResumeValidationReasonCode = "SESSION_CAPABILITY_INVALID"
	// ReasonSessionCapabilityUnavailable: the session/capability reader
	// could not be reached at all. Fails closed.
	ReasonSessionCapabilityUnavailable ResumeValidationReasonCode = "SESSION_CAPABILITY_UNAVAILABLE"

	// ReasonAuthorizationInvalid: ConsumeAuthorization reported (via its
	// error return) that no valid, unconsumed authorization exists for
	// this resume — expired, already consumed, or never issued.
	ReasonAuthorizationInvalid ResumeValidationReasonCode = "AUTHORIZATION_INVALID"
	// ReasonAuthorizationServiceUnavailable: the authorization service
	// itself could not be reached (as opposed to reaching it and being
	// told the authorization is invalid). Fails closed.
	ReasonAuthorizationServiceUnavailable ResumeValidationReasonCode = "AUTHORIZATION_SERVICE_UNAVAILABLE"
)

// CheckResult is the uniform pass/fail-plus-reason shape every one of the
// four checks returns (agents/runtime.md: "each producing a clear
// pass/fail plus a reason code on failure"). Reason/Detail are zero-value
// when Pass is true.
type CheckResult struct {
	Pass   bool
	Reason ResumeValidationReasonCode
	Detail string
}

func passResult() CheckResult { return CheckResult{Pass: true} }

func failResult(reason ResumeValidationReasonCode, detail string) CheckResult {
	return CheckResult{Pass: false, Reason: reason, Detail: detail}
}

// --- 1. Quota safe ----------------------------------------------------------

// QuotaSnapshotReader is the narrow seam ValidateResume's quota check uses
// to read the CURRENT quota state at resume time. Deliberately narrower
// than the frozen app.QuotaReader (which takes a full QuotaRequest and
// returns every limit) — this check only needs "the current observation
// for the same limit the pause-time baseline recorded," so a caller (a
// future integration node) adapts app.QuotaReader behind this interface
// rather than this package depending on the wider frozen port directly, in
// case a session has several concurrent limits and only one is relevant to
// a given pause.
type QuotaSnapshotReader interface {
	ReadCurrentQuota(ctx context.Context, sessionID domain.SessionID, limitID string) (domain.QuotaObservation, error)
}

// quotaWorseThan reports whether current represents a LESS safe quota
// state than baseline — the required test's exact framing: "a session
// shouldn't resume into a quota state that's gotten worse, not better,
// since it paused." Reached is the strongest signal (any transition INTO
// Reached is worse; baseline already Reached with current still Reached is
// unchanged, not worse, and is caught by the equal-UsedPercent case below
// or passes if current genuinely recovered per a reset). Absent either
// UsedPercent fails closed (unknown is not zero — CONTRACT_FREEZE.md — an
// unreadable comparison is not assumed safe).
func quotaWorseThan(baseline, current domain.QuotaObservation) (worse bool, detail string) {
	if !baseline.Reached && current.Reached {
		return true, "quota limit reached since pause (was not reached at pause time)"
	}
	if baseline.UsedPercent == nil || current.UsedPercent == nil {
		return true, "quota comparison unavailable: baseline or current UsedPercent is unknown"
	}
	if *current.UsedPercent > *baseline.UsedPercent {
		return true, fmt.Sprintf("quota used_percent increased since pause (%.2f%% -> %.2f%%)", *baseline.UsedPercent, *current.UsedPercent)
	}
	return false, ""
}

// CheckQuotaSafety implements deliverable 8's "quota safe" check: re-reads
// current quota and confirms it has not gotten WORSE than baseline (the
// observation recorded when the pause was originally requested). Improved
// or unchanged quota passes; worse quota fails with
// ReasonQuotaWorseSincePause, which ValidateResume maps onto the required
// "unsafe quota reschedules" behavior (Validating -> Sleeping), not a hard
// block — a session that is merely still rate-limited is expected to
// recover, unlike a repository/session/authorization conflict.
func CheckQuotaSafety(ctx context.Context, reader QuotaSnapshotReader, sessionID domain.SessionID, baseline domain.QuotaObservation) (CheckResult, error) {
	if reader == nil {
		return CheckResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "pause: CheckQuotaSafety requires a non-nil QuotaSnapshotReader", Retryable: false,
		}
	}
	current, err := reader.ReadCurrentQuota(ctx, sessionID, baseline.LimitID)
	if err != nil {
		return failResult(ReasonQuotaReadUnavailable, err.Error()), nil
	}
	if worse, detail := quotaWorseThan(baseline, current); worse {
		return failResult(ReasonQuotaWorseSincePause, detail), nil
	}
	return passResult(), nil
}

// --- 2. Repository fingerprint compatible -----------------------------------

// RepoFingerprint is the narrow, package-local view of "current repository
// state" ValidateResume's repository check needs — deliberately not
// internal/gitx.Fingerprint itself, so this package does not take a
// compile-time dependency on checkpoint's Git plumbing package just to
// declare an interface (a future adapter in a wiring/integration layer
// maps a real gitx.Fingerprint onto this shape). HeadOID mirrors
// app.RepositoryCheckpoint.GitHead exactly (both ultimately come from the
// same gitx.Fingerprint.HeadOID field per checkpoint-b04's capture.go), so
// the two are directly comparable.
type RepoFingerprint struct {
	// HeadOID is the current commit hash (or "(initial)" on an unborn
	// branch), matching app.RepositoryCheckpoint.GitHead's convention.
	HeadOID string
	// ChangedPaths lists every path that differs between the checkpoint
	// baseline and now (working tree + index + committed-since, as the
	// caller's adapter determines) — used only to decide overlap-vs-
	// unrelated per RepoChangePolicy; an empty slice with HeadOID equal to
	// the baseline means "no repository change at all" (trivially
	// compatible, no policy decision needed).
	ChangedPaths []string
}

// RepoFingerprintReader is the narrow seam for reading current repository
// state at resume time.
type RepoFingerprintReader interface {
	ReadCurrentFingerprint(ctx context.Context, worktreeID domain.WorktreeID) (RepoFingerprint, error)
}

// RepoChangePolicy governs what happens when the repository changed since
// pause in a way that does NOT overlap the paused work's own files
// (agents/runtime.md required test: "unrelated repo change follows
// configured policy"). An OVERLAPPING change always blocks regardless of
// policy (required test "repo overlap blocks" has no configurable
// exception — Constitution §7 rule 9, auto-resume is
// "permission-non-escalating": resuming into a conflicting edit is never
// made safe by a policy knob).
type RepoChangePolicy string

const (
	// RepoChangePolicyAllowUnrelated: an unrelated change (no path
	// overlap with the paused work's own files) does not block resume.
	// This is the default per ADD §20.9's UI offering "Inspect Diff" as
	// one of several manual options rather than an automatic hard block
	// for every non-overlapping change.
	RepoChangePolicyAllowUnrelated RepoChangePolicy = "allow_unrelated"
	// RepoChangePolicyBlockAny: ANY repository change since pause blocks
	// resume, overlapping or not — the conservative policy a
	// safety-sensitive workspace may opt into.
	RepoChangePolicyBlockAny RepoChangePolicy = "block_any"
)

// pathsOverlap reports whether any entry in changed also appears in
// pausedWorkPaths — the exact-path-match overlap test. A caller with a
// richer notion of overlap (e.g. directory-prefix or package-level) adapts
// pausedWorkPaths accordingly before calling; this function itself makes
// no assumption beyond string equality, so it never has to guess at a
// project's directory conventions.
func pathsOverlap(changed, pausedWorkPaths []string) bool {
	if len(changed) == 0 || len(pausedWorkPaths) == 0 {
		return false
	}
	work := make(map[string]bool, len(pausedWorkPaths))
	for _, p := range pausedWorkPaths {
		work[p] = true
	}
	for _, c := range changed {
		if work[c] {
			return true
		}
	}
	return false
}

// CheckRepositoryCompatibility implements deliverable 8's "repository
// fingerprint compatible" check in two steps: (1) app.
// RepositoryCheckpointService.Verify confirms the checkpoint taken at
// pause time is still intact (fails closed if invalid/erroring — cannot
// safely compare against a corrupt baseline); (2) the current fingerprint
// is compared against the checkpoint's own recorded GitHead. No change at
// all (HeadOID equal, no changed paths) trivially passes. A HeadOID
// mismatch or any changed path REQUIRES a policy decision: overlap with
// pausedWorkPaths always blocks (ReasonRepositoryOverlapBlocks, required
// test "repo overlap blocks"); a non-overlapping change is allowed or
// blocked per policy (required test "unrelated repo change follows
// configured policy").
func CheckRepositoryCompatibility(
	ctx context.Context,
	verifier app.RepositoryCheckpointService,
	reader RepoFingerprintReader,
	checkpointID domain.RepositoryCheckpointID,
	baselineGitHead string,
	worktreeID domain.WorktreeID,
	pausedWorkPaths []string,
	policy RepoChangePolicy,
) (CheckResult, error) {
	if verifier == nil {
		return CheckResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "pause: CheckRepositoryCompatibility requires a non-nil RepositoryCheckpointService", Retryable: false,
		}
	}
	if reader == nil {
		return CheckResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "pause: CheckRepositoryCompatibility requires a non-nil RepoFingerprintReader", Retryable: false,
		}
	}
	if policy == "" {
		policy = RepoChangePolicyAllowUnrelated
	}

	verification, err := verifier.Verify(ctx, checkpointID)
	if err != nil {
		return failResult(ReasonRepositoryCheckpointInvalid, err.Error()), nil
	}
	if !verification.Valid {
		return failResult(ReasonRepositoryCheckpointInvalid, fmt.Sprintf("repository checkpoint %q failed verification", checkpointID)), nil
	}

	current, err := reader.ReadCurrentFingerprint(ctx, worktreeID)
	if err != nil {
		return failResult(ReasonRepositoryFingerprintUnavailable, err.Error()), nil
	}

	if current.HeadOID == baselineGitHead && len(current.ChangedPaths) == 0 {
		return passResult(), nil
	}

	if pathsOverlap(current.ChangedPaths, pausedWorkPaths) {
		return failResult(ReasonRepositoryOverlapBlocks, string(domain.ReasonRepositoryChangedDuringSleep)), nil
	}

	if policy == RepoChangePolicyBlockAny {
		return failResult(ReasonRepositoryUnrelatedChangeBlocked, string(domain.ReasonRepositoryChangedDuringSleep)), nil
	}

	return passResult(), nil
}

// --- 3. Session/provider capability valid -----------------------------------

// SessionCapabilitySnapshot is what ValidateResume's session check needs
// from the provider: whether the session itself is still resumable, and
// (per ADD §9.10/Constitution §5.2/§7.3's explicit-capability discipline)
// its currently-detected capabilities, so a caller can additionally assert
// on a specific required capability (e.g. SessionResume) without this
// package hardcoding which capability matters — the check itself only
// requires Resumable; the capability set is carried through for the
// caller/audit trail.
type SessionCapabilitySnapshot struct {
	// Resumable is the provider's own answer to "can this session be
	// resumed right now" — false means confirmed not resumable (never
	// "not yet checked"; a reader that hasn't checked must not call this
	// at all, mirroring domain.ProviderCapabilities' own discipline).
	Resumable    bool
	Capabilities domain.ProviderCapabilities
}

// SessionCapabilityReader is the narrow seam for reading the current
// provider session's resumability. This is the "underlying provider
// session must still be valid/resumable" signal named in the task brief;
// a real implementation adapts whatever normalized session-state/
// capability signal claude-provider's integration exposes (this role
// consumes, does not own, that signal) behind this interface.
type SessionCapabilityReader interface {
	ReadSessionCapability(ctx context.Context, sessionID domain.SessionID) (SessionCapabilitySnapshot, error)
}

// CheckSessionCapability implements deliverable 8's "session/provider
// capability valid" check: the session must currently report Resumable —
// true. requireCapability, if non-empty semantically (callers pass a
// specific field check via requireResumeCapability below), additionally
// requires domain.ProviderCapabilities.SessionResume; left as a simple
// bool parameter (not a reflective field-name lookup) per Constitution §7
// rule 10 (no speculative generality this node doesn't need) — the one
// capability resume validation actually cares about is SessionResume.
func CheckSessionCapability(ctx context.Context, reader SessionCapabilityReader, sessionID domain.SessionID, requireResumeCapability bool) (CheckResult, error) {
	if reader == nil {
		return CheckResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "pause: CheckSessionCapability requires a non-nil SessionCapabilityReader", Retryable: false,
		}
	}
	snap, err := reader.ReadSessionCapability(ctx, sessionID)
	if err != nil {
		return failResult(ReasonSessionCapabilityUnavailable, err.Error()), nil
	}
	if !snap.Resumable {
		return failResult(ReasonSessionCapabilityInvalid, fmt.Sprintf("session %q is not resumable", sessionID)), nil
	}
	if requireResumeCapability && !snap.Capabilities.SessionResume {
		return failResult(ReasonSessionCapabilityInvalid, fmt.Sprintf("session %q provider capability SessionResume is confirmed absent", sessionID)), nil
	}
	return passResult(), nil
}

// --- 4. Authorization/consent valid ------------------------------------------

// CheckAuthorization implements deliverable 8's "authorization/consent
// valid" check via the frozen app.EvaluationService.ConsumeAuthorization
// (real port; predictor-09/10's Evaluation-persistence implementation is
// FAKED for this specific call this wave — see this package's doc.go
// addition / docs/implementation/vertical-slice/runtime.md's Wave 8 section for why:
// predictor-10's authorization-hardening pass is a concurrent sibling this
// same wave, not yet mergeable, per the task brief's explicit instruction
// to use a fake here consistent with the established fake-then-swap
// pattern runtime-a05/b05 already used for checkpoint-a05/b04). A
// non-nil error from ConsumeAuthorization is treated as "no valid,
// unconsumed authorization" (ReasonAuthorizationInvalid) UNLESS the error
// is itself an ErrCodeUnavailable-shaped dependency failure, in which case
// it is surfaced as ReasonAuthorizationServiceUnavailable — both fail the
// check, but the distinct reason lets a caller/audit trail tell "the
// authorization was rejected" apart from "we could not ask."
func CheckAuthorization(ctx context.Context, evaluations app.EvaluationService, req app.ConsumeAuthorizationRequest) (CheckResult, error) {
	if evaluations == nil {
		return CheckResult{}, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "pause: CheckAuthorization requires a non-nil EvaluationService", Retryable: false,
		}
	}
	_, err := evaluations.ConsumeAuthorization(ctx, req)
	if err != nil {
		if isUnavailable(err) {
			return failResult(ReasonAuthorizationServiceUnavailable, err.Error()), nil
		}
		return failResult(ReasonAuthorizationInvalid, err.Error()), nil
	}
	return passResult(), nil
}

// isUnavailable reports whether err is a *domain.Error with
// ErrCodeUnavailable, unwrapping via errors.As rather than a direct type
// assertion so a wrapped domain.Error (e.g. via fmt.Errorf("...: %w", err)
// somewhere upstream) is still recognized correctly.
func isUnavailable(err error) bool {
	var derr *domain.Error
	return errors.As(err, &derr) && derr.Code == domain.ErrCodeUnavailable
}

// --- Orchestration -----------------------------------------------------------

// ResumeValidationDeps bundles every collaborator ValidateResume needs, one
// per check (mirrors PersistDeps' bundle-of-narrow-seams shape). Every
// field is required; ValidateResume fails closed (ErrCodeUnavailable) if
// any is nil, before running any check — a missing dependency is a
// composition bug, never silently skipped (same discipline persistphase.go
// and safepoint.go already established in this package).
type ResumeValidationDeps struct {
	Quota                QuotaSnapshotReader
	RepositoryCheckpoint app.RepositoryCheckpointService
	RepoFingerprint      RepoFingerprintReader
	Session              SessionCapabilityReader
	Evaluations          app.EvaluationService
}

// ResumeValidationRequest is ValidateResume's input: everything needed to
// re-derive each of the four checks' own request shape.
type ResumeValidationRequest struct {
	SessionID domain.SessionID

	// QuotaBaseline is the quota observation recorded when THIS pause was
	// originally requested (ADD §20: a pause exists only because a
	// specific runway forecast/quota state justified it) — the caller
	// (a future integration node) is responsible for threading this
	// through from wherever the original Observe/RequestPause call
	// recorded it; ValidateResume itself does not read or infer it.
	QuotaBaseline domain.QuotaObservation

	RepositoryCheckpointID domain.RepositoryCheckpointID
	BaselineGitHead        string
	WorktreeID             domain.WorktreeID
	PausedWorkPaths        []string
	RepoPolicy             RepoChangePolicy

	RequireSessionResumeCapability bool

	Authorization app.ConsumeAuthorizationRequest
}

// ResumeValidationResult is ValidateResume's output: an overall pass/fail
// plus every individual check's own CheckResult, so a caller can both
// drive Resume's three-way verdict (see Verdict below) AND persist a full
// audit trail (e.g. a future resume_attempts row —
// 0052_resume_attempts.sql already has columns for exactly this shape:
// repository_fingerprint_before/after, quota_used_percent, failure_code).
type ResumeValidationResult struct {
	Quota         CheckResult
	Repository    CheckResult
	Session       CheckResult
	Authorization CheckResult
}

// AllPass reports whether every check passed.
func (r ResumeValidationResult) AllPass() bool {
	return r.Quota.Pass && r.Repository.Pass && r.Session.Pass && r.Authorization.Pass
}

// FirstFailure returns the first failing check's CheckResult in the fixed
// check order (quota, repository, session, authorization), and true — or
// a zero CheckResult and false if AllPass().
func (r ResumeValidationResult) FirstFailure() (CheckResult, bool) {
	for _, c := range []CheckResult{r.Quota, r.Repository, r.Session, r.Authorization} {
		if !c.Pass {
			return c, true
		}
	}
	return CheckResult{}, false
}

// Verdict maps this result onto lifecycle.go's ResumeRequest three-way
// verdict (Valid/QuotaUnsafe/Conflict) — the seam that lets a future
// integration node call ValidateResume then pass its result straight into
// Resume without re-deriving this mapping itself. Per the DAG's "unsafe
// quota reschedules" required test, a quota failure ALONE (repository/
// session/authorization all otherwise passing) maps to QuotaUnsafe, i.e.
// reschedule, never a hard Conflict block — quota recovering is expected,
// ordinary behavior, not a conflict to escalate to a human. Every other
// failure (repository, session, or authorization) maps to Conflict: none
// of those three are expected to self-resolve by merely waiting, per ADD
// §20.9's manual-resolution UI (Inspect Diff / Create New Plan / Resume
// Manually / Cancel) being repository/session-conflict-shaped, not a
// scheduler retry. AllPass maps to Valid.
func (r ResumeValidationResult) Verdict() ResumeRequest {
	switch {
	case r.AllPass():
		return ResumeRequest{Valid: true}
	case !r.Quota.Pass && r.Repository.Pass && r.Session.Pass && r.Authorization.Pass:
		return ResumeRequest{QuotaUnsafe: true}
	default:
		return ResumeRequest{Conflict: true}
	}
}

// ValidateResume runs all four agents/runtime.md Part A deliverable 8
// checks, in the fixed order documented in this file's package comment
// (quota, repository, session, authorization), and returns every check's
// individual result. Unlike Resume's own Apply-based short-circuiting,
// ValidateResume does NOT stop at the first failing check — a caller
// building a full audit trail (or a human resolving a BlockedConflict via
// ADD §20.9's UI) needs to see every check's outcome, not just the first
// one that failed, so a repository conflict and a simultaneously-invalid
// authorization are both visible in one call rather than requiring N round
// trips. A downstream read failure inside any one checker (e.g. the quota
// service errors when asked) does NOT abort this sequence either — per this
// file's package comment, that is reported as a failing CheckResult with an
// "_UNAVAILABLE" reason code, not a Go error, so it is exactly as visible in
// the returned ResumeValidationResult as any other rejection. ValidateResume
// DOES return a Go error immediately, before running any check, if deps or
// req themselves are malformed (a nil dependency or a missing SessionID) —
// that is a caller composition bug, not a validation outcome.
func ValidateResume(ctx context.Context, deps ResumeValidationDeps, req ResumeValidationRequest) (ResumeValidationResult, error) {
	if deps.Quota == nil {
		return ResumeValidationResult{}, missingDepError("Quota")
	}
	if deps.RepositoryCheckpoint == nil {
		return ResumeValidationResult{}, missingDepError("RepositoryCheckpoint")
	}
	if deps.RepoFingerprint == nil {
		return ResumeValidationResult{}, missingDepError("RepoFingerprint")
	}
	if deps.Session == nil {
		return ResumeValidationResult{}, missingDepError("Session")
	}
	if deps.Evaluations == nil {
		return ResumeValidationResult{}, missingDepError("Evaluations")
	}
	if req.SessionID == "" {
		return ResumeValidationResult{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "pause: ValidateResume requires a SessionID", Retryable: false,
		}
	}

	var result ResumeValidationResult

	quotaResult, err := CheckQuotaSafety(ctx, deps.Quota, req.SessionID, req.QuotaBaseline)
	if err != nil {
		return ResumeValidationResult{}, err
	}
	result.Quota = quotaResult

	repoResult, err := CheckRepositoryCompatibility(ctx, deps.RepositoryCheckpoint, deps.RepoFingerprint, req.RepositoryCheckpointID, req.BaselineGitHead, req.WorktreeID, req.PausedWorkPaths, req.RepoPolicy)
	if err != nil {
		return ResumeValidationResult{}, err
	}
	result.Repository = repoResult

	sessionResult, err := CheckSessionCapability(ctx, deps.Session, req.SessionID, req.RequireSessionResumeCapability)
	if err != nil {
		return ResumeValidationResult{}, err
	}
	result.Session = sessionResult

	authResult, err := CheckAuthorization(ctx, deps.Evaluations, req.Authorization)
	if err != nil {
		return ResumeValidationResult{}, err
	}
	result.Authorization = authResult

	return result, nil
}

// --- Wake job rescheduling on quota-unsafe --------------------------------

// WakeJobRescheduler is the narrow slice of scheduler.Store's real API
// RescheduleWakeJobOnQuotaUnsafe needs. Declared here (not a direct
// *scheduler.Store parameter) purely so this file's own tests can use a
// lightweight fake instead of standing up a real SQLite-backed
// scheduler.Store for a unit test that only cares about the reschedule
// DECISION, not scheduler.Store's own storage correctness (already proven
// by runtime-a06/a07's lease_test.go/restart_test.go) — production callers
// pass a real *scheduler.Store, which satisfies this interface directly.
type WakeJobRescheduler interface {
	Fail(ctx context.Context, id domain.WakeJobID, owner string, failureReason string) (scheduler.Job, error)
}

// RescheduleWakeJobOnQuotaUnsafe implements the required test "unsafe
// quota reschedules" at the scheduler-integration level (as opposed to
// ResumeValidationResult.Verdict, which only proves the PAUSE RECORD's own
// state-machine transition targets Sleeping): when ValidateResume's quota
// check is the (sole) reason a resume did not proceed, the underlying wake
// job must actually be given a new run_after via the scheduler's own ADD
// §20.7 retry/backoff schedule — otherwise a pause record could sit in
// Sleeping forever with its wake job stuck in a terminal or stale-leased
// state, never actually waking the session back up for a later attempt.
// This deliberately reuses scheduler.Store.Fail (runtime-a06) rather than
// inventing a second reschedule mechanism: a quota-unsafe resume attempt
// IS a failed attempt from the wake job's own perspective, and Fail's
// existing backoff-then-retry-or-dead semantics are exactly ADD §20.7's
// intended behavior for "try again later, give up after MaxAttempts."
//
// The caller must currently hold jobID's lease (owner) — this is the same
// precondition scheduler.Store.Fail itself requires (ErrCodeConflict
// otherwise) — which in practice means this is called from within the
// scheduler-driven wake pipeline (a09's own claim-then-drive loop), not
// from an arbitrary manual `auspex resume` invocation that never
// claimed a lease in the first place; a manual resume's quota-unsafe
// verdict is still correctly reflected on the PAUSE RECORD via
// Resume/Verdict regardless of whether a wake job reschedule applies.
func RescheduleWakeJobOnQuotaUnsafe(ctx context.Context, jobs WakeJobRescheduler, jobID domain.WakeJobID, owner string, result ResumeValidationResult) (scheduler.Job, bool, error) {
	if jobs == nil {
		return scheduler.Job{}, false, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "pause: RescheduleWakeJobOnQuotaUnsafe requires a non-nil WakeJobRescheduler", Retryable: false,
		}
	}
	if jobID == "" {
		return scheduler.Job{}, false, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "pause: RescheduleWakeJobOnQuotaUnsafe requires a WakeJobID", Retryable: false,
		}
	}
	verdict := result.Verdict()
	if !verdict.QuotaUnsafe {
		// Not a quota-unsafe verdict (either fully valid, or a
		// repository/session/authorization conflict) — rescheduling the
		// wake job is only this function's job for the quota case; a
		// Conflict verdict's wake job disposition belongs to whatever
		// BlockedConflict resolution flow ADD §20.9 describes (manual,
		// not an automatic scheduler retry), not to this function.
		return scheduler.Job{}, false, nil
	}
	reason := "resume validation: quota still unsafe"
	if failing, ok := result.FirstFailure(); ok {
		reason = fmt.Sprintf("resume validation: %s: %s", failing.Reason, failing.Detail)
	}
	job, err := jobs.Fail(ctx, jobID, owner, reason)
	if err != nil {
		return scheduler.Job{}, false, err
	}
	return job, true, nil
}
