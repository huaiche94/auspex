// sessionstatusapi_test.go: GET /v1/session/status and
// GET /v1/session/{id}/status through the real httpapi handler over a real
// migrated DB with a real sessionstatus.Reader (issue #10, FR-162). Covers a
// fully populated session (all six sections carry real data), an empty
// session (honest nulls/empties, never substituted zeros), an unknown
// session (404), most-recent-session resolution (no id), and auth. Lives in
// this package to reuse e2e_test.go's openMigratedE2EDB / e2eClock / e2eIDs
// helpers, exactly as cancelapi_test.go does.
package daemon_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/evaluation"
	"github.com/huaiche94/auspex/internal/httpapi"
	"github.com/huaiche94/auspex/internal/progress"
	"github.com/huaiche94/auspex/internal/repocheckpoint"
	"github.com/huaiche94/auspex/internal/scheduler"
	"github.com/huaiche94/auspex/internal/sessionstatus"
	"github.com/huaiche94/auspex/internal/statecheckpoint"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

const sessionStatusToken = "session-status-test-token"

// sessionStatusBody mirrors the wire shape closely enough to assert on every
// section's populated / null / empty state.
type sessionStatusBody struct {
	SchemaVersion string `json:"schema_version"`
	SessionID     string `json:"session_id"`
	Risk          *struct {
		OverallRiskScore     float64  `json:"overall_risk_score"`
		QuotaRiskScore       float64  `json:"quota_risk_score"`
		ContextRiskScore     float64  `json:"context_risk_score"`
		CompletionRiskScore  float64  `json:"completion_risk_score"`
		BlastRadiusRiskScore float64  `json:"blast_radius_risk_score"`
		Calibrated           bool     `json:"calibrated"`
		Confidence           string   `json:"confidence"`
		ReasonCodes          []string `json:"reason_codes"`
		TurnID               string   `json:"turn_id"`
	} `json:"risk"`
	// RawMessage so we can assert the literal JSON null for an absent runway.
	Runway json.RawMessage `json:"runway"`
	Quota  struct {
		AsOf    time.Time `json:"as_of"`
		Windows []struct {
			LimitID     string   `json:"limit_id"`
			UsedPercent *float64 `json:"used_percent"`
			AgeSeconds  int64    `json:"age_seconds"`
		} `json:"windows"`
	} `json:"quota"`
	Progress struct {
		TaskID *string `json:"task_id"`
		Nodes  []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Kind   string `json:"kind"`
		} `json:"nodes"`
		Edges []struct {
			FromNodeID string `json:"from_node_id"`
			ToNodeID   string `json:"to_node_id"`
		} `json:"edges"`
	} `json:"progress"`
	Checkpoint *struct {
		State *struct {
			ID                     string  `json:"id"`
			RepositoryCheckpointID *string `json:"repository_checkpoint_id"`
		} `json:"state"`
		Repository *struct {
			ID      string `json:"id"`
			GitHead string `json:"git_head"`
		} `json:"repository"`
	} `json:"checkpoint"`
	Pause *struct {
		ID                string `json:"id"`
		Status            string `json:"status"`
		AutoResumeEnabled bool   `json:"auto_resume_enabled"`
		WakeJobs          []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"wake_jobs"`
	} `json:"pause"`
}

func newSessionStatusServer(t *testing.T) (*httptest.Server, *sqlite.DB) {
	t.Helper()
	db := openMigratedE2EDB(t)
	clk := &e2eClock{t: time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)}
	reader := sessionstatus.NewReader(sessionstatus.Deps{
		DB:               db,
		Evaluation:       evaluation.NewSQLDataSource(db),
		Nodes:            progress.NewNodeStore(db, clk),
		Edges:            progress.NewEdgeStore(db),
		StateCheckpoints: statecheckpoint.NewStore(db),
		RepoCheckpoints:  repocheckpoint.NewStore(db),
		Jobs:             scheduler.NewStore(db.Conn(), clk, &e2eIDs{prefix: "wj"}),
	})
	handler := httpapi.NewHandler(httpapi.Deps{
		Version: "test-1", StartedAt: clk.Now(), Clock: clk,
		SessionStatus: reader,
	}, sessionStatusToken)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, db
}

func mustExec(t *testing.T, db *sqlite.DB, stmt string) {
	t.Helper()
	if _, err := db.Conn().ExecContext(context.Background(), stmt); err != nil {
		t.Fatalf("seed %q: %v", stmt, err)
	}
}

// seedPopulatedSession fills every FR-162 section for the harness-seeded
// sess1 / task1 / wt1: a turn-started event (session→prediction join), a
// prediction (risk), a quota observation 5 minutes stale, a progress node, a
// repository + state checkpoint (linked), a pause record, and a scheduled
// wake job. Deliberately NO runway_forecasts row — that table has no producer
// yet, and the endpoint must serve runway as honest null.
func seedPopulatedSession(t *testing.T, db *sqlite.DB) {
	t.Helper()
	for _, stmt := range []string{
		`INSERT INTO events (event_id, schema_version, event_type, occurred_at, observed_at, source, session_id, turn_id, payload_json)
		 VALUES ('ev-turn1', 'v1', 'provider.turn.started', '2026-07-14T09:50:00Z', '2026-07-14T09:50:00Z', 'test', 'sess1', 'turn1', '{}')`,
		`INSERT INTO events (event_id, schema_version, event_type, occurred_at, observed_at, source, session_id, payload_json)
		 VALUES ('ev-quota1', 'v1', 'provider.quota.observed', '2026-07-14T09:55:00Z', '2026-07-14T09:55:00Z', 'test', 'sess1',
		         '{"limit_id":"seven_day","used_percent":90.0,"resets_at":"2026-07-20T00:00:00Z"}')`,
		`INSERT INTO predictions (id, turn_id, predictor_id, predictor_version, feature_set_version,
		         quota_risk_score, context_risk_score, completion_risk_score, blast_radius_risk_score, overall_risk_score,
		         confidence, calibrated, reason_codes_json, created_at)
		 VALUES ('pred1', 'turn1', 'rule', 'v1', 'fs1', 0.5, 0.3, 0.2, 0.1, 0.42, 'medium', 0, '["quota_pressure"]', '2026-07-14T09:50:00Z')`,
		`INSERT INTO progress_nodes (id, task_id, ordinal, kind, title, status, acceptance_json, version, updated_at)
		 VALUES ('node1', 'task1', 0, 'document_section', 'irrelevant title', 'in_progress', '[]', 1, '2026-07-14T09:59:00Z')`,
		`INSERT INTO repository_checkpoints (id, worktree_id, task_id, status, artifact_root, manifest_path, git_head,
		         index_diff_hash, worktree_diff_hash, recoverability, total_bytes, created_at, metadata_json)
		 VALUES ('rck1', 'wt1', 'task1', 'created', '/tmp/x', '/tmp/x/m.json', 'headabc', 'idx1', 'wt1hash', 'full', 2048, '2026-07-14T09:59:20Z', '{}')`,
		`INSERT INTO state_checkpoints (id, task_id, progress_tree_version, repository_checkpoint_id, manifest_json, integrity_sha256, created_at)
		 VALUES ('sck1', 'task1', 1, 'rck1', '{}', 'deadbeef', '2026-07-14T09:59:30Z')`,
		`INSERT INTO pause_records (id, task_id, session_id, turn_id, runway_forecast_id, status, requested_at, auto_resume_enabled)
		 VALUES ('pause1', 'task1', 'sess1', 'turn1', 'rf1', 'sleeping', '2026-07-14T09:58:00Z', 1)`,
		`INSERT INTO wake_jobs (id, pause_id, job_kind, status, run_after, attempts, max_attempts, created_at, updated_at)
		 VALUES ('wj1', 'pause1', 'pause_resume', 'scheduled', '2026-07-14T12:00:00Z', 0, 3, '2026-07-14T09:58:00Z', '2026-07-14T09:58:00Z')`,
	} {
		mustExec(t, db, stmt)
	}
}

// seedEmptySession adds a session (sess2) in its OWN task-less worktree so
// session→task resolution honestly yields no task — otherwise the
// worktree-wide task fallback would attach sess1's task. started_at is BEFORE
// sess1's so sess1 stays "most recent" for the no-id route.
func seedEmptySession(t *testing.T, db *sqlite.DB) {
	t.Helper()
	for _, stmt := range []string{
		`INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
		 VALUES ('repo2', '/tmp/repo2', '/tmp/repo2/.git', '2026-07-14T09:00:00Z', '2026-07-14T09:00:00Z')`,
		`INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
		 VALUES ('wt2', 'repo2', '/tmp/repo2', '/tmp/repo2/.git', '2026-07-14T09:00:00Z', '2026-07-14T09:00:00Z')`,
		`INSERT INTO provider_sessions (id, worktree_id, provider, invocation_mode, started_at, metadata_json)
		 VALUES ('sess2', 'wt2', 'claude-code', 'interactive', '2026-07-14T09:00:00Z', '{}')`,
	} {
		mustExec(t, db, stmt)
	}
}

func getSessionStatus(t *testing.T, srv *httptest.Server, path, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func decodeSessionStatus(t *testing.T, resp *http.Response) sessionStatusBody {
	t.Helper()
	var body sessionStatusBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

func TestSessionStatus_PopulatedSession(t *testing.T) {
	srv, db := newSessionStatusServer(t)
	seedPopulatedSession(t, db)

	resp := getSessionStatus(t, srv, "/v1/session/sess1/status", sessionStatusToken)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decodeSessionStatus(t, resp)

	if body.SchemaVersion != "auspex.daemon.session_status.v1" {
		t.Errorf("schema_version = %q", body.SchemaVersion)
	}
	if body.SessionID != "sess1" {
		t.Errorf("session_id = %q, want sess1", body.SessionID)
	}

	// risk: all sub-scores present, uncalibrated, reason code carried.
	if body.Risk == nil {
		t.Fatal("risk = null, want populated")
	}
	if body.Risk.OverallRiskScore != 0.42 || body.Risk.QuotaRiskScore != 0.5 ||
		body.Risk.ContextRiskScore != 0.3 || body.Risk.CompletionRiskScore != 0.2 ||
		body.Risk.BlastRadiusRiskScore != 0.1 {
		t.Errorf("risk scores = %+v", *body.Risk)
	}
	if body.Risk.Calibrated {
		t.Error("risk.calibrated = true, want false")
	}
	if body.Risk.Confidence != "medium" || body.Risk.TurnID != "turn1" ||
		len(body.Risk.ReasonCodes) != 1 || body.Risk.ReasonCodes[0] != "quota_pressure" {
		t.Errorf("risk meta = %+v", *body.Risk)
	}

	// runway: honestly null (no runway_forecasts row).
	if string(body.Runway) != "null" {
		t.Errorf("runway = %s, want null", body.Runway)
	}

	// quota freshness: one window, 300s stale (10:00:00 − 09:55:00).
	if len(body.Quota.Windows) != 1 {
		t.Fatalf("quota windows = %d, want 1", len(body.Quota.Windows))
	}
	w := body.Quota.Windows[0]
	if w.LimitID != "seven_day" || w.UsedPercent == nil || *w.UsedPercent != 90 || w.AgeSeconds != 300 {
		t.Errorf("quota window = %+v (used=%v)", w, w.UsedPercent)
	}

	// progress: task resolved, one node, content-free projection.
	if body.Progress.TaskID == nil || *body.Progress.TaskID != "task1" {
		t.Errorf("progress.task_id = %v, want task1", body.Progress.TaskID)
	}
	if len(body.Progress.Nodes) != 1 || body.Progress.Nodes[0].ID != "node1" ||
		body.Progress.Nodes[0].Status != "in_progress" {
		t.Errorf("progress.nodes = %+v", body.Progress.Nodes)
	}

	// checkpoint: latest state + its linked repository checkpoint.
	if body.Checkpoint == nil || body.Checkpoint.State == nil {
		t.Fatal("checkpoint/state = null, want populated")
	}
	if body.Checkpoint.State.ID != "sck1" ||
		body.Checkpoint.State.RepositoryCheckpointID == nil ||
		*body.Checkpoint.State.RepositoryCheckpointID != "rck1" {
		t.Errorf("state checkpoint = %+v", *body.Checkpoint.State)
	}
	if body.Checkpoint.Repository == nil || body.Checkpoint.Repository.ID != "rck1" ||
		body.Checkpoint.Repository.GitHead != "headabc" {
		t.Errorf("repo checkpoint = %+v", body.Checkpoint.Repository)
	}

	// pause: latest record + its scheduled wake job.
	if body.Pause == nil {
		t.Fatal("pause = null, want populated")
	}
	if body.Pause.ID != "pause1" || body.Pause.Status != "sleeping" || !body.Pause.AutoResumeEnabled {
		t.Errorf("pause = %+v", *body.Pause)
	}
	if len(body.Pause.WakeJobs) != 1 || body.Pause.WakeJobs[0].ID != "wj1" ||
		body.Pause.WakeJobs[0].Status != "scheduled" {
		t.Errorf("pause.wake_jobs = %+v", body.Pause.WakeJobs)
	}
}

func TestSessionStatus_EmptySessionHonestNulls(t *testing.T) {
	srv, db := newSessionStatusServer(t)
	seedEmptySession(t, db)

	resp := getSessionStatus(t, srv, "/v1/session/sess2/status", sessionStatusToken)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decodeSessionStatus(t, resp)

	if body.SessionID != "sess2" {
		t.Errorf("session_id = %q, want sess2", body.SessionID)
	}
	// Every optional section: honest null / empty, never a substituted zero.
	if body.Risk != nil {
		t.Errorf("risk = %+v, want null", *body.Risk)
	}
	if string(body.Runway) != "null" {
		t.Errorf("runway = %s, want null", body.Runway)
	}
	if body.Quota.Windows == nil || len(body.Quota.Windows) != 0 {
		t.Errorf("quota.windows = %+v, want empty array", body.Quota.Windows)
	}
	if body.Progress.TaskID != nil {
		t.Errorf("progress.task_id = %v, want null (task-less worktree)", *body.Progress.TaskID)
	}
	if len(body.Progress.Nodes) != 0 || len(body.Progress.Edges) != 0 {
		t.Errorf("progress not empty: nodes=%+v edges=%+v", body.Progress.Nodes, body.Progress.Edges)
	}
	if body.Checkpoint != nil {
		t.Errorf("checkpoint = %+v, want null", *body.Checkpoint)
	}
	if body.Pause != nil {
		t.Errorf("pause = %+v, want null", *body.Pause)
	}
}

func TestSessionStatus_UnknownSession(t *testing.T) {
	srv, _ := newSessionStatusServer(t)

	resp := getSessionStatus(t, srv, "/v1/session/no-such-session/status", sessionStatusToken)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Error.Code != "AUSPEX_NOT_FOUND" {
		t.Errorf("error code = %q, want AUSPEX_NOT_FOUND", envelope.Error.Code)
	}
}

func TestSessionStatus_LatestResolvesMostRecentSession(t *testing.T) {
	srv, db := newSessionStatusServer(t)
	// sess1 (started 10:00, harness) is more recent than sess2 (started 09:00).
	seedPopulatedSession(t, db)
	seedEmptySession(t, db)

	resp := getSessionStatus(t, srv, "/v1/session/status", sessionStatusToken)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decodeSessionStatus(t, resp)
	if body.SessionID != "sess1" {
		t.Errorf("no-id latest resolved session_id = %q, want sess1", body.SessionID)
	}
	if body.Risk == nil || body.Risk.OverallRiskScore != 0.42 {
		t.Errorf("latest did not carry sess1's populated risk: %+v", body.Risk)
	}
}

func TestSessionStatus_RequiresAuth(t *testing.T) {
	srv, db := newSessionStatusServer(t)
	seedPopulatedSession(t, db)

	for name, tok := range map[string]string{"no token": "", "wrong token": "deadbeef"} {
		resp := getSessionStatus(t, srv, "/v1/session/sess1/status", tok)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", name, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
}

func TestSessionStatus_ReaderNotWired(t *testing.T) {
	// A handler composed without a SessionStatus reader answers 503, mirroring
	// the scheduler-store-not-wired guards on the other read routes.
	handler := httpapi.NewHandler(httpapi.Deps{Version: "test-1"}, sessionStatusToken)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp := getSessionStatus(t, srv, "/v1/session/status", sessionStatusToken)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}
