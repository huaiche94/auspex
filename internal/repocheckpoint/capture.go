// capture.go: the Repository Checkpoint create operation (agents/checkpoint.md
// Part B deliverable #4, "Repository Checkpoint create"; ADD §19.3 capture
// steps). This is the single hardest-constrained file in this package:
// every Git operation it issues is READ-ONLY (status, diff, ls-files) —
// never `git add`, `git commit`, `git checkout`, `git reset`, or anything
// else that could mutate the index, working tree, or ref state. Grep this
// file (and gitx generally) for any write verb if that invariant is ever
// in doubt; there should be none.
package repocheckpoint

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"time"

	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/gitx"
)

// CaptureRequest is the input to Capture.
type CaptureRequest struct {
	// CheckpointID is the caller-supplied ID (domain.IDGenerator output)
	// this checkpoint will be stored and addressed under.
	CheckpointID domain.RepositoryCheckpointID
	// RepositoryID/WorktreeID identify the manifest's `repository` block.
	RepositoryID string
	WorktreeID   domain.WorktreeID
	// TaskID/TurnID are optional linkage back to the requesting task/turn
	// (repository_checkpoints.task_id/turn_id).
	TaskID *domain.TaskID
	TurnID *string
	// WorktreePath is any path inside the working tree to capture (it is
	// resolved via gitx.ResolveRepo, so it need not be the repo root).
	WorktreePath string
	// ArtifactsRoot is the directory under which this checkpoint's own
	// artifact directory (ArtifactsRoot/CheckpointID/) is written,
	// matching ADD §19.2's
	// `<UserDataDir>/Preflight/repositories/<repo-id>/checkpoints/<id>/`
	// layout one level up — the caller (runtime role) owns resolving
	// UserDataDir itself; this package only needs a root to write under.
	ArtifactsRoot string
}

// CaptureOptions carries the tunable policy knobs (Part B security
// requirements: cap artifact size and file count). Zero values fall back
// to the Default* constants in security.go.
type CaptureOptions struct {
	MaxUntrackedFileBytes  int64
	MaxUntrackedTotalBytes int64
	MaxUntrackedFileCount  int
	// DisableSecretScan turns off internal/redact's filename/content
	// secret scan over untracked files (ADD §19.5's "secret scan" default
	// policy bullet). Deliberately an opt-OUT flag (zero value = false =
	// scanning ENABLED) rather than an opt-in one, so a caller that never
	// touches this field still gets the safe default — matching this
	// package's existing "safe by default, explicit escape hatch" posture
	// for every other cap in this struct. Expected use: a caller (e.g. a
	// future admin/debug command) that has already made an informed,
	// explicit decision to accept the leakage risk for a specific
	// checkpoint, never a default any code path should reach for silently.
	DisableSecretScan bool
}

func (o CaptureOptions) withDefaults() CaptureOptions {
	if o.MaxUntrackedFileBytes <= 0 {
		o.MaxUntrackedFileBytes = DefaultMaxFileBytes
	}
	if o.MaxUntrackedTotalBytes <= 0 {
		o.MaxUntrackedTotalBytes = DefaultMaxTotalBytes
	}
	if o.MaxUntrackedFileCount <= 0 {
		o.MaxUntrackedFileCount = DefaultMaxFileCount
	}
	return o
}

// CaptureResult is what Capture produces: the Row ready for Store.Insert
// and the Manifest that was written to disk (so a caller building a
// summary/log message does not need to re-read manifest.json immediately
// after writing it).
type CaptureResult struct {
	Row      Row
	Manifest Manifest
}

