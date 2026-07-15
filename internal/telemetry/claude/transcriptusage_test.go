// transcriptusage_test.go: unit tests for the #72 per-turn transcript
// usage extractor, against synthetic transcript fixtures (numbers only —
// the placeholder strings below are inert stand-ins, never asserted on).
package claude

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writeTranscript writes lines as one JSONL transcript in a temp dir and
// returns its path.
func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("writing synthetic transcript: %v", err)
	}
	return path
}

// Synthetic transcript lines. Shapes mirror real Claude Code 2.1.x session
// transcripts (STEP-1 verification, issue #72): a typed prompt is a user
// entry with string (or text-block array) content; tool results are user
// entries whose content array carries tool_result blocks; one API call
// repeats the same usage object across several assistant lines sharing a
// requestId.
const (
	promptLine     = `{"type":"user","message":{"role":"user","content":"p"}}`
	toolResultLine = `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"r"}]},"toolUseResult":true}`
	metaUserLine   = `{"type":"user","isMeta":true,"message":{"role":"user","content":"m"}}`
	imagePromptRow = `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"p"},{"type":"image"}]}}`
)

func assistantLine(requestID, model string, input, output, cacheRead, cacheCreate int64) string {
	return `{"type":"assistant","requestId":"` + requestID + `","message":{"role":"assistant","model":"` + model + `",` +
		`"usage":{"input_tokens":` + strconv.FormatInt(input, 10) + `,"output_tokens":` + strconv.FormatInt(output, 10) +
		`,"cache_read_input_tokens":` + strconv.FormatInt(cacheRead, 10) + `,"cache_creation_input_tokens":` + strconv.FormatInt(cacheCreate, 10) + `},` +
		`"content":[{"type":"text","text":"a"}]}}`
}

func i64(t *testing.T, p *int64, what string) int64 {
	t.Helper()
	if p == nil {
		t.Fatalf("%s = nil, want a value", what)
	}
	return *p
}

func TestReadTurnUsage_SumsLastTurnDedupedByRequestID(t *testing.T) {
	path := writeTranscript(t,
		// A PREVIOUS turn that must not leak into the result.
		promptLine,
		assistantLine("req-old", "model-old", 999, 999, 999, 999),
		// The last turn: boundary, then two API calls — the first
		// streamed as two lines with identical usage (dedupe target) —
		// with tool results and a meta entry interleaved (non-boundaries).
		promptLine,
		assistantLine("req-1", "model-a", 100, 10, 1000, 30),
		assistantLine("req-1", "model-a", 100, 10, 1000, 30),
		toolResultLine,
		metaUserLine,
		assistantLine("req-2", "model-b", 200, 20, 2000, 40),
	)

	u, ok := ReadTurnUsage(path)
	if !ok {
		t.Fatal("ReadTurnUsage: ok = false, want true")
	}
	if got := i64(t, u.InputTokens, "InputTokens"); got != 300 {
		t.Errorf("InputTokens = %d, want 300", got)
	}
	if got := i64(t, u.OutputTokens, "OutputTokens"); got != 30 {
		t.Errorf("OutputTokens = %d, want 30", got)
	}
	if got := i64(t, u.CacheReadInputTokens, "CacheReadInputTokens"); got != 3000 {
		t.Errorf("CacheReadInputTokens = %d, want 3000", got)
	}
	if got := i64(t, u.CacheCreationInputTokens, "CacheCreationInputTokens"); got != 70 {
		t.Errorf("CacheCreationInputTokens = %d, want 70", got)
	}
	if u.APICallCount != 2 {
		t.Errorf("APICallCount = %d, want 2 (deduped by requestId)", u.APICallCount)
	}
	if u.ModelID != "model-b" {
		t.Errorf("ModelID = %q, want last seen %q", u.ModelID, "model-b")
	}
	if total, ok := u.TotalTokens(); !ok || total != 330 {
		t.Errorf("TotalTokens() = %d,%v, want 330,true", total, ok)
	}

	// Determinism: a second read of the same file is identical.
	u2, ok2 := ReadTurnUsage(path)
	if !ok2 || u2.APICallCount != u.APICallCount || *u2.InputTokens != *u.InputTokens ||
		*u2.OutputTokens != *u.OutputTokens || u2.ModelID != u.ModelID {
		t.Errorf("second read differs: %+v vs %+v", u2, u)
	}
}

func TestReadTurnUsage_ImageBlockPromptIsABoundary(t *testing.T) {
	path := writeTranscript(t,
		promptLine,
		assistantLine("req-old", "m", 999, 9, 0, 0),
		imagePromptRow, // text+image array content: a typed prompt, resets the turn
		assistantLine("req-new", "m", 5, 7, 0, 0),
	)
	u, ok := ReadTurnUsage(path)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if got := i64(t, u.InputTokens, "InputTokens"); got != 5 {
		t.Errorf("InputTokens = %d, want 5 (image prompt must reset the turn)", got)
	}
}

