// stream_test.go: unit coverage for stream.go's defensive stream-json
// parsing, against the checked-in testdata/*.jsonl fixtures.
//
// # Fixture provenance
//
// testdata/stream_success.jsonl, stream_error.jsonl and
// stream_unknown_lines.jsonl are hand-authored to Claude Code CLI's
// PUBLIC stream-json output format (`claude -p --output-format
// stream-json --verbose`: system/assistant/user lines plus a terminal
// `result` line carrying subtype/is_error/duration_ms/duration_api_ms/
// num_turns/result/total_cost_usd/usage), the same format ADD §22.1 names
// as supported path 3 — they are NOT recordings of any real session (no
// real transcripts, costs, or session IDs; every value is a fixture value
// chosen for assertability). stream_unknown_lines.jsonl additionally
// contains one deliberately unknown `type` and one non-JSON line to prove
// the skip-not-crash contract.
//
// testdata/stream_recorded_hi.jsonl, by contrast, IS a real recording:
// the verbatim stdout of `claude -p "hi" --output-format stream-json
// --verbose`, recorded from claude CLI 2.1.201 on 2026-07-14 (owner-
// approved; a harmless "hi" exchange, IDs kept as-is). It is the ground
// truth the issue-#11 token capture was built against: the terminal
// `result` line carries `usage` with input_tokens/output_tokens/
// cache_read_input_tokens/cache_creation_input_tokens, the system `init`
// line carries `model`, and the assistant lines' own `usage` objects
// report output_tokens=2 mid-stream while the result line reports 157
// for the same message — the observed proof that per-message usage is a
// streaming snapshot and only the result line's totals are the turn's
// usage (see stream.go's file doc comment).
package managed

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readStreamFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return string(b)
}

