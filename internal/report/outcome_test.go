// outcome_test.go: the #140 outcome ledger (OutcomeEconomics) against a
// real migrated DB — the same seeding-by-SQL convention report_test.go
// uses, extended with the correlator-stamped attribution columns
// (events.task_id / events.progress_node_id) and the progress_nodes FK
// chain those stamps resolve against.
package report

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// seedProgressChain inserts the repositories -> worktrees ->
// provider_sessions -> tasks chain progress_nodes' FKs require, plus one
// node per (id, status) pair, all under one task.
func seedProgressChain(t *testing.T, db *sqlite.DB, sessionID string, nodes map[string]string) {
	t.Helper()
	now := testNow.Format(time.RFC3339Nano)
	exec(t, db, `INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
		VALUES ('repo-oe', '/tmp/oe', '/tmp/oe/.git', ?, ?)`, now, now)
	exec(t, db, `INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
		VALUES ('wt-oe', 'repo-oe', '/tmp/oe', '/tmp/oe/.git', ?, ?)`, now, now)
	exec(t, db, `INSERT INTO provider_sessions (id, worktree_id, provider, invocation_mode, started_at, metadata_json)
		VALUES (?, 'wt-oe', 'claude', 'managed_stream_json', ?, '{}')`, sessionID, now)
	exec(t, db, `INSERT INTO tasks (id, session_id, worktree_id, objective_hash, status, created_at, updated_at)
		VALUES ('task-oe', ?, 'wt-oe', 'hash-oe', 'pending', ?, ?)`, sessionID, now, now)
	ordinal := 0
	for id, status := range nodes {
		ordinal++
		exec(t, db, `INSERT INTO progress_nodes
				(id, task_id, ordinal, kind, title, status, version, updated_at)
			VALUES (?, 'task-oe', ?, 'step', 'n', ?, 1, ?)`,
			id, ordinal, status, now)
	}
}

// seedAttributedManagedTurn is seedManagedTurn plus the correlator's
// task/node stamp on both events.
func seedAttributedManagedTurn(t *testing.T, db *sqlite.DB, ts time.Time, sessionID, turnID, nodeID string, costUSD float64) {
	t.Helper()
	insert := func(eventType string, at time.Time, payload string) {
		t.Helper()
		eventSeq++
		exec(t, db, `INSERT INTO events
				(event_id, schema_version, event_type, occurred_at, observed_at, source, provider, session_id, turn_id, task_id, progress_node_id, payload_json)
			VALUES (?, 'auspex.event.v1', ?, ?, ?, 'test', 'claude', ?, ?, 'task-oe', ?, ?)`,
			fmt.Sprintf("ev-oe-%04d", eventSeq), eventType,
			at.UTC().Format(time.RFC3339Nano), at.UTC().Format(time.RFC3339Nano),
			sessionID, turnID, nodeID, payload)
	}
	insert("provider.turn.completed", ts, `{"result_subtype":"success"}`)
	insert("provider.usage.observed", ts.Add(time.Second),
		fmt.Sprintf(`{"total_cost_usd":%g,"input_tokens":10,"output_tokens":20}`, costUSD))
}

