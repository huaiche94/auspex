package codex

import (
	"testing"

	codexhooks "github.com/huaiche94/auspex/internal/hooks/codex"
	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

func compactStrPtr(s string) *string { return &s }

func TestNormalizePreCompact_FullWithCheckpoint(t *testing.T) {
	n, clock := newTestNormalizer()
	ev := codexhooks.PreCompactEvent{
		SessionID:      "019f-sess",
		CWD:            compactStrPtr("/repo"),
		Model:          compactStrPtr("gpt-5.2-codex"),
		PermissionMode: compactStrPtr("default"),
		Trigger:        compactStrPtr("auto"),
	}
	ckpt := &CompactionCheckpoint{
		Captured:               true,
		StateCheckpointID:      "sc-1",
		RepositoryCheckpointID: "rc-1",
	}

	out := n.NormalizePreCompact(ev, clock.Now(), ckpt)

	if out.EventType != v1.EventProviderSessionCompacted {
		t.Errorf("EventType = %q, want provider.session.compacted", out.EventType)
	}
	if out.Provider != Provider {
		t.Errorf("Provider = %q, want %q", out.Provider, Provider)
	}
	if out.SessionID != "019f-sess" {
		t.Errorf("SessionID = %q", out.SessionID)
	}
	if out.IdempotencyKey == "" {
		t.Error("IdempotencyKey is empty")
	}
	wantPayload := map[string]any{
		"phase":                    claudetelemetry.CompactionPhasePre,
		"trigger":                  "auto",
		"model_id":                 "gpt-5.2-codex",
		"permission_mode":          "default",
		"cwd":                      "/repo",
		"checkpoint_captured":      true,
		"state_checkpoint_id":      "sc-1",
		"repository_checkpoint_id": "rc-1",
	}
	for k, wv := range wantPayload {
		if gv := out.Payload[k]; gv != wv {
			t.Errorf("payload[%q] = %v, want %v", k, gv, wv)
		}
	}
	for k := range out.Payload {
		if _, expected := wantPayload[k]; !expected {
			t.Errorf("payload has unexpected key %q = %v", k, out.Payload[k])
		}
	}
}

func TestNormalizePreCompact_SkipReasonAndAbsentFields(t *testing.T) {
	n, clock := newTestNormalizer()
	out := n.NormalizePreCompact(codexhooks.PreCompactEvent{SessionID: "s"}, clock.Now(),
		&CompactionCheckpoint{SkipReason: "no_task"})

	if got := out.Payload["checkpoint_captured"]; got != false {
		t.Errorf("checkpoint_captured = %v, want false", got)
	}
	if got := out.Payload["checkpoint_skip_reason"]; got != "no_task" {
		t.Errorf("checkpoint_skip_reason = %v, want no_task", got)
	}
	for _, key := range []string{"trigger", "model_id", "permission_mode", "cwd", "state_checkpoint_id", "repository_checkpoint_id"} {
		if _, present := out.Payload[key]; present {
			t.Errorf("payload key %q must be absent (unknown is not zero)", key)
		}
	}
}

func TestNormalizePostCompact(t *testing.T) {
	n, clock := newTestNormalizer()
	out := n.NormalizePostCompact(codexhooks.PostCompactEvent{
		SessionID: "s",
		Trigger:   compactStrPtr("manual"),
	}, clock.Now())

	if out.EventType != v1.EventProviderSessionCompacted {
		t.Errorf("EventType = %q", out.EventType)
	}
	if got := out.Payload["phase"]; got != claudetelemetry.CompactionPhasePost {
		t.Errorf("phase = %v, want post", got)
	}
	if got := out.Payload["trigger"]; got != "manual" {
		t.Errorf("trigger = %v", got)
	}

	// Distinct keys across the two phases at the same instant.
	pre := n.NormalizePreCompact(codexhooks.PreCompactEvent{SessionID: "s"}, clock.Now(), nil)
	if pre.IdempotencyKey == out.IdempotencyKey {
		t.Error("pre and post compaction events must have distinct idempotency keys")
	}
}

// TestNormalizePreCompact_PhaseVocabularySharedWithClaude pins the
// cross-provider payload contract: the codex events carry the SAME phase
// strings the claude normalizer emits, so a provider-independent counter
// (features.SessionFeatures.CompactionCount) needs no per-provider
// vocabulary mapping.
func TestNormalizePreCompact_PhaseVocabularySharedWithClaude(t *testing.T) {
	if claudetelemetry.CompactionPhasePre != "pre" || claudetelemetry.CompactionPhasePost != "post" {
		t.Fatalf("phase vocabulary drifted: pre=%q post=%q",
			claudetelemetry.CompactionPhasePre, claudetelemetry.CompactionPhasePost)
	}
}
