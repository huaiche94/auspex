// appserverrun.go normalizes one managed App Server run's outcome
// (ADR-0056's mapping table; issue #9 M7 Phase 2) into the frozen event
// envelope — the third codex producer after the hook/rollout path
// (normalizer.go) and the exec JSONL path (managedexec.go), sharing
// their exact key vocabulary so every downstream consumer (retention
// export, ADR-0055's cohort ladder, the statusline read-back) works on
// App Server telemetry with zero changes.
//
// Failure semantics mirror managedexec.go: the turn maps to
// provider.turn.completed only when the server's own terminal
// notification said `completed`; an `interrupted` status maps to
// provider.turn.interrupted (the one EventType the exec path never
// emits — protocol interrupts are an App-Server-only capability); a
// `failed` status, a lost connection, or a turn that never reached a
// terminal notification map to provider.turn.failed with the reason
// disclosed in the payload.
package codex

import (
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/providers/codex/appserver"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// AppServerRunOutcome is the privacy-safe summary of one managed App
// Server run (one thread, one turn). Pointer fields follow the
// nil-means-unknown rule throughout.
type AppServerRunOutcome struct {
	SessionID  domain.SessionID
	TurnID     domain.TurnID
	WorktreeID domain.WorktreeID
	TaskID     *domain.TaskID

	// ThreadSeen/ThreadID mirror ManagedExecOutcome's thread fields: the
	// provider's own thread identifier (the thread/resume locator), an
	// id, never content.
	ThreadSeen bool
	ThreadID   string

	// ModelID is thread/start's resolved model — the cohort label the
	// exec JSONL stream never declares but the App Server does ("" when
	// the response omitted it; never guessed).
	ModelID string

	// Status is the FINAL turn status the server reported;
	// TerminalSeen=false means the connection ended before any terminal
	// turn notification (Status is then meaningless and ignored).
	Status       appserver.TurnStatus
	TerminalSeen bool
	// ConnectionLost marks a run whose App Server stream ended before
	// the turn's terminal notification — disclosed on the turn.failed
	// payload so a dropped connection is never mistaken for a provider
	// failure.
	ConnectionLost bool

	ItemCount         int
	DurationMs        *int64
	FailureMessageLen *int

	// Usage is the thread's cumulative token accounting from the LAST
	// thread/tokenUsage/updated notification, VERBATIM under codex wire
	// semantics (inputTokens includes cachedInputTokens). For a one-shot
	// run the thread is fresh, so the thread total IS the turn's own
	// accounting. nil when no usage notification arrived.
	Usage *appserver.TokenUsageBreakdown
	// ModelContextWindow rides the same notification (nil = not
	// reported).
	ModelContextWindow *int64

	// RateLimits is the LAST observed account/rateLimits snapshot
	// (updated notification or explicit read), nil when never observed.
	RateLimits *appserver.RateLimitSnapshot
}

// Failed reports whether the outcome normalizes to provider.turn.failed.
func (o AppServerRunOutcome) Failed() bool {
	return !o.TerminalSeen || o.Status == appserver.TurnStatusFailed
}

// Interrupted reports whether the outcome normalizes to
// provider.turn.interrupted.
func (o AppServerRunOutcome) Interrupted() bool {
	return o.TerminalSeen && o.Status == appserver.TurnStatusInterrupted
}

// NormalizeAppServerRun projects the run into its event batch:
// provider.session.started when the thread was observed, exactly one
// terminal turn event, one provider.usage.observed when usage was
// measured, one provider.context.observed when the context window was
// reported alongside it, and one provider.quota.observed per observed
// rate-limit window. Idempotency keys are turn-scoped (one run, one
// TurnID, one terminal outcome — the managed-path contract shared with
// NormalizeManagedExec). observedAt is the runner's observation instant;
// App Server notifications carry millisecond timestamps for items only,
// so the observation instant is the honest OccurredAt.
func (n *Normalizer) NormalizeAppServerRun(o AppServerRunOutcome, observedAt time.Time) []v1.Event {
	var events []v1.Event

	if o.ThreadSeen {
		started := n.envelope(v1.EventProviderSessionStarted, observedAt, o.SessionID)
		n.stampAppServerScope(&started, o)
		started.IdempotencyKey = digestKey("codex.appserver.thread", string(o.SessionID), string(o.TurnID))
		payload := map[string]any{}
		if o.ThreadID != "" {
			payload["thread_id"] = o.ThreadID
		}
		if o.ModelID != "" {
			payload["model_id"] = o.ModelID
		}
		started.Payload = payload
		events = append(events, started)
	}

	eventType := v1.EventProviderTurnCompleted
	switch {
	case o.Interrupted():
		eventType = v1.EventProviderTurnInterrupted
	case o.Failed():
		eventType = v1.EventProviderTurnFailed
	}
	terminal := n.envelope(eventType, observedAt, o.SessionID)
	n.stampAppServerScope(&terminal, o)
	terminal.IdempotencyKey = digestKey("codex.appserver.turn", string(o.SessionID), string(o.TurnID))
	payload := map[string]any{
		"terminal_seen": o.TerminalSeen,
	}
	if o.TerminalSeen {
		payload["turn_status"] = string(o.Status)
	}
	if o.ConnectionLost {
		payload["connection_lost"] = true
	}
	if o.ItemCount > 0 {
		payload["item_count"] = o.ItemCount
	}
	if o.DurationMs != nil {
		payload["duration_ms"] = *o.DurationMs
	}
	if o.FailureMessageLen != nil {
		payload["failure_message_len"] = *o.FailureMessageLen
	}
	terminal.Payload = payload
	events = append(events, terminal)

	if usage, ok := n.appServerUsageEvent(o, observedAt); ok {
		events = append(events, usage)
	}
	if ctxEv, ok := n.appServerContextEvent(o, observedAt); ok {
		events = append(events, ctxEv)
	}
	events = append(events, n.appServerQuotaEvents(o, observedAt)...)
	return events
}

// appServerUsageEvent builds the turn-exact provider.usage.observed
// event under the frozen shared key vocabulary (managedExecUsageEvent's
// normalization verbatim — fresh input split out of codex's
// cached-inclusive inputTokens; total = fresh input + output), plus the
// model_id cohort label the App Server uniquely supplies.
func (n *Normalizer) appServerUsageEvent(o AppServerRunOutcome, observedAt time.Time) (v1.Event, bool) {
	u := o.Usage
	if u == nil {
		return v1.Event{}, false
	}
	if u.InputTokens == 0 && u.CachedInputTokens == 0 && u.OutputTokens == 0 && u.ReasoningOutputTokens == 0 {
		// A usage notification that measured nothing observes nothing
		// (unknown is not zero).
		return v1.Event{}, false
	}

	ev := n.envelope(v1.EventProviderUsageObserved, observedAt, o.SessionID)
	n.stampAppServerScope(&ev, o)
	ev.IdempotencyKey = digestKey("codex.appserver.usage", string(o.SessionID), string(o.TurnID))

	fresh := u.InputTokens - u.CachedInputTokens
	if fresh < 0 {
		fresh = 0 // corrupt counter pair; never emit a negative class
	}
	payload := map[string]any{
		"input_tokens":            fresh,
		"cache_read_input_tokens": u.CachedInputTokens,
		"output_tokens":           u.OutputTokens,
		"reasoning_output_tokens": u.ReasoningOutputTokens,
		"total_tokens":            fresh + u.OutputTokens,
	}
	if o.ModelID != "" {
		payload["model_id"] = o.ModelID
	}
	ev.Payload = payload
	return ev, true
}

// appServerContextEvent reports the context measurement the usage
// notification carried (window size + the thread's cumulative tokens as
// the fill measurement's inputs), mirroring contextEvent's key set.
func (n *Normalizer) appServerContextEvent(o AppServerRunOutcome, observedAt time.Time) (v1.Event, bool) {
	if o.ModelContextWindow == nil {
		return v1.Event{}, false
	}
	ev := n.envelope(v1.EventProviderContextObserved, observedAt, o.SessionID)
	n.stampAppServerScope(&ev, o)
	ev.IdempotencyKey = digestKey("codex.appserver.context", string(o.SessionID), string(o.TurnID))
	payload := map[string]any{
		"window_tokens": *o.ModelContextWindow,
	}
	if u := o.Usage; u != nil && (u.InputTokens > 0 || u.OutputTokens > 0) {
		payload["used_tokens"] = u.InputTokens + u.OutputTokens
	}
	ev.Payload = payload
	return ev, true
}

// appServerQuotaEvents builds one provider.quota.observed per observed
// window under quotaEvent's exact key vocabulary. Window identity
// follows the rollout convention: "primary" (5h-class) and "secondary"
// (weekly-class) — the ids the statusline's window labeler already
// understands.
func (n *Normalizer) appServerQuotaEvents(o AppServerRunOutcome, observedAt time.Time) []v1.Event {
	rl := o.RateLimits
	if rl == nil {
		return nil
	}
	var events []v1.Event
	emit := func(limitID string, w *appserver.RateLimitWindow) {
		if w == nil {
			return
		}
		ev := n.envelope(v1.EventProviderQuotaObserved, observedAt, o.SessionID)
		n.stampAppServerScope(&ev, o)
		ev.IdempotencyKey = digestKey("codex.appserver.quota", string(o.SessionID), string(o.TurnID), limitID)
		payload := map[string]any{
			"limit_id":     limitID,
			"used_percent": w.UsedPercent,
		}
		if w.WindowDurationMins != nil {
			payload["window_minutes"] = *w.WindowDurationMins
		}
		if w.ResetsAt != nil {
			payload["resets_at"] = time.Unix(*w.ResetsAt, 0).UTC().Format(time.RFC3339Nano)
		}
		if rl.PlanType != nil && *rl.PlanType != "" {
			payload["plan_type"] = *rl.PlanType
		}
		ev.Payload = payload
		events = append(events, ev)
	}
	emit("primary", rl.Primary)
	emit("secondary", rl.Secondary)
	return events
}

// stampAppServerScope applies the managed-run scope columns every event
// of one App Server run shares (stampManagedExecScope, mirrored).
func (n *Normalizer) stampAppServerScope(ev *v1.Event, o AppServerRunOutcome) {
	ev.Source = string(domain.SourceProviderEvent)
	ev.TurnID = string(o.TurnID)
	ev.WorktreeID = string(o.WorktreeID)
	if o.TaskID != nil {
		ev.TaskID = string(*o.TaskID)
	}
}
