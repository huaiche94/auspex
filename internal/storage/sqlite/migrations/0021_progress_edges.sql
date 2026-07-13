-- 0021_progress_edges.sql
--
-- Auspex_ADD.md §12.2 canonical schema: `progress_edges` — explicit
-- dependency/relationship edges between Progress Tree nodes, beyond the
-- parent/child shape already carried by progress_nodes.parent_id
-- (checkpoint Part A range 0020-0029 per CONTRACT_FREEZE.md), transcribed
-- column-for-column from §12.2.
--
-- The composite PRIMARY KEY makes an edge naturally idempotent to
-- re-insert-with-conflict semantics: the same (task, from, to, kind) edge
-- cannot exist twice, so a duplicate plan upsert is a constraint violation
-- for the service layer to treat as "already present," never a silent
-- second row.
--
-- edge_kind is a plain TEXT discriminator (e.g. dependency policy classes
-- per ADD §6.4); like progress_nodes.status it is deliberately not a CHECK
-- constraint — released migrations are immutable (ADD §12.5), so the edge
-- vocabulary belongs to the Progress Tree service (checkpoint-a02), not
-- DDL.
CREATE TABLE progress_edges (
    task_id      TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    from_node_id TEXT NOT NULL REFERENCES progress_nodes(id) ON DELETE CASCADE,
    to_node_id   TEXT NOT NULL REFERENCES progress_nodes(id) ON DELETE CASCADE,
    edge_kind    TEXT NOT NULL,
    PRIMARY KEY(task_id, from_node_id, to_node_id, edge_kind)
);
