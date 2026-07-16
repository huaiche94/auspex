package codex

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	codexhooks "github.com/huaiche94/auspex/internal/hooks/codex"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// fixedClock is the deterministic domain.Clock fake, mirroring
// internal/telemetry/claude's test helpers.
type fixedClock struct{ t time.Time }

func (f fixedClock) Now() time.Time { return f.t }

// seqIDs is a deterministic domain.IDGenerator fake.
type seqIDs struct{ n int }

func (s *seqIDs) NewID() string {
	s.n++
	return "id-" + strconv.Itoa(s.n)
}

func fixture(t *testing.T, dir, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "provider-events", "codex", dir, name))
	if err != nil {
		t.Fatalf("reading fixture %s/%s: %v", dir, name, err)
	}
	return b
}

func newTestNormalizer() (*Normalizer, fixedClock) {
	clock := fixedClock{t: time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)}
	return NewNormalizer(clock, &seqIDs{}), clock
}

func normalSnapshot(t *testing.T) *RolloutSnapshot {
	t.Helper()
	snap, ok := ReadRolloutSnapshot(rolloutFixturePath(t, "normal.jsonl"))
	if !ok {
		t.Fatal("reading normal.jsonl rollout fixture failed")
	}
	return &snap
}

// --- SessionStart -----------------------------------------------------------

func TestNormalizeSessionStart_SourceMapping(t *testing.T) {
	cases := []struct {
		file string
		want v1.EventType
	}{
		{"normal.json", v1.EventProviderSessionStarted},
		{"resume.json", v1.EventProviderSessionResumed},
		{"compact.json", v1.EventProviderSessionCompacted},
		{"missing_fields.json", v1.EventProviderSessionStarted}, // absent source degrades to started
	}
	for _, tc := range cases {
		n, clock := newTestNormalizer()
		parsed, err := codexhooks.ParseSessionStart(fixture(t, "sessionstart", tc.file))
		if err != nil {
			t.Fatalf("ParseSessionStart(%s): %v", tc.file, err)
		}
		ev := n.NormalizeSessionStart(parsed, clock.Now())
		if ev.EventType != tc.want {
			t.Errorf("%s: EventType = %q, want %q", tc.file, ev.EventType, tc.want)
		}
		if ev.Provider != Provider {
			t.Errorf("%s: Provider = %q, want codex", tc.file, ev.Provider)
		}
		if ev.SchemaVersion != v1.SchemaVersionEvent {
			t.Errorf("%s: SchemaVersion = %q", tc.file, ev.SchemaVersion)
		}
		if ev.IdempotencyKey == "" {
			t.Errorf("%s: IdempotencyKey empty", tc.file)
		}
	}
}

func TestNormalizeSessionStart_UnknownSourceDegradesToStarted(t *testing.T) {
	n, clock := newTestNormalizer()
	ev := n.NormalizeSessionStart(codexhooks.SessionStartEvent{
		SessionID: "s1",
		Source:    "hologram", // a future enum value this build has never seen
	}, clock.Now())
	if ev.EventType != v1.EventProviderSessionStarted {
		t.Errorf("EventType = %q, want provider.session.started for an unknown source", ev.EventType)
	}
	if ev.Payload["source"] != "hologram" {
		t.Errorf("payload source = %v, want the raw enum value preserved", ev.Payload["source"])
	}
}

func TestNormalizeSessionStart_PayloadFields(t *testing.T) {
	n, clock := newTestNormalizer()
	parsed, err := codexhooks.ParseSessionStart(fixture(t, "sessionstart", "normal.json"))
	if err != nil {
		t.Fatal(err)
	}
	ev := n.NormalizeSessionStart(parsed, clock.Now())
	if ev.Payload["model_id"] != "gpt-5.2-codex" {
		t.Errorf("model_id = %v", ev.Payload["model_id"])
	}
	if ev.Payload["permission_mode"] != "default" {
		t.Errorf("permission_mode = %v", ev.Payload["permission_mode"])
	}
	if ev.Payload["cwd"] != "/home/dev/projects/sample" {
		t.Errorf("cwd = %v", ev.Payload["cwd"])
	}
}

