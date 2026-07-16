package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func rolloutFixturePath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("..", "..", "..", "testdata", "provider-events", "codex", "rollout", name)
}

func i64(v int64) *int64 { return &v }

func TestReadRolloutSnapshot_Normal_LastTokenCountWins(t *testing.T) {
	snap, ok := ReadRolloutSnapshot(rolloutFixturePath(t, "normal.jsonl"))
	if !ok {
		t.Fatal("ok = false, want a snapshot from normal.jsonl")
	}

	// The SECOND token_count line is the file's last — its numbers win.
	if snap.Last == nil {
		t.Fatal("Last = nil")
	}
	if got, want := snap.Last.InputTokens, i64(42738); got == nil || *got != *want {
		t.Errorf("Last.InputTokens = %v, want %d", got, *want)
	}
	if got := snap.Last.CachedInputTokens; got == nil || *got != 30976 {
		t.Errorf("Last.CachedInputTokens = %v, want 30976", got)
	}
	if got := snap.Last.OutputTokens; got == nil || *got != 1636 {
		t.Errorf("Last.OutputTokens = %v, want 1636", got)
	}
	if got := snap.Last.ReasoningOutputTokens; got == nil || *got != 559 {
		t.Errorf("Last.ReasoningOutputTokens = %v, want 559", got)
	}
	if got := snap.Last.TotalTokens; got == nil || *got != 44374 {
		t.Errorf("Last.TotalTokens = %v, want 44374", got)
	}

	if snap.Total == nil || snap.Total.TotalTokens == nil || *snap.Total.TotalTokens != 60141 {
		t.Errorf("Total = %+v, want cumulative total_tokens 60141", snap.Total)
	}
	if snap.ModelContextWindow == nil || *snap.ModelContextWindow != 353400 {
		t.Errorf("ModelContextWindow = %v, want 353400", snap.ModelContextWindow)
	}

	if len(snap.RateLimits) != 2 {
		t.Fatalf("RateLimits = %+v, want primary+secondary", snap.RateLimits)
	}
	// Sorted by LimitID: primary, secondary.
	primary, secondary := snap.RateLimits[0], snap.RateLimits[1]
	if primary.LimitID != "primary" || secondary.LimitID != "secondary" {
		t.Fatalf("window ids = %q,%q", primary.LimitID, secondary.LimitID)
	}
	if primary.UsedPercent == nil || *primary.UsedPercent != 13.0 {
		t.Errorf("primary.UsedPercent = %v, want 13.0 (the LAST line's value)", primary.UsedPercent)
	}
	if primary.WindowMinutes == nil || *primary.WindowMinutes != 300 {
		t.Errorf("primary.WindowMinutes = %v, want 300", primary.WindowMinutes)
	}
	if primary.ResetsAt == nil || !primary.ResetsAt.Equal(time.Unix(1784120400, 0)) {
		t.Errorf("primary.ResetsAt = %v, want epoch 1784120400", primary.ResetsAt)
	}
	if secondary.UsedPercent == nil || *secondary.UsedPercent != 49.2 {
		t.Errorf("secondary.UsedPercent = %v, want 49.2", secondary.UsedPercent)
	}
	if secondary.WindowMinutes == nil || *secondary.WindowMinutes != 10080 {
		t.Errorf("secondary.WindowMinutes = %v, want 10080", secondary.WindowMinutes)
	}
	if snap.PlanType != "pro" {
		t.Errorf("PlanType = %q, want pro", snap.PlanType)
	}
}

func TestReadRolloutSnapshot_NoTokenCount_FailsOpen(t *testing.T) {
	if _, ok := ReadRolloutSnapshot(rolloutFixturePath(t, "no_token_count.jsonl")); ok {
		t.Error("ok = true for a rollout with no token_count line, want false")
	}
}

func TestReadRolloutSnapshot_MissingFile_FailsOpen(t *testing.T) {
	if _, ok := ReadRolloutSnapshot(filepath.Join(t.TempDir(), "nope.jsonl")); ok {
		t.Error("ok = true for a missing file, want false")
	}
}

func TestReadRolloutSnapshot_MissingFields_UnknownStaysNil(t *testing.T) {
	snap, ok := ReadRolloutSnapshot(rolloutFixturePath(t, "missing_fields.jsonl"))
	if !ok {
		t.Fatal("ok = false, want a snapshot (a token_count line exists)")
	}
	if snap.Last == nil || snap.Last.InputTokens != nil {
		t.Errorf("Last.InputTokens = %+v, want nil (absent counter stays unknown)", snap.Last)
	}
	if snap.Last.OutputTokens == nil || *snap.Last.OutputTokens != 10 {
		t.Errorf("Last.OutputTokens = %v, want 10", snap.Last.OutputTokens)
	}
	if snap.ModelContextWindow != nil {
		t.Errorf("ModelContextWindow = %v, want nil for a null field", snap.ModelContextWindow)
	}
	// secondary was null and primary carried only used_percent.
	if len(snap.RateLimits) != 1 || snap.RateLimits[0].LimitID != "primary" {
		t.Fatalf("RateLimits = %+v, want just primary", snap.RateLimits)
	}
	if snap.RateLimits[0].ResetsAt != nil {
		t.Errorf("primary.ResetsAt = %v, want nil", snap.RateLimits[0].ResetsAt)
	}
	if snap.PlanType != "" {
		t.Errorf("PlanType = %q, want empty for null", snap.PlanType)
	}
}

