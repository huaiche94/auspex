// malicious_fixture_test.go implements qa-06 (docs/implementation/day1/
// EXECUTION_DAG.md's qa-06 row; agents/qa.md deliverable #6: "Path
// traversal/symlink and malicious fixture tests").
//
// # Relationship to checkpoint-b09's own adversarial sweep
//
// checkpoint's own b09 node (internal/repocheckpoint/security_adversarial_test.go,
// security_adversarial_internal_test.go) already built a comprehensive
// white-box adversarial sweep at the internal/repocheckpoint PACKAGE level —
// including finding and fixing a real path-traversal vulnerability in
// Verify (a tampered manifest.json artifact path could escape the
// checkpoint's own ArtifactRoot and read an arbitrary file on disk; fixed
// with the new safeArtifactPath guard, security.go). That node's own DAG
// note on its own adversarial test file ("Feeds qa-06") names this node as
// the integration-scope closure.
//
// This file is independent, external verification, not a rerun:
//
//   - it never imports internal/repocheckpoint's own test helpers or
//     fixtures (repoBuilder, captureReq, tamperManifestArtifactPath, etc. are
//     all re-derived here from scratch, with different attack shapes);
//   - it drives every scenario through the REAL, frozen
//     app.RepositoryCheckpointService port (repocheckpoint.NewService),
//     never the internal-package free functions (Capture/Verify/
//     RestoreDryRun) directly — checkpoint-b09's own adversarial tests call
//     those directly (white-box, package repocheckpoint / repocheckpoint_test);
//     this node calls only the same external surface a real caller
//     (runtime's orchestrator.CheckpointCreate, qa-02's own scenario) would
//     see, which is a genuinely different vantage point;
//   - every attack fixture below targets a distinct shape from checkpoint's
//     own:
//     (1) a chained double-symlink escape (symlink -> symlink -> outside
//     file, rather than a single-hop symlink or a symlinked-PARENT
//     directory, both already covered by b09's own suite);
//     (2) a tampered-manifest path-traversal attack against the STAGED
//     PATCH artifact specifically (staged.patch.gz), not the untracked.zip
//     entry b09's own regression test targets, driven through the full
//     capture -> archive -> verify -> restore-dry-run pipeline via the real
//     Service.Restore port — one step further than b09's own Verify-only
//     regression test — confirming the safeArtifactPath fix generalizes
//     across artifact kinds and holds from this external vantage point;
//     (3) a malicious CheckpointID reached through a MALICIOUS
//     domain.IDGenerator (modeling a compromised/buggy ID-generation
//     dependency) via the real Service.Create seam, rather than
//     b09's own test, which hand-supplies a literal CheckpointID string
//     directly to the free Capture function.
package integrationtest

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huaiche94/preflight/internal/app"
	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/gitx"
	"github.com/huaiche94/preflight/internal/repocheckpoint"
	"github.com/huaiche94/preflight/internal/storage/sqlite"
)

// --- independent fixtures (qa06-prefixed) ----------------------------------

type qa06Clock struct{ t time.Time }

func (c qa06Clock) Now() time.Time { return c.t }

type qa06IDs struct {
	n      int
	prefix string
}

func (g *qa06IDs) NewID() string {
	g.n++
	return g.prefix + "-" + qa06Itoa(g.n)
}

func qa06Itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// qa06Repo is an independent scratch-Git-repo builder — a distinct
// directory/commit history from every other integration test's own repo
// builder in this package.
type qa06Repo struct {
	t   *testing.T
	dir string
}

func newQA06Repo(t *testing.T) *qa06Repo {
	t.Helper()
	runner := gitx.ExecRunner{}
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", dir, err)
	}
	rb := &qa06Repo{t: t, dir: resolved}
	if _, err := runner.Run(context.Background(), rb.dir, "git", "--version"); err != nil {
		t.Skipf("git not available: %v", err)
	}
	rb.git("init", "-q", "-b", "main")
	rb.git("config", "user.name", "Preflight QA Adversarial")
	rb.git("config", "user.email", "qa-adversarial@preflight.invalid")
	rb.git("config", "commit.gpgsign", "false")
	return rb
}

func (rb *qa06Repo) git(args ...string) {
	rb.t.Helper()
	runner := gitx.ExecRunner{}
	res, err := runner.Run(context.Background(), rb.dir, "git", args...)
	if err != nil {
		rb.t.Fatalf("git %s: %v", args, err)
	}
	if res.ExitCode != 0 {
		rb.t.Fatalf("git %s: exit %d: %s", args, res.ExitCode, res.Stderr)
	}
}

