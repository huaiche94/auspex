package repocheckpoint_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/gitx"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// fixedClock is a deterministic domain.Clock test double.
type fixedClock struct{ t time.Time }

func (f fixedClock) Now() time.Time { return f.t }

// seqIDs is a deterministic domain.IDGenerator test double.
type seqIDs struct{ n int }

func (s *seqIDs) NewID() string {
	s.n++
	return "checkpoint-" + itoa(s.n)
}

func itoa(n int) string {
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

// repoBuilder creates and mutates a real temporary Git repository for
// integration tests, mirroring internal/gitx's own (unexported, so not
// reusable from this external test package) repoBuilder helper.
type repoBuilder struct {
	t      *testing.T
	dir    string
	runner domain.ProcessRunner
}

func newRepoBuilder(t *testing.T) *repoBuilder {
	t.Helper()
	rb := &repoBuilder{t: t, runner: gitx.ExecRunner{}}

	dir, err := os.MkdirTemp("", "auspex-repocheckpoint-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", dir, err)
	}
	rb.dir = resolved

	if _, err := rb.runner.Run(context.Background(), rb.dir, "git", "--version"); err != nil {
		t.Skipf("git not available: %v", err)
	}

	rb.git("init", "-q", "-b", "main")
	rb.git("config", "user.name", "Auspex Test")
	rb.git("config", "user.email", "test@auspex.invalid")
	rb.git("config", "commit.gpgsign", "false")
	// Pin line-ending conversion OFF: windows-latest's git ships with a
	// system-level core.autocrlf=true, which rewrites LF blobs to CRLF at
	// checkout — breaking every byte-exact round-trip assertion these
	// tests make about worktree file contents (issue #24). The fixtures
	// here write LF bytes and assert LF bytes; make that explicit rather
	// than inherited from the host's config.
	rb.git("config", "core.autocrlf", "false")
	return rb
}

func (rb *repoBuilder) git(args ...string) domain.ProcessResult {
	rb.t.Helper()
	res, err := rb.runner.Run(context.Background(), rb.dir, "git", args...)
	if err != nil {
		rb.t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	if res.ExitCode != 0 {
		rb.t.Fatalf("git %s: exit %d: %s", strings.Join(args, " "), res.ExitCode, res.Stderr)
	}
	return res
}

func (rb *repoBuilder) write(rel, content string) {
	rb.t.Helper()
	abs := filepath.Join(rb.dir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		rb.t.Fatalf("MkdirAll(%s): %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		rb.t.Fatalf("WriteFile(%s): %v", rel, err)
	}
}

func openTestDB(t *testing.T) *sqlite.DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "auspex.db")
	db, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("sqlite.AllMigrations: %v", err)
	}
	if err := db.Migrate(context.Background(), migrations); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	return db
}

// seedWorktree inserts a minimal repositories -> worktrees chain so
// repository_checkpoints' FK into worktrees(id) (0030's schema) is
// satisfiable, and returns the new worktree's ID.
func seedWorktree(t *testing.T, db *sqlite.DB) domain.WorktreeID {
	t.Helper()
	ctx := context.Background()
	repoID := "repo-" + t.Name()
	worktreeID := "worktree-" + t.Name()
	now := time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)

	err := db.WithTx(ctx, func(ctx context.Context) error {
		q := sqlite.QuerierFromContext(ctx, db)
		if _, err := q.ExecContext(ctx, `
			INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
			VALUES (?, ?, ?, ?, ?)`, repoID, "/tmp/"+repoID, "/tmp/"+repoID+"/.git", now, now); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
			VALUES (?, ?, ?, ?, ?, ?)`, worktreeID, repoID, "/tmp/"+repoID, "/tmp/"+repoID+"/.git", now, now); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seedWorktree: %v", err)
	}
	return domain.WorktreeID(worktreeID)
}
