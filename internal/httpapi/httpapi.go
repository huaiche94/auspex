// Package httpapi is the M6 daemon's authenticated loopback HTTP/JSON + SSE
// surface (issue #7; ADD §23.2-23.5, NFR-022) — the path
// internal/cli/errorcontract_test.go long documented as "does not exist in
// this repository": it exists now because the daemon does.
//
// Scope: the READ surface plus the live event stream (health / version /
// capabilities / status / scheduler jobs / events/stream, the #7 phase) and
// the ONE mutation the VS Code companion phase (#10, FR-163) needs: POST
// /v1/scheduler/jobs/{id}/cancel. The remaining §23.4 pause-mutation
// endpoints (POST /v1/pauses, :cancel, :resume) stay deferred per
// Constitution §7 rule 10 (one milestone at a time); `auspex pause|resume`
// already covers manual mutation on this machine.
//
// Security posture (§23.2, §27.5): every endpoint requires
// `Authorization: Bearer <token>` (constant-time compare), the Host header
// must be loopback (DNS-rebinding defense), CORS is disabled by omission
// (no Access-Control-* headers are ever written), request bodies are
// capped, and errors render as the §23.5 envelope.
package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/scheduler"
	"github.com/huaiche94/auspex/internal/sessionstatus"
	protocol "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// maxBodyBytes caps request bodies (§23.2 "body limits"). The read surface
// takes no bodies at all, so anything near this is already abuse.
const maxBodyBytes = 1 << 20

// sseHeartbeatInterval paces `: ping` comment lines on the event stream so
// intermediaries/clients can distinguish "quiet" from "dead".
const sseHeartbeatInterval = 15 * time.Second

// JobLister is the narrow slice of scheduler.Store the API reads.
type JobLister interface {
	List(ctx context.Context) ([]scheduler.Job, error)
}

// JobCanceller is the narrow slice of scheduler.Store the API mutates —
// the single FR-163 write (issue #10: "cancel scheduled resume") added to
// an otherwise read-only surface. Kept as its own interface rather than
// widening JobLister so read-only compositions (and tests) can keep wiring
// a lister without granting mutation.
type JobCanceller interface {
	Cancel(ctx context.Context, id domain.WakeJobID) (scheduler.Job, error)
}

// EventSource is the narrow slice of daemon.Broker the API reads.
type EventSource interface {
	Subscribe() (<-chan protocol.Event, func())
}

// SessionStatusReader is the narrow slice the FR-162 session-status endpoints
// read (issue #10): the assembled per-session risk/runway/quota/progress/
// checkpoint/pause view. An empty sessionID means "most recent session"; the
// implementation returns a domain not-found error when the (resolved)
// session does not exist. Consumer-defined here — like JobLister/EventSource
// — so a read-only composition can wire it without any wider contract.
type SessionStatusReader interface {
	Snapshot(ctx context.Context, sessionID domain.SessionID, now time.Time) (*sessionstatus.Snapshot, error)
}

// Deps bundles the sources the handlers serve from. All fields except
// Cancel are read-only; Cancel is the FR-163 mutation (see JobCanceller).
type Deps struct {
	Version       string
	StartedAt     time.Time
	Clock         domain.Clock
	Jobs          JobLister
	Cancel        JobCanceller
	Events        EventSource
	SessionStatus SessionStatusReader
}

// NewHandler builds the authenticated API around bearerToken (minted per
// restart by the daemon — this package never reads the token file).
func NewHandler(deps Deps, bearerToken string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", deps.handleHealth)
	mux.HandleFunc("GET /v1/version", deps.handleVersion)
	mux.HandleFunc("GET /v1/capabilities", deps.handleCapabilities)
	mux.HandleFunc("GET /v1/status", deps.handleStatus)
	// FR-162 per-session read-model (issue #10). Two routes onto one
	// handler: the no-id form serves the most recent session (the extension's
	// default view, which need not know a session id), the {id} form a
	// specific one. Distinct segment counts, so the ServeMux never confuses
	// "status" for an {id}.
	mux.HandleFunc("GET /v1/session/status", deps.handleSessionStatus)
	mux.HandleFunc("GET /v1/session/{id}/status", deps.handleSessionStatus)
	mux.HandleFunc("GET /v1/scheduler/jobs", deps.handleJobs)
	mux.HandleFunc("POST /v1/scheduler/jobs/{id}/cancel", deps.handleJobCancel)
	mux.HandleFunc("GET /v1/events/stream", deps.handleEvents)
	return guard(mux, bearerToken)
}

// guard is the §23.2 middleware stack: loopback-Host check, bearer auth,
// body cap. Auth failures return the §23.5 envelope with
// AUSPEX_UNAUTHORIZED and never reveal whether the token or the host was
// the problem beyond the code itself.
func guard(next http.Handler, bearerToken string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hostIsLoopback(r.Host) {
			writeError(w, http.StatusForbidden, &domain.Error{
				Code: domain.ErrCodeUnauthorized, Message: "non-loopback Host rejected", Retryable: false,
			})
			return
		}
		token, ok := bearerFromHeader(r.Header.Get("Authorization"))
		if !ok || !verifyToken(bearerToken, token) {
			writeError(w, http.StatusUnauthorized, &domain.Error{
				Code: domain.ErrCodeUnauthorized, Message: "missing or invalid bearer token", Retryable: false,
			})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		next.ServeHTTP(w, r)
	})
}

