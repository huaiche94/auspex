// restoredryrun.go: checkpoint-b08's "Restore dry-run" deliverable
// (agents/checkpoint.md Part B deliverable #9; Auspex_ADD.md §19.6
// "Restore") — the REPORTING half of restore. RestoreDryRun NEVER writes
// to the working tree, index, or any ref; it only reports, in full ADD
// §19.6 detail, what a real restore WOULD do. The mutating half now ships
// alongside it in restore.go (RestoreApply, issue #6 / ADR-048), which
// replays a verified checkpoint's patches into the worktree — so the
// never-writes guarantee is a property of THIS file's dry-run path, not of
// restore as a whole. RestoreApply reuses these checks (it runs
// RestoreDryRun first, then applies), keeping the two halves in agreement.
//
// ADD §19.6 lists restore's required checks almost verbatim as this file's
// own steps: "verify checksum; verify repo identity; reject path
// traversal; reject dirty target unless safety checkpoint/force; git apply
// --check; staged/unstaged separately; never delete extra files unless
// --exact; produce report." Every one of those is implemented below EXCEPT
// the two that only apply to a MUTATING restore ("never delete extra files
// unless --exact" has nothing to check without a real apply step) — dry-run
// mutates nothing, so there is nothing to constrain there; that
// constraint's real enforcement point is RestoreApply in restore.go.
package repocheckpoint

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/gitx"
)

// RestoreDryRunReport is the full ADD §19.6 "produce report" deliverable:
// everything an operator (or a future real-restore implementation) needs
// to know about whether restoring this checkpoint would succeed, without
// anything here having mutated the working tree, index, or refs.
type RestoreDryRunReport struct {
	CheckpointID domain.RepositoryCheckpointID

	// ChecksumValid mirrors Verify's own verdict — a checkpoint whose
	// artifacts do not match their recorded digests cannot be trusted to
	// restore correctly regardless of what the Git-level checks below say
	// (ADD §19.6: "verify checksum" is listed first for exactly this
	// reason: every later check is meaningless if the evidence itself is
	// already known to be corrupt).
	ChecksumValid    bool
	ChecksumProblems []string

	// RepositoryIdentityMatch reports whether the CURRENT worktree this
	// dry-run is evaluating against is the SAME repository the checkpoint
	// was captured from (ADD §19.6 "verify repo identity") — comparing the
	// checkpoint's recorded repository/worktree identity against what
	// gitx.ResolveRepo reports for the live path right now. This is
	// deliberately NOT a HEAD-position check (HEAD legitimately moves
	// between capture and a later restore attempt; that is exactly what
	// the ApplyCheck step below is for) — it is an identity check: restoring
	// checkpoint evidence from repository A onto an unrelated repository B
	// that merely happens to share a worktree path is a much worse mistake
	// than a stale HEAD, and worth its own distinct verdict.
	RepositoryIdentityMatch bool
	RepositoryIdentityNote  string

	// WorktreeDirty reports whether the CURRENT target has any staged,
	// unstaged, or untracked changes at all (ADD §19.6 "reject dirty
	// target unless safety checkpoint/force"). A dry-run never rejects
	// anything itself (nothing is being applied) but surfaces this fact so
	// a caller deciding whether to proceed with a REAL restore later knows
	// whether AllowDirty would be required.
	WorktreeDirty      bool
	WorktreeDirtyPaths []string

	// StagedApplyCheck/UnstagedApplyCheck are `git apply --check` run
	// separately against the checkpoint's staged.patch.gz/unstaged.patch.gz
	// content (ADD §19.6: "git apply --check; staged/unstaged separately").
	// A checkpoint with an empty patch for one scope reports
	// WouldApply:true trivially for that scope (gitx.ApplyCheck's own
	// documented "nothing to apply" case).
	StagedApplyCheck   gitx.ApplyCheckResult
	UnstagedApplyCheck gitx.ApplyCheckResult

	// WouldSucceed is this function's own verdict, covering every check
	// EXCEPT the dirty-target one: true only when checksum verification,
	// repository identity, and both apply-checks all passed. Problems
	// collects every one of those failure reasons (never just the first)
	// — same "collect everything in one pass" discipline as
	// VerifyResult.Problems and statecheckpoint.ReconcileReport.Violations.
	//
	// Dirty-target is deliberately excluded from both WouldSucceed and
	// Problems here: ADD §19.6 makes it conditional ("reject dirty target
	// UNLESS safety checkpoint/force"), and this free function has no
	// AllowDirty policy input to decide that condition with — WorktreeDirty/
	// WorktreeDirtyPaths above report the fact; Service.Restore (the
	// frozen-port caller, which DOES receive AllowDirty) is what combines
	// this verdict with that policy decision into a final answer.
	WouldSucceed bool
	Problems     []string
}

