// subagent_test.go: the #145 parent-child cost rollup — seeded parent
// edges (codex session-started payload linkage) + costed turns across a
// two-level delegation tree, plus cycle safety.
package report

import (
	"context"
	"strings"
	"testing"
)

func TestGenerateReport_SubagentAttribution(t *testing.T) {
	engine, db := newTestEngine(t)

	// Tree: root -> child-a -> grandchild; child-b of root too.
	insertEvent(t, db, "provider.session.started", day(8, 0), "codex", "sess-root", "", `{}`)
	insertEvent(t, db, "provider.session.started", day(8, 1), "codex", "sess-child-a", "", `{"parent_session_id":"sess-root"}`)
	insertEvent(t, db, "provider.session.started", day(8, 2), "codex", "sess-child-b", "", `{"parent_session_id":"sess-root"}`)
	insertEvent(t, db, "provider.session.started", day(8, 3), "codex", "sess-grand", "", `{"parent_session_id":"sess-child-a"}`)

	seedManagedTurn(t, db, day(9, 0), "codex", "sess-root", "turn-r1", 1.00)
	seedManagedTurn(t, db, day(9, 10), "codex", "sess-child-a", "turn-a1", 2.00)
	seedManagedTurn(t, db, day(9, 20), "codex", "sess-child-b", "turn-b1", 0.50)
	seedManagedTurn(t, db, day(9, 30), "codex", "sess-grand", "turn-g1", 4.00)
	// An unrelated session with no delegation: absent from the section.
	seedManagedTurn(t, db, day(9, 40), "claude", "sess-solo", "turn-s1", 9.99)

	rep, err := engine.GenerateReport(context.Background(), 0)
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}
	if len(rep.SubagentAttribution) != 1 {
		t.Fatalf("SubagentAttribution = %+v, want exactly the one delegating root", rep.SubagentAttribution)
	}
	root := rep.SubagentAttribution[0]
	if root.RootSessionID != "sess-root" || root.Subagents != 3 {
		t.Errorf("root = %+v, want sess-root with 3 subagents", root)
	}
	if root.OwnCostUSD == nil || *root.OwnCostUSD != 1.00 {
		t.Errorf("OwnCostUSD = %v, want 1.00", root.OwnCostUSD)
	}
	if root.SubagentCostUSD == nil || *root.SubagentCostUSD != 6.50 {
		t.Errorf("SubagentCostUSD = %v, want 6.50 (2.00+0.50+4.00 — grandchild rolls to the root)", root.SubagentCostUSD)
	}
	if root.TotalCostUSD == nil || *root.TotalCostUSD != 7.50 {
		t.Errorf("TotalCostUSD = %v, want 7.50", root.TotalCostUSD)
	}

	text := RenderText(rep)
	if !strings.Contains(text, "Subagent attribution") || !strings.Contains(text, "sess-root") {
		t.Errorf("rendered report missing the subagent section:\n%s", text)
	}
}

func TestBuildSubagentAttribution_CycleSafe(t *testing.T) {
	c1, c2 := 1.0, 2.0
	turns := []turnRecord{
		{sessionID: "a", costUSD: &c1},
		{sessionID: "b", costUSD: &c2},
	}
	// a -> b -> a: the walk stops on revisit instead of looping.
	edges := map[string]string{"a": "b", "b": "a"}
	roots := buildSubagentAttribution(turns, edges)
	// Both sessions resolve to SOME stable root; the invariant under a
	// cycle is termination and exactly-once cost counting.
	var total float64
	for _, r := range roots {
		if r.TotalCostUSD != nil {
			total += *r.TotalCostUSD
		}
	}
	if total > 3.0+1e-9 {
		t.Fatalf("cycle double-counted spend: total %v > 3.0", total)
	}
}
