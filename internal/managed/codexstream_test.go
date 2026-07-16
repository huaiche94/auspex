// codexstream_test.go: unit coverage for codexstream.go's defensive
// `codex exec --json` parsing, against the checked-in fixtures under
// testdata/provider-events/codex/exec (the repo-root fixture tree, where
// every other codex payload fixture lives).
//
// # Fixture provenance
//
// All five fixtures are SYNTHETIC JSONL streams hand-authored to ADD
// §21.8's normalization list plus codex-cli v0.144.4's embedded serde
// event schema (the binary's ThreadEvent tag set — thread.started,
// turn.started, turn.completed, turn.failed, item.started, item.updated,
// item.completed, error — and its TokenUsage field names input_tokens/
// cached_input_tokens/output_tokens/reasoning_output_tokens/total_tokens).
// They are NOT recordings of any real session (recording one would spend
// real provider quota): every number is a fixture value chosen for
// assertability, and every text field is a FIXTURE-* placeholder that
// exists solely so the privacy assertions below can prove non-retention.
// malformed.jsonl and unknown_fields.jsonl additionally prove the
// skip-not-crash contract (unknown event types interleaved, non-JSON
// lines, a type-mismatched usage object).
package managed

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readCodexExecFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "provider-events", "codex", "exec", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return string(b)
}

func TestReadCodexStream_NormalFixture(t *testing.T) {
	var relay strings.Builder
	fixture := readCodexExecFixture(t, "normal.jsonl")
	summary := readCodexStream(strings.NewReader(fixture), &relay)

	if summary.ThreadStartedLines != 1 || summary.TurnStartedLines != 1 || summary.ItemLines != 3 {
		t.Errorf("line counts = thread:%d turn:%d item:%d, want 1/1/3",
			summary.ThreadStartedLines, summary.TurnStartedLines, summary.ItemLines)
	}
	if summary.SkippedLines != 0 || summary.ErrorLines != 0 {
		t.Errorf("skipped/error lines = %d/%d, want 0/0", summary.SkippedLines, summary.ErrorLines)
	}
	if summary.ThreadID != "019f0000-3333-7aaa-8bbb-ccccdddd0201" {
		t.Errorf("ThreadID = %q, want the fixture's thread id", summary.ThreadID)
	}
	if summary.Failed != nil {
		t.Errorf("Failed = %+v, want nil on the happy path", summary.Failed)
	}
	completed := summary.Completed
	if completed == nil {
		t.Fatal("Completed is nil, want the parsed turn.completed event")
	}
	u := completed.Usage
	if u == nil {
		t.Fatal("Completed.Usage is nil, want the fixture's usage object")
	}
	// Codex's OWN wire semantics, verbatim: input includes cached, and
	// total is codex's cached-inclusive sum — normalization to the shared
	// vocabulary is the telemetry package's job, never the parser's.
	if *u.InputTokens != 4200 || *u.CachedInputTokens != 3072 ||
		*u.OutputTokens != 180 || *u.ReasoningOutputTokens != 64 || *u.TotalTokens != 4380 {
		t.Errorf("usage = %+v, want 4200/3072/180/64/4380 verbatim", u)
	}

	// Relay passthrough: every raw line verbatim (display surface).
	if relay.String() != fixture {
		t.Error("relay output differs from the fixture bytes — raw lines must pass through verbatim")
	}
}

func TestReadCodexStream_TurnFailedFixture(t *testing.T) {
	summary := readCodexStream(strings.NewReader(readCodexExecFixture(t, "turn_failed.jsonl")), nil)

	if summary.Completed != nil {
		t.Errorf("Completed = %+v, want nil (the fixture's turn never completed)", summary.Completed)
	}
	if summary.ErrorLines != 1 {
		t.Errorf("ErrorLines = %d, want 1 (the standalone error event is counted, not fatal)", summary.ErrorLines)
	}
	if summary.ItemLines != 2 || summary.SkippedLines != 0 {
		t.Errorf("item/skipped = %d/%d, want 2/0", summary.ItemLines, summary.SkippedLines)
	}
	failed := summary.Failed
	if failed == nil {
		t.Fatal("Failed is nil, want the parsed turn.failed event")
	}
	if failed.MessageLen == nil || *failed.MessageLen != len("FIXTURE-TURN-FAILED-MESSAGE-77") {
		t.Errorf("Failed.MessageLen = %v, want %d (length only, never the text)", failed.MessageLen, len("FIXTURE-TURN-FAILED-MESSAGE-77"))
	}
}

