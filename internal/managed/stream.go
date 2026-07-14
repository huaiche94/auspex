// stream.go: defensive line-by-line parsing of Claude Code's `--output-
// format stream-json` output (ADD §22.1 supported path 3). The parsing
// posture mirrors internal/providers/claude/statusline.go's fail-open
// discipline exactly, for the same reason issue #27 documented there: one
// unrecognized line — a new event type the provider adds, a malformed
// line, a line too large to be interesting — must degrade to a skip
// count, never take the whole run's telemetry down. The runner's job is
// to keep the user's provider turn moving and attribute its outcome; a
// parse gap is a counted, visible degradation, not a crash.
//
// Only the `result` line's fields are modeled (that is where the MVP's
// outcome attribution lives — total_cost_usd/duration_ms/num_turns/
// is_error, ADD §8.7 "exact completed usage"); system/assistant/user
// lines are recognized and counted but not decoded further. Per-message
// live usage modeling is issue #8's continuous-runway increment, not this
// one (see doc.go).
//
// # Privacy (Constitution §7 rule 2)
//
// The result line's `result` text and the assistant messages' content are
// NEVER retained on any returned struct — StreamResult keeps only the
// result text's byte length, the same length-only discipline
// internal/hooks/claude applies to provider error messages. The raw lines
// are relayed verbatim to the caller-supplied writer (the user's own
// terminal — display, not retention) and then dropped.
package managed

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// StreamSummary is the accumulated, privacy-safe result of reading one
// managed run's stream-json output to EOF. Line counts are observations
// about the stream's shape (used by tests and future diagnostics);
// SkippedLines counts every line that was present but not understood —
// malformed JSON, an unknown `type`, or a missing `type` — per the
// fail-open parsing contract in the file doc comment.
type StreamSummary struct {
	SystemLines    int
	AssistantLines int
	// UserLines counts "user" lines (tool results echoed back into the
	// transcript in --verbose mode) — recognized so a routine tool-using
	// run does not report a wall of skips, but not decoded further.
	UserLines    int
	SkippedLines int

	// Result is the parsed terminal `result` line, nil when the stream
	// ended without one (provider crashed mid-stream, or an older CLI) —
	// unknown is not zero: a missing result line yields NO usage
	// attribution rather than fabricated zeros. If multiple result lines
	// ever appear, the last one wins (the terminal line is the outcome).
	Result *StreamResult
}

// StreamResult is the decoded `result` line. Every measured field is a
// pointer: nil means the field was absent from the line (older CLI,
// partial error line), never a substituted zero.
type StreamResult struct {
	Subtype       string
	IsError       *bool
	DurationMs    *int64
	DurationAPIMs *int64
	NumTurns      *int64
	TotalCostUSD  *float64
	// ResultTextLen is the byte length of the line's `result` text; the
	// text itself is dropped on this stack frame (file doc comment). nil
	// when the line carried no `result` field; a present-but-empty text
	// is a genuine 0.
	ResultTextLen *int
}

// rawStreamLine mirrors the on-wire union of the stream-json line shapes
// this package recognizes. Decoding only the recognized fields makes
// unknown sibling fields free to ignore (encoding/json drops them), the
// same tolerance rawStatusLine documents.
type rawStreamLine struct {
	Type          string   `json:"type"`
	Subtype       string   `json:"subtype"`
	IsError       *bool    `json:"is_error"`
	DurationMs    *int64   `json:"duration_ms"`
	DurationAPIMs *int64   `json:"duration_api_ms"`
	NumTurns      *int64   `json:"num_turns"`
	TotalCostUSD  *float64 `json:"total_cost_usd"`
	Result        *string  `json:"result"`
}

// readStream consumes r line by line until EOF (or a read error, which is
// treated as EOF — the process-level failure surfaces from the caller's
// Wait, not from here), relaying every raw line verbatim to relay when
// non-nil and folding each into the summary. bufio.Reader.ReadBytes is
// used instead of bufio.Scanner deliberately: assistant lines carry whole
// message bodies and can exceed any fixed token limit, and a Scanner
// buffer overflow would abort the WHOLE stream (the exact wholesale
// failure mode issue #27's statusline incident documents) rather than
// degrade one line.
func readStream(r io.Reader, relay io.Writer) StreamSummary {
	var summary StreamSummary
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if relay != nil {
				// Best-effort human display; a relay write failure must
				// not stop outcome attribution.
				_, _ = relay.Write(line)
			}
			summary.observeLine(line)
		}
		if err != nil {
			return summary
		}
	}
}

// observeLine folds one raw line into the summary, per the file doc
// comment's fail-open contract. TrimSpace (not just TrimSuffix of "\n")
// deliberately also strips a CR so CRLF-delimited output — a Windows
// provider build, or a fixture checked out with autocrlf — parses
// identically to LF output.
func (s *StreamSummary) observeLine(raw []byte) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return
	}

	var line rawStreamLine
	if err := json.Unmarshal([]byte(trimmed), &line); err != nil {
		s.SkippedLines++
		return
	}

	switch line.Type {
	case "system":
		s.SystemLines++
	case "assistant":
		s.AssistantLines++
	case "user":
		s.UserLines++
	case "result":
		res := StreamResult{
			Subtype:       line.Subtype,
			IsError:       line.IsError,
			DurationMs:    line.DurationMs,
			DurationAPIMs: line.DurationAPIMs,
			NumTurns:      line.NumTurns,
			TotalCostUSD:  line.TotalCostUSD,
		}
		if line.Result != nil {
			n := len(*line.Result)
			res.ResultTextLen = &n
		}
		s.Result = &res
	default:
		// Unknown or missing type: counted, never fatal (the provider
		// adding a line type must not break attribution the day it
		// ships — the same open-set posture issue #21 gave rate-limit
		// windows).
		s.SkippedLines++
	}
}