// hostIsLoopback accepts 127.0.0.1, ::1, and localhost (with or without a
// port) — everything else is a DNS-rebinding or proxy-forwarded request a
// loopback-only API must refuse regardless of token validity.
func hostIsLoopback(host string) bool {
	h := host
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		h = splitHost
	}
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(h, "[]"))
	return ip != nil && ip.IsLoopback()
}

func bearerFromHeader(header string) (string, bool) {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	return header[len(prefix):], true
}

// verifyToken mirrors daemon.VerifyToken (constant-time, empty-expected
// never matches) — duplicated two lines rather than importing the daemon
// package into its own API surface.
func verifyToken(expected, presented string) bool {
	return expected != "" && subtle.ConstantTimeCompare([]byte(expected), []byte(presented)) == 1
}

// --- §23.5 error envelope --------------------------------------------------

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Retryable bool              `json:"retryable"`
	Details   map[string]string `json:"details,omitempty"`
}

// writeError renders err as the §23.5 envelope. domain error codes map to
// the wire's AUSPEX_-prefixed form; a non-domain error renders as
// AUSPEX_INTERNAL with its message (loopback-local operator tooling —
// hiding the message would only slow the one authorized user down).
func writeError(w http.ResponseWriter, status int, err error) {
	body := errorBody{Code: "AUSPEX_INTERNAL", Message: err.Error()}
	var derr *domain.Error
	if errors.As(err, &derr) {
		body.Code = "AUSPEX_" + strings.ToUpper(string(derr.Code))
		body.Message = derr.Message
		body.Retryable = derr.Retryable
		body.Details = derr.Details
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: body})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Headers are gone; nothing correct left to send.
		return
	}
}

// --- handlers ----------------------------------------------------------------

type healthResponse struct {
	SchemaVersion string `json:"schema_version"`
	Status        string `json:"status"`
	Version       string `json:"version"`
}

func (d Deps) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, healthResponse{SchemaVersion: "auspex.daemon.health.v1", Status: "ok", Version: d.Version})
}

type versionResponse struct {
	SchemaVersion   string `json:"schema_version"`
	Version         string `json:"version"`
	ProtocolVersion string `json:"protocol_version"`
}

func (d Deps) handleVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, versionResponse{SchemaVersion: "auspex.daemon.version.v1", Version: d.Version, ProtocolVersion: "v1"})
}

type capabilitiesResponse struct {
	SchemaVersion string   `json:"schema_version"`
	Endpoints     []string `json:"endpoints"`
	SSE           bool     `json:"sse"`
}

func (d Deps) handleCapabilities(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, capabilitiesResponse{
		SchemaVersion: "auspex.daemon.capabilities.v1",
		Endpoints: []string{
			"/v1/health", "/v1/version", "/v1/capabilities",
			"/v1/status", "/v1/session/status", "/v1/session/{id}/status",
			"/v1/scheduler/jobs", "/v1/scheduler/jobs/{id}/cancel",
			"/v1/events/stream",
		},
		SSE: true,
	})
}

type statusResponse struct {
	SchemaVersion string         `json:"schema_version"`
	Version       string         `json:"version"`
	StartedAt     time.Time      `json:"started_at"`
	UptimeSeconds int64          `json:"uptime_seconds"`
	Jobs          map[string]int `json:"jobs"`
}

func (d Deps) handleStatus(w http.ResponseWriter, r *http.Request) {
	counts := map[string]int{}
	if d.Jobs != nil {
		jobs, err := d.Jobs.List(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		for _, j := range jobs {
			counts[j.Status]++
		}
	}
	uptime := int64(0)
	if d.Clock != nil {
		uptime = int64(d.Clock.Now().Sub(d.StartedAt).Seconds())
	}
	writeJSON(w, statusResponse{
		SchemaVersion: "auspex.daemon.status.v1",
		Version:       d.Version,
		StartedAt:     d.StartedAt,
		UptimeSeconds: uptime,
		Jobs:          counts,
	})
}

// sessionStatusResponse is the FR-162 per-session envelope (issue #10): the
// schema-versioned wrapper around the assembled read-model. The Snapshot is
// embedded so its fields (session_id, risk, runway, quota, progress,
// checkpoint, pause) promote inline alongside schema_version.
type sessionStatusResponse struct {
	SchemaVersion string `json:"schema_version"`
	*sessionstatus.Snapshot
}

// handleSessionStatus serves the FR-162 read-model for one session (issue
// #10). The {id} path value is empty for the /v1/session/status route, which
// the reader treats as "most recent session." An unknown (or absent-latest)
// session surfaces the reader's domain not-found error → 404, the same
// mapping the cancel mutation uses. This is a purely read-only endpoint;
// unlike /v1/status (daemon-global: version, uptime, wake-job counts) this is
// session-scoped, so it carries its own schema identifier and lives under its
// own resource rather than widening the frozen status payload.
func (d Deps) handleSessionStatus(w http.ResponseWriter, r *http.Request) {
	if d.SessionStatus == nil {
		writeError(w, http.StatusServiceUnavailable, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "session status reader not wired", Retryable: true,
		})
		return
	}
	now := time.Now()
	if d.Clock != nil {
		now = d.Clock.Now()
	}
	snap, err := d.SessionStatus.Snapshot(r.Context(), domain.SessionID(r.PathValue("id")), now)
	if err != nil {
		writeError(w, statusForError(err), err)
		return
	}
	writeJSON(w, sessionStatusResponse{
		SchemaVersion: "auspex.daemon.session_status.v1",
		Snapshot:      snap,
	})
}

