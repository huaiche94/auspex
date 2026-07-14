// managedrun.go: normalization of one managed one-shot run's terminal
// outcome (`auspex run`, issue #8 — ADD §8.1's `claude -p --output-format
// stream-json` path) into the frozen pkg/protocol/v1.Event envelope. This
// lives HERE, not in internal/managed, because this package's own doc
// comment freezes the discipline that it is the sole path from Claude
// provider payloads into the wire event protocol — internal/managed
// parses the stream-json lines into the privacy-safe ManagedRunOutcome
// below (its own intermediate struct, mirroring how internal/providers/
// claude and internal/hooks/claude own their parse steps) and hands it to
// this Normalizer for envelope/idempotency-key construction.
//
// # Event shapes (ADD §8.7 "Claude managed stream-json": exact completed
// usage — yes)
//
// One run yields one terminal turn event — provider.turn.completed, or
// provider.turn.failed when the process exit code was non-zero, the
// result line reported is_error, or the spawn itself failed — plus, when
// the stream's `result` line actually carried usage figures, one
// provider.usage.observed event. Unlike the statusline usage event
// (cumulative session totals at an arbitrary snapshot instant, no turn
// linkage), the managed usage event is EXACTLY one turn's cost/duration/
// token figures as reported by the provider's own result line, so it is
// stamped with the run's TurnID — this is the "exact outcome attribution"
// capability ADD §8.1 lists for managed mode, and the reason its
// idempotency key is turn-scoped rather than timestamp-scoped: a
// re-delivered persist of the same run's outcome is the same observation,
// not a new one.
//
// # Privacy (Constitution §7 rule 2)
//
// The result line's `result` text never reaches this file — internal/
// managed retains only its byte length (ResultTextLen), the same
// length-only discipline NormalizeStopFailure applies to provider error
// messages. Absent measurements stay absent (nil pointers -> omitted
// payload keys): unknown is not zero, exactly as everywhere else in this
// package.
package claude

