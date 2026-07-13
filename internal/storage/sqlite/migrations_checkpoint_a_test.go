// migrations_checkpoint_a_test.go — checkpoint-a01 (checkpoint role,
// Part A): tests for the Progress Tree migration files
// 0020_progress_nodes.sql / 0021_progress_edges.sql / 0022_artifacts.sql
// (checkpoint's 0020-0029 range per CONTRACT_FREEZE.md).
//
// This file is owned by the checkpoint role, NOT by foundation, even
// though it lives in foundation's internal/storage/sqlite directory: the
// DAG's frozen validation command for checkpoint-a01 is
// `go test ./internal/storage/sqlite/... -run Migration0020`, which
// requires the tests to live in this package. Every test name carries the
// "Migration0020" selector (0020 = the range's lower bound, standing for
// the whole checkpoint-a01 migration set) so that command selects exactly
// this file's tests. Foundation's own migrate_test.go is untouched.
package sqlite_test

import (
	"context"
	"testing"

	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// openMigrated returns a temp-file DB with every embedded migration
// applied — the same real AllMigrations() set a deployed binary carries.
// Shared by this file and migrations_checkpoint_b_test.go (both
// checkpoint-owned).
func openMigrated(t *testing.T) *sqlite.DB {
	t.Helper()
	db := openTemp(t)
	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}
	if err := db.Migrate(context.Background(), migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

// mustExec fails the test on error; for seed/act statements that are
// expected to succeed.
func mustExec(t *testing.T, db *sqlite.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Conn().ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// tryExec returns the statement error; for assertions that a constraint
// rejects a write.
func tryExec(t *testing.T, db *sqlite.DB, query string, args ...any) error {
	t.Helper()
	_, err := db.Conn().ExecContext(context.Background(), query, args...)
	return err
}

// seedTask inserts the minimal foundation-owned row chain
// (repositories -> worktrees -> tasks) that checkpoint Part A's tables FK
// into, and returns the task id.
func seedTask(t *testing.T, db *sqlite.DB, taskID string) string {
	t.Helper()
	const ts = "2026-07-12T00:00:00Z"
	mustExec(t, db,
		`INSERT OR IGNORE INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
		 VALUES ('repo1', '/tmp/repo', '/tmp/repo/.git', ?, ?)`, ts, ts)
	mustExec(t, db,
		`INSERT OR IGNORE INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
		 VALUES ('wt1', 'repo1', '/tmp/repo', '/tmp/repo/.git', ?, ?)`, ts, ts)
	mustExec(t, db,
		`INSERT INTO tasks (id, worktree_id, objective_hash, status, created_at, updated_at)
		 VALUES (?, 'wt1', 'hash1', 'pending', ?, ?)`, taskID, ts, ts)
	return taskID
}

// insertNode inserts a progress_nodes row with sensible defaults. parentID
// may be nil for a root node.
func insertNode(t *testing.T, db *sqlite.DB, id, taskID string, parentID *string, ordinal int) {
	t.Helper()
	mustExec(t, db,
		`INSERT INTO progress_nodes (id, task_id, parent_id, ordinal, kind, title, status, version, updated_at)
		 VALUES (?, ?, ?, ?, 'step', 'node '||?, 'pending', 1, '2026-07-12T00:00:00Z')`,
		id, taskID, parentID, ordinal, id)
}

func TestMigration0020_AllMigrations_IncludesProgressTreeRange(t *testing.T) {
	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}

	// Assert presence (version + name) of exactly this node's files without
	// asserting the total migration count — other roles' ranges land in the
	// same directory on their own schedule and must not break this test.
	want := map[int]string{
		20: "progress_nodes",
		21: "progress_edges",
		22: "artifacts",
	}
	found := map[int]string{}
	for _, m := range migrations {
		if _, ok := want[m.Version]; ok {
			found[m.Version] = m.Name
		}
	}
	for version, name := range want {
		if found[version] != name {
			t.Errorf("migration %04d = %q, want %q", version, found[version], name)
		}
	}
}

func TestMigration0020_FromEmptyDatabase_CreatesProgressTreeSchema(t *testing.T) {
	db := openMigrated(t)
	ctx := context.Background()

	for _, table := range []string{"progress_nodes", "progress_edges", "artifacts"} {
		var name string
		q := `SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`
		if err := db.Conn().QueryRowContext(ctx, q, table).Scan(&name); err != nil {
			t.Errorf("table %s not created: %v", table, err)
		}
	}

	// ADD §12.3 required index ships with the table's own migration.
	var idx string
	q := `SELECT name FROM sqlite_master WHERE type = 'index' AND name = 'idx_progress_nodes_task_status'`
	if err := db.Conn().QueryRowContext(ctx, q).Scan(&idx); err != nil {
		t.Errorf("index idx_progress_nodes_task_status not created: %v", err)
	}
}

func TestMigration0020_ProgressNodes_RejectsUnknownTask(t *testing.T) {
	db := openMigrated(t)

	err := tryExec(t, db,
		`INSERT INTO progress_nodes (id, task_id, ordinal, kind, title, status, version, updated_at)
		 VALUES ('n1', 'no-such-task', 0, 'step', 'orphan', 'pending', 1, '2026-07-12T00:00:00Z')`)
	if err == nil {
		t.Fatal("expected a foreign key violation inserting a node with an unknown task_id")
	}
}

func TestMigration0020_ProgressNodes_TaskDeleteCascades(t *testing.T) {
	db := openMigrated(t)
	ctx := context.Background()
	taskID := seedTask(t, db, "task1")

	root := "n-root"
	insertNode(t, db, root, taskID, nil, 0)
	insertNode(t, db, "n-child", taskID, &root, 0)

	mustExec(t, db, `DELETE FROM tasks WHERE id = ?`, taskID)

	var count int
	if err := db.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM progress_nodes`).Scan(&count); err != nil {
		t.Fatalf("count progress_nodes: %v", err)
	}
	if count != 0 {
		t.Errorf("progress_nodes count after task delete = %d, want 0 (ON DELETE CASCADE)", count)
	}
}

func TestMigration0020_ProgressNodes_ParentDeleteCascadesSubtree(t *testing.T) {
	db := openMigrated(t)
	ctx := context.Background()
	taskID := seedTask(t, db, "task1")

	parent := "n-parent"
	child := "n-child"
	insertNode(t, db, parent, taskID, nil, 0)
	insertNode(t, db, child, taskID, &parent, 0)
	insertNode(t, db, "n-grandchild", taskID, &child, 0)
	insertNode(t, db, "n-sibling", taskID, nil, 1)

	mustExec(t, db, `DELETE FROM progress_nodes WHERE id = ?`, parent)

	var count int
	if err := db.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM progress_nodes`).Scan(&count); err != nil {
		t.Fatalf("count progress_nodes: %v", err)
	}
	if count != 1 {
		t.Errorf("progress_nodes count after parent delete = %d, want 1 (only the unrelated sibling survives)", count)
	}
}

func TestMigration0020_ProgressNodes_UniqueSiblingOrdinal(t *testing.T) {
	db := openMigrated(t)
	taskID := seedTask(t, db, "task1")

	parent := "n-parent"
	insertNode(t, db, parent, taskID, nil, 0)
	insertNode(t, db, "n-a", taskID, &parent, 0)

	err := tryExec(t, db,
		`INSERT INTO progress_nodes (id, task_id, parent_id, ordinal, kind, title, status, version, updated_at)
		 VALUES ('n-b', ?, ?, 0, 'step', 'dup ordinal', 'pending', 1, '2026-07-12T00:00:00Z')`,
		taskID, parent)
	if err == nil {
		t.Fatal("expected unique constraint violation on duplicate (task_id, parent_id, ordinal)")
	}
}

func TestMigration0020_ProgressEdges_DuplicateEdgeRejected(t *testing.T) {
	db := openMigrated(t)
	taskID := seedTask(t, db, "task1")
	insertNode(t, db, "n-a", taskID, nil, 0)
	insertNode(t, db, "n-b", taskID, nil, 1)

	mustExec(t, db,
		`INSERT INTO progress_edges (task_id, from_node_id, to_node_id, edge_kind)
		 VALUES (?, 'n-a', 'n-b', 'depends_on')`, taskID)

	// Same (task, from, to, kind) twice is a PK violation — the service
	// layer treats it as "already present," never a silent second row.
	err := tryExec(t, db,
		`INSERT INTO progress_edges (task_id, from_node_id, to_node_id, edge_kind)
		 VALUES (?, 'n-a', 'n-b', 'depends_on')`, taskID)
	if err == nil {
		t.Fatal("expected primary key violation on duplicate edge")
	}

	// A different edge_kind between the same nodes is a distinct edge.
	mustExec(t, db,
		`INSERT INTO progress_edges (task_id, from_node_id, to_node_id, edge_kind)
		 VALUES (?, 'n-a', 'n-b', 'informs')`, taskID)
}

func TestMigration0020_ProgressEdges_NodeDeleteCascades(t *testing.T) {
	db := openMigrated(t)
	ctx := context.Background()
	taskID := seedTask(t, db, "task1")
	insertNode(t, db, "n-a", taskID, nil, 0)
	insertNode(t, db, "n-b", taskID, nil, 1)
	mustExec(t, db,
		`INSERT INTO progress_edges (task_id, from_node_id, to_node_id, edge_kind)
		 VALUES (?, 'n-a', 'n-b', 'depends_on')`, taskID)

	mustExec(t, db, `DELETE FROM progress_nodes WHERE id = 'n-b'`)

	var count int
	if err := db.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM progress_edges`).Scan(&count); err != nil {
		t.Fatalf("count progress_edges: %v", err)
	}
	if count != 0 {
		t.Errorf("progress_edges count after node delete = %d, want 0 (ON DELETE CASCADE)", count)
	}

	// Edges also reject endpoints that never existed.
	if err := tryExec(t, db,
		`INSERT INTO progress_edges (task_id, from_node_id, to_node_id, edge_kind)
		 VALUES (?, 'n-a', 'no-such-node', 'depends_on')`, taskID); err == nil {
		t.Fatal("expected foreign key violation on edge to unknown node")
	}
}

func TestMigration0020_Artifacts_NodeDeleteDetachesEvidence(t *testing.T) {
	db := openMigrated(t)
	ctx := context.Background()
	taskID := seedTask(t, db, "task1")
	insertNode(t, db, "n-a", taskID, nil, 0)

	mustExec(t, db,
		`INSERT INTO artifacts (id, task_id, progress_node_id, kind, uri, bytes, sha256, validation_status, created_at)
		 VALUES ('art1', ?, 'n-a', 'file', 'file:///tmp/evidence.md', 42, 'abc123', 'verified', '2026-07-12T00:00:00Z')`,
		taskID)

	// Deleting the node must detach (SET NULL), never destroy, the durable
	// evidence row (Constitution §6.2: evidence outlives conversational
	// state — and here, the node row itself).
	mustExec(t, db, `DELETE FROM progress_nodes WHERE id = 'n-a'`)

	var nodeID *string
	if err := db.Conn().QueryRowContext(ctx, `SELECT progress_node_id FROM artifacts WHERE id = 'art1'`).Scan(&nodeID); err != nil {
		t.Fatalf("select artifact: %v", err)
	}
	if nodeID != nil {
		t.Errorf("artifacts.progress_node_id = %v, want nil after node delete (ON DELETE SET NULL)", *nodeID)
	}

	// Deleting the whole task, by contrast, cascades the evidence away.
	mustExec(t, db, `DELETE FROM tasks WHERE id = ?`, taskID)
	var count int
	if err := db.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM artifacts`).Scan(&count); err != nil {
		t.Fatalf("count artifacts: %v", err)
	}
	if count != 0 {
		t.Errorf("artifacts count after task delete = %d, want 0 (ON DELETE CASCADE)", count)
	}
}

