// codexstream.go: defensive line-by-line parsing of `codex exec --json`'s
// JSONL event stream (ADD §21.8) — the Codex analog of stream.go, under
// the identical fail-open discipline: one unrecognized line (a new event
// type, a malformed line) must degrade to a skip count, never take the
// whole run's telemetry down. The wire event vocabulary was verified
// against codex-cli v0.144.4's embedded serde schema (the binary's
// ThreadEvent tag set: thread.started / turn.started / turn.completed /
// turn.failed / item.started / item.updated / item.completed / error) and
// ADD §21.8's normalization list; the checked-in fixtures under
// testdata/provider-events/codex/exec are synthetic streams authored to
// exactly that schema (see codexstream_test.go for full provenance).
//
// Only the terminal turn events are modeled: turn.completed's `usage`
// object is where the run's exact per-turn token accounting lives (ADD
// §21.8 "`turn.completed.usage` separate token fields" — the managed-exec
// analog of claude's result-line usage), and turn.failed's error message
// is reduced to its byte length on this stack frame. item.* lines are
// counted but deliberately not decoded (Phase 1: no clean EventType
// mapping is emitted for them — see internal/telemetry/codex/
// managedexec.go's mapping table), and standalone `error` events are
// counted per ADD §21.7's "unknown notification ignore + metric"
// tolerance — the failure verdict belongs to the process exit code and
// the turn.failed event, never to a mid-stream diagnostic alone.
//
// # Privacy (Constitution §7 rule 2)
//
// Item payloads carry full agent/command text (agent_message text,
// command_execution command/aggregated_output). None of those fields are
// even named in the decode structs below, so the text is skipped inside
// encoding/json and never copied into any Go value this package returns —
// the same numbers-only projection internal/telemetry/codex's rollout
// reader applies. turn.failed's error message keeps only its byte length,
// the established length-only discipline for provider error text. Raw
// lines are relayed verbatim to the caller-supplied writer (the user's
// own terminal — display, not retention) and then dropped.
package managed

import (
	"encoding/json"
	"io"
	"strings"

	codextelemetry "github.com/huaiche94/auspex/internal/telemetry/codex"
)

// CodexStreamSummary is the accumulated, privacy-safe result of reading
// one managed `codex exec --json` run's stdout to EOF. Line counts are
// observations about the stream's shape; SkippedLines counts every line
// that was present but not understood — malformed JSON, an unknown
// `type`, or a missing `type` — per the fail-open contract in the file
// doc comment.
type CodexStreamSummary struct {
	ThreadStartedLines int
	TurnStartedLines   int
	// ItemLines counts item.started/item.updated/item.completed lines —
	// recognized so a routine tool-using run does not report a wall of
	// skips, but not decoded further (Phase 1; the same posture stream.go
	// takes for assistant/user lines).
	ItemLines int
	// ErrorLines counts standalone `error` events: ignore + metric (ADD
	// §21.7). The count is surfaced on the terminal event payload; it is
	// never by itself a failure verdict.
	ErrorLines   int
	SkippedLines int

	// ThreadID is codex's own thread identifier from the thread.started
	// event ("" when the stream never carried one — unknown is not zero,
	// never fabricated). Captured verbatim: it is the provider-side
	// session locator a future `codex exec resume` integration would need
	// (out of Phase-1 scope, declared so in internal/providers/codex).
	// Last one wins should a stream ever carry several, mirroring
	// stream.go's Model convention.
	ThreadID string

	// Completed is the last turn.completed event observed, nil when the
	// stream ended without one (provider crashed mid-stream) — a missing
	// terminal event yields NO usage attribution rather than fabricated
	// zeros. Last one wins, mirroring stream.go's Result convention.
	Completed *CodexTurnCompleted
	// Failed is the last turn.failed event observed, nil when none.
	Failed *CodexTurnFailed
}

// CodexTurnCompleted is the decoded turn.completed event.
type CodexTurnCompleted struct {
	// Usage is the event's `usage` token accounting under Codex's OWN
	// wire semantics (input_tokens INCLUDES the cached portion —
	// codextelemetry.TokenUsage's documented contract; normalization to
	// the shared vocabulary happens in internal/telemetry/codex, never
	// here). nil when the event carried no usage object at all.
	Usage *codextelemetry.TokenUsage
}

// CodexTurnFailed is the decoded turn.failed event. MessageLen is the
// byte length of the event's error.message; the text itself is dropped on
// this stack frame (file doc comment). nil when the event carried no
// error message; a present-but-empty message is a genuine 0.
type CodexTurnFailed struct {
	MessageLen *int
}

// rawCodexLine mirrors the on-wire union of the exec JSONL line shapes
// this package recognizes. Decoding only the recognized fields makes
// unknown sibling fields free to ignore (encoding/json drops them), the
// same open-set tolerance rawStreamLine documents.
type rawCodexLine struct {
	Type     string                     `json:"type"`
	ThreadID string                     `json:"thread_id"`
	Usage    *codextelemetry.TokenUsage `json:"usage"`
	Error    *rawCodexFailure           `json:"error"`
}

// rawCodexFailure is turn.failed's `error` object; only the message is
// decoded, and only its length survives (privacy, file doc comment).
type rawCodexFailure struct {
	Message *string `json:"message"`
}

// readCodexStream consumes r line by line until EOF under exactly
// stream.go's read contract (scanStreamLines: read errors are EOF, relay
// is best-effort, no fixed line-length limit), folding each line into the
// summary.
func readCodexStream(r io.Reader, relay io.Writer) CodexStreamSummary {
	var summary CodexStreamSummary
	scanStreamLines(r, relay, summary.observeLine)
	return summary
}

// observeLine folds one raw line into the summary, per the file doc
// comment's fail-open contract. TrimSpace matches stream.go's CRLF
// tolerance.
func (s *CodexStreamSummary) observeLine(raw []byte) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return
	}

	var line rawCodexLine
	if err := json.Unmarshal([]byte(trimmed), &line); err != nil {
		s.SkippedLines++
		return
	}

	switch line.Type {
	case "thread.started":
		s.ThreadStartedLines++
		if line.ThreadID != "" {
			s.ThreadID = line.ThreadID
		}
	case "turn.started":
		s.TurnStartedLines++
	case "turn.completed":
		completed := CodexTurnCompleted{}
		if line.Usage != nil {
			// Copy the struct (not the rawCodexLine's pointer) so the
			// returned summary never aliases decode-scratch memory —
			// stream.go's exact convention.
			u := *line.Usage
			completed.Usage = &u
		}
		s.Completed = &completed
	case "turn.failed":
		failed := CodexTurnFailed{}
		if line.Error != nil && line.Error.Message != nil {
			n := len(*line.Error.Message)
			failed.MessageLen = &n
		}
		s.Failed = &failed
	case "item.started", "item.updated", "item.completed":
		s.ItemLines++
	case "error":
		s.ErrorLines++
	default:
		// Unknown or missing type: counted, never fatal — the provider
		// adding an event type must not break attribution the day it
		// ships (stream.go's exact open-set posture, ADD §21.7).
		s.SkippedLines++
	}
}
