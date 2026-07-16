// toolops_test.go: unit tests for the #67 per-turn file-operation
// aggregate — the transcript replay (readTurnToolOps), the aggregate
// arithmetic, and NormalizeStop's stamping of the five additive payload
// keys (ADR-052). Synthetic transcripts reuse transcriptusage_test.go's
// helpers; every path below is an inert test input (fixtures are inputs,
// never persisted outputs).
package claude

import (
	"path/filepath"
	"strings"
	"testing"

	claudehooks "github.com/huaiche94/auspex/internal/hooks/claude"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// toolUseLine builds one assistant transcript entry carrying a single
// tool_use content block (the shape the provider streams one block per
// line — see transcriptusage.go's dedupe note).
func toolUseLine(blockID, tool, pathField, path string) string {
	return `{"type":"assistant","requestId":"req-` + blockID + `","message":{"role":"assistant","model":"m",` +
		`"content":[{"type":"tool_use","id":"` + blockID + `","name":"` + tool + `","input":{"` + pathField + `":"` + path + `"}}]}}`
}

func sidechainToolUseLine(blockID, tool, path string) string {
	return `{"type":"assistant","isSidechain":true,"requestId":"req-` + blockID + `","message":{"role":"assistant",` +
		`"content":[{"type":"tool_use","id":"` + blockID + `","name":"` + tool + `","input":{"file_path":"` + path + `"}}]}}`
}

// TestReadTurnToolOps_WorkedExample pins the research doc's aggregate
// arithmetic on the canonical worked example: 3 ops on 2 distinct files
// (Read a, Edit a, Read b) = 1 repeat, repeat_rate 1/3, max 2 ops on one
// file.
func TestReadTurnToolOps_WorkedExample(t *testing.T) {
	path := writeTranscript(t,
		// A PREVIOUS turn whose churn must not leak into the result.
		promptLine,
		toolUseLine("old-1", "Edit", "file_path", "/repo/stale.go"),
		toolUseLine("old-2", "Edit", "file_path", "/repo/stale.go"),
		// The last turn: the worked example, with tool results interleaved
		// exactly as the provider writes them.
		promptLine,
		toolUseLine("t1", "Read", "file_path", "/repo/a.go"),
		toolResultLine,
		toolUseLine("t2", "Edit", "file_path", "/repo/a.go"),
		toolResultLine,
		toolUseLine("t3", "Read", "file_path", "/repo/b.go"),
		toolResultLine,
	)

	ops, ok := ReadTurnToolOps(path)
	if !ok {
		t.Fatal("ReadTurnToolOps: ok = false, want true")
	}
	if got := i64(t, ops.DistinctFilesTouched, "DistinctFilesTouched"); got != 2 {
		t.Errorf("DistinctFilesTouched = %d, want 2", got)
	}
	if got := i64(t, ops.TotalFileOps, "TotalFileOps"); got != 3 {
		t.Errorf("TotalFileOps = %d, want 3", got)
	}
	if got := i64(t, ops.RepeatedOps, "RepeatedOps"); got != 1 {
		t.Errorf("RepeatedOps = %d, want 1", got)
	}
	if got := i64(t, ops.MaxOpsOnOneFile, "MaxOpsOnOneFile"); got != 2 {
		t.Errorf("MaxOpsOnOneFile = %d, want 2", got)
	}
	rate, rateOK := ops.RepeatRate()
	if !rateOK {
		t.Fatal("RepeatRate: ok = false, want true")
	}
	if rate < 0.333 || rate > 0.334 {
		t.Errorf("RepeatRate = %v, want 1/3", rate)
	}
}

// TestReadTurnToolOps_ClassificationDedupeAndSidechain: ignored tools
// (Bash/Grep/unknown) never count, a tool_use block re-streamed under the
// same block ID counts once, NotebookEdit's notebook_path is a file
// target, and sidechain (subagent) activity is out of scope — matching
// the usage extractor's main-chain attribution model.
func TestReadTurnToolOps_ClassificationDedupeAndSidechain(t *testing.T) {
	path := writeTranscript(t,
		promptLine,
		toolUseLine("t1", "Write", "file_path", "/repo/new.go"),
		toolUseLine("t1", "Write", "file_path", "/repo/new.go"),          // same block ID: one op
		toolUseLine("t2", "Bash", "file_path", "/repo/new.go"),           // ignored class
		toolUseLine("t3", "FutureFileTool", "file_path", "/repo/new.go"), // unknown tool: never guessed in
		toolUseLine("t4", "NotebookEdit", "notebook_path", "/repo/nb.ipynb"),
		toolUseLine("t5", "Read", "other_field", "/repo/ghost.go"), // no file target: not a file op
		sidechainToolUseLine("t6", "Edit", "/repo/subagent.go"),    // sidechain: out of scope
		`not json at all`, // malformed line: contributes nothing
	)

	ops, ok := ReadTurnToolOps(path)
	if !ok {
		t.Fatal("ReadTurnToolOps: ok = false, want true")
	}
	if got := i64(t, ops.TotalFileOps, "TotalFileOps"); got != 2 {
		t.Errorf("TotalFileOps = %d, want 2 (Write once + NotebookEdit)", got)
	}
	if got := i64(t, ops.DistinctFilesTouched, "DistinctFilesTouched"); got != 2 {
		t.Errorf("DistinctFilesTouched = %d, want 2", got)
	}
	if got := i64(t, ops.RepeatedOps, "RepeatedOps"); got != 0 {
		t.Errorf("RepeatedOps = %d, want 0", got)
	}
	if got := i64(t, ops.MaxOpsOnOneFile, "MaxOpsOnOneFile"); got != 1 {
		t.Errorf("MaxOpsOnOneFile = %d, want 1", got)
	}
}

// TestReadTurnToolOps_ZeroOpsTurnIsAMeasurement: a bounded turn whose
// main chain performed no file operations reports honest zeros (ok=true)
// — the repeat_rate omission then happens at stamping time (total = 0).
func TestReadTurnToolOps_ZeroOpsTurnIsAMeasurement(t *testing.T) {
	path := writeTranscript(t,
		promptLine,
		toolUseLine("t1", "Edit", "file_path", "/repo/last-turn.go"),
		promptLine, // new turn, no ops after it
		assistantLine("req-9", "m", 10, 1, 0, 0),
	)
	ops, ok := ReadTurnToolOps(path)
	if !ok {
		t.Fatal("ReadTurnToolOps: ok = false, want true (the turn is bounded)")
	}
	for name, p := range map[string]*int64{
		"DistinctFilesTouched": ops.DistinctFilesTouched,
		"TotalFileOps":         ops.TotalFileOps,
		"RepeatedOps":          ops.RepeatedOps,
		"MaxOpsOnOneFile":      ops.MaxOpsOnOneFile,
	} {
		if got := i64(t, p, name); got != 0 {
			t.Errorf("%s = %d, want 0", name, got)
		}
	}
	if _, rateOK := ops.RepeatRate(); rateOK {
		t.Error("RepeatRate: ok = true on a zero-op turn, want false (omitted, not fabricated)")
	}
}

// TestReadTurnToolOps_FailOpenCases: every unreadable/unboundable
// condition is ok=false — the caller degrades to the hook-counted total;
// no condition may fail the Stop hook.
func TestReadTurnToolOps_FailOpenCases(t *testing.T) {
	cases := map[string]string{
		"no prompt boundary": writeTranscript(t,
			toolUseLine("t1", "Edit", "file_path", "/repo/a.go"),
		),
		"empty file": writeTranscript(t, ""),
	}
	for name, path := range cases {
		if _, ok := ReadTurnToolOps(path); ok {
			t.Errorf("%s: ok = true, want false", name)
		}
	}
	if _, ok := ReadTurnToolOps(filepath.Join(t.TempDir(), "does-not-exist.jsonl")); ok {
		t.Error("missing file: ok = true, want false")
	}
	if _, ok := ReadTurnToolOps(t.TempDir()); ok {
		t.Error("directory: ok = true, want false")
	}
}

// TestReadTurnToolOps_TailWindowBounds mirrors the usage extractor's
// bounded-I/O behavior through the shared scanTranscriptTail: when the
// tail window no longer contains the turn's prompt boundary, extraction
// honestly fails rather than reporting a partial turn.
func TestReadTurnToolOps_TailWindowBounds(t *testing.T) {
	padding := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"` + strings.Repeat("x", 512) + `"}]}}`
	path := writeTranscript(t,
		promptLine,
		toolUseLine("t1", "Edit", "file_path", "/repo/a.go"),
		padding,
		toolUseLine("t2", "Edit", "file_path", "/repo/a.go"),
	)

	// A window large enough for the whole file: full turn.
	ops, ok := readTurnToolOps(path, 1<<20, transcriptMaxLineBytes)
	if !ok || i64(t, ops.TotalFileOps, "TotalFileOps") != 2 {
		t.Fatalf("full window: ok=%v ops=%+v, want ok with 2 total ops", ok, ops)
	}

	// A window that clips the boundary away: honest failure.
	if _, ok := readTurnToolOps(path, 128, transcriptMaxLineBytes); ok {
		t.Error("clipped window: ok = true, want false (boundary out of window)")
	}
}

// TestNormalizeStop_WithToolOps pins ADR-052 approval touch 2: the five
// additive provider.turn.completed payload keys, their exact spellings,
// and the omission rules (nil aggregate stamps nothing; the hook-counted
// degrade stamps total_file_ops alone; repeat_rate is omitted when
// total_file_ops = 0).
func TestNormalizeStop_WithToolOps(t *testing.T) {
	n, clock := newTestNormalizer()
	parsed, err := claudehooks.ParseStop(fixture(t, "stop", "normal.json"))
	if err != nil {
		t.Fatalf("ParseStop: %v", err)
	}

	distinct, total, repeated, maxOps := int64(2), int64(3), int64(1), int64(2)
	full := &TurnToolOps{
		DistinctFilesTouched: &distinct,
		TotalFileOps:         &total,
		RepeatedOps:          &repeated,
		MaxOpsOnOneFile:      &maxOps,
	}
	ev := n.NormalizeStop(parsed, clock.Now(), nil, full)
	requireEnvelope(t, ev, v1.EventProviderTurnCompleted, parsed.SessionID)
	want := map[string]any{
		"distinct_files_touched": int64(2),
		"total_file_ops":         int64(3),
		"repeated_ops":           int64(1),
		"max_ops_on_one_file":    int64(2),
	}
	for key, wantV := range want {
		if got := ev.Payload[key]; got != wantV {
			t.Errorf("payload[%q] = %v (%T), want %v", key, got, got, wantV)
		}
	}
	rate, isFloat := ev.Payload["repeat_rate"].(float64)
	if !isFloat || rate < 0.333 || rate > 0.334 {
		t.Errorf("payload[repeat_rate] = %v, want 1/3", ev.Payload["repeat_rate"])
	}

	// nil aggregate (capture inactive): the payload stays byte-identical
	// to the pre-#67 shape — no aggregate keys at all.
	ev = n.NormalizeStop(parsed, clock.Now(), nil, nil)
	for _, key := range []string{"distinct_files_touched", "total_file_ops", "repeated_ops", "repeat_rate", "max_ops_on_one_file"} {
		if _, present := ev.Payload[key]; present {
			t.Errorf("nil toolOps: payload key %q present, want absent", key)
		}
	}

	// Hook-counted degrade: total only; identity-dependent fields and the
	// rate stay honestly absent.
	degraded := HookCountedToolOps(3)
	ev = n.NormalizeStop(parsed, clock.Now(), nil, &degraded)
	if got := ev.Payload["total_file_ops"]; got != int64(3) {
		t.Errorf("degrade: payload[total_file_ops] = %v, want 3", got)
	}
	for _, key := range []string{"distinct_files_touched", "repeated_ops", "repeat_rate", "max_ops_on_one_file"} {
		if _, present := ev.Payload[key]; present {
			t.Errorf("degrade: payload key %q present, want absent (unknown is not zero)", key)
		}
	}

	// Zero-op turn: four zeros are measurements; repeat_rate is omitted
	// (§7.3 — a rate over zero ops is not a measurement).
	zero := int64(0)
	zeros := &TurnToolOps{
		DistinctFilesTouched: &zero,
		TotalFileOps:         &zero,
		RepeatedOps:          &zero,
		MaxOpsOnOneFile:      &zero,
	}
	ev = n.NormalizeStop(parsed, clock.Now(), nil, zeros)
	if got := ev.Payload["total_file_ops"]; got != int64(0) {
		t.Errorf("zero turn: payload[total_file_ops] = %v, want 0", got)
	}
	if _, present := ev.Payload["repeat_rate"]; present {
		t.Error("zero turn: payload[repeat_rate] present, want omitted when total_file_ops = 0")
	}
}

// TestFixture_PostToolUse_NeverNormalizesToAnEvent documents (and pins)
// the ADR-052 shape decision: PostToolUse is scratch-accumulation only —
// no EventType exists for it in this package's normalizer and none may be
// invented (pkg/protocol/v1's taxonomy is closed; provider.tool.* stays
// unused). The fixture corpus is exercised at parse level here so the
// suite covers all four fixture categories end to end.
func TestFixture_PostToolUse_NeverNormalizesToAnEvent(t *testing.T) {
	for _, name := range []string{"normal.json", "missing_fields.json", "unknown_fields.json"} {
		if _, err := claudehooks.ParsePostToolUse(fixture(t, "posttooluse", name)); err != nil {
			t.Errorf("ParsePostToolUse(%s): %v (fixture must parse — unknown fields tolerated)", name, err)
		}
	}
	if _, err := claudehooks.ParsePostToolUse(fixture(t, "posttooluse", "malformed.json")); err == nil {
		t.Error("ParsePostToolUse(malformed.json): expected a parse error, got nil")
	}
}