func TestReadCodexStream_MalformedFixture_SkipsNotCrashes(t *testing.T) {
	summary := readCodexStream(strings.NewReader(readCodexExecFixture(t, "malformed.jsonl")), nil)

	// The non-JSON line and the type-mismatched usage line are skipped;
	// everything after them still parses (one line's damage stays one
	// line's damage).
	if summary.SkippedLines != 2 {
		t.Errorf("SkippedLines = %d, want 2", summary.SkippedLines)
	}
	if summary.Completed == nil || summary.Completed.Usage == nil {
		t.Fatal("Completed/Usage is nil — the good turn.completed after the malformed lines was lost")
	}
	if *summary.Completed.Usage.InputTokens != 1500 {
		t.Errorf("InputTokens = %d, want 1500 (the LAST well-formed turn.completed wins)", *summary.Completed.Usage.InputTokens)
	}
}

func TestReadCodexStream_MissingFieldsFixture_UnknownIsNotZero(t *testing.T) {
	summary := readCodexStream(strings.NewReader(readCodexExecFixture(t, "missing_fields.jsonl")), nil)

	if summary.ThreadStartedLines != 1 || summary.ThreadID != "" {
		t.Errorf("thread lines/id = %d/%q, want 1/\"\" (the event was seen, its id was not)", summary.ThreadStartedLines, summary.ThreadID)
	}
	if summary.Completed == nil {
		t.Fatal("Completed is nil, want the bare turn.completed event")
	}
	if summary.Completed.Usage != nil {
		t.Errorf("Usage = %+v, want nil — a turn.completed with no usage object measures nothing", summary.Completed.Usage)
	}
}

func TestReadCodexStream_UnknownFieldsFixture_ToleratesOpenSet(t *testing.T) {
	summary := readCodexStream(strings.NewReader(readCodexExecFixture(t, "unknown_fields.jsonl")), nil)

	// The two unknown event TYPES are counted as skips; unknown FIELDS on
	// known events are free to ignore.
	if summary.SkippedLines != 2 {
		t.Errorf("SkippedLines = %d, want 2 (thread.goal.updated + turn.diff)", summary.SkippedLines)
	}
	if summary.ThreadStartedLines != 1 || summary.TurnStartedLines != 1 || summary.ItemLines != 1 {
		t.Errorf("thread/turn/item lines = %d/%d/%d, want 1/1/1",
			summary.ThreadStartedLines, summary.TurnStartedLines, summary.ItemLines)
	}
	u := summary.Completed.Usage
	if u == nil {
		t.Fatal("Usage is nil, want the fixture's usage object despite the sibling unknown field")
	}
	// cached=0 is a GENUINE zero here (present in the fixture), distinct
	// from the missing_fields case's nil.
	if *u.InputTokens != 2000 || *u.CachedInputTokens != 0 || *u.OutputTokens != 100 {
		t.Errorf("usage = %+v, want 2000/0/100", u)
	}
}

// TestReadCodexStream_Privacy_NoTextRetained pins the parser's
// numbers-only projection (Constitution §7 rule 2): item text, command
// strings, aggregated output, and error messages from the fixtures must
// never appear anywhere in the returned summary — the same
// nothing-decodes-text guarantee internal/telemetry/codex's rollout
// reader pins against its with_message_text fixture.
func TestReadCodexStream_Privacy_NoTextRetained(t *testing.T) {
	needles := []struct{ fixture, needle string }{
		{"normal.jsonl", "FIXTURE-AGENT-TEXT-42"},
		{"normal.jsonl", "echo 21+21"},
		{"turn_failed.jsonl", "FIXTURE-TURN-FAILED-MESSAGE-77"},
		{"turn_failed.jsonl", "FIXTURE-STREAM-ERROR-500"},
	}
	for _, tc := range needles {
		fixture := readCodexExecFixture(t, tc.fixture)
		if !strings.Contains(fixture, tc.needle) {
			t.Fatalf("privacy fixture table is stale: %s no longer contains needle %q — update the table", tc.fixture, tc.needle)
		}
		summary := readCodexStream(strings.NewReader(fixture), nil)
		serialized, err := json.Marshal(summary)
		if err != nil {
			t.Fatalf("marshal summary: %v", err)
		}
		if strings.Contains(string(serialized), tc.needle) {
			t.Errorf("summary for %s retains raw text %q: %s", tc.fixture, tc.needle, serialized)
		}
	}
}
