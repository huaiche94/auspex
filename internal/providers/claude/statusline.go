// Package claude implements fixture-backed parsing of Claude Code's
// provider-native payloads (status-line snapshots, hook payloads) into
// intermediate Go structs. This package does not normalize into the frozen
// pkg/protocol/v1.Event envelope — that is claude-provider-04's job. This
// wave (claude-provider-01/02/03) only covers the parsing step (Constitution
// §7 rule 10: implement one wave at a time).
package claude

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
)

// StatusLineSnapshot is the parsed, unknown-field-tolerant representation of
// a single Claude Code status-line JSON payload (ADD §22.5). All
// optional/measured fields use Go pointer types: nil means "unknown", never
// a substituted zero (Constitution §7 / ADD principle 1, CONTRACT_FREEZE.md
// "Unknown/null semantics").
type StatusLineSnapshot struct {
	SessionID domain.SessionID

	ModelID          *string
	ModelDisplayName *string

	// EffortLevel is the reasoning-effort level the session is currently
	// running at (statusline payload "effort.level" — the only surface
	// that carries effort continuously; #20 Phase 0). nil means the
	// payload carried none (model without effort support, or an older
	// Claude Code).
	EffortLevel *string

	CurrentDir *string
	ProjectDir *string

	// Context window usage.
	ContextInputTokens  *int64
	ContextOutputTokens *int64
	ContextWindowSize   *int64
	ContextUsedPercent  *float64

	// Cumulative cost/duration/LOC.
	TotalCostUSD       *float64
	TotalDurationMs    *int64
	TotalAPIDurationMs *int64
	TotalLinesAdded    *int64
	TotalLinesRemoved  *int64

	// RateLimitWindows carries EVERY rate-limit window present in the
	// payload, sorted by LimitID (JSON object order is not stable).
	// Issue #21: the previous shape hardcoded five_hour/seven_day fields
	// and silently dropped anything else — a window the provider adds
	// (e.g. a per-model weekly limit) must be ingested the day it
	// appears, with no parser change.
	RateLimitWindows []RateLimitWindow
}

// RateLimitWindow is one rolling quota window from the payload's
// rate_limits object. LimitID is the payload's own key (five_hour,
// seven_day, whatever arrives next); both measurements follow the
// nil-means-unknown rule.
type RateLimitWindow struct {
	LimitID     string
	UsedPercent *float64
	ResetsAt    *time.Time
}

// rawStatusLine mirrors the on-wire JSON shape. Using `any`/pointer leaves
// for every optional field lets json.Unmarshal distinguish "absent",
// "null", and "present" without ever defaulting to a zero value. Unknown
// top-level and nested fields are tolerated automatically because we only
// decode the fields we recognize; encoding/json ignores the rest.
type rawStatusLine struct {
	SessionID string `json:"session_id"`

	Model *struct {
		ID          *string `json:"id"`
		DisplayName *string `json:"display_name"`
	} `json:"model"`

	Effort *struct {
		Level *string `json:"level"`
	} `json:"effort"`

	Workspace *struct {
		CurrentDir *string `json:"current_dir"`
		ProjectDir *string `json:"project_dir"`
	} `json:"workspace"`

	ContextWindow *struct {
		TotalInputTokens  *int64   `json:"total_input_tokens"`
		TotalOutputTokens *int64   `json:"total_output_tokens"`
		ContextWindowSize *int64   `json:"context_window_size"`
		UsedPercentage    *float64 `json:"used_percentage"`
	} `json:"context_window"`

	Cost *struct {
		TotalCostUSD       *float64 `json:"total_cost_usd"`
		TotalDurationMs    *int64   `json:"total_duration_ms"`
		TotalAPIDurationMs *int64   `json:"total_api_duration_ms"`
		TotalLinesAdded    *int64   `json:"total_lines_added"`
		TotalLinesRemoved  *int64   `json:"total_lines_removed"`
	} `json:"cost"`

	RateLimits map[string]*rawRateWindow `json:"rate_limits"`
}

