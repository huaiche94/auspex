package claude

import (
	"encoding/json"
	"strings"
	"testing"

	claudehooks "github.com/huaiche94/auspex/internal/hooks/claude"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

func strPtr(s string) *string { return &s }
func intPtr(n int) *int       { return &n }

func TestNormalizePreCompact_FullWithCheckpoint(t *testing.T) {
	n, clock := newTestNormalizer()
	ev := claudehooks.PreCompactEvent{
		SessionID:             "sess-1",
		CWD:                   strPtr("/repo"),
		Trigger:               strPtr(claudehooks.CompactTriggerAuto),
		CustomInstructionsLen: intPtr(23),
	}
	ckpt := &CompactionCheckpoint{
		Captured:               true,
		StateCheckpointID:      "sc-1",
		RepositoryCheckpointID: "rc-1",
	}

	out := n.NormalizePreCompact(ev, clock.Now(), ckpt)

	requireEnvelope(t, out, v1.EventProviderSessionCompacted, "sess-1")
	if out.IdempotencyKey == "" {
		t.Error("IdempotencyKey is empty")
	}
	want := map[string]any{
		"phase":                    CompactionPhasePre,
		"trigger":                  claudehooks.CompactTriggerAuto,
		"custom_instructions_len":  23,
		"cwd":                      "/repo",
		"checkpoint_captured":      true,
		"state_checkpoint_id":      "sc-1",
		"repository_checkpoint_id": "rc-1",
	}
	requirePayloadEquals(t, out.Payload, want)
}

func TestNormalizePreCompact_CheckpointFailureRecordsReason(t *testing.T) {
	n, clock := newTestNormalizer()
	ev := claudehooks.PreCompactEvent{SessionID: "sess-1"}
	ckpt := &CompactionCheckpoint{Captured: false, SkipReason: "checkpoint_failed"}

	out := n.NormalizePreCompact(ev, clock.Now(), ckpt)

	if got := out.Payload["checkpoint_captured"]; got != false {
		t.Errorf("checkpoint_captured = %v, want false", got)
	}
	if got := out.Payload["checkpoint_skip_reason"]; got != "checkpoint_failed" {
		t.Errorf("checkpoint_skip_reason = %v, want checkpoint_failed", got)
	}
	if _, present := out.Payload["state_checkpoint_id"]; present {
		t.Error("state_checkpoint_id must not be stamped on a failed capture")
	}
}

func TestNormalizePreCompact_NilCheckpointStampsNoCheckpointKeys(t *testing.T) {
	n, clock := newTestNormalizer()
	out := n.NormalizePreCompact(claudehooks.PreCompactEvent{SessionID: "sess-1"}, clock.Now(), nil)

	for _, key := range []string{"checkpoint_captured", "state_checkpoint_id", "repository_checkpoint_id", "checkpoint_skip_reason"} {
		if _, present := out.Payload[key]; present {
			t.Errorf("payload key %q must be absent when capture was not configured (unknown is not zero)", key)
		}
	}
	if got := out.Payload["phase"]; got != CompactionPhasePre {
		t.Errorf("phase = %v, want %q", got, CompactionPhasePre)
	}
}

// TestNormalizePreCompact_AbsentOptionalFieldsStampNothing pins the
// unknown-is-not-zero rule for the pre-compact payload.
func TestNormalizePreCompact_AbsentOptionalFieldsStampNothing(t *testing.T) {
	n, clock := newTestNormalizer()
	out := n.NormalizePreCompact(claudehooks.PreCompactEvent{SessionID: "sess-1"}, clock.Now(), nil)

	for _, key := range []string{"trigger", "custom_instructions_len", "cwd"} {
		if _, present := out.Payload[key]; present {
			t.Errorf("payload key %q must be absent when the source field was", key)
		}
	}
}

// TestNormalizePreCompact_NoRawTextEverInPayload asserts the payload
// carries only the custom-instructions LENGTH — the raw text cannot even
// reach this function (the parser never returns it), but the payload's
// serialized form is additionally scanned as a leakage backstop, matching
// the fixture suite's raw-prompt-absence discipline.
func TestNormalizePreCompact_NoRawTextEverInPayload(t *testing.T) {
	n, clock := newTestNormalizer()
	secret := "SECRET-COMPACT-INSTRUCTIONS"
	parsed, err := claudehooks.ParsePreCompact([]byte(`{"session_id":"s","custom_instructions":"` + secret + `"}`))
	if err != nil {
		t.Fatalf("ParsePreCompact: %v", err)
	}
	out := n.NormalizePreCompact(parsed, clock.Now(), nil)
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if strings.Contains(string(b), secret) {
		t.Fatalf("raw custom_instructions text leaked into the serialized event: %s", b)
	}
	if got := out.Payload["custom_instructions_len"]; got != len(secret) {
		t.Errorf("custom_instructions_len = %v, want %d", got, len(secret))
	}
}

func TestNormalizePreCompact_IdempotencyKeyDeterministic(t *testing.T) {
	n1, clock := newTestNormalizer()
	n2, _ := newTestNormalizer()
	ev := claudehooks.PreCompactEvent{SessionID: "sess-1"}
	a := n1.NormalizePreCompact(ev, clock.Now(), nil)
	b := n2.NormalizePreCompact(ev, clock.Now(), nil)
	if a.IdempotencyKey != b.IdempotencyKey {
		t.Errorf("idempotency key not deterministic: %q vs %q", a.IdempotencyKey, b.IdempotencyKey)
	}
}

func TestNormalizePostCompact(t *testing.T) {
	n, clock := newTestNormalizer()
	ev := claudehooks.PostCompactEvent{
		SessionID: "sess-1",
		CWD:       strPtr("/repo"),
		Trigger:   strPtr(claudehooks.CompactTriggerManual),
	}
	out := n.NormalizePostCompact(ev, clock.Now())

	requireEnvelope(t, out, v1.EventProviderSessionCompacted, "sess-1")
	want := map[string]any{
		"phase":   CompactionPhasePost,
		"trigger": claudehooks.CompactTriggerManual,
		"cwd":     "/repo",
	}
	requirePayloadEquals(t, out.Payload, want)

	// Pre and post keys for the same session/instant must never collide.
	pre := n.NormalizePreCompact(claudehooks.PreCompactEvent{SessionID: "sess-1"}, clock.Now(), nil)
	if pre.IdempotencyKey == out.IdempotencyKey {
		t.Error("pre and post compaction events must have distinct idempotency keys")
	}
}

// requirePayloadEquals asserts payload matches want exactly (no extra or
// missing keys), comparing numbers loosely (ints survive as ints in the
// in-memory map; this helper is for pre-serialization payloads).
func requirePayloadEquals(t *testing.T, payload map[string]any, want map[string]any) {
	t.Helper()
	for k, wv := range want {
		gv, present := payload[k]
		if !present {
			t.Errorf("payload missing key %q", k)
			continue
		}
		if gv != wv {
			t.Errorf("payload[%q] = %v (%T), want %v (%T)", k, gv, gv, wv, wv)
		}
	}
	for k := range payload {
		if _, expected := want[k]; !expected {
			t.Errorf("payload has unexpected key %q = %v", k, payload[k])
		}
	}
}
