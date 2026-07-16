// managedexec_test.go: unit coverage for NormalizeManagedExec (issue #9
// M7 Phase 1) — the event mapping, the shared token-key vocabulary under
// codex's differing raw semantics, the unknown-is-not-zero gaps, and the
// turn-scoped idempotency contract (re-delivery dedupes durably, via the
// same provider-agnostic claude EventStore the rest of this package
// persists through).
package codex

import (
	"context"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// (i64 comes from rolloutusage_test.go — same package.)

// normalExecOutcome mirrors what internal/managed's parser produces from
// testdata/provider-events/codex/exec/normal.jsonl plus a clean exit.
func normalExecOutcome() ManagedExecOutcome {
	task := domain.TaskID("task-exec-1")
	return ManagedExecOutcome{
		SessionID:         "sess-exec-1",
		TurnID:            "turn-exec-1",
		WorktreeID:        "wt-exec-1",
		TaskID:            &task,
		ExitCode:          0,
		ThreadStartedSeen: true,
		ThreadID:          "019f0000-3333-7aaa-8bbb-ccccdddd0201",
		TurnCompletedSeen: true,
		Usage: &TokenUsage{
			InputTokens:           i64(4200),
			CachedInputTokens:     i64(3072),
			OutputTokens:          i64(180),
			ReasoningOutputTokens: i64(64),
			TotalTokens:           i64(4380),
		},
	}
}

func TestNormalizeManagedExec_HappyPath_SessionTurnUsage(t *testing.T) {
	n, clock := newTestNormalizer()
	events := n.NormalizeManagedExec(normalExecOutcome(), clock.Now())

	if len(events) != 3 {
		t.Fatalf("len(events) = %d, want 3 (session.started + turn.completed + usage.observed)", len(events))
	}

	started, terminal, usage := events[0], events[1], events[2]
	if started.EventType != v1.EventProviderSessionStarted {
		t.Errorf("events[0] = %q, want provider.session.started (thread.started mapping)", started.EventType)
	}
	if started.Payload["thread_id"] != "019f0000-3333-7aaa-8bbb-ccccdddd0201" {
		t.Errorf("session.started payload = %+v, want the provider's thread_id", started.Payload)
	}
	if terminal.EventType != v1.EventProviderTurnCompleted {
		t.Errorf("events[1] = %q, want provider.turn.completed", terminal.EventType)
	}
	if terminal.Payload["exit_code"] != 0 || terminal.Payload["turn_completed_seen"] != true {
		t.Errorf("turn.completed payload = %+v, want exit_code 0 / turn_completed_seen true", terminal.Payload)
	}
	if usage.EventType != v1.EventProviderUsageObserved {
		t.Errorf("events[2] = %q, want provider.usage.observed", usage.EventType)
	}
	// The frozen shared vocabulary with codex semantics NORMALIZED:
	// fresh input = 4200-3072, cache read = 3072, total = fresh+output —
	// deliberately not codex's own cached-inclusive total_tokens (4380).
	if usage.Payload["input_tokens"] != int64(1128) ||
		usage.Payload["cache_read_input_tokens"] != int64(3072) ||
		usage.Payload["output_tokens"] != int64(180) ||
		usage.Payload["reasoning_output_tokens"] != int64(64) ||
		usage.Payload["total_tokens"] != int64(1308) {
		t.Errorf("usage payload = %+v, want 1128/3072/180/64/1308", usage.Payload)
	}
	if _, hasModel := usage.Payload["model_id"]; hasModel {
		t.Error("usage payload carries model_id — the exec stream declares no model, nothing may be fabricated")
	}

	// Scope stamping shared by every event of the run.
	for i, ev := range events {
		if ev.Provider != Provider {
			t.Errorf("events[%d].Provider = %q, want codex", i, ev.Provider)
		}
		if ev.Source != string(domain.SourceProviderEvent) {
			t.Errorf("events[%d].Source = %q, want provider_event", i, ev.Source)
		}
		if ev.TurnID != "turn-exec-1" || ev.WorktreeID != "wt-exec-1" || ev.TaskID != "task-exec-1" {
			t.Errorf("events[%d] scope = turn %q worktree %q task %q, want the run's own", i, ev.TurnID, ev.WorktreeID, ev.TaskID)
		}
		if ev.SessionID != "sess-exec-1" || ev.IdempotencyKey == "" {
			t.Errorf("events[%d] session/key = %q/%q", i, ev.SessionID, ev.IdempotencyKey)
		}
	}
}

func TestNormalizeManagedExec_Failure_TurnFailed(t *testing.T) {
	msgLen := 30
	o := ManagedExecOutcome{
		SessionID:         "sess-exec-2",
		TurnID:            "turn-exec-2",
		WorktreeID:        "wt-exec-2",
		ExitCode:          1,
		ThreadStartedSeen: true,
		ThreadID:          "019f0000-3333-7aaa-8bbb-ccccdddd0202",
		TurnFailedSeen:    true,
		ErrorEvents:       1,
		FailureMessageLen: &msgLen,
	}
	n, clock := newTestNormalizer()
	events := n.NormalizeManagedExec(o, clock.Now())

	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2 (session.started + turn.failed; no usage was measured)", len(events))
	}
	failed := events[1]
	if failed.EventType != v1.EventProviderTurnFailed {
		t.Fatalf("events[1] = %q, want provider.turn.failed", failed.EventType)
	}
	if failed.Payload["exit_code"] != 1 || failed.Payload["turn_failed_seen"] != true ||
		failed.Payload["turn_completed_seen"] != false {
		t.Errorf("turn.failed payload = %+v", failed.Payload)
	}
	if failed.Payload["error_events"] != 1 || failed.Payload["failure_message_len"] != 30 {
		t.Errorf("turn.failed payload = %+v, want error_events 1 / failure_message_len 30", failed.Payload)
	}
}