func TestNormalizeSessionStart_MissingFieldsStampNothing(t *testing.T) {
	n, clock := newTestNormalizer()
	parsed, err := codexhooks.ParseSessionStart(fixture(t, "sessionstart", "missing_fields.json"))
	if err != nil {
		t.Fatal(err)
	}
	ev := n.NormalizeSessionStart(parsed, clock.Now())
	for _, key := range []string{"model_id", "permission_mode", "cwd", "source"} {
		if _, present := ev.Payload[key]; present {
			t.Errorf("payload key %q present for an absent field (unknown is not zero)", key)
		}
	}
}

// --- UserPromptSubmit --------------------------------------------------------

func TestNormalizeUserPromptSubmit_TurnStartedWithProviderTurnID(t *testing.T) {
	n, clock := newTestNormalizer()
	parsed, err := codexhooks.ParseUserPromptSubmit(fixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatal(err)
	}
	ev := n.NormalizeUserPromptSubmit(parsed, clock.Now())
	if ev.EventType != v1.EventProviderTurnStarted {
		t.Errorf("EventType = %q", ev.EventType)
	}
	if ev.TurnID != "019f0000-2222-7aaa-8bbb-ccccdddd0101" {
		t.Errorf("TurnID = %q, want the provider's own turn_id", ev.TurnID)
	}
	if ev.Payload["prompt_sha256"] != parsed.PromptSHA256 {
		t.Errorf("prompt_sha256 = %v", ev.Payload["prompt_sha256"])
	}
	if ev.Payload["model_id"] != "gpt-5.2-codex" {
		t.Errorf("model_id = %v", ev.Payload["model_id"])
	}
	// Issue #42 feature vocabulary must be present (extraction marker set).
	if _, ok := ev.Payload["has_refactor_verb"]; !ok {
		t.Error("derived prompt features missing from payload")
	}
}

func TestNormalizeUserPromptSubmit_IdempotencyStableAcrossRedelivery(t *testing.T) {
	clock := fixedClock{t: time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)}
	parsed, err := codexhooks.ParseUserPromptSubmit(fixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Two separate deliveries (fresh normalizers, distinct EventIDs, even
	// different wall-clock instants): the key is turn-scoped, not
	// time-scoped, because codex's turn_id is provider-stable.
	ev1 := NewNormalizer(clock, &seqIDs{}).NormalizeUserPromptSubmit(parsed, clock.Now())
	later := fixedClock{t: clock.t.Add(3 * time.Second)}
	ev2 := NewNormalizer(later, &seqIDs{n: 100}).NormalizeUserPromptSubmit(parsed, later.Now())
	if ev1.IdempotencyKey != ev2.IdempotencyKey {
		t.Errorf("IdempotencyKey not stable across re-delivery: %q vs %q", ev1.IdempotencyKey, ev2.IdempotencyKey)
	}
	if ev1.EventID == ev2.EventID {
		t.Error("EventIDs must differ across deliveries")
	}
}

// --- Stop ---------------------------------------------------------------------

func TestNormalizeStop_NoSnapshot_BareTurnCompleted(t *testing.T) {
	n, clock := newTestNormalizer()
	parsed, err := codexhooks.ParseStop(fixture(t, "stop", "normal.json"))
	if err != nil {
		t.Fatal(err)
	}
	events := n.NormalizeStop(parsed, clock.Now(), nil)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1 (no rollout, no fabricated observations)", len(events))
	}
	ev := events[0]
	if ev.EventType != v1.EventProviderTurnCompleted {
		t.Errorf("EventType = %q", ev.EventType)
	}
	if ev.TurnID != "019f0000-2222-7aaa-8bbb-ccccdddd0101" {
		t.Errorf("TurnID = %q", ev.TurnID)
	}
	for _, key := range []string{"input_tokens", "output_tokens", "total_tokens", "cache_read_input_tokens"} {
		if _, present := ev.Payload[key]; present {
			t.Errorf("payload key %q present without a rollout snapshot (unknown is not zero)", key)
		}
	}
	if ev.Payload["stop_hook_active"] != false {
		t.Errorf("stop_hook_active = %v, want false", ev.Payload["stop_hook_active"])
	}
}

