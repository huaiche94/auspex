// Package codex normalizes the intermediate Go structs produced by
// internal/hooks/codex (SessionStartEvent, UserPromptSubmitEvent,
// StopEvent), this package's own rollout reader (RolloutSnapshot), and
// internal/managed's exec-stream summary (ManagedExecOutcome,
// managedexec.go — issue #9 M7 Phase 1) into the frozen
// pkg/protocol/v1.Event envelope — issue #9 Phase 1's analog of
// internal/telemetry/claude. This is the sole path from raw Codex CLI
// payloads into Auspex's wire event protocol; no other package
// constructs a v1.Event from Codex payloads.
//
// This package only ever emits EventType values already defined in
// pkg/protocol/v1.EventType's closed taxonomy. If a Codex surface needs an
// event type that does not exist there, that is a contract gap to raise
// with the lead, not something this package works around.
//
// Event persistence deliberately has no codex-side store:
// internal/telemetry/claude.EventStore is provider-agnostic (it writes
// whatever Event.Provider each envelope carries into the generic `events`
// table, and every consumer goes through the narrow
// orchestrator.EventPersister seam), so Codex events persist through the
// exact same store instance cmd/auspex already wires — reuse without
// editing claude's package API, and no new migration (issue #9 Phase 1
// storage decision).
package codex

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/features"
	codexhooks "github.com/huaiche94/auspex/internal/hooks/codex"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// Provider is the frozen provider identifier this package stamps onto every
// produced event's Event.Provider field.
const Provider = "codex"

// Normalizer turns parsed Codex structs into frozen pkg/protocol/v1.Event
// values. It depends only on the frozen domain.Clock/domain.IDGenerator
// ports so tests can supply deterministic fakes — the same construction as
// internal/telemetry/claude.Normalizer.
type Normalizer struct {
	Clock domain.Clock
	IDs   domain.IDGenerator
}

// NewNormalizer constructs a Normalizer from explicit Clock/IDGenerator
// dependencies. Both are required; tests supply fakes.
func NewNormalizer(clock domain.Clock, ids domain.IDGenerator) *Normalizer {
	return &Normalizer{Clock: clock, IDs: ids}
}

// envelope fills the fields common to every produced Event. occurredAt is
// the event-specific "when did this actually happen" timestamp, which
// equals ObservedAt here because Codex hook payloads carry no event
// timestamp of their own (the rollout lines do, but the snapshot is read at
// Stop time and honestly attributed to that observation instant).
func (n *Normalizer) envelope(eventType v1.EventType, occurredAt time.Time, sessionID domain.SessionID) v1.Event {
	now := n.Clock.Now()
	return v1.Event{
		SchemaVersion: v1.SchemaVersionEvent,
		EventID:       n.IDs.NewID(),
		EventType:     eventType,
		OccurredAt:    occurredAt,
		ObservedAt:    now,
		Source:        string(domain.SourceHook),
		Provider:      Provider,
		SessionID:     string(sessionID),
		Payload:       map[string]any{},
	}
}

// digestKey builds a deterministic SHA-256 idempotency key from the given
// parts — the same algorithm internal/telemetry/claude uses (unit-separator
// joining so part boundaries can never collide), re-declared here because
// the digest discipline is each provider package's own responsibility
// (CONTRACT_FREEZE.md: the owning role defines the exact digest algorithm).
func digestKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte{0x1f}) // unit separator
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// turnRef returns the stable per-turn idempotency ingredient: Codex's own
// turn_id when the payload carried one (so a re-delivered hook for the same
// turn dedupes exactly), else the observation timestamp (claude's
// time-based fallback discipline — two genuinely different observations
// still get different keys).
func turnRef(turnID domain.TurnID, observedAt time.Time) string {
	if turnID != "" {
		return string(turnID)
	}
	return observedAt.UTC().Format(time.RFC3339Nano)
}