func TestReadStream_SuccessFixture(t *testing.T) {
	var relay strings.Builder
	summary := readStream(strings.NewReader(readStreamFixture(t, "stream_success.jsonl")), &relay)

	if summary.SystemLines != 1 || summary.AssistantLines != 1 || summary.UserLines != 0 {
		t.Errorf("line counts = system:%d assistant:%d user:%d, want 1/1/0",
			summary.SystemLines, summary.AssistantLines, summary.UserLines)
	}
	if summary.SkippedLines != 0 {
		t.Errorf("SkippedLines = %d, want 0", summary.SkippedLines)
	}
	res := summary.Result
	if res == nil {
		t.Fatal("Result = nil, want the parsed terminal result line")
	}
	if res.Subtype != "success" {
		t.Errorf("Subtype = %q, want %q", res.Subtype, "success")
	}
	if res.IsError == nil || *res.IsError {
		t.Errorf("IsError = %v, want false", res.IsError)
	}
	if res.TotalCostUSD == nil || *res.TotalCostUSD != 0.0417 {
		t.Errorf("TotalCostUSD = %v, want 0.0417", res.TotalCostUSD)
	}
	if res.DurationMs == nil || *res.DurationMs != 2385 {
		t.Errorf("DurationMs = %v, want 2385", res.DurationMs)
	}
	if res.DurationAPIMs == nil || *res.DurationAPIMs != 2181 {
		t.Errorf("DurationAPIMs = %v, want 2181", res.DurationAPIMs)
	}
	if res.NumTurns == nil || *res.NumTurns != 3 {
		t.Errorf("NumTurns = %v, want 3", res.NumTurns)
	}
	// The result text is 34 bytes ("Done. I added the requested test.");
	// only its length may survive parsing (Constitution §7 rule 2).
	if res.ResultTextLen == nil || *res.ResultTextLen != len("Done. I added the requested test.") {
		t.Errorf("ResultTextLen = %v, want %d", res.ResultTextLen, len("Done. I added the requested test."))
	}
	if res.Usage == nil {
		t.Fatal("Usage = nil, want the result line's token block")
	}
	if res.Usage.InputTokens == nil || *res.Usage.InputTokens != 2100 ||
		res.Usage.OutputTokens == nil || *res.Usage.OutputTokens != 350 ||
		res.Usage.CacheReadInputTokens == nil || *res.Usage.CacheReadInputTokens != 14000 ||
		res.Usage.CacheCreationInputTokens == nil || *res.Usage.CacheCreationInputTokens != 4200 {
		t.Errorf("Usage = %+v, want in/out/cache-read/cache-create 2100/350/14000/4200", res.Usage)
	}
	if summary.Model != "claude-sonnet-4-5" {
		t.Errorf("Model = %q, want the init line's claude-sonnet-4-5", summary.Model)
	}
	// Raw lines are relayed verbatim (the human display surface).
	if got, want := relay.String(), readStreamFixture(t, "stream_success.jsonl"); got != want {
		t.Errorf("relay mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestReadStream_ErrorFixture(t *testing.T) {
	summary := readStream(strings.NewReader(readStreamFixture(t, "stream_error.jsonl")), nil)

	res := summary.Result
	if res == nil {
		t.Fatal("Result = nil, want the parsed error result line")
	}
	if res.IsError == nil || !*res.IsError {
		t.Errorf("IsError = %v, want true", res.IsError)
	}
	if res.Subtype != "error_during_execution" {
		t.Errorf("Subtype = %q, want error_during_execution", res.Subtype)
	}
	// The error fixture's result line has NO `result` field: length must
	// be unknown (nil), not 0 — unknown is not zero.
	if res.ResultTextLen != nil {
		t.Errorf("ResultTextLen = %v, want nil for an absent result field", *res.ResultTextLen)
	}
	// … and no `usage` object either (an older CLI or a partial error
	// line): the whole block stays nil, never four fabricated zeros.
	if res.Usage != nil {
		t.Errorf("Usage = %+v, want nil for an absent usage object", res.Usage)
	}
}

// TestReadStream_RecordedRealSession parses the REAL recorded probe
// stream verbatim (provenance in the file doc comment: claude CLI
// 2.1.201, 2026-07-14) — the one test grounded in observed provider
// output rather than hand-authored fixtures, so a drift between the
// modeled shape and what the CLI actually emits fails HERE first.
func TestReadStream_RecordedRealSession(t *testing.T) {
	summary := readStream(strings.NewReader(readStreamFixture(t, "stream_recorded_hi.jsonl")), nil)

	// 9 system lines (hook_started/hook_response/hook_progress/init), 2
	// assistant lines, and 1 rate_limit_event line — an out-of-taxonomy
	// type the fail-open contract counts as skipped, never fatal.
	if summary.SystemLines != 9 || summary.AssistantLines != 2 || summary.UserLines != 0 {
		t.Errorf("line counts = system:%d assistant:%d user:%d, want 9/2/0",
			summary.SystemLines, summary.AssistantLines, summary.UserLines)
	}
	if summary.SkippedLines != 1 {
		t.Errorf("SkippedLines = %d, want 1 (the rate_limit_event line)", summary.SkippedLines)
	}
	if summary.Model != "claude-fable-5" {
		t.Errorf("Model = %q, want claude-fable-5 (the recorded init line's model)", summary.Model)
	}

	res := summary.Result
	if res == nil {
		t.Fatal("Result = nil, want the recorded terminal result line")
	}
	if res.Subtype != "success" || res.IsError == nil || *res.IsError {
		t.Errorf("Subtype/IsError = %q/%v, want success/false", res.Subtype, res.IsError)
	}
	if res.DurationMs == nil || *res.DurationMs != 7758 ||
		res.DurationAPIMs == nil || *res.DurationAPIMs != 7615 ||
		res.NumTurns == nil || *res.NumTurns != 1 ||
		res.TotalCostUSD == nil || *res.TotalCostUSD != 0.160478 {
		t.Errorf("result figures = dur %v api %v turns %v cost %v, want 7758/7615/1/0.160478",
			res.DurationMs, res.DurationAPIMs, res.NumTurns, res.TotalCostUSD)
	}
	// The recorded result text is 262 bytes of UTF-8; only the length
	// survives parsing (Constitution §7 rule 2).
	if res.ResultTextLen == nil || *res.ResultTextLen != 262 {
		t.Errorf("ResultTextLen = %v, want 262", res.ResultTextLen)
	}
	// The token block — the exact fields issue #11's capture rides on,
	// with the values the CLI actually reported.
	if res.Usage == nil {
		t.Fatal("Usage = nil, want the recorded result line's usage block")
	}
	if res.Usage.InputTokens == nil || *res.Usage.InputTokens != 5322 ||
		res.Usage.OutputTokens == nil || *res.Usage.OutputTokens != 157 ||
		res.Usage.CacheReadInputTokens == nil || *res.Usage.CacheReadInputTokens != 14908 ||
		res.Usage.CacheCreationInputTokens == nil || *res.Usage.CacheCreationInputTokens != 4225 {
		t.Errorf("Usage = %+v, want in/out/cache-read/cache-create 5322/157/14908/4225", res.Usage)
	}
}

func TestReadStream_UnknownAndMalformedLinesAreSkippedNotFatal(t *testing.T) {
	summary := readStream(strings.NewReader(readStreamFixture(t, "stream_unknown_lines.jsonl")), nil)

	if summary.SkippedLines != 2 {
		t.Errorf("SkippedLines = %d, want 2 (one unknown type, one non-JSON line)", summary.SkippedLines)
	}
	if summary.UserLines != 1 {
		t.Errorf("UserLines = %d, want 1", summary.UserLines)
	}
	res := summary.Result
	if res == nil {
		t.Fatal("Result = nil — skipped lines must not prevent the result line from parsing")
	}
	if res.TotalCostUSD == nil || *res.TotalCostUSD != 0.0009 {
		t.Errorf("TotalCostUSD = %v, want 0.0009", res.TotalCostUSD)
	}
	// result:"" is a PRESENT, empty text: a genuine zero, distinct from
	// the error fixture's absent-field nil.
	if res.ResultTextLen == nil || *res.ResultTextLen != 0 {
		t.Errorf("ResultTextLen = %v, want 0 for a present-but-empty result field", res.ResultTextLen)
	}
}

// TestReadStream_CRLFAndFinalLineWithoutNewline constructs the stream
// in-memory (never a byte-exact git round trip — CRLF handling must not
// depend on checkout line-ending config, per this repo's windows-latest
// CI discipline): CRLF-delimited lines and a final line with no trailing
// newline must parse identically to plain LF input.
func TestReadStream_CRLFAndFinalLineWithoutNewline(t *testing.T) {
	lines := []string{
		`{"type":"system","subtype":"init","session_id":"s"}`,
		`{"type":"result","subtype":"success","is_error":false,"duration_ms":10,"num_turns":1,"total_cost_usd":0.002,"session_id":"s"}`,
	}
	summary := readStream(strings.NewReader(strings.Join(lines, "\r\n")), nil)

	if summary.SystemLines != 1 {
		t.Errorf("SystemLines = %d, want 1", summary.SystemLines)
	}
	if summary.SkippedLines != 0 {
		t.Errorf("SkippedLines = %d, want 0 — CRLF must not corrupt line parsing", summary.SkippedLines)
	}
	if summary.Result == nil || summary.Result.TotalCostUSD == nil || *summary.Result.TotalCostUSD != 0.002 {
		t.Fatalf("Result = %+v, want a parsed result line from the final, newline-less line", summary.Result)
	}
}

func TestReadStream_EmptyStreamHasNoResult(t *testing.T) {
	summary := readStream(strings.NewReader(""), nil)
	if summary.Result != nil {
		t.Fatalf("Result = %+v, want nil for an empty stream", summary.Result)
	}
	if summary.SkippedLines != 0 {
		t.Errorf("SkippedLines = %d, want 0", summary.SkippedLines)
	}
}
