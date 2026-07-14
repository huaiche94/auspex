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