type rawRateWindow struct {
	UsedPercentage *float64       `json:"used_percentage"`
	ResetsAt       *flexTimestamp `json:"resets_at"`
}

// flexTimestamp decodes the two encodings resets_at has appeared in: Unix
// epoch seconds (the documented on-wire shape — statusline.md "rate_limits")
// and RFC3339 strings (the shape this parser originally assumed, kept so
// older hand-authored payloads stay parseable). Any other shape decodes to
// "unknown" WITHOUT returning an error: encoding/json aborts the entire
// Unmarshal on a single recognized-field error, and issue #27's incident
// showed what that costs — rate_limits appears only after a session's first
// API response, so every later payload failed wholesale, blanking the
// statusline and silencing quota/context/usage ingest for the rest of every
// real session. One unrecognized field must degrade to nil, never take the
// whole snapshot down.
type flexTimestamp struct {
	t  time.Time
	ok bool
}

func (f *flexTimestamp) UnmarshalJSON(b []byte) error {
	var epoch float64
	if err := json.Unmarshal(b, &epoch); err == nil {
		sec, frac := math.Modf(epoch)
		f.t = time.Unix(int64(sec), int64(frac*1e9)).UTC()
		f.ok = true
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		if t, perr := time.Parse(time.RFC3339, s); perr == nil {
			f.t, f.ok = t, true
		}
		return nil
	}
	return nil
}

// timePtr returns the decoded timestamp, or nil when the field was absent,
// null, or an unrecognized shape.
func (f *flexTimestamp) timePtr() *time.Time {
	if f == nil || !f.ok {
		return nil
	}
	t := f.t
	return &t
}

// ParseStatusLine parses a single Claude Code status-line JSON snapshot
// (read from stdin per ADD §22.5). It tolerates unknown fields at any
// nesting level, tolerates unrecognized encodings of recognized fields by
// degrading them to unknown (see flexTimestamp), and never substitutes a
// zero value for a field that was null or absent in the source payload.
//
// A malformed (syntactically invalid) payload returns a *domain.Error with
// Code ErrCodeValidation so callers (the hook wrapper) can fall back to a
// safe default response rather than crash.
func ParseStatusLine(raw []byte) (StatusLineSnapshot, error) {
	var r rawStatusLine
	if err := json.Unmarshal(raw, &r); err != nil {
		return StatusLineSnapshot{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   fmt.Sprintf("claude statusline: invalid JSON: %v", err),
			Retryable: false,
		}
	}

	if r.SessionID == "" {
		return StatusLineSnapshot{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "claude statusline: missing session_id",
			Retryable: false,
		}
	}

	snap := StatusLineSnapshot{
		SessionID: domain.SessionID(r.SessionID),
	}

	if r.Model != nil {
		snap.ModelID = r.Model.ID
		snap.ModelDisplayName = r.Model.DisplayName
	}

	if r.Effort != nil {
		snap.EffortLevel = r.Effort.Level
	}

	if r.Workspace != nil {
		snap.CurrentDir = r.Workspace.CurrentDir
		snap.ProjectDir = r.Workspace.ProjectDir
	}

	if r.ContextWindow != nil {
		snap.ContextInputTokens = r.ContextWindow.TotalInputTokens
		snap.ContextOutputTokens = r.ContextWindow.TotalOutputTokens
		snap.ContextWindowSize = r.ContextWindow.ContextWindowSize
		snap.ContextUsedPercent = r.ContextWindow.UsedPercentage
	}

	if r.Cost != nil {
		snap.TotalCostUSD = r.Cost.TotalCostUSD
		snap.TotalDurationMs = r.Cost.TotalDurationMs
		snap.TotalAPIDurationMs = r.Cost.TotalAPIDurationMs
		snap.TotalLinesAdded = r.Cost.TotalLinesAdded
		snap.TotalLinesRemoved = r.Cost.TotalLinesRemoved
	}

	for limitID, w := range r.RateLimits {
		if w == nil {
			continue
		}
		usedPercent := w.UsedPercentage
		resetsAt := w.ResetsAt.timePtr()
		if usedPercent == nil && resetsAt == nil {
			// A window that measured nothing observes nothing (ADD
			// §22.10: absence means unknown, not zero usage).
			continue
		}
		snap.RateLimitWindows = append(snap.RateLimitWindows, RateLimitWindow{
			LimitID:     limitID,
			UsedPercent: usedPercent,
			ResetsAt:    resetsAt,
		})
	}
	sort.Slice(snap.RateLimitWindows, func(i, j int) bool {
		return snap.RateLimitWindows[i].LimitID < snap.RateLimitWindows[j].LimitID
	})

	return snap, nil
}