func TestReadRolloutSnapshot_MalformedLines_SkippedNotFatal(t *testing.T) {
	snap, ok := ReadRolloutSnapshot(rolloutFixturePath(t, "malformed_lines.jsonl"))
	if !ok {
		t.Fatal("ok = false; garbage lines must be skipped, not fatal")
	}
	if snap.Last == nil || snap.Last.InputTokens == nil || *snap.Last.InputTokens != 900 {
		t.Errorf("Last = %+v, want the valid token_count line's numbers", snap.Last)
	}
	if snap.PlanType != "plus" {
		t.Errorf("PlanType = %q, want plus", snap.PlanType)
	}
}

func TestReadRolloutSnapshot_TailWindow_SeekSkipsFragment(t *testing.T) {
	// Build a file whose front (outside the tail window) holds one
	// token_count and whose tail holds another: only the tail one may win,
	// and the mid-line fragment at the seek point must not be parsed.
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-big.jsonl")
	front := `{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":1,"output_tokens":1}}}}` + "\n"
	pad := `{"type":"event_msg","payload":{"type":"task_started","filler":"` + strings.Repeat("x", 512) + `"}}` + "\n"
	tail := `{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":7,"cached_input_tokens":2,"output_tokens":3}}}}` + "\n"
	content := front + strings.Repeat(pad, 8) + tail
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// A window smaller than the file but larger than the tail line.
	snap, ok := readRolloutSnapshot(path, 1024, rolloutMaxLineBytes)
	if !ok {
		t.Fatal("ok = false, want the tail token_count")
	}
	if snap.Last == nil || snap.Last.InputTokens == nil || *snap.Last.InputTokens != 7 {
		t.Errorf("Last.InputTokens = %+v, want 7 (tail line)", snap.Last)
	}
}

func TestReadRolloutSnapshot_OversizedLine_SkippedWhole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-longline.jsonl")
	huge := `{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":999}},"filler":"` + strings.Repeat("y", 4096) + `"}}` + "\n"
	small := `{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":5,"output_tokens":5}}}}` + "\n"
	if err := os.WriteFile(path, []byte(small+huge), 0o644); err != nil {
		t.Fatal(err)
	}

	// maxLine below the huge line's length: the huge (and LAST) line is
	// skipped whole, so the earlier small line's numbers win — degraded
	// capture, disclosed failure direction, never a truncated parse.
	snap, ok := readRolloutSnapshot(path, rolloutTailWindowBytes, 1024)
	if !ok {
		t.Fatal("ok = false, want the small line's snapshot")
	}
	if snap.Last == nil || snap.Last.InputTokens == nil || *snap.Last.InputTokens != 5 {
		t.Errorf("Last.InputTokens = %+v, want 5 (oversized line skipped whole)", snap.Last)
	}
}

// --- rollout path resolution -------------------------------------------------

func TestFindRolloutPath_ScansSessionsLayout(t *testing.T) {
	dir := t.TempDir()
	sessionID := "019f0000-1111-7aaa-8bbb-ccccdddd0009"
	day := filepath.Join(dir, "2026", "07", "14")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	older := filepath.Join(day, "rollout-2026-07-14T08-00-00-"+sessionID+".jsonl")
	newer := filepath.Join(day, "rollout-2026-07-14T10-00-00-"+sessionID+".jsonl")
	other := filepath.Join(day, "rollout-2026-07-14T09-00-00-019f0000-ffff-7aaa-8bbb-ccccdddd0000.jsonl")
	for _, p := range []string{older, newer, other} {
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, ok := FindRolloutPath(dir, sessionID)
	if !ok {
		t.Fatal("ok = false, want a match")
	}
	if got != newer {
		t.Errorf("path = %q, want the newest recording %q", got, newer)
	}

	if _, ok := FindRolloutPath(dir, "no-such-session"); ok {
		t.Error("ok = true for an unknown session, want false")
	}
	if _, ok := FindRolloutPath("", sessionID); ok {
		t.Error("ok = true for an empty dir, want false")
	}
}

func TestDefaultSessionsDir_HonorsCodexHome(t *testing.T) {
	t.Setenv("CODEX_HOME", "/tmp/isolated-codex-home")
	dir, ok := DefaultSessionsDir()
	if !ok {
		t.Fatal("ok = false with CODEX_HOME set")
	}
	if dir != filepath.Join("/tmp/isolated-codex-home", "sessions") {
		t.Errorf("dir = %q", dir)
	}
}
