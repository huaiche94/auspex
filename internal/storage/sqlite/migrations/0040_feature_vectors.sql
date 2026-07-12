-- 0040_feature_vectors.sql
--
-- Preflight_ADD.md §12.2 canonical schema: `feature_vectors`. First table in
-- predictor's migration range (0040-0049 per CONTRACT_FREEZE.md's
-- migration-range table).
--
-- turn_id intentionally has NO FK constraint at this migration: the `turns`
-- table it would reference (Preflight_ADD.md §12.2) is claude-provider's
-- 0010-0019 range and does not exist yet on this branch. This mirrors
-- 0004_tasks.sql's own precedent for active_node_id/progress_nodes exactly
-- ("SQLite has no deferred cross-table FK addition without recreating the
-- table"): a syntactically present but unresolvable REFERENCES clause is
-- not just inert, it actively breaks unrelated cascading DELETEs anywhere
-- else in the schema, because SQLite's foreign_keys=ON resolves every
-- FK-referenced table reachable from a DELETE's cascade graph at prepare
-- time, not only the table being deleted from (verified empirically against
-- TestCoreMigrations_ForeignKeys_RepositoryToWorktree, which failed with
-- "no such table: main.turns" once this table declared REFERENCES turns(id)
-- with no such table present). turn_id remains a plain TEXT PRIMARY KEY;
-- predictor's persistence layer (predictor-09) is responsible for keeping
-- it consistent with turns.id once claude-provider's migrations land.
CREATE TABLE feature_vectors (
    turn_id              TEXT PRIMARY KEY,
    feature_set_version  TEXT NOT NULL,
    features_json        TEXT NOT NULL,
    created_at           TEXT NOT NULL
);
