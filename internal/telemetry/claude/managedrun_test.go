// managedrun_test.go: unit coverage for managedrun.go's projection of a
// managed one-shot run's terminal outcome (issue #8, ADD §8.1) into the
// frozen event envelope — reusing this package's established fixtures
// (newTestNormalizer, requireEnvelope) so the managed events are held to
// exactly the same envelope discipline as the hook/statusline ones.
package claude

import (
	"testing"

	"github.com/huaiche94/auspex/internal/domain"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

func ptrBool(b bool) *bool          { return &b }
func ptrInt(n int) *int             { return &n }
func ptrInt64(n int64) *int64       { return &n }
func ptrFloat64(f float64) *float64 { return &f }

func successfulOutcome() ManagedRunOutcome {
	taskID := domain.TaskID("task-m1")
	return ManagedRunOutcome{
		SessionID:     "sess-m1",
		TurnID:        "turn-m1",
		WorktreeID:    "wt-m1",
		TaskID:        &taskID,
		ExitCode:      0,
		ResultSeen:    true,
		ResultSubtype: "success",
		IsError:       ptrBool(false),
		DurationMs:    ptrInt64(2385),
		DurationAPIMs: ptrInt64(2181),
		NumTurns:      ptrInt64(3),
		TotalCostUSD:  ptrFloat64(0.0417),
		ResultTextLen: ptrInt(34),

		InputTokens:              ptrInt64(2100),
		OutputTokens:             ptrInt64(350),
		CacheReadInputTokens:     ptrInt64(14000),
		CacheCreationInputTokens: ptrInt64(4200),
		ModelID:                  "claude-sonnet-4-5",
	}
}

func TestNormalizeManagedRun_Success_TurnCompletedPlusUsage(t *testing.T) {
	n, clock := newTestNormalizer()
	events := n.NormalizeManagedRun(successfulOutcome(), clock.Now())

	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 (turn.completed + usage.observed)", len(events))
	}

	completed := events[0]
	requireEnvelope(t, completed, v1.EventProviderTurnCompleted, "sess-m1")
	if completed.Source != string(domain.SourceProviderEvent) {
		t.Errorf("Source = %q, want %q (the provider's own structured stream, not a hook or statusline)", completed.Source, domain.SourceProviderEvent)
	}
	if completed.TurnID != "turn-m1" || completed.WorktreeID != "wt-m1" || completed.TaskID != "task-m1" {
		t.Errorf("scope = turn %q worktree %q task %q, want turn-m1/wt-m1/task-m1", completed.TurnID, completed.WorktreeID, completed.TaskID)
	}
	if completed.Payload["exit_code"] != 0 || completed.Payload["result_seen"] != true {
		t.Errorf("payload = %+v, want exit_code 0 / result_seen true", completed.Payload)
	}
	if completed.Payload["result_text_len"] != 34 {
		t.Errorf("payload result_text_len = %v, want 34 (length only — never the text)", completed.Payload["result_text_len"])
	}
	if _, present := completed.Payload["spawn_failed"]; present {
		t.Error("payload carries spawn_failed on a successful run")
	}

	usage := events[1]
	requireEnvelope(t, usage, v1.EventProviderUsageObserved, "sess-m1")
	if usage.TurnID != "turn-m1" {
		t.Errorf("usage TurnID = %q, want turn-m1 — exact turn attribution is the point of the managed usage event", usage.TurnID)
	}
	if usage.Payload["total_cost_usd"] != 0.0417 || usage.Payload["total_duration_ms"] != int64(2385) ||
		usage.Payload["total_api_duration_ms"] != int64(2181) || usage.Payload["num_turns"] != int64(3) {
		t.Errorf("usage payload = %+v, want the result line's four figures under the statusline-compatible keys", usage.Payload)
	}
	// Issue #11: the per-turn token components verbatim, plus the derived
	// total_tokens = input + output (the documented sum choice — cache
	// traffic excluded, see managedUsageEvent) and the model label.
	if usage.Payload["input_tokens"] != int64(2100) || usage.Payload["output_tokens"] != int64(350) ||
		usage.Payload["cache_read_input_tokens"] != int64(14000) ||
		usage.Payload["cache_creation_input_tokens"] != int64(4200) {
		t.Errorf("usage payload = %+v, want the four raw token counters verbatim", usage.Payload)
	}
	if usage.Payload["total_tokens"] != int64(2450) {
		t.Errorf("usage payload total_tokens = %v, want 2450 (input 2100 + output 350, cache traffic excluded)", usage.Payload["total_tokens"])
	}
	if usage.Payload["model_id"] != "claude-sonnet-4-5" {
		t.Errorf("usage payload model_id = %v, want claude-sonnet-4-5 (the init line's declaration)", usage.Payload["model_id"])
	}
}

