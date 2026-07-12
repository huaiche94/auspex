-- 0002_worktrees.sql
--
-- Preflight_ADD.md §12.2 canonical schema: `worktrees`. FKs into
-- repositories (0001). Deleting a repository cascades to its worktrees,
-- since a worktree has no independent existence once its parent repository
-- record is gone (ADD §12.2 ON DELETE CASCADE).
CREATE TABLE worktrees (
    id            TEXT PRIMARY KEY,
    repository_id TEXT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    root_path     TEXT NOT NULL,
    git_dir       TEXT NOT NULL,
    branch_name   TEXT,
    created_at    TEXT NOT NULL,
    last_seen_at  TEXT NOT NULL,
    UNIQUE(repository_id, root_path)
);