func TestNormalizeManagedExec_FailureVerdicts(t *testing.T) {
	cases := []struct {
		name string
		o    ManagedExecOutcome
		want v1.EventType
	}{
		{"clean exit + turn.completed", ManagedExecOutcome{ExitCode: 0, TurnCompletedSeen: true}, v1.EventProviderTurnCompleted},
		{"non-zero exit", ManagedExecOutcome{ExitCode: 2, TurnCompletedSeen: true}, v1.EventProviderTurnFailed},
		{"turn.failed despite exit 0", ManagedExecOutcome{ExitCode: 0, TurnFailedSeen: true}, v1.EventProviderTurnFailed},
		{"spawn failed", ManagedExecOutcome{ExitCode: -1, SpawnFailed: true}, v1.EventProviderTurnFailed},
		// A standalone error event alone is metric, not verdict (ADD
		// §21.7 tolerance): the run still completed.
		{"error events but clean completion", ManagedExecOutcome{ExitCode: 0, TurnCompletedSeen: true, ErrorEvents: 2}, v1.EventProviderTurnCompleted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.o.SessionID, tc.o.TurnID = "sess-exec-3", "turn-exec-3"
			n, clock := newTestNormalizer()
			events := n.NormalizeManagedExec(tc.o, clock.Now())
			// None of these outcomes measures usage, so the terminal
			// event is always the last one.
			terminal := events[len(events)-1]
			if terminal.EventType != tc.want {
				t.Errorf("terminal = %q, want %q", terminal.EventType, tc.want)
			}
			if tc.o.SpawnFailed && terminal.Payload["spawn_failed"] != true {
				t.Errorf("payload = %+v, want spawn_failed true", terminal.Payload)
			}
		})
	}
}