// NormalizeSessionStart projects a parsed SessionStartEvent into the
// matching session-lifecycle event. The mapping honors Codex's own source
// enum against the frozen taxonomy — no new EventType is invented:
//
//	startup, clear, "" (absent), anything unknown -> provider.session.started
//	resume                                        -> provider.session.resumed
//	compact                                       -> provider.session.compacted
//
// "clear" starts a fresh conversation (a start, not a resume), and an
// unknown future enum value degrades to the weakest true claim — a session
// began being observed.
func (n *Normalizer) NormalizeSessionStart(ev codexhooks.SessionStartEvent, observedAt time.Time) v1.Event {
	eventType := v1.EventProviderSessionStarted
	switch ev.Source {
	case codexhooks.SessionStartResume:
		eventType = v1.EventProviderSessionResumed
	case codexhooks.SessionStartCompact:
		eventType = v1.EventProviderSessionCompacted
	}

	out := n.envelope(eventType, observedAt, ev.SessionID)
	out.IdempotencyKey = digestKey(
		"codex.sessionstart", string(ev.SessionID), ev.Source,
		observedAt.UTC().Format(time.RFC3339Nano),
	)

	payload := map[string]any{}
	if ev.Source != "" {
		payload["source"] = ev.Source // provider enum value, not user text
	}
	if ev.Model != nil && *ev.Model != "" {
		payload["model_id"] = *ev.Model
	}
	if ev.PermissionMode != nil && *ev.PermissionMode != "" {
		payload["permission_mode"] = *ev.PermissionMode // provider enum value
	}
	if ev.CWD != nil && *ev.CWD != "" {
		payload["cwd"] = *ev.CWD // a path, same allowance as claude's turn.started
	}
	out.Payload = payload
	return out
}

// NormalizeUserPromptSubmit projects a parsed UserPromptSubmitEvent into a
// provider.turn.started Event. Per Constitution §7 rule 2 only
// already-derived signals are copied into the payload — the hash/size trio
// plus the issue-#42 derived feature booleans/counts (via the shared
// features codec, so Codex and Claude turn.started payloads carry the
// identical key vocabulary); no raw prompt text can pass through because
// codexhooks.ParseUserPromptSubmit never returns any. The event's TurnID is
// Codex's own turn_id — provider-stable, so the prediction↔actual join
// needs no OpenTurnResolver stamping on this provider's terminal events.
func (n *Normalizer) NormalizeUserPromptSubmit(ev codexhooks.UserPromptSubmitEvent, observedAt time.Time) v1.Event {
	out := n.envelope(v1.EventProviderTurnStarted, observedAt, ev.SessionID)
	out.TurnID = string(ev.TurnID)
	out.IdempotencyKey = digestKey(
		"codex.userpromptsubmit", string(ev.SessionID), string(ev.TurnID), ev.PromptSHA256,
	)

	payload := map[string]any{}
	if ev.CWD != nil && *ev.CWD != "" {
		payload["cwd"] = *ev.CWD
	}
	if ev.Model != nil && *ev.Model != "" {
		payload["model_id"] = *ev.Model
	}
	// Same extracted-iff-marked discipline as claude's normalizer (#50):
	// a Features set carrying the extraction marker persists the full
	// codec vocabulary; a zero-value Features persists only the size trio.
	if f := ev.Features; f.SHA256Hex != "" {
		for k, v := range features.EncodePromptFeatures(f) {
			payload[k] = v
		}
	} else {
		payload["prompt_sha256"] = ev.PromptSHA256
		payload["prompt_byte_length"] = ev.PromptByteLength
		payload["prompt_approx_tokens"] = ev.PromptApproxTokens
	}
	out.Payload = payload
	return out
}