func TestGenerateReport_OutcomeEconomics(t *testing.T) {
	engine, db := newTestEngine(t)
	seedProgressChain(t, db, "sess-oe", map[string]string{
		"node-done-1": "completed",
		"node-done-2": "completed",
		"node-fail":   "failed",
	})

	// Two completed nodes ($0.40+$0.60=1.00 across two turns; $2.00 one
	// turn), one failed node ($3.00), one costed turn with NO stamp
	// ($0.50), and one stamped turn whose node id resolves to nothing.
	seedAttributedManagedTurn(t, db, day(9, 0), "sess-oe", "turn-oe1", "node-done-1", 0.40)
	seedAttributedManagedTurn(t, db, day(9, 10), "sess-oe", "turn-oe2", "node-done-1", 0.60)
	seedAttributedManagedTurn(t, db, day(9, 20), "sess-oe", "turn-oe3", "node-done-2", 2.00)
	seedAttributedManagedTurn(t, db, day(9, 30), "sess-oe", "turn-oe4", "node-fail", 3.00)
	seedManagedTurn(t, db, day(9, 40), "claude", "sess-oe", "turn-oe5", 0.50) // no stamp
	seedAttributedManagedTurn(t, db, day(9, 50), "sess-oe", "turn-oe6", "node-ghost", 0.25)

	rep, err := engine.GenerateReport(context.Background(), 0)
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}
	oe := rep.OutcomeEconomics

	if oe.AttributedTurns != 5 || oe.UnattributedTurns != 1 {
		t.Errorf("attributed/unattributed turns = %d/%d, want 5/1", oe.AttributedTurns, oe.UnattributedTurns)
	}
	if oe.AttributedCostUSD == nil || *oe.AttributedCostUSD != 6.25 {
		t.Errorf("AttributedCostUSD = %v, want 6.25", oe.AttributedCostUSD)
	}
	if oe.UnattributedCostUSD == nil || *oe.UnattributedCostUSD != 0.50 {
		t.Errorf("UnattributedCostUSD = %v, want 0.50", oe.UnattributedCostUSD)
	}

	byOutcome := map[string]OutcomeRow{}
	for _, row := range oe.Outcomes {
		byOutcome[row.Outcome] = row
	}
	if row := byOutcome["completed"]; row.Nodes != 2 || row.Turns != 3 || row.CostUSD == nil || *row.CostUSD != 3.00 {
		t.Errorf("completed row = %+v, want 2 nodes / 3 turns / $3.00", row)
	}
	if row := byOutcome["failed"]; row.Nodes != 1 || row.Turns != 1 || row.CostUSD == nil || *row.CostUSD != 3.00 {
		t.Errorf("failed row = %+v, want 1 node / 1 turn / $3.00", row)
	}
	if row := byOutcome["unknown"]; row.Nodes != 1 || row.CostUSD == nil || *row.CostUSD != 0.25 {
		t.Errorf("unknown row = %+v, want the ghost-node disclosure at $0.25", row)
	}

	// Two completed nodes < MinOutcomeNodes: quantiles honestly absent.
	if oe.CompletedNodes != 2 {
		t.Errorf("CompletedNodes = %d, want 2", oe.CompletedNodes)
	}
	if oe.CostPerCompletedNodeMedianUSD != nil || oe.CostPerCompletedNodeP90USD != nil {
		t.Errorf("cost-per-completed-node quantiles below the gate must be nil, got %v/%v",
			oe.CostPerCompletedNodeMedianUSD, oe.CostPerCompletedNodeP90USD)
	}

	// Render smoke: the section header and the disclosure line exist.
	text := RenderText(rep)
	for _, want := range []string{"Outcome economics", "unattributed spend", "not enough data (2 completed node(s)"} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered report missing %q", want)
		}
	}
}

func TestGenerateReport_OutcomeEconomics_QuantilesAtGate(t *testing.T) {
	engine, db := newTestEngine(t)
	nodes := map[string]string{}
	for i := 1; i <= 5; i++ {
		nodes[fmt.Sprintf("node-%d", i)] = "completed"
	}
	seedProgressChain(t, db, "sess-oe5", nodes)
	for i := 1; i <= 5; i++ {
		seedAttributedManagedTurn(t, db, day(8, i), "sess-oe5",
			fmt.Sprintf("turn-q%d", i), fmt.Sprintf("node-%d", i), float64(i)) // $1..$5
	}

	rep, err := engine.GenerateReport(context.Background(), 0)
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}
	oe := rep.OutcomeEconomics
	if oe.CompletedNodes != 5 {
		t.Fatalf("CompletedNodes = %d, want 5", oe.CompletedNodes)
	}
	if oe.CostPerCompletedNodeMedianUSD == nil || *oe.CostPerCompletedNodeMedianUSD != 3.0 {
		t.Errorf("median = %v, want 3.0", oe.CostPerCompletedNodeMedianUSD)
	}
	if oe.CostPerCompletedNodeP90USD == nil || *oe.CostPerCompletedNodeP90USD != 5.0 {
		t.Errorf("P90 = %v, want 5.0 (nearest-rank over 1..5)", oe.CostPerCompletedNodeP90USD)
	}
}