func TestMigration0020_Artifacts_DuplicateEvidenceRejected_DifferentDigestDistinct(t *testing.T) {
	db := openMigrated(t)
	taskID := seedTask(t, db, "task1")
	insertNode(t, db, "n-a", taskID, nil, 0)

	insert := func(id, uri, sha string) error {
		return tryExec(t, db,
			`INSERT INTO artifacts (id, task_id, progress_node_id, kind, uri, bytes, sha256, validation_status, created_at)
			 VALUES (?, ?, 'n-a', 'file', ?, 42, ?, 'verified', '2026-07-12T00:00:00Z')`,
			id, taskID, uri, sha)
	}

	if err := insert("art1", "file:///tmp/evidence.md", "sha-one"); err != nil {
		t.Fatalf("insert art1: %v", err)
	}
	// Identical evidence (same node, uri, digest) is one row, not two.
	if err := insert("art2", "file:///tmp/evidence.md", "sha-one"); err == nil {
		t.Fatal("expected unique constraint violation on duplicate (progress_node_id, uri, sha256)")
	}
	// Same URI with a different digest is a DISTINCT row — the storage
	// layer must retain both so CompleteNode (checkpoint-a04) can surface
	// the conflict rather than silently overwrite (Constitution §6.6).
	if err := insert("art3", "file:///tmp/evidence.md", "sha-two"); err != nil {
		t.Fatalf("insert art3 (same uri, different sha256) should be a distinct row: %v", err)
	}
}

func TestMigration0020_Artifacts_RejectsUnknownTask(t *testing.T) {
	db := openMigrated(t)

	err := tryExec(t, db,
		`INSERT INTO artifacts (id, task_id, kind, uri, bytes, sha256, validation_status, created_at)
		 VALUES ('art1', 'no-such-task', 'file', 'file:///tmp/x', 1, 'sha', 'verified', '2026-07-12T00:00:00Z')`)
	if err == nil {
		t.Fatal("expected foreign key violation inserting an artifact with an unknown task_id")
	}
}