// NormalizeStop projects a parsed StopEvent plus the optional rollout
// snapshot into the turn's terminal event set:
//
//	provider.turn.completed  — always; carries the per-turn token ACTUAL
//	                           (snapshot's last_token_usage) when available
//	provider.context.observed — when the snapshot yields a context measurement
//	provider.quota.observed   — one per rollout rate-limit window observed
//
// snap == nil means no rollout was readable and the terminal event carries
// no usage fields at all — fail-open enrichment, never a new failure mode
// (ADR-051's contract, applied to the rollout).
//
// Token-key vocabulary: the payload reuses managedUsageEvent's frozen keys
// so usage readers need no per-source mapping, with Codex's differing raw
// semantics normalized rather than leaked (Constitution §7: provider wire
// payloads must not leak unnormalized):
//
//	input_tokens             = codex input_tokens - cached_input_tokens
//	                           (codex's input INCLUDES the cached portion;
//	                           the shared vocabulary's input_tokens means
//	                           fresh, uncached input). When the cached
//	                           counter is absent the split is unknown and
//	                           input_tokens is NOT emitted (unknown is not
//	                           zero) — the raw total still reaches
//	                           context.observed, which wants exactly the
//	                           full-context number.
//	cache_read_input_tokens  = codex cached_input_tokens
//	output_tokens            = codex output_tokens (includes reasoning)
//	reasoning_output_tokens  = codex reasoning_output_tokens (additive,
//	                           codex-specific numeric key; a subset of
//	                           output_tokens)
//	total_tokens             = input_tokens + output_tokens under the SAME
//	                           definition managedUsageEvent documents
//	                           (fresh work only, cache carried separately)
//	                           — deliberately NOT codex's own total_tokens,
//	                           which includes cached input.
func (n *Normalizer) NormalizeStop(ev codexhooks.StopEvent, observedAt time.Time, snap *RolloutSnapshot) []v1.Event {
	ref := turnRef(ev.TurnID, observedAt)

	completed := n.envelope(v1.EventProviderTurnCompleted, observedAt, ev.SessionID)
	completed.TurnID = string(ev.TurnID)
	completed.IdempotencyKey = digestKey("codex.stop", string(ev.SessionID), ref)

	payload := map[string]any{}
	if ev.StopHookActive != nil {
		payload["stop_hook_active"] = *ev.StopHookActive
	}
	if ev.Model != nil && *ev.Model != "" {
		payload["model_id"] = *ev.Model
	}
	if snap != nil && snap.Last != nil {
		u := snap.Last
		var freshInput *int64
		if u.InputTokens != nil && u.CachedInputTokens != nil {
			v := *u.InputTokens - *u.CachedInputTokens
			freshInput = &v
			payload["input_tokens"] = v
		}
		if u.CachedInputTokens != nil {
			payload["cache_read_input_tokens"] = *u.CachedInputTokens
		}
		if u.OutputTokens != nil {
			payload["output_tokens"] = *u.OutputTokens
		}
		if u.ReasoningOutputTokens != nil {
			payload["reasoning_output_tokens"] = *u.ReasoningOutputTokens
		}
		if freshInput != nil && u.OutputTokens != nil {
			payload["total_tokens"] = *freshInput + *u.OutputTokens
		}
	}
	completed.Payload = payload

	events := []v1.Event{completed}

	if snap != nil {
		if ctxEv, ok := n.contextEvent(ev, observedAt, snap, ref); ok {
			events = append(events, ctxEv)
		}
		for _, w := range snap.RateLimits {
			events = append(events, n.quotaEvent(ev, observedAt, snap, w, ref))
		}
	}
	return events
}

// contextEvent builds the provider.context.observed event from the
// snapshot's last request: the tokens the final API call actually sent plus
// produced IS the session's current context fill (codex input_tokens is the
// full prompt context, cached and fresh alike). No used_percent is emitted —
// the rollout carries none, and downstream derives it from the two
// measurements (unknown is not zero).
func (n *Normalizer) contextEvent(ev codexhooks.StopEvent, observedAt time.Time, snap *RolloutSnapshot, ref string) (v1.Event, bool) {
	var usedTokens *int64
	if snap.Last != nil && snap.Last.InputTokens != nil {
		used := *snap.Last.InputTokens
		if snap.Last.OutputTokens != nil {
			used += *snap.Last.OutputTokens
		}
		usedTokens = &used
	}
	if usedTokens == nil && snap.ModelContextWindow == nil {
		return v1.Event{}, false
	}

	out := n.envelope(v1.EventProviderContextObserved, observedAt, ev.SessionID)
	out.TurnID = string(ev.TurnID)
	out.Source = string(domain.SourceProviderEvent) // rollout event_msg line, not the hook payload itself
	out.IdempotencyKey = digestKey("codex.rollout.context", string(ev.SessionID), ref)

	payload := map[string]any{}
	if usedTokens != nil {
		payload["used_tokens"] = *usedTokens
	}
	if snap.ModelContextWindow != nil {
		payload["window_tokens"] = *snap.ModelContextWindow
	}
	out.Payload = payload
	return out, true
}

// quotaEvent builds one provider.quota.observed event per rollout
// rate-limit window, under the same key vocabulary claude's statusline
// quota events use (limit_id/used_percent/resets_at) plus the
// codex-specific window_minutes and plan_type ids the rollout carries.
func (n *Normalizer) quotaEvent(ev codexhooks.StopEvent, observedAt time.Time, snap *RolloutSnapshot, w RateLimitWindow, ref string) v1.Event {
	out := n.envelope(v1.EventProviderQuotaObserved, observedAt, ev.SessionID)
	out.TurnID = string(ev.TurnID)
	out.Source = string(domain.SourceProviderEvent)
	out.IdempotencyKey = digestKey("codex.rollout.quota", string(ev.SessionID), w.LimitID, ref)

	payload := map[string]any{
		"limit_id": w.LimitID,
	}
	if w.UsedPercent != nil {
		payload["used_percent"] = *w.UsedPercent
	}
	if w.WindowMinutes != nil {
		payload["window_minutes"] = *w.WindowMinutes
	}
	if w.ResetsAt != nil {
		payload["resets_at"] = w.ResetsAt.UTC().Format(time.RFC3339Nano)
	}
	if snap.PlanType != "" {
		payload["plan_type"] = snap.PlanType // account plan enum id
	}
	out.Payload = payload
	return out
}