type jobsResponse struct {
	SchemaVersion string    `json:"schema_version"`
	Jobs          []jobView `json:"jobs"`
}

type jobView struct {
	ID          string     `json:"id"`
	PauseID     string     `json:"pause_id"`
	Kind        string     `json:"kind"`
	Status      string     `json:"status"`
	RunAfter    time.Time  `json:"run_after"`
	LeaseOwner  *string    `json:"lease_owner,omitempty"`
	LeaseExpiry *time.Time `json:"lease_expires_at,omitempty"`
	Attempts    int        `json:"attempts"`
	MaxAttempts int        `json:"max_attempts"`
	LastError   *string    `json:"last_error,omitempty"`
}

func (d Deps) handleJobs(w http.ResponseWriter, r *http.Request) {
	if d.Jobs == nil {
		writeError(w, http.StatusServiceUnavailable, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "scheduler store not wired", Retryable: true,
		})
		return
	}
	jobs, err := d.Jobs.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	views := make([]jobView, 0, len(jobs))
	for _, j := range jobs {
		views = append(views, toJobView(j))
	}
	writeJSON(w, jobsResponse{SchemaVersion: "auspex.daemon.jobs.v1", Jobs: views})
}

// toJobView projects a scheduler.Job onto the wire shape — one projection
// shared by the list and cancel responses so the two can never drift.
func toJobView(j scheduler.Job) jobView {
	return jobView{
		ID: string(j.ID), PauseID: string(j.PauseID), Kind: j.Kind, Status: j.Status,
		RunAfter: j.RunAfter, LeaseOwner: j.LeaseOwner, LeaseExpiry: j.LeaseExpires,
		Attempts: j.Attempts, MaxAttempts: j.MaxAttempts, LastError: j.LastError,
	}
}

// jobResponse is the single-job envelope POST …/cancel returns: the
// updated job, in the SAME jobView shape the list endpoint serves, under
// its own schema identifier.
type jobResponse struct {
	SchemaVersion string  `json:"schema_version"`
	Job           jobView `json:"job"`
}

// handleJobCancel is FR-163's API half (issue #10): POST
// /v1/scheduler/jobs/{id}/cancel → scheduler.Store.Cancel, which is legal
// only from `scheduled` (cancel.go documents the state rules and the
// claim-vs-cancel race resolution). Domain error codes map onto HTTP the
// same way the read handlers' §23.5 envelope does: not_found → 404,
// conflict → 409 (job already leased/done/dead), validation → 400.
func (d Deps) handleJobCancel(w http.ResponseWriter, r *http.Request) {
	if d.Cancel == nil {
		writeError(w, http.StatusServiceUnavailable, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "scheduler store not wired", Retryable: true,
		})
		return
	}
	job, err := d.Cancel.Cancel(r.Context(), domain.WakeJobID(r.PathValue("id")))
	if err != nil {
		writeError(w, statusForError(err), err)
		return
	}
	writeJSON(w, jobResponse{SchemaVersion: "auspex.daemon.job.v1", Job: toJobView(job)})
}

// statusForError maps *domain.Error codes onto HTTP statuses for the
// mutation path (the read handlers only ever surface 500/503, so this
// mapping lives with the first handler that needs the fuller table).
func statusForError(err error) int {
	var derr *domain.Error
	if !errors.As(err, &derr) {
		return http.StatusInternalServerError
	}
	switch derr.Code {
	case domain.ErrCodeNotFound:
		return http.StatusNotFound
	case domain.ErrCodeConflict:
		return http.StatusConflict
	case domain.ErrCodeValidation:
		return http.StatusBadRequest
	case domain.ErrCodeUnauthorized:
		return http.StatusUnauthorized
	case domain.ErrCodeUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// handleEvents is the §23.4 SSE stream: one `event:`/`data:` block per
// broker event, JSON payload, heartbeat comments while quiet. Live view
// only — no replay (broker.go documents why).
func (d Deps) handleEvents(w http.ResponseWriter, r *http.Request) {
	if d.Events == nil {
		writeError(w, http.StatusServiceUnavailable, &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "event broker not wired", Retryable: true,
		})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, &domain.Error{
			Code: domain.ErrCodeInternal, Message: "response writer does not support streaming", Retryable: false,
		})
		return
	}

	events, cancel := d.Events.Subscribe()
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case ev, open := <-events:
			if !open {
				return
			}
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.EventType, data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