func TestNormalizeStop_WithSnapshot_FullEventSet(t *testing.T) {
	n, clock := newTestNormalizer()
	parsed, err := codexhooks.ParseStop(fixture(t, "stop", "normal.json"))
	if err != nil {
		t.Fatal(err)
	}
	events := n.NormalizeStop(parsed, clock.Now(), normalSnapshot(t))
	if len(events) != 4 {
		t.Fatalf("events = %d, want 4 (turn.completed + context + 2 quota)", len(events))
	}
	if events[0].EventType != v1.EventProviderTurnCompleted ||
		events[1].EventType != v1.EventProviderContextObserved ||
		events[2].EventType != v1.EventProviderQuotaObserved ||
		events[3].EventType != v1.EventProviderQuotaObserved {
		t.Fatalf("event types = %v", []v1.EventType{events[0].EventType, events[1].EventType, events[2].EventType, events[3].EventType})
	}

	// turn.completed: managedUsageEvent vocabulary with codex semantics
	// normalized — input_tokens is the FRESH (uncached) input.
	completed := events[0].Payload
	if completed["input_tokens"] != int64(42738-30976) {
		t.Errorf("input_tokens = %v, want %d (raw input minus cached)", completed["input_tokens"], 42738-30976)
	}
	if completed["cache_read_input_tokens"] != int64(30976) {
		t.Errorf("cache_read_input_tokens = %v", completed["cache_read_input_tokens"])
	}
	if completed["output_tokens"] != int64(1636) {
		t.Errorf("output_tokens = %v", completed["output_tokens"])
	}
	if completed["reasoning_output_tokens"] != int64(559) {
		t.Errorf("reasoning_output_tokens = %v", completed["reasoning_output_tokens"])
	}
	if completed["total_tokens"] != int64(42738-30976+1636) {
		t.Errorf("total_tokens = %v, want fresh input + output", completed["total_tokens"])
	}
	if completed["model_id"] != "gpt-5.2-codex" {
		t.Errorf("model_id = %v", completed["model_id"])
	}

	// context.observed: full raw context (cached included) + window.
	ctxPayload := events[1].Payload
	if ctxPayload["used_tokens"] != int64(42738+1636) {
		t.Errorf("used_tokens = %v, want raw input + output", ctxPayload["used_tokens"])
	}
	if ctxPayload["window_tokens"] != int64(353400) {
		t.Errorf("window_tokens = %v", ctxPayload["window_tokens"])
	}
	if events[1].TurnID != string(parsed.TurnID) {
		t.Errorf("context TurnID = %q, want the turn's id", events[1].TurnID)
	}

	// quota.observed: primary then secondary (sorted), claude-compatible
	// key vocabulary plus window_minutes/plan_type.
	primary := events[2].Payload
	if primary["limit_id"] != "primary" || primary["used_percent"] != 13.0 {
		t.Errorf("primary quota payload = %v", primary)
	}
	if primary["window_minutes"] != int64(300) {
		t.Errorf("primary window_minutes = %v", primary["window_minutes"])
	}
	if primary["plan_type"] != "pro" {
		t.Errorf("primary plan_type = %v", primary["plan_type"])
	}
	if _, err := time.Parse(time.RFC3339Nano, primary["resets_at"].(string)); err != nil {
		t.Errorf("primary resets_at unparseable: %v", primary["resets_at"])
	}
	secondary := events[3].Payload
	if secondary["limit_id"] != "secondary" || secondary["used_percent"] != 49.2 {
		t.Errorf("secondary quota payload = %v", secondary)
	}

	// Distinct idempotency keys across the batch.
	seen := map[string]bool{}
	for _, ev := range events {
		if ev.IdempotencyKey == "" || seen[ev.IdempotencyKey] {
			t.Errorf("idempotency key empty or duplicated: %q", ev.IdempotencyKey)
		}
		seen[ev.IdempotencyKey] = true
	}
}