// RestoreDryRun evaluates whether restoring row's checkpoint onto the live
// repository at worktreePath would succeed right now, performing every ADD
// §19.6 check EXCEPT anything that requires actually mutating the working
// tree, index, or refs (real restore is out of vertical-slice scope). It never
// calls a Git subcommand capable of mutating repository state — the same
// invariant capture.go's own doc comment establishes for Capture, extended
// here to Restore's dry-run half.
//
// expectedRepositoryID is the CURRENT repository identity the caller
// independently resolved for worktreePath's own WorktreeID (e.g.
// Service.Restore's own resolveWorktree callback, the same seam Create
// uses) — compared against what the checkpoint's manifest recorded at
// capture time, so a worktree that has since been re-provisioned to point
// at an unrelated repository (same WorktreeID, different underlying repo)
// is caught as a genuine identity mismatch rather than silently treated as
// "the same place." Pass "" when the caller has no independent identity
// source to compare against (the check degrades to WorktreeID-only,
// documented in the returned report's note).
func RestoreDryRun(ctx context.Context, gitClient *gitx.Client, row Row, worktreePath string, expectedRepositoryID string) (RestoreDryRunReport, error) {
	report := RestoreDryRunReport{CheckpointID: row.ID}

	// Step 1 (ADD §19.6): verify checksum. Reuses Verify's own artifact
	// digest recomputation rather than duplicating it — a dry-run that
	// trusted the stored checksums at face value would violate this whole
	// role's "never trust a stored checksum alone" discipline
	// (verify.go's own doc comment).
	verifyResult, err := Verify(row)
	if err != nil {
		return RestoreDryRunReport{}, fmt.Errorf("repocheckpoint: RestoreDryRun: verify checksum: %w", err)
	}
	report.ChecksumValid = verifyResult.Valid
	report.ChecksumProblems = verifyResult.Problems
	if !verifyResult.Valid {
		report.Problems = append(report.Problems, verifyResult.Problems...)
	}

	manifest := verifyResult.Manifest

	// Step 2 (ADD §19.6): verify repo identity. Resolve the CURRENT
	// worktreePath (read-only; ResolveRepo issues only `git rev-parse`)
	// and compare against what the manifest recorded at capture time.
	// reject path traversal is folded into this same resolution: a
	// worktreePath argument that does not resolve to a real Git worktree
	// at all (e.g. a path escaping outside any repository, or one crafted
	// with ".." components that lands somewhere unexpected) fails
	// ResolveRepo itself with ErrCodeNotFound/ErrCodeValidation, which
	// this function propagates as a hard error rather than a soft
	// "identity mismatch" report entry — an unresolvable target is not
	// something a dry-run report can meaningfully reason about further.
	repoInfo, err := gitClient.ResolveRepo(ctx, worktreePath)
	if err != nil {
		return RestoreDryRunReport{}, fmt.Errorf("repocheckpoint: RestoreDryRun: resolve worktree %s: %w", worktreePath, err)
	}
	identityMatch, identityNote := checkRepositoryIdentity(manifest, row, expectedRepositoryID)
	report.RepositoryIdentityMatch = identityMatch
	report.RepositoryIdentityNote = identityNote
	if !identityMatch {
		report.Problems = append(report.Problems, identityNote)
	}

	// Step 3 (ADD §19.6): reject dirty target unless safety
	// checkpoint/force. A dry-run reports dirtiness as a fact rather than
	// enforcing AllowDirty itself (there is no mutation for it to gate
	// here) - Service.Restore, the frozen-port caller, is where AllowDirty
	// actually changes the verdict (see service.go).
	status, err := gitClient.Status(ctx, repoInfo.WorktreeRoot)
	if err != nil {
		return RestoreDryRunReport{}, fmt.Errorf("repocheckpoint: RestoreDryRun: status %s: %w", repoInfo.WorktreeRoot, err)
	}
	dirty, dirtyPaths := dirtyEntries(status.Entries)
	report.WorktreeDirty = dirty
	report.WorktreeDirtyPaths = dirtyPaths

	// Step 4 (ADD §19.6): git apply --check, staged/unstaged separately.
	//
	// A patch-artifact load/decompress failure here is reported as a
	// dry-run PROBLEM, not returned as a hard function error: it is by
	// construction the same corruption checksum verification (step 1)
	// would already have flagged (a tampered or truncated gzip stream
	// fails both its own digest check and gzip decoding), and a dry-run's
	// whole purpose is to report exactly this kind of finding in
	// report.Problems rather than abort before producing a report at all —
	// same "collect every problem, never just the first" discipline this
	// function follows everywhere else.
	stagedPatch, unstagedPatch, loadErr := loadCheckpointPatches(row.ArtifactRoot)
	if loadErr != nil {
		report.Problems = append(report.Problems, fmt.Sprintf("could not load patch artifacts for apply-check: %v", loadErr))
		report.WouldSucceed = false
		return report, nil
	}

	stagedCheck, err := gitClient.ApplyCheck(ctx, repoInfo.WorktreeRoot, stagedPatch, true)
	if err != nil {
		return RestoreDryRunReport{}, fmt.Errorf("repocheckpoint: RestoreDryRun: apply-check staged patch: %w", err)
	}
	report.StagedApplyCheck = stagedCheck
	if !stagedCheck.WouldApply {
		report.Problems = append(report.Problems, fmt.Sprintf("staged patch would not apply: %s", stagedCheck.Detail))
	}

	unstagedCheck, err := gitClient.ApplyCheck(ctx, repoInfo.WorktreeRoot, unstagedPatch, false)
	if err != nil {
		return RestoreDryRunReport{}, fmt.Errorf("repocheckpoint: RestoreDryRun: apply-check unstaged patch: %w", err)
	}
	report.UnstagedApplyCheck = unstagedCheck
	if !unstagedCheck.WouldApply {
		report.Problems = append(report.Problems, fmt.Sprintf("unstaged patch would not apply: %s", unstagedCheck.Detail))
	}

	report.WouldSucceed = len(report.Problems) == 0
	return report, nil
}

