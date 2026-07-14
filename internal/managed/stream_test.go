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
// num_turns/result/total_cost_usd), the same format ADD §22.1 names as
// supported path 3 — they are NOT recordings of any real session (no real
// transcripts, costs, or session IDs; every value is a fixture value
// chosen for assertability). stream_unknown_lines.jsonl additionally
// contains one deliberately unknown `type` and one non-JSON line to prove
// the skip-not-crash contract.
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
