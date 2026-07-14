// Package claude normalizes the intermediate Go structs produced by
// internal/providers/claude and internal/hooks/claude (StatusLineSnapshot,
// UserPromptSubmitEvent, StopEvent, StopFailureEvent) into the frozen
// pkg/protocol/v1.Event envelope (ADD §11.1, CONTRACT_FREEZE.md). This is
// the sole path from raw Claude Code provider payloads into Auspex's
// wire event protocol (claude-provider-04) — no other package in this
// repository constructs a v1.Event from Claude payloads.
//
// This package only ever emits EventType values already defined in
// pkg/protocol/v1.EventType. If a source struct needs an event type that
// does not exist in that closed taxonomy, that is a contract gap to raise
// with contract-integrator, not something this package works around.
package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	claudehooks "github.com/huaiche94/auspex/internal/hooks/claude"
	claudeprovider "github.com/huaiche94/auspex/internal/providers/claude"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// Provider is the frozen provider identifier this package stamps onto every
// produced event's Event.Provider field.
const Provider = "claude"

// Normalizer turns Wave-1 parsed Claude Code structs into frozen
// pkg/protocol/v1.Event values. It depends only on the frozen
// domain.Clock/domain.IDGenerator ports (internal/domain/clock.go) so tests
// can supply deterministic fakes rather than this package reaching for
// time.Now/crypto/rand itself or depending on foundation's concrete
// internal/idgen implementation, which is out of scope for this wave
// (claude-provider-04 depends on claude-provider-01/02/03 and
// contract-integrator-04 only — not foundation-06).
type Normalizer struct {
	Clock domain.Clock
	IDs   domain.IDGenerator
}

// NewNormalizer constructs a Normalizer from explicit Clock/IDGenerator
// dependencies. Both are required; callers in package tests should supply
// fakes (see normalizer_test.go).
func NewNormalizer(clock domain.Clock, ids domain.IDGenerator) *Normalizer {
	return &Normalizer{Clock: clock, IDs: ids}
}

// envelope fills the fields common to every produced Event: schema version,
// a fresh EventID from the injected IDGenerator, EventType, ObservedAt (now,
// per the injected Clock), Source, and Provider. occurredAt is the
// event-specific "when did this actually happen" timestamp, which may equal
// ObservedAt when the source payload carries no better timestamp of its
// own (status-line snapshots and hook payloads do not carry one).
func (n *Normalizer) envelope(eventType v1.EventType, occurredAt time.Time, sessionID domain.SessionID) v1.Event {
	now := n.Clock.Now()
	return v1.Event{
		SchemaVersion: v1.SchemaVersionEvent,
		EventID:       n.IDs.NewID(),
		EventType:     eventType,
		OccurredAt:    occurredAt,
		ObservedAt:    now,
		Source:        string(domain.SourceStatusLine), // overwritten by callers where a different source applies
		Provider:      Provider,
		SessionID:     string(sessionID),
		Payload:       map[string]any{},
	}
}

// digestKey builds a deterministic SHA-256 idempotency key from the given
// parts (CONTRACT_FREEZE.md: "Owning role... defines the exact digest
// algorithm; the field itself is frozen here"). Parts are joined with a
// unit separator byte so that e.g. ("ab", "c") and ("a", "bc") never
// collide.
func digestKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte{0x1f}) // unit separator
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// NormalizeStatusLine projects a StatusLineSnapshot into zero or more
// usage/quota/context observation events. A snapshot can carry context
// usage, a cumulative usage/cost figure, and ANY number of rolling quota
// windows (five_hour and seven_day today; issue #21 made the set open so
// a window the provider adds is ingested the day it appears) — each
// becomes its own Event because pkg/protocol/v1.EventType distinguishes
// them
// (EventProviderContextObserved, EventProviderUsageObserved,
// EventProviderQuotaObserved), and CONTRACT_FREEZE.md's "unknown is not
// zero" rule means an absent measurement must not be synthesized into an
// event that claims to observe it. observedAt is the wall-clock time this
// snapshot was captured (the hook wrapper's read time); it is used as
// OccurredAt since Claude Code's status-line payload carries no event
// timestamp of its own.
func (n *Normalizer) NormalizeStatusLine(snap claudeprovider.StatusLineSnapshot, observedAt time.Time) []v1.Event {
	var events []v1.Event

	if ev, ok := n.contextEvent(snap, observedAt); ok {
		events = append(events, ev)
	}
	if ev, ok := n.usageEvent(snap, observedAt); ok {
		events = append(events, ev)
	}
	for _, w := range snap.RateLimitWindows {
		if ev, ok := n.quotaEvent(snap, observedAt, w.LimitID, w.UsedPercent, w.ResetsAt); ok {
			events = append(events, ev)
		}
	}

	return events
}