// checkRepositoryIdentity compares the checkpoint's recorded repository
// identity (manifest.Repository.RepositoryID, captured at Capture time)
// against expectedRepositoryID — the CURRENT repository identity the
// caller independently resolved for row.WorktreeID right now (e.g. via the
// same resolveWorktree seam Service.Create uses). GitHead is deliberately
// NOT part of this comparison (see RestoreDryRunReport's doc comment: HEAD
// legitimately moves between capture and a later restore attempt, and a
// stale HEAD is exactly what the ApplyCheck step is for) — this check is
// specifically "is this still the SAME repository," not "is it at the
// same commit."
//
// When expectedRepositoryID is empty (a caller with no independent
// identity source — e.g. RestoreDryRun invoked directly, outside
// Service.Restore's own resolveWorktree-equipped path), this check
// degrades to comparing the manifest's own recorded WorktreeID against
// row.WorktreeID (always trivially true for a checkpoint this package
// itself produced, since Capture writes both from the same request) rather
// than silently reporting a false positive — a caller that wants the
// stronger cross-checked guarantee must supply expectedRepositoryID.
func checkRepositoryIdentity(manifest Manifest, row Row, expectedRepositoryID string) (bool, string) {
	if expectedRepositoryID == "" {
		if manifest.Repository.WorktreeID != string(row.WorktreeID) {
			return false, fmt.Sprintf("checkpoint manifest worktree_id %q does not match row worktree_id %q", manifest.Repository.WorktreeID, row.WorktreeID)
		}
		return true, "no independent repository identity supplied; only checkpoint-internal worktree_id consistency was checked"
	}
	if manifest.Repository.RepositoryID != expectedRepositoryID {
		return false, fmt.Sprintf("checkpoint was captured from repository_id %q, but the current worktree now resolves to repository_id %q", manifest.Repository.RepositoryID, expectedRepositoryID)
	}
	return true, ""
}