// ContextObservation projects the parsed snapshot into the frozen
// domain.ContextObservation shape (internal/domain/usage.go). Source is
// always domain.SourceStatusLine. Confidence is domain.ConfidenceExact
// when both used tokens and window size are present, else
// domain.ConfidenceUnavailable — callers must not guess.
func (s StatusLineSnapshot) ContextObservation(observedAt time.Time) domain.ContextObservation {
	confidence := domain.ConfidenceUnavailable
	if s.ContextInputTokens != nil && s.ContextWindowSize != nil {
		confidence = domain.ConfidenceExact
	}

	var usedTokens *int64
	if s.ContextInputTokens != nil {
		total := *s.ContextInputTokens
		if s.ContextOutputTokens != nil {
			total += *s.ContextOutputTokens
		}
		usedTokens = &total
	}

	return domain.ContextObservation{
		SessionID:    s.SessionID,
		UsedTokens:   usedTokens,
		WindowTokens: s.ContextWindowSize,
		UsedPercent:  s.ContextUsedPercent,
		Source:       domain.SourceStatusLine,
		Confidence:   confidence,
		ObservedAt:   observedAt,
	}
}

// quotaLimitNames maps known limit ids to their human-readable names; an
// id outside this map renders as itself — a new window arriving on the
// wire must never be dropped or mislabeled while waiting for a name.
var quotaLimitNames = map[string]string{
	"five_hour": "5h rolling usage",
	"seven_day": "7d rolling usage",
}

// QuotaObservations projects every captured rate-limit window into the
// frozen domain.QuotaObservation shape (issue #21: one observation per
// window, however many arrive). Windows that measured nothing were
// already skipped at parse time (ADD §22.10: absence means unknown, not
// zero usage). Confidence follows the per-window field completeness rule
// the old fixed-window methods used: high with both measurements, medium
// with one.
func (s StatusLineSnapshot) QuotaObservations(observedAt time.Time) []domain.QuotaObservation {
	if len(s.RateLimitWindows) == 0 {
		return nil
	}
	out := make([]domain.QuotaObservation, 0, len(s.RateLimitWindows))
	for _, w := range s.RateLimitWindows {
		confidence := domain.ConfidenceHigh
		if w.UsedPercent == nil || w.ResetsAt == nil {
			confidence = domain.ConfidenceMedium
		}
		name := quotaLimitNames[w.LimitID]
		if name == "" {
			name = w.LimitID
		}
		out = append(out, domain.QuotaObservation{
			SessionID:   s.SessionID,
			Provider:    "claude",
			LimitID:     w.LimitID,
			LimitName:   name,
			UsedPercent: w.UsedPercent,
			ResetsAt:    w.ResetsAt,
			Source:      domain.SourceStatusLine,
			Confidence:  confidence,
			ObservedAt:  observedAt,
		})
	}
	return out
}

// WeeklyLimitUsedPercent returns the seven_day window's used percentage,
// or nil when that window was not observed — the statusline's weekly
// segment reads this without caring how many other windows exist.
func (s StatusLineSnapshot) WeeklyLimitUsedPercent() *float64 {
	for _, w := range s.RateLimitWindows {
		if w.LimitID == "seven_day" {
			return w.UsedPercent
		}
	}
	return nil
}
