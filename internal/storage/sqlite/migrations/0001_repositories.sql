-- 0001_repositories.sql
--
-- Auspex_ADD.md §12.2 canonical schema: `repositories`. The root
-- foreign-key target for `worktrees` (0002) and, in later roles' migration
-- ranges, everything else that is scoped to a checked-out repository.
--
-- Numbering starts at 0001, not 0000, per ADD §12.5's documented
-- convention ("Migration file: 0001_name.sql").
--
-- canonical_root is the resolved repository root Auspex operates
-- against; git_common_dir is the shared .git directory (identical across
-- all worktrees of the same repository, per `git worktree` semantics),
-- hence the uniqueness constraint on it rather than on canonical_root.
CREATE TABLE repositories (
    id                 TEXT PRIMARY KEY,
    canonical_root     TEXT NOT NULL,
    git_common_dir     TEXT NOT NULL,
    remote_fingerprint TEXT,
    created_at         TEXT NOT NULL,
    last_seen_at       TEXT NOT NULL,
    UNIQUE(git_common_dir)
);