// Capture performs ADD §19.3's capture protocol end to end:
//
//  1. resolve the repository (read-only);
//  2. take an initial fingerprint;
//  3. generate binary-safe staged/unstaged patches (read-only diffs);
//  4. build the untracked-file archive under the security policy;
//  5. take a final fingerprint and fail if it differs from the initial one
//     (race detection — ADD §19.3 step 11: a repository mutated during
//     capture makes the evidence inconsistent, so this is a hard failure
//     here; the retry-once policy is checkpoint-b07's scope, per this
//     wave's brief, so this function itself performs exactly one attempt
//     and returns errIntegrity on a detected race for the caller to
//     retry);
//  6. write manifest.json + summary.md + patches + archive atomically
//     under ArtifactsRoot/CheckpointID/ (never partially visible);
//  7. return the Row/Manifest for the caller to persist via Store.Insert
//     inside its own transaction (this function does not touch the DB —
//     see the package doc's cross-part-boundary note: Part B does not
//     reach into Part A's stores, and callers control their own
//     transaction boundary the same way internal/telemetry/claude's
//     EventStore does).
//
// Capture never runs a Git subcommand capable of mutating repository state
// (Constitution §7 rule 6; this node's own DAG risk note). Every gitx call
// here is Status/Fingerprint/DiffNumstat/DiffPatch/ListUntracked — all
// read-only.
func Capture(ctx context.Context, gitClient *gitx.Client, clock domain.Clock, req CaptureRequest, opts CaptureOptions) (CaptureResult, error) {
	opts = opts.withDefaults()

	initial, err := gitClient.Fingerprint(ctx, req.WorktreePath)
	if err != nil {
		return CaptureResult{}, fmt.Errorf("repocheckpoint: initial fingerprint: %w", err)
	}

	stagedPatchRaw, err := gitClient.DiffPatch(ctx, initial.WorktreeRoot, true)
	if err != nil {
		return CaptureResult{}, fmt.Errorf("repocheckpoint: staged patch: %w", err)
	}
	unstagedPatchRaw, err := gitClient.DiffPatch(ctx, initial.WorktreeRoot, false)
	if err != nil {
		return CaptureResult{}, fmt.Errorf("repocheckpoint: unstaged patch: %w", err)
	}

	// ADD §19.3 step 8 ("secret/size/symlink filters") applies to ALL
	// captured content, not only the untracked archive (§19.5's own
	// "Untracked policy" section states the secret-scan bullet there
	// separately and narrowly; §19.3's general placement between diff
	// generation and archival is broader). A secret staged/unstaged into
	// an already-tracked file must not survive into the patch artifacts
	// unredacted — see patchredact.go for the full design rationale
	// (redact-in-place, not skip-with-annotation) and the exact scope of
	// what gets rewritten (added/removed line bodies only, never context
	// or header lines, so the patch remains structurally applicable).
	stagedPatch, stagedHadSecret := redactPatchSecrets(stagedPatchRaw)
	unstagedPatch, unstagedHadSecret := redactPatchSecrets(unstagedPatchRaw)

	archiveResult, err := buildUntrackedArchive(ctx, gitClient, initial.WorktreeRoot,
		opts.MaxUntrackedFileBytes, opts.MaxUntrackedTotalBytes, opts.MaxUntrackedFileCount, !opts.DisableSecretScan)
	if err != nil {
		return CaptureResult{}, fmt.Errorf("repocheckpoint: untracked archive: %w", err)
	}

	final, err := gitClient.Fingerprint(ctx, req.WorktreePath)
	if err != nil {
		return CaptureResult{}, fmt.Errorf("repocheckpoint: final fingerprint: %w", err)
	}
	if !initial.Equal(final) {
		return CaptureResult{}, errIntegrity(
			"repocheckpoint: repository state changed during capture (fingerprint %s -> %s); capture aborted to avoid inconsistent evidence",
			initial.Digest, final.Digest)
	}

	now := clock.Now().UTC()

	stagedGz, err := gzipBytes(stagedPatch)
	if err != nil {
		return CaptureResult{}, fmt.Errorf("repocheckpoint: gzip staged patch: %w", err)
	}
	unstagedGz, err := gzipBytes(unstagedPatch)
	if err != nil {
		return CaptureResult{}, fmt.Errorf("repocheckpoint: gzip unstaged patch: %w", err)
	}

	files := map[string][]byte{
		"staged.patch.gz":   stagedGz,
		"unstaged.patch.gz": unstagedGz,
	}
	if archiveResult.Data != nil {
		files["untracked.zip"] = archiveResult.Data
	}

	var artifactRefs []ArtifactFile
	var totalBytes int64
	for name, content := range files {
		digest := sha256.Sum256(content)
		artifactRefs = append(artifactRefs, ArtifactFile{
			Path:   name,
			SHA256: hex.EncodeToString(digest[:]),
			Bytes:  int64(len(content)),
		})
		totalBytes += int64(len(content))
	}

	status := StatusComplete
	recoverabilityLevel := RecoverabilityComplete
	if len(archiveResult.Skipped) > 0 {
		status = StatusPartial
		recoverabilityLevel = RecoverabilityPartial
	}

	warnings := make([]string, 0, len(archiveResult.Skipped)+1)
	for _, sk := range archiveResult.Skipped {
		warnings = append(warnings, fmt.Sprintf("%s: %s", sk.Path, sk.Reason))
	}
	// Disclosed the same way an untracked skip is disclosed ("skipped
	// reasons recorded" per ADD §19.5), even though nothing here was
	// skipped: the patch artifact was altered from Git's raw output before
	// archiving. Recoverability stays Complete — unlike a skipped
	// untracked file, a redacted patch line is still fully present and
	// applicable, just with the secret-shaped span replaced.
	if stagedHadSecret {
		warnings = append(warnings, "staged.patch.gz: secret-shaped content detected and redacted in one or more added/removed lines ("+string(SkipSecretContent)+")")
	}
	if unstagedHadSecret {
		warnings = append(warnings, "unstaged.patch.gz: secret-shaped content detected and redacted in one or more added/removed lines ("+string(SkipSecretContent)+")")
	}

	staged, unstaged, untracked := classifyCounts(final.Entries)
	linesAdded, linesDeleted := sumNumstat(final.IndexNumstat, final.WorktreeNumstat)

	manifest := Manifest{
		SchemaVersion: ManifestSchemaVersion,
		CheckpointID:  string(req.CheckpointID),
		CreatedAt:     now,
		Status:        status,
		Repository: RepositoryInfo{
			RepositoryID: req.RepositoryID,
			WorktreeID:   string(req.WorktreeID),
			GitHead:      final.HeadOID,
			Branch:       final.Branch,
		},
		Snapshot: SnapshotInfo{
			IndexDiffHash:    sha256Hex(stagedPatch),
			WorktreeDiffHash: sha256Hex(unstagedPatch),
			StagedFiles:      staged,
			UnstagedFiles:    unstaged,
			UntrackedFiles:   untracked,
			LinesAdded:       linesAdded,
			LinesDeleted:     linesDeleted,
		},
		Artifacts: artifactRefs,
		Recoverability: RecoverabilityInfo{
			Level:            recoverabilityLevel,
			SkippedFileCount: len(archiveResult.Skipped),
			Warnings:         warnings,
		},
	}

	manifestJSON, err := marshalManifest(manifest)
	if err != nil {
		return CaptureResult{}, err
	}
	files["manifest.json"] = manifestJSON
	files["summary.md"] = []byte(renderSummary(manifest))
	if len(archiveResult.Skipped) > 0 {
		skippedJSON, err := marshalSkipped(archiveResult.Skipped)
		if err != nil {
			return CaptureResult{}, err
		}
		files["skipped-files.json"] = skippedJSON
	}

	// checkpoint-b09 security gate, defense in depth: req.CheckpointID is a
	// caller-supplied public-API field (production wiring always passes a
	// domain.IDGenerator-produced opaque ID, never untrusted input, but this
	// function's own contract does not otherwise prevent a caller from
	// passing one) — reject it outright if it could turn ArtifactsRoot/<ID>
	// into a path escaping ArtifactsRoot, rather than silently joining it
	// and letting writeArtifactDir's own defense-in-depth check (which
	// would also catch a "../" segment) be the only backstop.
	if !safeRelativeName(string(req.CheckpointID)) {
		return CaptureResult{}, errIntegrity("repocheckpoint: capture: checkpoint ID %q is not a safe path segment", req.CheckpointID)
	}
	finalDir := filepath.Join(req.ArtifactsRoot, string(req.CheckpointID))
	if err := writeArtifactDir(finalDir, files); err != nil {
		return CaptureResult{}, err
	}

	row := Row{
		ID:               req.CheckpointID,
		WorktreeID:       req.WorktreeID,
		TaskID:           req.TaskID,
		TurnID:           req.TurnID,
		Status:           status,
		ArtifactRoot:     finalDir,
		ManifestPath:     filepath.Join(finalDir, "manifest.json"),
		GitHead:          final.HeadOID,
		IndexDiffHash:    manifest.Snapshot.IndexDiffHash,
		WorktreeDiffHash: manifest.Snapshot.WorktreeDiffHash,
		Recoverability:   recoverabilityLevel,
		TotalBytes:       &totalBytes,
		CreatedAt:        now.Format(time.RFC3339),
	}

	return CaptureResult{Row: row, Manifest: manifest}, nil
}

func gzipBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// classifyCounts reports staged/unstaged/untracked file counts from a
// parsed status entry list, matching Appendix D's `snapshot` counts.
func classifyCounts(entries []gitx.Entry) (staged, unstaged, untracked int) {
	for _, e := range entries {
		switch e.Kind {
		case gitx.KindUntracked:
			untracked++
		case gitx.KindChanged, gitx.KindRenamed, gitx.KindUnmerged:
			if e.Index != 0 && e.Index != gitx.Unmodified {
				staged++
			}
			if e.Worktree != 0 && e.Worktree != gitx.Unmodified {
				unstaged++
			}
		}
	}
	return staged, unstaged, untracked
}

func sumNumstat(sets ...[]gitx.NumstatEntry) (added, deleted int) {
	for _, set := range sets {
		for _, e := range set {
			added += e.Added
			deleted += e.Deleted
		}
	}
	return added, deleted
}