func TestNormalizeStop_PartialUsage_UnknownIsNotZero(t *testing.T) {
	n, clock := newTestNormalizer()
	parsed, err := codexhooks.ParseStop(fixture(t, "stop", "missing_fields.json"))
	if err != nil {
		t.Fatal(err)
	}
	snap, ok := ReadRolloutSnapshot(rolloutFixturePath(t, "missing_fields.jsonl"))
	if !ok {
		t.Fatal("missing_fields.jsonl must still yield a snapshot")
	}
	events := n.NormalizeStop(parsed, clock.Now(), &snap)

	completed := events[0].Payload
	// input/cached both unknown: neither input_tokens nor total_tokens may
	// be fabricated; output alone is honest.
	for _, key := range []string{"input_tokens", "cache_read_input_tokens", "total_tokens"} {
		if _, present := completed[key]; present {
			t.Errorf("payload key %q present despite unknown counters", key)
		}
	}
	if completed["output_tokens"] != int64(10) {
		t.Errorf("output_tokens = %v, want 10", completed["output_tokens"])
	}

	// No context event: used_tokens unknowable (no input) and window null.
	for _, ev := range events[1:] {
		if ev.EventType == v1.EventProviderContextObserved {
			if _, present := ev.Payload["window_tokens"]; present {
				t.Errorf("window_tokens fabricated: %v", ev.Payload)
			}
		}
	}
	// One quota window (primary) came through.
	quotaCount := 0
	for _, ev := range events {
		if ev.EventType == v1.EventProviderQuotaObserved {
			quotaCount++
			if _, present := ev.Payload["plan_type"]; present {
				t.Errorf("plan_type fabricated for a null plan_type: %v", ev.Payload)
			}
		}
	}
	if quotaCount != 1 {
		t.Errorf("quota events = %d, want 1", quotaCount)
	}
}

func TestNormalizeStop_IdempotencyStableForSameTurn(t *testing.T) {
	parsed, err := codexhooks.ParseStop(fixture(t, "stop", "normal.json"))
	if err != nil {
		t.Fatal(err)
	}
	clock1 := fixedClock{t: time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)}
	clock2 := fixedClock{t: clock1.t.Add(2 * time.Second)} // re-entrant stop fires later
	ev1 := NewNormalizer(clock1, &seqIDs{}).NormalizeStop(parsed, clock1.Now(), nil)
	ev2 := NewNormalizer(clock2, &seqIDs{n: 100}).NormalizeStop(parsed, clock2.Now(), nil)
	if ev1[0].IdempotencyKey != ev2[0].IdempotencyKey {
		t.Errorf("turn.completed key not stable for the same turn_id: %q vs %q", ev1[0].IdempotencyKey, ev2[0].IdempotencyKey)
	}
}

func TestNormalizeStop_NoTurnID_FallsBackToTimeScopedKey(t *testing.T) {
	parsed, err := codexhooks.ParseStop(fixture(t, "stop", "missing_fields.json"))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.TurnID != "" {
		t.Fatal("fixture drifted: missing_fields.json must carry no turn_id")
	}
	clock1 := fixedClock{t: time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)}
	clock2 := fixedClock{t: clock1.t.Add(2 * time.Second)}
	ev1 := NewNormalizer(clock1, &seqIDs{}).NormalizeStop(parsed, clock1.Now(), nil)
	ev2 := NewNormalizer(clock2, &seqIDs{n: 100}).NormalizeStop(parsed, clock2.Now(), nil)
	if ev1[0].IdempotencyKey == ev2[0].IdempotencyKey {
		t.Error("without a turn_id, two different observation instants must not collide")
	}
}