func TestNormalizeManagedRun_PartialTokens_NoFabricatedTotalOrModel(t *testing.T) {
	n, clock := newTestNormalizer()
	o := successfulOutcome()
	// The usage object carried only output_tokens, and the stream never
	// declared a model: total_tokens must NOT be synthesized from one
	// known and one unknown half, and model_id must not be guessed.
	o.InputTokens = nil
	o.CacheReadInputTokens = nil
	o.CacheCreationInputTokens = nil
	o.ModelID = ""
	events := n.NormalizeManagedRun(o, clock.Now())

	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 (output_tokens alone is still a real measurement)", len(events))
	}
	usage := events[1]
	if usage.Payload["output_tokens"] != int64(350) {
		t.Errorf("usage payload = %+v, want output_tokens 350 kept verbatim", usage.Payload)
	}
	for _, absent := range []string{"total_tokens", "input_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", "model_id"} {
		if v, present := usage.Payload[absent]; present {
			t.Errorf("usage payload carries %s = %v, want absent (unknown is not zero)", absent, v)
		}
	}
}

func TestNormalizeManagedRun_TokensOnlyResultLine_StillEmitsUsage(t *testing.T) {
	n, clock := newTestNormalizer()
	// A result line carrying ONLY the token block (no cost/duration/
	// num_turns) still measured something: the usage event must not be
	// suppressed by the older cost/duration-only trigger.
	o := ManagedRunOutcome{
		SessionID: "sess-m3", TurnID: "turn-m3", WorktreeID: "wt-m3",
		ExitCode: 0, ResultSeen: true, ResultSubtype: "success",
		InputTokens:  ptrInt64(5322),
		OutputTokens: ptrInt64(157),
	}
	events := n.NormalizeManagedRun(o, clock.Now())

	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 (turn.completed + usage.observed)", len(events))
	}
	usage := events[1]
	requireEnvelope(t, usage, v1.EventProviderUsageObserved, "sess-m3")
	if usage.Payload["total_tokens"] != int64(5479) {
		t.Errorf("usage payload total_tokens = %v, want 5479 (5322 + 157, the recorded probe's real figures)", usage.Payload["total_tokens"])
	}
	if _, present := usage.Payload["total_cost_usd"]; present {
		t.Error("usage payload fabricated total_cost_usd on a tokens-only result line")
	}
}

func TestNormalizeManagedRun_FailureClassification(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ManagedRunOutcome)
	}{
		{"non-zero exit", func(o *ManagedRunOutcome) { o.ExitCode = 1 }},
		{"result is_error", func(o *ManagedRunOutcome) { o.IsError = ptrBool(true) }},
		{"spawn failed", func(o *ManagedRunOutcome) { o.SpawnFailed = true; o.ExitCode = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, clock := newTestNormalizer()
			o := successfulOutcome()
			tc.mutate(&o)
			events := n.NormalizeManagedRun(o, clock.Now())
			if events[0].EventType != v1.EventProviderTurnFailed {
				t.Errorf("EventType = %q, want %q", events[0].EventType, v1.EventProviderTurnFailed)
			}
		})
	}
}

func TestNormalizeManagedRun_NoResultLine_NoUsageEvent(t *testing.T) {
	n, clock := newTestNormalizer()
	o := ManagedRunOutcome{
		SessionID: "sess-m2", TurnID: "turn-m2", WorktreeID: "wt-m2",
		ExitCode: 1, // crashed mid-stream: no result line was ever seen
	}
	events := n.NormalizeManagedRun(o, clock.Now())

	if len(events) != 1 {
		t.Fatalf("events = %d, want 1 — a stream with no result line observes NO usage (unknown is not zero)", len(events))
	}
	requireEnvelope(t, events[0], v1.EventProviderTurnFailed, "sess-m2")
	if events[0].Payload["result_seen"] != false {
		t.Errorf("payload = %+v, want result_seen false", events[0].Payload)
	}
	if _, present := events[0].Payload["result_text_len"]; present {
		t.Error("payload carries result_text_len with no result line")
	}
	if events[0].TaskID != "" {
		t.Errorf("TaskID = %q, want empty when the caller declared none", events[0].TaskID)
	}
}

func TestNormalizeManagedRun_IdempotencyKeys_TurnScopedAndDistinct(t *testing.T) {
	n1, clock := newTestNormalizer()
	first := n1.NormalizeManagedRun(successfulOutcome(), clock.Now())
	n2, _ := newTestNormalizer()
	second := n2.NormalizeManagedRun(successfulOutcome(), clock.Now())

	// Same run outcome re-normalized -> same keys (a re-delivered persist
	// of one run's terminal outcome must dedupe, not duplicate) …
	if first[0].IdempotencyKey == "" || first[0].IdempotencyKey != second[0].IdempotencyKey {
		t.Errorf("turn event keys differ across re-normalization: %q vs %q", first[0].IdempotencyKey, second[0].IdempotencyKey)
	}
	if first[1].IdempotencyKey == "" || first[1].IdempotencyKey != second[1].IdempotencyKey {
		t.Errorf("usage event keys differ across re-normalization: %q vs %q", first[1].IdempotencyKey, second[1].IdempotencyKey)
	}
	// … while the two events of one run never collide with each other.
	if first[0].IdempotencyKey == first[1].IdempotencyKey {
		t.Error("turn and usage events share one idempotency key")
	}

	// A different turn of the same session is a different observation.
	otherTurn := successfulOutcome()
	otherTurn.TurnID = "turn-m1b"
	n3, _ := newTestNormalizer()
	third := n3.NormalizeManagedRun(otherTurn, clock.Now())
	if third[0].IdempotencyKey == first[0].IdempotencyKey {
		t.Error("distinct turns produced identical turn-event idempotency keys")
	}
}
