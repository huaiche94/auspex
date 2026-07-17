// rolloutscan_test.go: unit coverage for the issue-#92 watcher's line
// decoder + normalizer wrapper. The load-bearing assertions are (1) the
// dedupe proof — NormalizeRolloutTurnComplete's idempotency keys are
// byte-identical to NormalizeStop's for the same session/turn — and (2)
// the privacy pin — content-bearing rollout lines decode to nothing.
package codex

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	codexhooks "github.com/huaiche94/auspex/internal/hooks/codex"
)

func TestDecodeRolloutLine_SessionMeta_StringSource(t *testing.T) {
	line := []byte(`{"timestamp":"2026-07-14T09:00:00.000Z","type":"session_meta","payload":{"id":"019f-meta","cwd":"/home/dev/p","originator":"codex-tui","cli_version":"0.144.4","source":"cli","thread_source":"user"}}`)
	rl, ok := DecodeRolloutLine(line)
	if !ok || rl.Meta == nil {
		t.Fatalf("DecodeRolloutLine = (%+v, %v), want a Meta line", rl, ok)
	}
	if rl.Meta.SessionID != "019f-meta" || rl.Meta.Originator != "codex-tui" || rl.Meta.CWD != "/home/dev/p" {
		t.Errorf("Meta = %+v", rl.Meta)
	}
	if rl.Meta.Surface != "cli" {
		t.Errorf("Surface = %q, want cli", rl.Meta.Surface)
	}
	if rl.Meta.ParentSessionID != "" {
		t.Errorf("ParentSessionID = %q, want empty for a top-level session", rl.Meta.ParentSessionID)
	}
	if want := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC); !rl.Timestamp.Equal(want) {
		t.Errorf("Timestamp = %v, want %v", rl.Timestamp, want)
	}
}

func TestDecodeRolloutLine_SessionMeta_SubagentObjectSource(t *testing.T) {
	// Shape observed on real 0.144.0-alpha.4 subagent rollouts: source is
	// an object carrying the spawn linkage; agent_nickname/agent_path are
	// present on the wire and must NOT surface in the projection.
	line := []byte(`{"timestamp":"2026-07-14T11:00:00.000Z","type":"session_meta","payload":{"id":"019f-sub","cwd":"/home/dev/p","originator":"codex_vscode","source":{"subagent":{"thread_spawn":{"parent_thread_id":"019f-parent","depth":1,"agent_nickname":"Jason","agent_path":"/root/requirements_gap"}}},"thread_source":"subagent"}}`)
	rl, ok := DecodeRolloutLine(line)
	if !ok || rl.Meta == nil {
		t.Fatalf("DecodeRolloutLine = (%+v, %v), want a Meta line", rl, ok)
	}
	if rl.Meta.Surface != "subagent" {
		t.Errorf("Surface = %q, want subagent", rl.Meta.Surface)
	}
	if rl.Meta.ParentSessionID != "019f-parent" {
		t.Errorf("ParentSessionID = %q, want the thread_spawn parent", rl.Meta.ParentSessionID)
	}
	if dump := fmt.Sprintf("%#v", rl.Meta); strings.Contains(dump, "Jason") || strings.Contains(dump, "requirements_gap") {
		t.Errorf("agent nickname/path leaked into the projection: %s", dump)
	}
}

func TestDecodeRolloutLine_SessionMeta_TopLevelParentWins(t *testing.T) {
	line := []byte(`{"timestamp":"2026-07-14T11:00:00.000Z","type":"session_meta","payload":{"id":"019f-sub","parent_thread_id":"019f-top","source":{"subagent":{"thread_spawn":{"parent_thread_id":"019f-nested"}}}}}`)
	rl, ok := DecodeRolloutLine(line)
	if !ok || rl.Meta == nil {
		t.Fatal("want a Meta line")
	}
	if rl.Meta.ParentSessionID != "019f-top" {
		t.Errorf("ParentSessionID = %q, want the top-level parent_thread_id", rl.Meta.ParentSessionID)
	}
}

func TestDecodeRolloutLine_SurfaceFromOriginatorFallback(t *testing.T) {
	cases := []struct{ originator, want string }{
		{"codex_vscode", "vscode"},
		{"codex-tui", "cli"},
		{"codex_cli_rs", "cli"},
		{"codex_exec", "cli"},
		{"some_future_client", ""},
	}
	for _, tc := range cases {
		line := []byte(`{"type":"session_meta","payload":{"id":"019f-x","originator":"` + tc.originator + `"}}`)
		rl, ok := DecodeRolloutLine(line)
		if !ok || rl.Meta == nil {
			t.Fatalf("%s: want a Meta line", tc.originator)
		}
		if rl.Meta.Surface != tc.want {
			t.Errorf("originator %q: Surface = %q, want %q", tc.originator, rl.Meta.Surface, tc.want)
		}
	}
}

