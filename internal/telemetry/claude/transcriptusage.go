// transcriptusage.go: per-turn token-usage extraction from a Claude Code
// session transcript (issue #72 proposal item 4 — the capture-before-model
// prerequisite shared by #66/#65/#42/#11). The Stop hook's stdin payload
// carries `transcript_path` (a documented hook-payload field), and the
// JSONL file behind it records one entry per API call the just-finished
// turn made, each carrying the provider's own exact `message.usage`
// accounting — the per-turn token actual that native hook mode otherwise
// has no source for (the statusline is session-cumulative).
//
// # Constitution §7 rule 4 posture ("undocumented transcripts are never
// parsed on a stable path")
//
// The transcript's internal JSONL shape is NOT a documented provider
// contract, so this reader is deliberately not a stable path: it is a
// best-effort, fail-open ENRICHMENT — every error (missing file, malformed
// or oversized lines, an unrecognized schema, no attributable turn) returns
// ok=false and the Stop hook proceeds byte-identically to the pre-#72
// behavior, with no payload fields added (unknown is not zero). Nothing
// downstream requires the fields to exist; a provider-side format change
// degrades capture coverage, never correctness. The lead's integration ADR
// (#72) records this trade-off.
//
// # Privacy (Constitution §7 rule 2)
//
// Only NUMBERS and a model identifier ever leave this file: token counts,
// an API-call count, and `message.model`. Prompt/completion text passes
// through transient JSON decoding (like ParseUserPromptSubmit's raw stdin)
// but is never copied into TurnUsage, never logged, never persisted — the
// content field is inspected solely for its block-type tags to classify a
// user entry as a prompt boundary vs. a tool result.
//
// # Attribution model
//
// The turn = every MAIN-CHAIN assistant entry after the transcript's last
// user PROMPT entry (a user entry that is a typed prompt, not a tool
// result and not a meta/compact-summary entry). Two deliberate choices:
//
//   - Main chain only: subagent (sidechain) activity is recorded in
//     separate files under <session>/subagents/, not in the session
//     transcript, so its API calls never appear here; the defensive
//     isSidechain filter below documents (and pins) that scope. The
//     prediction being calibrated (#42's forecast at UserPromptSubmit
//     time) is for the main-loop turn, so this is the matching actual.
//   - Dedupe by requestId: one API call streams as SEVERAL transcript
//     lines (one per content block), each repeating the SAME usage object
//     (verified against real 2.1.x transcripts: usage never differs across
//     lines of one requestId). Summing per line would multi-count; the
//     accumulator keeps the last usage seen per requestId and sums those,
//     which also yields api_call_count = distinct API calls.
package claude

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// TurnUsage is the numbers-only per-turn usage extracted from a session
// transcript. Token fields are pointers per the repository-wide rule: nil
// means the transcript's usage objects never carried that counter (an
// older provider version, say) — unknown, never a substituted zero.
type TurnUsage struct {
	InputTokens              *int64
	OutputTokens             *int64
	CacheReadInputTokens     *int64
	CacheCreationInputTokens *int64

	// APICallCount is the number of distinct API calls (unique requestIds,
	// plus any usage-bearing assistant entry that carried none) summed
	// into the totals above. Always >= 1 when ReadTurnUsage returns ok.
	APICallCount int64

	// ModelID is the last model identifier seen on the turn's own
	// usage-bearing assistant entries, "" when none carried one. Synthetic
	// placeholder entries (model "<synthetic>", used by the provider for
	// locally fabricated error messages) never set it.
	ModelID string
}

// TotalTokens returns input + output — the same per-turn total the
// managed-run usage event persists as total_tokens (managedUsageEvent's
// documented sum choice: cache traffic is context-window replay, not the
// turn's own fresh work volume; the raw cache counters are carried
// alongside so that choice stays revisitable). ok=false when either half
// is unknown: a total synthesized from one known and one unknown half
// would be a fabrication.
func (u TurnUsage) TotalTokens() (int64, bool) {
	if u.InputTokens == nil || u.OutputTokens == nil {
		return 0, false
	}
	return *u.InputTokens + *u.OutputTokens, true
}