func (n *Normalizer) contextEvent(snap claudeprovider.StatusLineSnapshot, observedAt time.Time) (v1.Event, bool) {
	if snap.ContextInputTokens == nil && snap.ContextWindowSize == nil && snap.ContextUsedPercent == nil {
		return v1.Event{}, false
	}

	ev := n.envelope(v1.EventProviderContextObserved, observedAt, snap.SessionID)
	ev.Source = string(domain.SourceStatusLine)
	ev.IdempotencyKey = digestKey(
		"statusline.context", string(snap.SessionID),
		observedAt.UTC().Format(time.RFC3339Nano),
	)

	payload := map[string]any{}
	if snap.ContextInputTokens != nil {
		used := *snap.ContextInputTokens
		if snap.ContextOutputTokens != nil {
			used += *snap.ContextOutputTokens
		}
		payload["used_tokens"] = used
	}
	if snap.ContextWindowSize != nil {
		payload["window_tokens"] = *snap.ContextWindowSize
	}
	if snap.ContextUsedPercent != nil {
		payload["used_percent"] = *snap.ContextUsedPercent
	}
	ev.Payload = payload
	return ev, true
}

func (n *Normalizer) usageEvent(snap claudeprovider.StatusLineSnapshot, observedAt time.Time) (v1.Event, bool) {
	if snap.TotalCostUSD == nil && snap.TotalDurationMs == nil && snap.TotalAPIDurationMs == nil &&
		snap.TotalLinesAdded == nil && snap.TotalLinesRemoved == nil {
		return v1.Event{}, false
	}

	ev := n.envelope(v1.EventProviderUsageObserved, observedAt, snap.SessionID)
	ev.Source = string(domain.SourceStatusLine)
	ev.IdempotencyKey = digestKey(
		"statusline.usage", string(snap.SessionID),
		observedAt.UTC().Format(time.RFC3339Nano),
	)

	payload := map[string]any{}
	if snap.TotalCostUSD != nil {
		payload["total_cost_usd"] = *snap.TotalCostUSD
	}
	if snap.TotalDurationMs != nil {
		payload["total_duration_ms"] = *snap.TotalDurationMs
	}
	if snap.TotalAPIDurationMs != nil {
		payload["total_api_duration_ms"] = *snap.TotalAPIDurationMs
	}
	if snap.TotalLinesAdded != nil {
		payload["total_lines_added"] = *snap.TotalLinesAdded
	}
	if snap.TotalLinesRemoved != nil {
		payload["total_lines_removed"] = *snap.TotalLinesRemoved
	}
	// #20 Phase 1: cohort labels at observation granularity. The status
	// line is the only surface carrying model+effort continuously (the
	// same rationale as provider_sessions' 0005 resolution cache), and a
	// usage sample without identity labels is unlabeled history the
	// cohort ladder (ADD §15.2, ADR-047) can never re-label after a
	// mid-session /model or /fast switch. Labels are additive metadata —
	// they never gate emission (the usage-field check above is
	// unchanged), and absent identity stamps nothing: unknown is not
	// zero.
	if snap.ModelID != nil {
		payload["model_id"] = *snap.ModelID
	}
	if snap.EffortLevel != nil {
		payload["effort"] = *snap.EffortLevel
	}
	ev.Payload = payload
	return ev, true
}

func (n *Normalizer) quotaEvent(snap claudeprovider.StatusLineSnapshot, observedAt time.Time, limitID string, usedPercent *float64, resetsAt *time.Time) (v1.Event, bool) {
	if usedPercent == nil && resetsAt == nil {
		return v1.Event{}, false
	}

	ev := n.envelope(v1.EventProviderQuotaObserved, observedAt, snap.SessionID)
	ev.Source = string(domain.SourceStatusLine)
	ev.IdempotencyKey = digestKey(
		"statusline.quota", string(snap.SessionID), limitID,
		observedAt.UTC().Format(time.RFC3339Nano),
	)

	payload := map[string]any{
		"limit_id": limitID,
	}
	if usedPercent != nil {
		payload["used_percent"] = *usedPercent
	}
	if resetsAt != nil {
		payload["resets_at"] = resetsAt.UTC().Format(time.RFC3339Nano)
	}
	ev.Payload = payload
	return ev, true
}

