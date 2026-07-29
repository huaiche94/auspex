// spinsignals_sql_test.go: #143's SQL signal reader against a real
// migrated DB, plus the pipeline-level proof that the signals land in
// the persisted features_json (the M13 corpus this slice exists for).
package evaluation_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/evaluation"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

func seedSpinChain(t *testing.T, db *sqlite.DB, sessionID, taskID string) {
	t.Helper()
	now := "2026-07-29T10:00:00Z"
	execSQL := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Conn().ExecContext(context.Background(), q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	execSQL(`INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
		VALUES ('repo-sp', '/tmp/sp', '/tmp/sp/.git', ?, ?)`, now, now)
	execSQL(`INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
		VALUES ('wt-sp', 'repo-sp', '/tmp/sp', '/tmp/sp/.git', ?, ?)`, now, now)
	execSQL(`INSERT INTO provider_sessions (id, worktree_id, provider, invocation_mode, started_at, metadata_json)
		VALUES (?, 'wt-sp', 'claude', 'hook', ?, '{}')`, sessionID, now)
	execSQL(`INSERT INTO tasks (id, session_id, worktree_id, objective_hash, status, created_at, updated_at)
		VALUES (?, ?, 'wt-sp', 'hash-sp', 'pending', ?, ?)`, taskID, sessionID, now, now)
}

func seedSpinTurn(t *testing.T, db *sqlite.DB, sessionID string, seq int, at time.Time, payload string) {
	t.Helper()
	if _, err := db.Conn().ExecContext(context.Background(), `INSERT INTO events
			(event_id, schema_version, event_type, occurred_at, observed_at, source, provider, session_id, turn_id, payload_json)
		VALUES (?, 'auspex.event.v1', 'provider.turn.completed', ?, ?, 'test', 'claude', ?, ?, ?)`,
		fmt.Sprintf("ev-sp-%03d", seq),
		at.UTC().Format(time.RFC3339Nano), at.UTC().Format(time.RFC3339Nano),
		sessionID, fmt.Sprintf("turn-sp-%03d", seq), payload); err != nil {
		t.Fatalf("seed turn: %v", err)
	}
}

func TestSQLDataSource_SpinSignals(t *testing.T) {
	db := openMigratedDB(t)
	seedSpinChain(t, db, "sess-sp", "task-sp")
	base := time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC)

	// Three aggregate-reporting turns (rates 0.2, 0.5, 0.8 oldest->newest),
	// one turn with no aggregates (skipped, disclosed via ReportingTurns),
	// then a node completion, then ONE more turn after it.
	seedSpinTurn(t, db, "sess-sp", 1, base, `{"total_file_ops":10,"repeated_ops":2}`)
	seedSpinTurn(t, db, "sess-sp", 2, base.Add(1*time.Minute), `{"total_file_ops":10,"repeated_ops":5}`)
	seedSpinTurn(t, db, "sess-sp", 3, base.Add(2*time.Minute), `{}`)
	advance := base.Add(3 * time.Minute)
	if _, err := db.Conn().ExecContext(context.Background(), `INSERT INTO progress_nodes
			(id, task_id, ordinal, kind, title, status, version, updated_at)
		VALUES ('node-sp', 'task-sp', 1, 'step', 'n', 'completed', 1, ?)`,
		advance.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	seedSpinTurn(t, db, "sess-sp", 4, base.Add(4*time.Minute), `{"total_file_ops":10,"repeated_ops":8}`)
	for i := 0; i < 3; i++ {
		if _, err := db.Conn().ExecContext(context.Background(), `INSERT INTO artifacts
				(id, task_id, progress_node_id, kind, uri, bytes, sha256, validation_status, created_at)
			VALUES (?, 'task-sp', 'node-sp', 'test_log', ?, 1, 'x', 'valid', ?)`,
			fmt.Sprintf("art-%d", i), fmt.Sprintf("file:/tmp/a-%d", i), advance.Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("seed artifact: %v", err)
		}
	}

	src := evaluation.NewSQLDataSource(db)
	taskID := domain.TaskID("task-sp")
	signals, ok, err := src.SpinSignals(context.Background(), "sess-sp", &taskID)
	if err != nil {
		t.Fatalf("SpinSignals: %v", err)
	}
	if !ok {
		t.Fatal("SpinSignals ok = false, want measurable")
	}
	if signals.ReportingTurns != 3 {
		t.Errorf("ReportingTurns = %d, want 3 (the aggregate-less turn is excluded)", signals.ReportingTurns)
	}
	if signals.LastTurnRepeatRate == nil || *signals.LastTurnRepeatRate != 0.8 {
		t.Errorf("LastTurnRepeatRate = %v, want 0.8 (most recent reporting turn)", signals.LastTurnRepeatRate)
	}
	if signals.MeanRepeatRate == nil || *signals.MeanRepeatRate != 0.5 {
		t.Errorf("MeanRepeatRate = %v, want 0.5 (mean of 0.2/0.5/0.8)", signals.MeanRepeatRate)
	}
	if signals.TurnsSinceNodeAdvance == nil || *signals.TurnsSinceNodeAdvance != 1 {
		t.Errorf("TurnsSinceNodeAdvance = %v, want 1 (one completed turn after the node advance)", signals.TurnsSinceNodeAdvance)
	}
	if signals.EvidenceCount == nil || *signals.EvidenceCount != 3 {
		t.Errorf("EvidenceCount = %v, want 3", signals.EvidenceCount)
	}
}

func TestSQLDataSource_SpinSignals_ColdStartIsHonest(t *testing.T) {
	db := openMigratedDB(t)
	src := evaluation.NewSQLDataSource(db)
	signals, ok, err := src.SpinSignals(context.Background(), "sess-none", nil)
	if err != nil {
		t.Fatalf("SpinSignals: %v", err)
	}
	if ok {
		t.Fatalf("SpinSignals ok = true on a cold session with no task, want false (got %+v)", signals)
	}
}

// spinFakeSource wraps the standard pipeline fake with the optional
// spinSignalSource seam, so the pipeline-level test can prove the
// signals land in the persisted features_json.
type spinFakeSource struct {
	*fakeDataSource
	signals evaluation.SpinSignals
}

func (s *spinFakeSource) SpinSignals(_ context.Context, _ domain.SessionID, _ *domain.TaskID) (evaluation.SpinSignals, bool, error) {
	return s.signals, true, nil
}

func TestEvaluateTurn_PersistsSpinSignalsInFeaturesJSON(t *testing.T) {
	rate := 0.75
	since := int64(4)
	base := newFakeDataSource()
	clk := newFakeClock(time.Now())
	ids := &sequentialIDs{prefix: "spin"}
	svc, db := newTestService(t, clk, ids, base)
	// Swap the source for the seam-implementing wrapper (same fake under
	// it, so every other pipeline stage behaves identically).
	svc.Source = &spinFakeSource{fakeDataSource: base, signals: evaluation.SpinSignals{
		LastTurnRepeatRate:    &rate,
		MeanRepeatRate:        &rate,
		ReportingTurns:        3,
		TurnsSinceNodeAdvance: &since,
	}}

	eval, err := svc.EvaluateTurn(context.Background(), app.EvaluateTurnRequest{
		SessionID: "sess-spinp", TurnID: "turn-spinp", Provider: "claude-code", PromptHash: "sha256:sp",
	})
	if err != nil {
		t.Fatalf("EvaluateTurn: %v", err)
	}
	_ = eval

	var featuresJSON string
	if err := db.Conn().QueryRowContext(context.Background(),
		`SELECT features_json FROM feature_vectors WHERE turn_id = 'turn-spinp'`).Scan(&featuresJSON); err != nil {
		t.Fatalf("read features_json: %v", err)
	}
	var doc struct {
		Spin *struct {
			LastTurnRepeatRate    *float64 `json:"last_turn_repeat_rate"`
			MeanRepeatRate        *float64 `json:"mean_repeat_rate"`
			ReportingTurns        int      `json:"reporting_turns"`
			TurnsSinceNodeAdvance *int64   `json:"turns_since_node_advance"`
		} `json:"spin"`
	}
	if err := json.Unmarshal([]byte(featuresJSON), &doc); err != nil {
		t.Fatalf("features_json is not JSON: %v\n%s", err, featuresJSON)
	}
	if doc.Spin == nil {
		t.Fatalf("features_json has no spin block: %s", featuresJSON)
	}
	if doc.Spin.LastTurnRepeatRate == nil || *doc.Spin.LastTurnRepeatRate != 0.75 ||
		doc.Spin.ReportingTurns != 3 ||
		doc.Spin.TurnsSinceNodeAdvance == nil || *doc.Spin.TurnsSinceNodeAdvance != 4 {
		t.Errorf("persisted spin block = %+v", doc.Spin)
	}
}