func TestDecodeRolloutLine_TaskEvents(t *testing.T) {
	started, ok := DecodeRolloutLine([]byte(`{"timestamp":"2026-07-14T09:00:02.000Z","type":"event_msg","payload":{"type":"task_started","turn_id":"019f-turn","started_at":1783804802,"model_context_window":353400}}`))
	if !ok || started.TaskStarted == nil || started.TaskStarted.TurnID != "019f-turn" {
		t.Fatalf("task_started = (%+v, %v)", started, ok)
	}
	complete, ok := DecodeRolloutLine([]byte(`{"timestamp":"2026-07-14T09:01:05.000Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"019f-turn","completed_at":1783804865,"last_agent_message":"SECRET final prose"}}`))
	if !ok || complete.TaskComplete == nil || complete.TaskComplete.TurnID != "019f-turn" {
		t.Fatalf("task_complete = (%+v, %v)", complete, ok)
	}
	if dump := fmt.Sprintf("%#v", complete); strings.Contains(dump, "SECRET") {
		t.Errorf("last_agent_message leaked into the projection: %s", dump)
	}
}

func TestDecodeRolloutLine_TokenCountAndTurnContext(t *testing.T) {
	tc, ok := DecodeRolloutLine([]byte(`{"timestamp":"2026-07-14T09:01:00.000Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":12000,"cached_input_tokens":8000,"output_tokens":500,"reasoning_output_tokens":100,"total_tokens":12500},"model_context_window":353400},"rate_limits":{"primary":{"used_percent":10.5,"window_minutes":300,"resets_at":1784120400},"plan_type":"pro"}}}`))
	if !ok || tc.TokenCount == nil {
		t.Fatalf("token_count = (%+v, %v)", tc, ok)
	}
	if tc.TokenCount.Last == nil || tc.TokenCount.Last.InputTokens == nil || *tc.TokenCount.Last.InputTokens != 12000 {
		t.Errorf("TokenCount.Last = %+v", tc.TokenCount.Last)
	}
	if len(tc.TokenCount.RateLimits) != 1 || tc.TokenCount.RateLimits[0].LimitID != "primary" {
		t.Errorf("RateLimits = %+v", tc.TokenCount.RateLimits)
	}

	ctxLine, ok := DecodeRolloutLine([]byte(`{"timestamp":"2026-07-14T09:00:01.000Z","type":"turn_context","payload":{"cwd":"/home/dev/p","model":"gpt-5.2-codex","effort":"medium"}}`))
	if !ok || ctxLine.TurnContext == nil || ctxLine.TurnContext.Model != "gpt-5.2-codex" {
		t.Fatalf("turn_context = (%+v, %v)", ctxLine, ok)
	}
}

func TestDecodeRolloutLine_ContentAndGarbageLinesContributeNothing(t *testing.T) {
	lines := [][]byte{
		[]byte(`{"timestamp":"2026-07-14T09:00:03.000Z","type":"event_msg","payload":{"type":"user_message","message":"SECRET user prose","kind":"plain"}}`),
		[]byte(`{"timestamp":"2026-07-14T09:00:04.000Z","type":"event_msg","payload":{"type":"agent_message","message":"SECRET agent prose"}}`),
		[]byte(`{"timestamp":"2026-07-14T09:00:05.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"SECRET response prose"}]}}`),
		[]byte(`{"timestamp":"2026-07-14T09:00:06.000Z","type":"world_state","payload":{}}`),
		[]byte(`not json at all`),
		[]byte(`{"type":"event_msg","payload":{"type":"some_future_kind"}}`),
	}
	for i, line := range lines {
		if rl, ok := DecodeRolloutLine(line); ok {
			t.Errorf("line %d: DecodeRolloutLine = (%+v, true), want ok=false", i, rl)
		}
	}
}

// TestNormalizeRolloutTurnComplete_KeysMatchHookStopPath is the unit half
// of the issue-#92 dedupe proof: for the same session id + turn id, the
// watcher wrapper and the hook path's NormalizeStop produce IDENTICAL
// idempotency keys for every event in the terminal set — even at
// different observation instants and with different attribution — so the
// store's UNIQUE index collapses double capture by construction.
func TestNormalizeRolloutTurnComplete_KeysMatchHookStopPath(t *testing.T) {
	snap := normalSnapshot(t)
	stop := codexhooks.StopEvent{
		SessionID: "019f-session",
		TurnID:    "019f-turn",
	}

	hookNorm, hookClock := newTestNormalizer()
	hookEvents := hookNorm.NormalizeStop(stop, hookClock.Now(), snap)

	watchNorm := NewNormalizer(fixedClock{t: time.Date(2026, 7, 15, 3, 4, 5, 0, time.UTC)}, &seqIDs{n: 100})
	completedAt := time.Date(2026, 7, 14, 9, 1, 5, 0, time.UTC) // the task_complete line's own timestamp
	watchEvents := watchNorm.NormalizeRolloutTurnComplete(stop, completedAt, snap, RolloutAttribution{
		Originator: "codex_vscode", Surface: "subagent", ParentSessionID: "019f-parent",
	})

	if len(hookEvents) != len(watchEvents) {
		t.Fatalf("event counts differ: hook %d, watcher %d", len(hookEvents), len(watchEvents))
	}
	for i := range hookEvents {
		if hookEvents[i].EventType != watchEvents[i].EventType {
			t.Errorf("event %d: type %q vs %q", i, hookEvents[i].EventType, watchEvents[i].EventType)
		}
		if hookEvents[i].IdempotencyKey == "" {
			t.Fatalf("event %d: hook path produced an empty idempotency key", i)
		}
		if hookEvents[i].IdempotencyKey != watchEvents[i].IdempotencyKey {
			t.Errorf("event %d (%s): keys differ — hook %q, watcher %q (dedupe-by-construction broken)",
				i, hookEvents[i].EventType, hookEvents[i].IdempotencyKey, watchEvents[i].IdempotencyKey)
		}
	}
}