// dirtyEntries reports whether status has any entries at all (staged,
// unstaged, unmerged, or untracked — ignored entries are excluded by
// Client.Status's own pinned --untracked-files policy) and their paths.
func dirtyEntries(entries []gitx.Entry) (bool, []string) {
	if len(entries) == 0 {
		return false, nil
	}
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		paths = append(paths, e.Path)
	}
	return true, paths
}

// loadCheckpointPatches reads and gunzips a checkpoint's
// staged.patch.gz/unstaged.patch.gz artifacts from its own artifact
// directory. Either file may be absent (a checkpoint with no staged or no
// unstaged changes at capture time never writes an empty-but-present gzip
// for that scope — see capture.go's files map, which only includes a key
// when Capture actually produced content)... actually capture.go always
// writes both keys unconditionally (gzip of a possibly-empty patch is
// still a valid, small gzip stream), so both files always exist for any
// checkpoint produced by this package's own Capture; a missing file here
// is therefore a genuine integrity problem, not a normal "no changes"
// case, and is reported as a hard error rather than silently treated as
// an empty patch.
func loadCheckpointPatches(artifactRoot string) (staged, unstaged []byte, err error) {
	staged, err = readGzipArtifact(filepath.Join(artifactRoot, "staged.patch.gz"))
	if err != nil {
		return nil, nil, err
	}
	unstaged, err = readGzipArtifact(filepath.Join(artifactRoot, "unstaged.patch.gz"))
	if err != nil {
		return nil, nil, err
	}
	return staged, unstaged, nil
}

// readGzipArtifact reads and fully decompresses a gzip file, capping how
// much it will decompress via io.LimitReader — the same "never read an
// unbounded amount of content" discipline internal/redact.ScanPath applies
// to untrusted file content, applied here to this package's OWN previously
// captured artifacts (a corrupted or maliciously-crafted gzip bomb sitting
// in a checkpoint's artifact directory should not be able to exhaust
// memory during a dry-run read).
func readGzipArtifact(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("gzip reader for %s: %w", path, err)
	}
	defer func() { _ = gr.Close() }()

	var buf bytes.Buffer
	// 256 MiB decompressed cap: comfortably above any realistic single
	// checkpoint's patch size (checkpoint-b05's own large-diff test tops
	// out at 50 files x 200 lines / 5000 single-file lines, orders of
	// magnitude smaller) while still bounding worst-case memory use for a
	// corrupted or adversarial artifact.
	const maxDecompressedBytes = 256 << 20
	if _, err := io.Copy(&buf, io.LimitReader(gr, maxDecompressedBytes+1)); err != nil {
		return nil, fmt.Errorf("decompress %s: %w", path, err)
	}
	if buf.Len() > maxDecompressedBytes {
		return nil, fmt.Errorf("decompressed %s exceeds %d byte cap", path, maxDecompressedBytes)
	}
	return buf.Bytes(), nil
}