func TestNormalizeManagedExec_UnknownIsNotZero(t *testing.T) {
	t.Run("no usage object -> no usage event", func(t *testing.T) {
		o := ManagedExecOutcome{SessionID: "s", TurnID: "t", TurnCompletedSeen: true}
		n, clock := newTestNormalizer()
		events := n.NormalizeManagedExec(o, clock.Now())
		if len(events) != 1 || events[0].EventType != v1.EventProviderTurnCompleted {
			t.Fatalf("events = %+v, want exactly one turn.completed", events)
		}
	})

	t.Run("empty usage object -> no usage event", func(t *testing.T) {
		o := ManagedExecOutcome{SessionID: "s", TurnID: "t", TurnCompletedSeen: true, Usage: &TokenUsage{}}
		n, clock := newTestNormalizer()
		if events := n.NormalizeManagedExec(o, clock.Now()); len(events) != 1 {
			t.Fatalf("events = %+v, want no usage event fabricated from an empty usage object", events)
		}
	})

	t.Run("missing cached counter -> no input/total, rest emitted", func(t *testing.T) {
		o := ManagedExecOutcome{
			SessionID: "s", TurnID: "t", TurnCompletedSeen: true,
			Usage: &TokenUsage{InputTokens: i64(900), OutputTokens: i64(50)},
		}
		n, clock := newTestNormalizer()
		events := n.NormalizeManagedExec(o, clock.Now())
		if len(events) != 2 {
			t.Fatalf("len(events) = %d, want 2", len(events))
		}
		payload := events[1].Payload
		if _, ok := payload["input_tokens"]; ok {
			t.Errorf("payload = %+v: input_tokens emitted although the cached split is unknown", payload)
		}
		if _, ok := payload["total_tokens"]; ok {
			t.Errorf("payload = %+v: total_tokens fabricated from an unknown fresh-input half", payload)
		}
		if payload["output_tokens"] != int64(50) {
			t.Errorf("payload = %+v, want output_tokens 50", payload)
		}
	})

	t.Run("no thread.started -> no session.started", func(t *testing.T) {
		o := ManagedExecOutcome{SessionID: "s", TurnID: "t", TurnCompletedSeen: true}
		n, clock := newTestNormalizer()
		for _, ev := range n.NormalizeManagedExec(o, clock.Now()) {
			if ev.EventType == v1.EventProviderSessionStarted {
				t.Error("session.started fabricated without an observed thread.started")
			}
		}
	})
}

// TestNormalizeManagedExec_Idempotency_RedeliveryDedupes pins the
// turn-scoped key contract end to end: the SAME run outcome normalized at
// two different wall-clock instants (a re-delivered persist) yields
// identical idempotency keys, and the provider-agnostic event store keeps
// exactly one row per key.
func TestNormalizeManagedExec_Idempotency_RedeliveryDedupes(t *testing.T) {
	db := openFixtureSuiteDB(t)
	store := claudetelemetry.NewEventStore(db)
	ctx := context.Background()
	o := normalExecOutcome()

	clock1 := fixedClock{t: time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC)}
	first := NewNormalizer(clock1, &seqIDs{}).NormalizeManagedExec(o, clock1.Now())
	clock2 := fixedClock{t: clock1.t.Add(7 * time.Second)}
	second := NewNormalizer(clock2, &seqIDs{n: 500}).NormalizeManagedExec(o, clock2.Now())

	if err := store.PersistAll(ctx, db, first); err != nil {
		t.Fatalf("PersistAll(first): %v", err)
	}
	if err := store.PersistAll(ctx, db, second); err != nil {
		t.Fatalf("PersistAll(second/duplicate): %v", err)
	}

	for i := range first {
		if first[i].IdempotencyKey != second[i].IdempotencyKey {
			t.Fatalf("key[%d] not stable across deliveries: %q vs %q", i, first[i].IdempotencyKey, second[i].IdempotencyKey)
		}
		count, err := store.CountByIdempotencyKey(ctx, first[i].IdempotencyKey)
		if err != nil {
			t.Fatalf("CountByIdempotencyKey[%d]: %v", i, err)
		}
		if count != 1 {
			t.Errorf("row count for event[%d] = %d, want 1 (re-run must dedupe)", i, count)
		}
	}

	// A DIFFERENT run (new TurnID) of the same session must NOT dedupe:
	// two genuinely different runs are two observations.
	o2 := o
	o2.TurnID = "turn-exec-1b"
	third := NewNormalizer(clock2, &seqIDs{n: 900}).NormalizeManagedExec(o2, clock2.Now())
	for i := range first {
		if third[i].IdempotencyKey == first[i].IdempotencyKey {
			t.Errorf("event[%d] key collides across distinct turns", i)
		}
	}
}