// Read bounds. The turn being attributed sits at the transcript's tail, so
// a file larger than the window is scanned from (size - window) forward —
// bounded I/O regardless of transcript size, with per-line memory capped
// separately. If the window turns out not to contain the turn's user
// prompt boundary, extraction honestly fails (ok=false) rather than
// summing a partial turn.
const (
	transcriptTailWindowBytes int64 = 32 << 20 // 32 MiB scanned at most
	transcriptMaxLineBytes    int   = 8 << 20  // lines beyond this are skipped, not parsed
)

// syntheticTranscriptModel is the placeholder model the provider stamps on
// locally fabricated assistant entries (e.g. API-error notices); it is an
// artifact, not an observed identity, and never becomes ModelID.
const syntheticTranscriptModel = "<synthetic>"

// ReadTurnUsage extracts the last turn's usage from the transcript at
// path. ok=false means "nothing attributable was extracted" — for ANY
// reason (see the file doc comment's fail-open contract) — and the caller
// must add nothing to its event payload. It never returns an error by
// design: no transcript condition may fail the Stop hook.
func ReadTurnUsage(path string) (TurnUsage, bool) {
	return readTurnUsage(path, transcriptTailWindowBytes, transcriptMaxLineBytes)
}

// readTurnUsage is ReadTurnUsage with injectable bounds so tests can
// exercise the tail-window and oversized-line branches without
// multi-megabyte fixtures.
func readTurnUsage(path string, tailWindow int64, maxLine int) (TurnUsage, bool) {
	f, err := os.Open(path)
	if err != nil {
		return TurnUsage{}, false
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return TurnUsage{}, false
	}

	skipFirstLine := false
	if size := info.Size(); size > tailWindow {
		if _, err := f.Seek(size-tailWindow, io.SeekStart); err != nil {
			return TurnUsage{}, false
		}
		// The seek almost certainly landed mid-line; the first "line" read
		// is a fragment and must not be parsed.
		skipFirstLine = true
	}

	r := bufio.NewReaderSize(f, 64<<10)
	acc := turnAccumulator{calls: map[string]transcriptAPIUsage{}}
	for {
		line, tooLong, err := nextTranscriptLine(r, maxLine)
		if err != nil && !errors.Is(err, io.EOF) {
			return TurnUsage{}, false
		}
		if skipFirstLine {
			skipFirstLine = false
		} else if !tooLong && len(line) > 0 {
			// An oversized line is skipped whole rather than truncated-
			// parsed. Failure direction is disclosed and fail-open: a
			// skipped assistant line undercounts one API call; a skipped
			// prompt boundary merges this turn with the previous one only
			// if no later boundary exists — both degrade capture, never
			// the hook.
			acc.consume(line)
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	return acc.result()
}

// nextTranscriptLine reads one newline-delimited line, capping retained
// bytes at maxLine: a longer line is fully consumed from the reader but
// returned empty with tooLong=true. err is io.EOF exactly when the reader
// is exhausted (the final unterminated line, if any, is still returned).
func nextTranscriptLine(r *bufio.Reader, maxLine int) (line []byte, tooLong bool, err error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if !tooLong {
			if len(buf)+len(chunk) > maxLine {
				tooLong = true
				buf = nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue // mid-line; keep consuming
		}
		if len(buf) > 0 && buf[len(buf)-1] == '\n' {
			buf = buf[:len(buf)-1]
		}
		return buf, tooLong, err
	}
}

// transcriptEntry is the minimal projection of one transcript JSONL line —
// only the fields turn attribution needs. Content is retained transiently
// as raw JSON solely so isPromptBoundary can read its block-type tags; no
// content ever reaches TurnUsage.
type transcriptEntry struct {
	Type             string `json:"type"`
	IsSidechain      bool   `json:"isSidechain"`
	IsMeta           bool   `json:"isMeta"`
	IsCompactSummary bool   `json:"isCompactSummary"`
	RequestID        string `json:"requestId"`
	Message          *struct {
		Role    string              `json:"role"`
		Model   string              `json:"model"`
		Usage   *transcriptAPIUsage `json:"usage"`
		Content json.RawMessage     `json:"content"`
	} `json:"message"`
}

// transcriptAPIUsage mirrors the provider's message.usage token counters —
// the four classes #66's cache-aware costing needs. Pointers: an absent
// counter must stay unknown through the sum, never a fabricated zero.
type transcriptAPIUsage struct {
	InputTokens              *int64 `json:"input_tokens"`
	OutputTokens             *int64 `json:"output_tokens"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
}

// turnAccumulator folds transcript lines in stream order: every prompt
// boundary resets it, so at EOF it holds exactly the LAST turn's calls.
type turnAccumulator struct {
	sawBoundary bool
	calls       map[string]transcriptAPIUsage // final usage per API call (requestId)
	keyless     int                           // synthetic-key counter for usage without a requestId
	lastModel   string
}

func (a *turnAccumulator) consume(line []byte) {
	var e transcriptEntry
	if json.Unmarshal(line, &e) != nil {
		return // not a JSON object we understand: contributes nothing
	}
	if e.IsSidechain {
		return // main chain only — see the file doc comment
	}
	switch e.Type {
	case "user":
		if isPromptBoundary(e) {
			a.sawBoundary = true
			a.calls = map[string]transcriptAPIUsage{}
			a.keyless = 0
			a.lastModel = ""
		}
	case "assistant":
		if e.Message == nil || e.Message.Usage == nil {
			return // fabricated/error entry: no API call to account
		}
		key := e.RequestID
		if key == "" {
			// No requestId to dedupe on: count the entry as its own call
			// under a key no real requestId can collide with.
			a.keyless++
			key = fmt.Sprintf("\x00keyless-%d", a.keyless)
		}
		// Last write wins per requestId (observed identical across a
		// call's lines; last is the final accounting if that ever changes).
		a.calls[key] = *e.Message.Usage
		if m := e.Message.Model; m != "" && m != syntheticTranscriptModel {
			a.lastModel = m
		}
	}
}

// isPromptBoundary reports whether a user-typed entry starts a new turn: a
// main-chain user message that is not a tool result and not one of the
// provider's meta/compact-summary interpolations. String content is always
// a typed prompt; array content is a prompt (text and/or image blocks)
// unless it carries any tool_result block.
func isPromptBoundary(e transcriptEntry) bool {
	if e.IsMeta || e.IsCompactSummary {
		return false
	}
	if e.Message == nil || e.Message.Role != "user" || len(e.Message.Content) == 0 {
		return false
	}
	var s string
	if json.Unmarshal(e.Message.Content, &s) == nil {
		return true
	}
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(e.Message.Content, &blocks) != nil || len(blocks) == 0 {
		return false
	}
	for _, b := range blocks {
		if b.Type == "tool_result" {
			return false
		}
	}
	return true
}

// result sums the accumulated per-call usage into a TurnUsage. ok=false
// when no boundary was seen (the turn could not be bounded — e.g. it fell
// outside the tail window) or the bounded turn contains no usage-bearing
// API call: in either case there is nothing honest to report.
func (a *turnAccumulator) result() (TurnUsage, bool) {
	if !a.sawBoundary || len(a.calls) == 0 {
		return TurnUsage{}, false
	}
	u := TurnUsage{APICallCount: int64(len(a.calls)), ModelID: a.lastModel}
	for _, c := range a.calls {
		addTokens(&u.InputTokens, c.InputTokens)
		addTokens(&u.OutputTokens, c.OutputTokens)
		addTokens(&u.CacheReadInputTokens, c.CacheReadInputTokens)
		addTokens(&u.CacheCreationInputTokens, c.CacheCreationInputTokens)
	}
	return u, true
}

// addTokens folds one call's counter into the turn sum. A call that omits
// the counter contributes nothing to it; the sum stays nil until at least
// one call actually carried the field (unknown is not zero).
func addTokens(dst **int64, src *int64) {
	if src == nil {
		return
	}
	if *dst == nil {
		v := *src
		*dst = &v
		return
	}
	**dst += *src
}
