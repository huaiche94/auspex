// managedexec.go: normalization of one managed `codex exec --json`
// one-shot run's terminal outcome (`auspex run --provider codex`, issue
// #9 M7 Phase 1 — ADD §21.8) into the frozen pkg/protocol/v1.Event
// envelope — the codex analog of internal/telemetry/claude/managedrun.go.
// It lives HERE, not in internal/managed, because this package's doc
// comment freezes the discipline that it is the sole path from Codex
// provider payloads into the wire event protocol — internal/managed
// parses the exec JSONL lines into its own privacy-safe summary
// (codexstream.go) and hands the projection below to this Normalizer for
// envelope/idempotency-key construction.
//
// # Event mapping (ADD §21.8's normalization list, applied to a managed
// one-shot run)
//
//	thread.started  -> provider.session.started (each `codex exec` run
//	                   starts a fresh provider thread; the event carries
//	                   codex's own thread_id as the provider-side session
//	                   locator a future `codex exec resume` would need)
//	turn.started    -> already satisfied by the gate's pre-spawn
//	                   provider.turn.started (internal/orchestrator's
//	                   EvaluateManagedPrompt — the managed pattern
//	                   persists the turn's start BEFORE the provider
//	                   exists); re-emitting from the stream would double-
//	                   count the turn, so it is counted, not normalized.
//	turn.completed  -> provider.turn.completed, plus one
//	                   provider.usage.observed from its `usage` object
//	                   when that object actually measured something
//	turn.failed     -> provider.turn.failed (error message reduced to its
//	                   byte length upstream — never text)
//	error           -> ignore + metric (ADD §21.7 tolerance): counted
//	                   onto the terminal event's payload as error_events,
//	                   never a failure verdict by itself
//	item.*          -> nothing (Phase 1): no frozen EventType cleanly
//	                   fits an item observed only at run end — the
//	                   provider.tool.* mapping for command_execution/
//	                   mcp_tool_call items is deferred, not approximated.
//	                   The closed v1.EventType taxonomy is never extended
//	                   from here (package doc).
//
// # Failure semantics
//
// The run normalizes to provider.turn.failed when the spawn failed, the
// process exited non-zero, or the stream's own turn.failed event was
// observed — the same three-way honesty split ManagedRunOutcome.Failed
// documents for claude (turn.failed standing in for the result line's
// is_error).
//
// # Privacy (Constitution §7 rule 2)
//
// No prompt, item, or error text can reach this file: internal/managed's
// parser retains only counts, ids, and the failure message's byte length.
// Absent measurements stay absent (nil pointers -> omitted payload keys):
// unknown is not zero, exactly as everywhere else in this package.
package codex

