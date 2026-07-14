// cancelapi_test.go: POST /v1/scheduler/jobs/{id}/cancel through the real
// httpapi handler over a real migrated store (issue #10, FR-163) — auth
// required, happy-path cancel, cancel-after-claim conflict, unknown-id 404.
// Lives in this package (not internal/httpapi) to reuse the same e2e
// DB/store helpers daemon_test.go's HTTP round-trip tests already use;
// httptest.NewServer serves on 127.0.0.1, so the guard's loopback-Host
// check passes exactly as it does against the real daemon listener.
package daemon_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/httpapi"
	"github.com/huaiche94/auspex/internal/scheduler"
)

const cancelTestToken = "cancel-api-test-token"

// newCancelAPIServer composes the real handler over a real migrated store
// with one seeded pause record, returning the server and the store so
// tests can arrange job states directly.
func newCancelAPIServer(t *testing.T) (*httptest.Server, *scheduler.Store) {
	t.Helper()
	db := openMigratedE2EDB(t)
	clk := &e2eClock{t: time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)}
	wakes := scheduler.NewStore(db.Conn(), clk, &e2eIDs{prefix: "wj"})

	// openMigratedE2EDB seeds repositories→worktrees→provider_sessions→tasks;
	// wake_jobs additionally needs a pause_records row to reference
	// (0051's FK). Insert one directly, like scheduler's own lease tests.
	if _, err := db.Conn().ExecContext(context.Background(), `
		INSERT INTO pause_records (id, task_id, session_id, turn_id, runway_forecast_id, status, requested_at, auto_resume_enabled)
		VALUES ('pauseC', 'task1', 'sess1', 'turn1', 'rf1', 'sleeping', '2026-07-14T10:00:00Z', 1)
	`); err != nil {
		t.Fatalf("seed pause record: %v", err)
	}

	// Events deliberately unwired: these tests exercise only the cancel
	// route, and the handler treats a nil broker as 503 on /v1/events/stream.
	handler := httpapi.NewHandler(httpapi.Deps{
		Version: "test-1", StartedAt: clk.Now(), Clock: clk,
		Jobs: wakes, Cancel: wakes,
	}, cancelTestToken)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, wakes
}

func scheduleCancelTestJob(t *testing.T, wakes *scheduler.Store) scheduler.Job {
	t.Helper()
	job, err := wakes.Schedule(context.Background(), scheduler.ScheduleRequest{
		PauseID: "pauseC", Kind: "pause_resume",
		RunAfter: time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC), MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	return job
}

func postCancel(t *testing.T, srv *httptest.Server, jobID, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/scheduler/jobs/"+jobID+"/cancel", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST cancel: %v", err)
	}
	return resp
}

func TestCancelAPI_RequiresAuth(t *testing.T) {
	srv, wakes := newCancelAPIServer(t)
	job := scheduleCancelTestJob(t, wakes)

	for name, wrong := range map[string]string{"no token": "", "wrong token": "deadbeef"} {
		resp := postCancel(t, srv, string(job.ID), wrong)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: cancel = %d, want 401", name, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}

	// And the unauthenticated attempts must not have cancelled anything.
	got, err := wakes.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != scheduler.StatusScheduled {
		t.Errorf("job status after unauthenticated cancels = %q, want scheduled", got.Status)
	}
}

func TestCancelAPI_CancelsScheduledJob(t *testing.T) {
	srv, wakes := newCancelAPIServer(t)
	job := scheduleCancelTestJob(t, wakes)

	resp := postCancel(t, srv, string(job.ID), cancelTestToken)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel = %d, want 200", resp.StatusCode)
	}

	var body struct {
		SchemaVersion string `json:"schema_version"`
		Job           struct {
			ID        string  `json:"id"`
			Status    string  `json:"status"`
			LastError *string `json:"last_error"`
		} `json:"job"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.SchemaVersion != "auspex.daemon.job.v1" {
		t.Errorf("schema_version = %q, want auspex.daemon.job.v1", body.SchemaVersion)
	}
	if body.Job.ID != string(job.ID) || body.Job.Status != scheduler.StatusDead {
		t.Errorf("job = %+v, want id %q status dead", body.Job, job.ID)
	}
	if body.Job.LastError == nil || *body.Job.LastError != scheduler.CancelledByOperator {
		t.Errorf("last_error = %v, want %q", body.Job.LastError, scheduler.CancelledByOperator)
	}
}

func TestCancelAPI_ConflictAfterClaim(t *testing.T) {
	srv, wakes := newCancelAPIServer(t)
	job := scheduleCancelTestJob(t, wakes)
	if _, err := wakes.Claim(context.Background(), "worker-1", scheduler.DefaultLeaseDuration); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	resp := postCancel(t, srv, string(job.ID), cancelTestToken)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("cancel after claim = %d, want 409", resp.StatusCode)
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Error.Code != "AUSPEX_CONFLICT" {
		t.Errorf("error code = %q, want AUSPEX_CONFLICT", envelope.Error.Code)
	}

	// The claimed job is untouched — still leased to its worker.
	got, err := wakes.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != scheduler.StatusLeased {
		t.Errorf("job status after rejected cancel = %q, want leased", got.Status)
	}
}

func TestCancelAPI_UnknownJobNotFound(t *testing.T) {
	srv, _ := newCancelAPIServer(t)

	resp := postCancel(t, srv, "no-such-job", cancelTestToken)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cancel unknown = %d, want 404", resp.StatusCode)
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.EqualFold(envelope.Error.Code, "AUSPEX_NOT_FOUND") {
		t.Errorf("error code = %q, want AUSPEX_NOT_FOUND", envelope.Error.Code)
	}
}