func TestReadTurnUsage_FailOpenCases(t *testing.T) {
	cases := map[string]string{
		"no prompt boundary": writeTranscript(t,
			toolResultLine,
			assistantLine("req-1", "m", 1, 2, 3, 4),
		),
		"boundary but no usage-bearing call": writeTranscript(t,
			promptLine,
			`{"type":"assistant","message":{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"e"}]}}`,
		),
		"empty file": writeTranscript(t, ""),
		"sidechain-only calls after boundary": writeTranscript(t,
			promptLine,
			`{"type":"assistant","isSidechain":true,"requestId":"req-s","message":{"role":"assistant","model":"m","usage":{"input_tokens":1,"output_tokens":2}}}`,
		),
	}
	for name, path := range cases {
		if _, ok := ReadTurnUsage(path); ok {
			t.Errorf("%s: ok = true, want false", name)
		}
	}

	if _, ok := ReadTurnUsage(filepath.Join(t.TempDir(), "does-not-exist.jsonl")); ok {
		t.Error("missing file: ok = true, want false")
	}
	if _, ok := ReadTurnUsage(t.TempDir()); ok {
		t.Error("directory path: ok = true, want false")
	}
}

func TestReadTurnUsage_MalformedLinesAndPartialUsageAreTolerated(t *testing.T) {
	path := writeTranscript(t,
		`not json at all`,
		promptLine,
		`{"type":"assistant","requestId":`, // truncated JSON: skipped
		// Usage without cache counters: cache sums must stay nil, never 0.
		`{"type":"assistant","requestId":"req-1","message":{"role":"assistant","model":"m","usage":{"input_tokens":11,"output_tokens":3}}}`,
		// Keyless usage-bearing entry: counted as its own call.
		`{"type":"assistant","message":{"role":"assistant","usage":{"input_tokens":4,"output_tokens":1}}}`,
	)
	u, ok := ReadTurnUsage(path)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if got := i64(t, u.InputTokens, "InputTokens"); got != 15 {
		t.Errorf("InputTokens = %d, want 15", got)
	}
	if got := i64(t, u.OutputTokens, "OutputTokens"); got != 4 {
		t.Errorf("OutputTokens = %d, want 4", got)
	}
	if u.CacheReadInputTokens != nil || u.CacheCreationInputTokens != nil {
		t.Errorf("cache counters = %v/%v, want nil/nil (never carried — unknown is not zero)",
			u.CacheReadInputTokens, u.CacheCreationInputTokens)
	}
	if u.APICallCount != 2 {
		t.Errorf("APICallCount = %d, want 2 (one keyed + one keyless)", u.APICallCount)
	}
	if _, ok := u.TotalTokens(); !ok {
		t.Error("TotalTokens() ok = false, want true (both halves known)")
	}
}

func TestReadTurnUsage_TailWindow(t *testing.T) {
	// Bounds are injected small so the branches are exercised without
	// multi-megabyte fixtures.
	longPad := strings.Repeat("x", 512)
	path := writeTranscript(t,
		`{"pad":"`+longPad+`"}`, // pushes the boundary+calls into the tail
		promptLine,
		assistantLine("req-1", "m", 10, 5, 0, 0),
	)

	// Window covers the whole file: normal extraction.
	if u, ok := readTurnUsage(path, 1<<20, 1<<20); !ok || *u.InputTokens != 10 {
		t.Fatalf("full window: got %+v ok=%v, want InputTokens=10 ok=true", u, ok)
	}

	// Window covers the boundary and the call but not the pad line: the
	// partial first line is discarded and extraction still succeeds.
	tail := int64(len(promptLine) + len(assistantLine("req-1", "m", 10, 5, 0, 0)) + 10)
	if u, ok := readTurnUsage(path, tail, 1<<20); !ok || *u.InputTokens != 10 {
		t.Fatalf("tail window: got %+v ok=%v, want InputTokens=10 ok=true", u, ok)
	}

	// Window too small to contain the user prompt boundary: the turn
	// cannot be bounded, so extraction honestly fails.
	if _, ok := readTurnUsage(path, 20, 1<<20); ok {
		t.Error("window without a boundary: ok = true, want false")
	}
}

func TestReadTurnUsage_OversizedLinesAreSkippedWhole(t *testing.T) {
	path := writeTranscript(t,
		promptLine,
		// Oversized assistant line (> maxLine): skipped, not truncated-parsed.
		`{"type":"assistant","requestId":"req-big","message":{"role":"assistant","usage":{"input_tokens":999,"output_tokens":999}},"pad":"`+strings.Repeat("y", 300)+`"}`,
		assistantLine("req-1", "m", 7, 2, 0, 0),
	)
	u, ok := readTurnUsage(path, 1<<20, 256)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if got := i64(t, u.InputTokens, "InputTokens"); got != 7 {
		t.Errorf("InputTokens = %d, want 7 (oversized line must contribute nothing)", got)
	}
	if u.APICallCount != 1 {
		t.Errorf("APICallCount = %d, want 1", u.APICallCount)
	}
}