import (
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// ManagedExecOutcome is the privacy-safe summary of one finished (or
// failed-to-spawn) managed `codex exec --json` run. ExitCode follows
// internal/gitx.ExecRunner's convention (-1 when the process could not be
// run/waited to completion); all measured fields follow the pointer
// nil-means-unknown rule.
type ManagedExecOutcome struct {
	SessionID  domain.SessionID
	TurnID     domain.TurnID
	WorktreeID domain.WorktreeID
	// TaskID is the caller-declared task this run belongs to (`auspex run
	// --task-id`), nil when the caller declared none.
	TaskID *domain.TaskID

	ExitCode int
	// SpawnFailed marks a run whose provider process never started —
	// recorded explicitly so the turn.failed event is honest about WHAT
	// failed (no provider work happened), exactly like claude's
	// ManagedRunOutcome.SpawnFailed.
	SpawnFailed bool

	// ThreadStartedSeen is true when the stream carried a thread.started
	// event at all; ThreadID is that event's thread_id ("" when the event
	// omitted it — the session.started event is still emitted, with an
	// honestly empty payload, because the thread's start WAS observed).
	ThreadStartedSeen bool
	ThreadID          string

	// TurnCompletedSeen/TurnFailedSeen record which terminal stream
	// events were observed; a stream with neither (provider crashed
	// mid-stream) observes nothing about the turn's own accounting — no
	// usage event is fabricated from it.
	TurnCompletedSeen bool
	TurnFailedSeen    bool
	// ErrorEvents counts standalone `error` stream events (ignore +
	// metric, ADD §21.7).
	ErrorEvents int
	// FailureMessageLen is the byte length of turn.failed's error
	// message; the text itself never reaches this package. nil when
	// turn.failed carried no message (or was never seen).
	FailureMessageLen *int

	// Usage is turn.completed's token accounting VERBATIM under Codex's
	// own wire semantics (TokenUsage's documented contract: input_tokens
	// includes the cached portion; total_tokens is codex's own
	// cached-inclusive sum, deliberately ignored by normalization). nil
	// when turn.completed was never seen or carried no usage object.
	Usage *TokenUsage
}

// Failed reports whether this outcome normalizes to provider.turn.failed
// rather than provider.turn.completed (see the file doc comment's failure
// semantics).
func (o ManagedExecOutcome) Failed() bool {
	return o.SpawnFailed || o.ExitCode != 0 || o.TurnFailedSeen
}

// NormalizeManagedExec projects a managed exec run's terminal outcome
// into its event batch: provider.session.started when the stream's
// thread.started was observed, one provider.turn.completed/failed always,
// and one provider.usage.observed when turn.completed actually measured
// usage. Source is domain.SourceProviderEvent on every event (the figures
// come from the provider's own structured stream, not a hook payload),
// and every idempotency key is turn-scoped — one managed run mints one
// TurnID and has exactly one terminal outcome, so a re-delivered persist
// of the same run's outcome dedupes rather than duplicates (the same
// contract claude's NormalizeManagedRun documents). observedAt is the
// wall-clock time the managed runner observed the process exit; the
// stream events carry no timestamps of their own, so the observation
// instant is the honest OccurredAt for all of them.
func (n *Normalizer) NormalizeManagedExec(o ManagedExecOutcome, observedAt time.Time) []v1.Event {
	var events []v1.Event

	if o.ThreadStartedSeen {
		started := n.envelope(v1.EventProviderSessionStarted, observedAt, o.SessionID)
		n.stampManagedExecScope(&started, o)
		started.IdempotencyKey = digestKey("codex.managed.thread", string(o.SessionID), string(o.TurnID))
		payload := map[string]any{}
		if o.ThreadID != "" {
			// The provider's own thread identifier — an id, never
			// content; the locator `codex exec resume <id>` would take.
			payload["thread_id"] = o.ThreadID
		}
		started.Payload = payload
		events = append(events, started)
	}

	eventType := v1.EventProviderTurnCompleted
	if o.Failed() {
		eventType = v1.EventProviderTurnFailed
	}
	terminal := n.envelope(eventType, observedAt, o.SessionID)
	n.stampManagedExecScope(&terminal, o)
	terminal.IdempotencyKey = digestKey("codex.managed.turn", string(o.SessionID), string(o.TurnID))

	payload := map[string]any{
		"exit_code": o.ExitCode,
		// The exec analog of claude's result_seen: whether the stream's
		// own terminal success event arrived at all.
		"turn_completed_seen": o.TurnCompletedSeen,
	}
	if o.SpawnFailed {
		payload["spawn_failed"] = true
	}
	if o.TurnFailedSeen {
		payload["turn_failed_seen"] = true
	}
	if o.ErrorEvents > 0 {
		payload["error_events"] = o.ErrorEvents
	}
	if o.FailureMessageLen != nil {
		payload["failure_message_len"] = *o.FailureMessageLen
	}
	terminal.Payload = payload
	events = append(events, terminal)

	if usage, ok := n.managedExecUsageEvent(o, observedAt); ok {
		events = append(events, usage)
	}
	return events
}

// managedExecUsageEvent builds the turn-exact provider.usage.observed
// event when turn.completed actually measured something ("unknown is not
// zero": a run with no turn.completed, or a usage object with no
// counters, must not synthesize an event that claims to observe usage —
// claude's managedUsageEvent contract verbatim).
//
// Token-key vocabulary: identical to NormalizeStop's (the frozen shared
// keys, with Codex's differing raw semantics normalized rather than
// leaked):
//
//	input_tokens            = codex input_tokens - cached_input_tokens
//	                          (fresh, uncached input; NOT emitted when
//	                          the cached counter is absent — the split is
//	                          then unknown)
//	cache_read_input_tokens = codex cached_input_tokens
//	output_tokens           = codex output_tokens (includes reasoning)
//	reasoning_output_tokens = codex reasoning_output_tokens (additive,
//	                          codex-specific numeric key)
//	total_tokens            = input_tokens + output_tokens under the
//	                          frozen managedUsageEvent definition (fresh
//	                          work only) — deliberately NOT codex's own
//	                          total_tokens, which includes cached input
//
// No model_id is stamped: the exec JSONL stream declares no model, and
// this field is never guessed or defaulted (unknown is not zero).
func (n *Normalizer) managedExecUsageEvent(o ManagedExecOutcome, observedAt time.Time) (v1.Event, bool) {
	if !o.TurnCompletedSeen || o.Usage == nil {
		return v1.Event{}, false
	}
	u := o.Usage
	if u.InputTokens == nil && u.CachedInputTokens == nil &&
		u.OutputTokens == nil && u.ReasoningOutputTokens == nil {
		return v1.Event{}, false
	}

	ev := n.envelope(v1.EventProviderUsageObserved, observedAt, o.SessionID)
	n.stampManagedExecScope(&ev, o)
	ev.IdempotencyKey = digestKey("codex.managed.usage", string(o.SessionID), string(o.TurnID))

	payload := map[string]any{}
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
	ev.Payload = payload
	return ev, true
}

// stampManagedExecScope applies the managed-run scope columns every event
// of one run shares (claude's stampManagedScope, mirrored): Source
// (provider's own structured stream), the run's TurnID (every event of
// the run joins on it), the caller-declared WorktreeID, and TaskID when
// declared.
func (n *Normalizer) stampManagedExecScope(ev *v1.Event, o ManagedExecOutcome) {
	ev.Source = string(domain.SourceProviderEvent)
	ev.TurnID = string(o.TurnID)
	ev.WorktreeID = string(o.WorktreeID)
	if o.TaskID != nil {
		ev.TaskID = string(*o.TaskID)
	}
}