func TestNormalizeRolloutTurnComplete_SourceAndAttribution(t *testing.T) {
	n, _ := newTestNormalizer()
	stop := codexhooks.StopEvent{SessionID: "019f-session", TurnID: "019f-turn"}
	completedAt := time.Date(2026, 7, 14, 9, 1, 5, 0, time.UTC)
	events := n.NormalizeRolloutTurnComplete(stop, completedAt, normalSnapshot(t), RolloutAttribution{
		Originator: "codex_vscode", Surface: "subagent", ParentSessionID: "019f-parent",
	})
	if len(events) == 0 {
		t.Fatal("no events")
	}
	for _, ev := range events {
		if ev.Source != string(domain.SourceProviderEvent) {
			t.Errorf("%s: Source = %q, want %q (a rollout line, not a hook delivery)", ev.EventType, ev.Source, domain.SourceProviderEvent)
		}
		if got := ev.Payload["originator"]; got != "codex_vscode" {
			t.Errorf("%s: payload originator = %v", ev.EventType, got)
		}
		if got := ev.Payload["surface"]; got != "subagent" {
			t.Errorf("%s: payload surface = %v", ev.EventType, got)
		}
		if got := ev.Payload["parent_session_id"]; got != "019f-parent" {
			t.Errorf("%s: payload parent_session_id = %v", ev.EventType, got)
		}
		if !ev.OccurredAt.Equal(completedAt) {
			t.Errorf("%s: OccurredAt = %v, want the task_complete line's timestamp %v", ev.EventType, ev.OccurredAt, completedAt)
		}
	}
}

func TestNormalizeRolloutTurnComplete_EmptyAttributionAddsNoKeys(t *testing.T) {
	n, _ := newTestNormalizer()
	stop := codexhooks.StopEvent{SessionID: "019f-session", TurnID: "019f-turn"}
	events := n.NormalizeRolloutTurnComplete(stop, time.Date(2026, 7, 14, 9, 1, 5, 0, time.UTC), nil, RolloutAttribution{})
	for _, ev := range events {
		for _, key := range []string{"originator", "surface", "parent_session_id"} {
			if _, present := ev.Payload[key]; present {
				t.Errorf("%s: payload carries %q despite empty attribution (unknown is not zero)", ev.EventType, key)
			}
		}
	}
}

// TestNormalizeRolloutTurnComplete_NoTurnID_TimestampRefIsDeterministic
// pins the legacy-rollout fallback (task events without turn_id): the key
// derives from the task_complete LINE's timestamp — file content — so a
// restart's re-scan reproduces it exactly, while two genuinely different
// completions never collide.
func TestNormalizeRolloutTurnComplete_NoTurnID_TimestampRefIsDeterministic(t *testing.T) {
	stop := codexhooks.StopEvent{SessionID: "019f-session"} // no TurnID
	completedAt := time.Date(2026, 7, 14, 9, 1, 31, 0, time.UTC)

	n1 := NewNormalizer(fixedClock{t: time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)}, &seqIDs{})
	n2 := NewNormalizer(fixedClock{t: time.Date(2026, 7, 16, 22, 0, 0, 0, time.UTC)}, &seqIDs{n: 50}) // "after restart"

	ev1 := n1.NormalizeRolloutTurnComplete(stop, completedAt, nil, RolloutAttribution{})
	ev2 := n2.NormalizeRolloutTurnComplete(stop, completedAt, nil, RolloutAttribution{})
	if ev1[0].IdempotencyKey != ev2[0].IdempotencyKey {
		t.Error("same line re-scanned after restart produced a different key — restart would duplicate rows")
	}

	ev3 := n1.NormalizeRolloutTurnComplete(stop, completedAt.Add(time.Minute), nil, RolloutAttribution{})
	if ev1[0].IdempotencyKey == ev3[0].IdempotencyKey {
		t.Error("two different completions collided on one key")
	}
}