import (
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// ManagedRunOutcome is the privacy-safe summary of one finished (or
// failed-to-spawn) managed one-shot provider run. All measured fields
// follow the pointer nil-means-unknown rule; ExitCode is always known for
// a process that ran (0 is a genuine observation, not a default) and is
// -1 when the process could not be run/waited to completion, matching
// internal/gitx.ExecRunner's convention for the same situation.
type ManagedRunOutcome struct {
	SessionID  domain.SessionID
	TurnID     domain.TurnID
	WorktreeID domain.WorktreeID
	// TaskID is the caller-declared task this run belongs to (`auspex run
	// --task-id`), nil when the caller declared none — event correlation
	// (issue #1) may still fill the persisted column when it resolves one.
	TaskID *domain.TaskID

	ExitCode int
	// SpawnFailed marks a run whose provider process never started
	// (binary not found, permission denied). Recorded explicitly so the
	// turn.failed event is honest about WHAT failed: no provider work
	// happened, as opposed to a provider run that started and exited
	// non-zero.
	SpawnFailed bool

	// ResultSeen is true when the stream contained a `result` line at
	// all; the fields below it are only meaningful in that case. A stream
	// with no result line (provider crashed mid-stream) observes nothing
	// about cost/duration — no usage event is fabricated from it.
	ResultSeen    bool
	ResultSubtype string
	IsError       *bool
	DurationMs    *int64
	DurationAPIMs *int64
	NumTurns      *int64
	TotalCostUSD  *float64
	// ResultTextLen is the byte length of the result line's `result`
	// text; the text itself is never retained (see the file doc comment).
	// nil when the result line carried no `result` field at all — a
	// present-but-empty result is a genuine 0.
	ResultTextLen *int

	// The result line's per-turn token counters (issue #11: the LAST
	// capture gap — the actual tokens one turn consumed). Observed on
	// claude CLI 2.1.201's result-line `usage` object (internal/managed/
	// testdata/stream_recorded_hi.jsonl); each is nil when the usage
	// object omitted it, or when the result line carried no usage object
	// at all (older CLI) — unknown is not zero, as everywhere else here.
	InputTokens              *int64
	OutputTokens             *int64
	CacheReadInputTokens     *int64
	CacheCreationInputTokens *int64

	// ModelID is the model the stream's system init line declared for the
	// run, "" when the stream never carried one — this field must never
	// be guessed or defaulted. Stamped onto the usage event so ADR-047's
	// cohort ladder can family-label the token sample, exactly like the
	// statusline usage snapshot's model_id (#20 Phase 1).
	ModelID string
}

// Failed reports whether this outcome normalizes to provider.turn.failed
// rather than provider.turn.completed: the spawn failed, the process
// exited non-zero, or the provider's own result line said is_error.
func (o ManagedRunOutcome) Failed() bool {
	return o.SpawnFailed || o.ExitCode != 0 || (o.IsError != nil && *o.IsError)
}

// NormalizeManagedRun projects a managed run's terminal outcome into one
// provider.turn.completed/failed event plus, when the result line carried
// usage figures, one provider.usage.observed event (see the file doc
// comment). Source is domain.SourceProviderEvent on every event: the
// figures come from the provider's own structured stream, not from a hook
// payload or a statusline snapshot. observedAt is the wall-clock time the
// managed runner observed the process exit.
func (n *Normalizer) NormalizeManagedRun(o ManagedRunOutcome, observedAt time.Time) []v1.Event {
	eventType := v1.EventProviderTurnCompleted
	if o.Failed() {
		eventType = v1.EventProviderTurnFailed
	}

	ev := n.envelope(eventType, observedAt, o.SessionID)
	n.stampManagedScope(&ev, o)
	// Turn-scoped idempotency key (not timestamp-scoped like the hook
	// Stop key): one managed run mints one TurnID and has exactly one
	// terminal outcome, so a re-delivered persist of the same outcome
	// must dedupe rather than duplicate.
	ev.IdempotencyKey = digestKey("managed.turn", string(o.SessionID), string(o.TurnID))

	payload := map[string]any{
		"exit_code":   o.ExitCode,
		"result_seen": o.ResultSeen,
	}
	if o.SpawnFailed {
		payload["spawn_failed"] = true
	}
	if o.ResultSeen && o.ResultSubtype != "" {
		payload["result_subtype"] = o.ResultSubtype
	}
	if o.IsError != nil {
		payload["is_error"] = *o.IsError
	}
	if o.NumTurns != nil {
		payload["num_turns"] = *o.NumTurns
	}
	if o.ResultTextLen != nil {
		payload["result_text_len"] = *o.ResultTextLen
	}
	ev.Payload = payload

	events := []v1.Event{ev}
	if usage, ok := n.managedUsageEvent(o, observedAt); ok {
		events = append(events, usage)
	}
	return events
}

// managedUsageEvent builds the turn-exact provider.usage.observed event
// when the result line actually measured something (ADD §22.10 / this
// repo's "unknown is not zero" rule: a stream with no result line, or a
// result line with no usage fields, must not synthesize an event that
// claims to observe usage).
func (n *Normalizer) managedUsageEvent(o ManagedRunOutcome, observedAt time.Time) (v1.Event, bool) {
	if !o.ResultSeen {
		return v1.Event{}, false
	}
	if o.TotalCostUSD == nil && o.DurationMs == nil && o.DurationAPIMs == nil && o.NumTurns == nil &&
		o.InputTokens == nil && o.OutputTokens == nil &&
		o.CacheReadInputTokens == nil && o.CacheCreationInputTokens == nil {
		return v1.Event{}, false
	}

	ev := n.envelope(v1.EventProviderUsageObserved, observedAt, o.SessionID)
	n.stampManagedScope(&ev, o)
	ev.IdempotencyKey = digestKey("managed.usage", string(o.SessionID), string(o.TurnID))

	// Payload keys match the statusline usage event's naming
	// (total_cost_usd/total_duration_ms/total_api_duration_ms) so usage
	// readers need no per-source key mapping; num_turns is managed-only
	// (the statusline never reports it).
	payload := map[string]any{}
	if o.TotalCostUSD != nil {
		payload["total_cost_usd"] = *o.TotalCostUSD
	}
	if o.DurationMs != nil {
		payload["total_duration_ms"] = *o.DurationMs
	}
	if o.DurationAPIMs != nil {
		payload["total_api_duration_ms"] = *o.DurationAPIMs
	}
	if o.NumTurns != nil {
		payload["num_turns"] = *o.NumTurns
	}

	// Per-turn token counters (issue #11): the raw components verbatim,
	// under the provider's own field names, so research can re-derive any
	// aggregate later without re-capturing. Absent counters stamp nothing.
	if o.InputTokens != nil {
		payload["input_tokens"] = *o.InputTokens
	}
	if o.OutputTokens != nil {
		payload["output_tokens"] = *o.OutputTokens
	}
	if o.CacheReadInputTokens != nil {
		payload["cache_read_input_tokens"] = *o.CacheReadInputTokens
	}
	if o.CacheCreationInputTokens != nil {
		payload["cache_creation_input_tokens"] = *o.CacheCreationInputTokens
	}
	// total_tokens is THE per-turn sample ADR-047's cohort ladder
	// (evaluation.RecentSimilarTurnTokens) and the token forecaster's
	// P50/P80/P90 calibration read — defined here, deliberately, as
	// input_tokens + output_tokens ONLY. Rationale: cache reads (and
	// cache creation) are context-window traffic — tokens replayed from
	// or written to the prompt cache, dominated by session history the
	// turn did not choose — while input+output is the turn's own fresh
	// work volume, the quantity the forecast is trying to predict. This
	// sum choice is REVISITABLE (a future calibration pass may show the
	// cache components carry predictive signal worth folding in); the raw
	// components are persisted alongside precisely so revisiting never
	// requires re-capturing. Both components must be present — a total
	// synthesized from one known and one unknown half would be a
	// fabrication (unknown is not zero).
	if o.InputTokens != nil && o.OutputTokens != nil {
		payload["total_tokens"] = *o.InputTokens + *o.OutputTokens
	}
	// The identity label riding the measurement (never a measurement
	// itself, so it does not by itself justify emitting the event):
	// stamped at observation granularity, like the statusline snapshot's
	// model_id, so cohort membership survives mid-session /model switches.
	if o.ModelID != "" {
		payload["model_id"] = o.ModelID
	}
	ev.Payload = payload
	return ev, true
}

// stampManagedScope applies the managed-run scope columns every event of
// one run shares: Source (provider's own structured stream), the run's
// TurnID (the whole point of managed attribution — every event of the run
// joins on it), the caller-declared WorktreeID, and TaskID when declared.
func (n *Normalizer) stampManagedScope(ev *v1.Event, o ManagedRunOutcome) {
	ev.Source = string(domain.SourceProviderEvent)
	ev.TurnID = string(o.TurnID)
	ev.WorktreeID = string(o.WorktreeID)
	if o.TaskID != nil {
		ev.TaskID = string(*o.TaskID)
	}
}