func (rb *qa06Repo) write(rel, content string) {
	rb.t.Helper()
	abs := filepath.Join(rb.dir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		rb.t.Fatalf("MkdirAll(%s): %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		rb.t.Fatalf("WriteFile(%s): %v", rel, err)
	}
}

func qa06OpenDB(t *testing.T) *sqlite.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, "qa06.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}
	if err := db.Migrate(context.Background(), migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

// qa06SeedWorktree satisfies repository_checkpoints' NOT NULL FK into
// worktrees(id) (migration 0030) — a real repositories -> worktrees row
// pair, independent of every other integration test's own seed helper.
func qa06SeedWorktree(t *testing.T, db *sqlite.DB, repoID, worktreePath string, worktreeID domain.WorktreeID) {
	t.Helper()
	now := "2026-07-12T11:00:00Z"
	stmts := []string{
		`INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
		 VALUES ('` + repoID + `', '` + worktreePath + `', '` + worktreePath + `/.git', '` + now + `', '` + now + `')`,
		`INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
		 VALUES ('` + string(worktreeID) + `', '` + repoID + `', '` + worktreePath + `', '` + worktreePath + `/.git', '` + now + `', '` + now + `')`,
	}
	for _, stmt := range stmts {
		if _, err := db.Conn().ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
}

// qa06Harness bundles the REAL app.RepositoryCheckpointService port
// (repocheckpoint.NewService — the same production entrypoint
// orchestrator.CheckpointCreate/qa-02's scenario both call) plus this
// test's own tracked artifactsRoot, so every scenario below can locate a
// checkpoint's real on-disk directory (ArtifactsRoot/<ID>/, capture.go's
// own documented, stable layout) without reaching into
// internal/repocheckpoint's unexported Service fields.
type qa06Harness struct {
	svc           *repocheckpoint.Service
	artifactsRoot string
}

func (h qa06Harness) checkpointDir(id domain.RepositoryCheckpointID) string {
	return filepath.Join(h.artifactsRoot, string(id))
}

// qa06NewHarness builds a qa06Harness against a fresh migrated on-disk
// SQLite DB and a single fixed worktree resolution — every scenario below
// is a genuinely external, black-box exercise of the frozen service
// contract, never a call into internal/repocheckpoint's own
// package-private functions.
func qa06NewHarness(t *testing.T, repo *qa06Repo, worktreeID domain.WorktreeID, clock domain.Clock, ids domain.IDGenerator) qa06Harness {
	t.Helper()
	db := qa06OpenDB(t)
	qa06SeedWorktree(t, db, "repo-qa06-"+string(worktreeID), repo.dir, worktreeID)
	store := repocheckpoint.NewStore(db)
	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()
	resolve := func(_ context.Context, id domain.WorktreeID) (repocheckpoint.WorktreeLocation, error) {
		if id != worktreeID {
			return repocheckpoint.WorktreeLocation{}, &domain.Error{Code: domain.ErrCodeNotFound, Message: "unknown worktree"}
		}
		return repocheckpoint.WorktreeLocation{RepositoryID: "repo-qa06-" + string(worktreeID), Path: repo.dir}, nil
	}
	svc := repocheckpoint.NewService(client, store, clock, ids, artifactsRoot, resolve, repocheckpoint.CaptureOptions{})
	return qa06Harness{svc: svc, artifactsRoot: artifactsRoot}
}

// --- scenario 1: chained double-symlink escape ----------------------------

// TestMaliciousFixture_ChainedDoubleSymlinkEscape_NeverArchived plants an
// untracked file whose path is TWO symlink hops away from its real,
// outside-the-worktree content (evidence.txt -> sub/hop1 -> outside
// secret), distinct from checkpoint-b09's own single-hop symlink and
// symlinked-parent-directory cases. Drives the REAL Service.Create
// end-to-end and confirms the escape never appears in the resulting
// archive, and a legitimate untracked sibling file still does (proving
// this is a real, working capture, not a vacuous "everything got skipped"
// pass).
func TestMaliciousFixture_ChainedDoubleSymlinkEscape_NeverArchived(t *testing.T) {
	repo := newQA06Repo(t)
	repo.write("tracked.txt", "committed content\n")
	repo.git("add", "tracked.txt")
	repo.git("commit", "-q", "-m", "initial")
	repo.write("legit-untracked.txt", "genuine untracked content, must be archived\n")

	outsideDir := t.TempDir()
	secretOutside := filepath.Join(outsideDir, "true-secret.txt")
	if err := os.WriteFile(secretOutside, []byte("MUST NEVER BE ARCHIVED"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	// hop1 (inside a subdirectory of the repo) points at the outside
	// secret; evidence.txt (inside the repo root) is a SECOND symlink
	// pointing at hop1 — a two-level chain, not a single hop.
	if err := os.MkdirAll(filepath.Join(repo.dir, "sub"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	hop1 := filepath.Join(repo.dir, "sub", "hop1")
	if err := os.Symlink(secretOutside, hop1); err != nil {
		t.Skipf("symlinks not supported on this platform: %v", err)
	}
	evidenceLink := filepath.Join(repo.dir, "evidence.txt")
	if err := os.Symlink(hop1, evidenceLink); err != nil {
		t.Skipf("symlinks not supported on this platform: %v", err)
	}

	worktreeID := domain.WorktreeID("wt-qa06-symlink")
	h := qa06NewHarness(t, repo, worktreeID, qa06Clock{t: time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)}, &qa06IDs{prefix: "sym"})

	result, err := h.svc.Create(context.Background(), app.CreateRepositoryCheckpointRequest{WorktreeID: worktreeID})
	if err != nil {
		t.Fatalf("Service.Create: %v", err)
	}
	if result.ID == "" {
		t.Fatal("expected a real checkpoint ID from Create")
	}

	verification, err := h.svc.Verify(context.Background(), result.ID)
	if err != nil {
		t.Fatalf("Service.Verify: %v", err)
	}
	if !verification.Valid {
		t.Fatal("expected the checkpoint (with the malicious symlink chain correctly excluded) to verify as valid")
	}

	zipPath := filepath.Join(h.checkpointDir(result.ID), "untracked.zip")
	names := qa06ReadZipNames(t, zipPath)
	foundLegit := false
	for _, n := range names {
		if n == "evidence.txt" || n == "sub/hop1" || n == "hop1" {
			t.Fatalf("SECURITY: symlink-escape entry %q present in archive", n)
		}
		if n == "legit-untracked.txt" {
			foundLegit = true
		}
	}
	if !foundLegit {
		t.Fatalf("expected the legitimate untracked sibling file to still be archived; entries: %v", names)
	}
	qa06AssertZipNeverContainsContent(t, zipPath, "MUST NEVER BE ARCHIVED")
}

// --- scenario 2: tampered-manifest path traversal against the STAGED PATCH
// artifact, driven through the full capture -> verify -> restore-dry-run
// pipeline via the real Service.Restore entrypoint -------------------------

// TestMaliciousFixture_TamperedManifestPatchPath_RestoreDryRunRejects
// hand-tampers a REAL manifest.json's staged.patch.gz artifact path entry
// (as opposed to checkpoint-b09's own regression test, which targets the
// untracked.zip entry) to a traversal string reaching a real secret file
// outside the checkpoint's artifact root, then drives it through the real,
// frozen Service.Restore port (dry-run only — never mutates anything) —
// one layer further than checkpoint-b09's own Verify-only regression test —
// confirming the safeArtifactPath fix holds (a) for a different artifact
// kind and (b) from this external, black-box service-port vantage point.
func TestMaliciousFixture_TamperedManifestPatchPath_RestoreDryRunRejects(t *testing.T) {
	repo := newQA06Repo(t)
	repo.write("tracked.txt", "v1\n")
	repo.git("add", "tracked.txt")
	repo.git("commit", "-q", "-m", "initial")
	// A staged change so Capture actually produces a non-empty
	// staged.patch.gz artifact to tamper with.
	repo.write("tracked.txt", "v2 - staged change\n")
	repo.git("add", "tracked.txt")

	worktreeID := domain.WorktreeID("wt-qa06-patch-tamper")
	h := qa06NewHarness(t, repo, worktreeID, qa06Clock{t: time.Date(2026, 7, 12, 12, 30, 0, 0, time.UTC)}, &qa06IDs{prefix: "patch"})

	result, err := h.svc.Create(context.Background(), app.CreateRepositoryCheckpointRequest{WorktreeID: worktreeID})
	if err != nil {
		t.Fatalf("Service.Create: %v", err)
	}

	checkpointDir := h.checkpointDir(result.ID)
	manifestPath := filepath.Join(checkpointDir, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("expected a real manifest.json at %s: %v", manifestPath, err)
	}
	patchPath := filepath.Join(checkpointDir, "staged.patch.gz")
	if _, err := os.Stat(patchPath); err != nil {
		t.Skipf("no staged.patch.gz produced by this capture (nothing staged?): %v", err)
	}

	// A real secret file outside the checkpoint directory entirely, that a
	// correctly-guarded Restore dry-run must never read.
	secretDir := t.TempDir()
	secretFile := filepath.Join(secretDir, "qa06-secret.txt")
	if err := os.WriteFile(secretFile, []byte("QA06 SECRET: MUST NEVER BE READ BY RESTORE DRY-RUN"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	traversal, err := filepath.Rel(checkpointDir, secretFile)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	if !qa06HasTraversalSegment(traversal) {
		t.Fatalf("test setup bug: expected a ../-containing relative path, got %q", traversal)
	}

	qa06TamperManifestArtifactPath(t, manifestPath, "staged.patch.gz", traversal)

	restoreResult, err := h.svc.Restore(context.Background(), app.RestoreRepositoryCheckpointRequest{ID: result.ID})
	if err == nil {
		t.Fatalf("SECURITY: Service.Restore succeeded against a manifest whose staged.patch.gz path escapes the checkpoint artifact root; result=%+v", restoreResult)
	}
	var derr *domain.Error
	if !errors.As(err, &derr) {
		t.Fatalf("err = %v (%T), want *domain.Error", err, err)
	}
	if derr.Code != domain.ErrCodeConflict {
		t.Errorf("err.Code = %q, want %q", derr.Code, domain.ErrCodeConflict)
	}
	foundTraversalProblem := false
	for _, p := range derr.Details {
		if strings.Contains(p, "escapes checkpoint artifact root") {
			foundTraversalProblem = true
		}
		if strings.Contains(p, "QA06 SECRET") {
			t.Fatal("SECURITY: the secret file's content leaked into Restore's error details")
		}
	}
	if !foundTraversalProblem {
		t.Fatalf("expected a traversal-specific problem in err.Details, got: %v (message: %s)", derr.Details, derr.Message)
	}
	if strings.Contains(derr.Message, "QA06 SECRET") {
		t.Fatal("SECURITY: the secret file's content leaked into Restore's error message")
	}
}

// --- scenario 3: malicious CheckpointID via the real Service.Create seam,
// reached through a MALICIOUS domain.IDGenerator ---------------------------

// TestMaliciousFixture_ServiceCreate_MaliciousIDGenerator_TraversalRejected
// proves Capture's CheckpointID guard holds even when reached through the
// real Service.Create seam with a MALICIOUS domain.IDGenerator (modeling a
// compromised or buggy ID-generation dependency) — a different attack
// surface from checkpoint-b09's own test, which calls the free Capture
// function directly with a hand-chosen literal CheckpointID string. Two
// distinct traversal shapes are tried, neither identical to b09's own
// ("../../escape-checkpoint-id" and an absolute path): a "./.."-prefixed
// nested traversal, and a nested traversal buried after an ordinary-looking
// prefix segment (an ID that superficially "looks like" a normal opaque ID
// until its trailing segment is inspected).
func TestMaliciousFixture_ServiceCreate_MaliciousIDGenerator_TraversalRejected(t *testing.T) {
	repo := newQA06Repo(t)
	repo.write("a.txt", "content\n")
	repo.git("add", "a.txt")
	repo.git("commit", "-q", "-m", "initial")

	worktreeID := domain.WorktreeID("wt-qa06-malicious-id")
	db := qa06OpenDB(t)
	qa06SeedWorktree(t, db, "repo-qa06-malicious-id", repo.dir, worktreeID)
	store := repocheckpoint.NewStore(db)
	client := gitx.NewClient(gitx.ExecRunner{})
	artifactsRoot := t.TempDir()
	resolve := func(_ context.Context, id domain.WorktreeID) (repocheckpoint.WorktreeLocation, error) {
		if id != worktreeID {
			return repocheckpoint.WorktreeLocation{}, &domain.Error{Code: domain.ErrCodeNotFound, Message: "unknown worktree"}
		}
		return repocheckpoint.WorktreeLocation{RepositoryID: "repo-qa06-malicious-id", Path: repo.dir}, nil
	}

	maliciousIDs := []string{
		"./../../escape-via-dot-slash",
		"looks-like-a-normal-id/../../escape-nested",
	}
	for _, malID := range maliciousIDs {
		t.Run(malID, func(t *testing.T) {
			svc := repocheckpoint.NewService(client, store, qa06Clock{t: time.Date(2026, 7, 12, 13, 0, 0, 0, time.UTC)}, &qa06FixedIDs{id: malID}, artifactsRoot, resolve, repocheckpoint.CaptureOptions{})
			_, err := svc.Create(context.Background(), app.CreateRepositoryCheckpointRequest{WorktreeID: worktreeID})
			if err == nil {
				t.Fatalf("SECURITY: Service.Create succeeded with a malicious IDGenerator-supplied CheckpointID %q", malID)
			}
			// Nothing must exist outside artifactsRoot as a result.
			escaped := filepath.Join(filepath.Dir(filepath.Dir(artifactsRoot)), "escape-via-dot-slash")
			if _, statErr := os.Stat(escaped); statErr == nil {
				t.Fatal("SECURITY: a directory was created outside ArtifactsRoot from a malicious generated CheckpointID")
			}
			escapedNested := filepath.Join(filepath.Dir(filepath.Dir(artifactsRoot)), "escape-nested")
			if _, statErr := os.Stat(escapedNested); statErr == nil {
				t.Fatal("SECURITY: a directory was created outside ArtifactsRoot from a malicious generated nested CheckpointID")
			}
		})
	}
}

// qa06FixedIDs is a domain.IDGenerator that always returns the same
// (attacker-controlled-shaped) string — modeling a compromised/buggy ID
// generator dependency, the attack surface this scenario targets.
type qa06FixedIDs struct{ id string }

func (g *qa06FixedIDs) NewID() string { return g.id }

// --- helpers ---------------------------------------------------------------

func qa06HasTraversalSegment(p string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// qa06TamperManifestArtifactPath rewrites exactly one artifacts[].path
// field inside a real manifest.json on disk, keeping the rest of the
// document byte-for-byte as Capture produced it — a surgical, realistic
// tamper (this is exactly the shape a compromised checkpoint directory, or
// a hand-edited manifest from an untrusted "restore this checkpoint"
// source, would take).
func qa06TamperManifestArtifactPath(t *testing.T, manifestPath, artifactName, newPath string) {
	t.Helper()
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	escaped := qa06JSONEscape(newPath)
	candidates := [][2]string{
		{`"path":"` + artifactName + `"`, `"path":"` + escaped + `"`},
		{`"path": "` + artifactName + `"`, `"path": "` + escaped + `"`},
	}
	replaced := string(raw)
	changed := false
	for _, c := range candidates {
		if strings.Contains(replaced, c[0]) {
			replaced = strings.Replace(replaced, c[0], c[1], 1)
			changed = true
			break
		}
	}
	if !changed {
		t.Fatalf("qa06TamperManifestArtifactPath: could not find artifact %q path field to tamper in manifest:\n%s", artifactName, raw)
	}
	if err := os.WriteFile(manifestPath, []byte(replaced), 0o644); err != nil {
		t.Fatalf("write tampered manifest: %v", err)
	}
}

func qa06JSONEscape(s string) string {
	out := make([]byte, 0, len(s)+2)
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			out = append(out, '\\', '\\')
			continue
		}
		out = append(out, s[i])
	}
	return string(out)
}

func qa06ReadZipNames(t *testing.T, path string) []string {
	t.Helper()
	if _, statErr := os.Stat(path); statErr != nil {
		return nil // no archive at all — trivially contains nothing
	}
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip %s: %v", path, err)
	}
	defer func() { _ = r.Close() }()
	names := make([]string, 0, len(r.File))
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	return names
}

func qa06AssertZipNeverContainsContent(t *testing.T, zipPath, forbiddenSubstring string) {
	t.Helper()
	if _, statErr := os.Stat(zipPath); statErr != nil {
		return
	}
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open zip %s: %v", zipPath, err)
	}
	defer func() { _ = r.Close() }()
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			continue
		}
		content, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			continue
		}
		if strings.Contains(string(content), forbiddenSubstring) {
			t.Fatalf("SECURITY: forbidden content found in zip entry %q", f.Name)
		}
	}
}