// NormalizeUserPromptSubmit projects a parsed UserPromptSubmitEvent into a
// provider.turn.started Event. Per Constitution §7 rule 2 and the
// package-level privacy contract, only the already-hashed/length/approx-
// token fields from UserPromptSubmitEvent are copied into the payload;
// no raw prompt text ever passes through this function because
// claudehooks.ParseUserPromptSubmit never returns any in the first place.
func (n *Normalizer) NormalizeUserPromptSubmit(ev claudehooks.UserPromptSubmitEvent, observedAt time.Time) v1.Event {
	out := n.envelope(v1.EventProviderTurnStarted, observedAt, ev.SessionID)
	out.Source = string(domain.SourceHook)
	out.IdempotencyKey = digestKey("userpromptsubmit", string(ev.SessionID), ev.PromptSHA256)

	payload := map[string]any{
		"prompt_sha256":        ev.PromptSHA256,
		"prompt_byte_length":   ev.PromptByteLength,
		"prompt_approx_tokens": ev.PromptApproxTokens,
	}
	if ev.CWD != nil {
		payload["cwd"] = *ev.CWD
	}
	out.Payload = payload
	return out
}

// NormalizeStop projects a parsed StopEvent into a provider.turn.completed
// Event.
func (n *Normalizer) NormalizeStop(ev claudehooks.StopEvent, observedAt time.Time) v1.Event {
	out := n.envelope(v1.EventProviderTurnCompleted, observedAt, ev.SessionID)
	out.Source = string(domain.SourceHook)
	out.IdempotencyKey = digestKey("stop", string(ev.SessionID), observedAt.UTC().Format(time.RFC3339Nano))

	payload := map[string]any{}
	if ev.StopHookActive != nil {
		payload["stop_hook_active"] = *ev.StopHookActive
	}
	// #20 Phase 0: the effort the completed turn actually ran under is a
	// calibration label — recorded on the turn.completed event so future
	// stratification (#11) can join it against the turn's prediction row.
	if ev.EffortLevel != nil {
		payload["effort"] = *ev.EffortLevel
	}
	out.Payload = payload
	return out
}

// NormalizeStopFailure projects a parsed StopFailureEvent into one or two
// events: always a provider.turn.failed Event, and additionally a
// provider.rate_limit.hit Event when the classified FailureClass is
// domain.FailureProviderRateLimit — the two EventTypes are not mutually
// exclusive in the frozen taxonomy (a turn can fail *because of* a rate
// limit), and downstream consumers interested specifically in rate-limit
// pressure (e.g. predictor's runway forecasting) should not have to
// pattern-match Payload["failure_class"] on every turn.failed event to find
// rate-limit occurrences.
//
// Per Constitution §7 rule 2 and this package's privacy contract, only
// ErrorMessageLen (an int) is copied into the payload — the raw error
// message text is never retained past claudehooks.ParseStopFailure, so it
// cannot leak here even if a future edit tried to add it.
func (n *Normalizer) NormalizeStopFailure(ev claudehooks.StopFailureEvent, observedAt time.Time) []v1.Event {
	base := n.envelope(v1.EventProviderTurnFailed, observedAt, ev.SessionID)
	base.Source = string(domain.SourceHook)
	base.IdempotencyKey = digestKey("stopfailure", string(ev.SessionID), observedAt.UTC().Format(time.RFC3339Nano))

	payload := map[string]any{
		"failure_class":     string(ev.FailureClass),
		"error_message_len": ev.ErrorMessageLen,
	}
	if ev.RawErrorType != nil {
		payload["raw_error_type"] = *ev.RawErrorType
	}
	if ev.RawStatusCode != nil {
		payload["raw_status_code"] = *ev.RawStatusCode
	}
	base.Payload = payload

	events := []v1.Event{base}

	if ev.FailureClass == domain.FailureProviderRateLimit {
		rl := n.envelope(v1.EventProviderRateLimitHit, observedAt, ev.SessionID)
		rl.Source = string(domain.SourceHook)
		rl.IdempotencyKey = digestKey("stopfailure.ratelimit", string(ev.SessionID), observedAt.UTC().Format(time.RFC3339Nano))
		rlPayload := map[string]any{
			"failure_class": string(ev.FailureClass),
		}
		if ev.RawStatusCode != nil {
			rlPayload["raw_status_code"] = *ev.RawStatusCode
		}
		rl.Payload = rlPayload
		events = append(events, rl)
	}

	return events
}
