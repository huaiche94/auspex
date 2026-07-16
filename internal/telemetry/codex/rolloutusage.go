// rolloutusage.go: per-turn token/quota/context extraction from a Codex
// session rollout file — issue #9 Phase 1's analog of
// internal/telemetry/claude/transcriptusage.go, under the same ADR-051
// posture. Codex hook payloads carry NO token fields at all; the per-turn
// actuals live in the session rollout JSONL
// ($CODEX_HOME/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl) as
// `{"type":"event_msg","payload":{"type":"token_count",...}}` lines whose
// info block carries total_token_usage (session-cumulative),
// last_token_usage (the just-finished request), and model_context_window,
// and whose rate_limits block carries the primary (5h) and secondary
// (weekly) rolling quota windows. The Stop hook's transcript_path points at
// exactly this file (codex-rs rust-v0.144.4: Session::hook_transcript_path
// returns current_rollout_path; nullable when no rollout has been
// materialized — FindRolloutPath covers that gap by session-id scan).
//
// # Constitution §7 rule 4 posture
//
// The rollout's internal JSONL shape is not a documented provider contract,
// so this reader is a best-effort, fail-open ENRICHMENT exactly like
// ADR-051's transcript reader: every error (missing file, malformed or
// oversized lines, no token_count line in the tail window) returns ok=false
// and the Stop hook proceeds with no usage/quota/context payload fields
// added (unknown is not zero). A provider-side format change degrades
// capture coverage, never correctness.
//
// # Privacy (Constitution §7 rule 2)
//
// Rollout files contain full message text (user_message/agent_message
// event_msg lines, response_item message lines). This reader decodes ONLY
// the token_count projection below — type tags, token counters, the context
// window size, and the rate-limit numbers/ids. No content field is even
// named in the decode structs, so message text is skipped inside
// encoding/json and never copied into any Go value this package returns
// (pinned by privacy_test.go against the with_message_text fixture).
package codex

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// TokenUsage mirrors one rollout token-usage object (total_token_usage /
// last_token_usage). Pointers per the repository-wide rule: nil means the
// counter was absent/null in the source — unknown, never a substituted
// zero. Note Codex's own semantics, which differ from Anthropic's:
// InputTokens INCLUDES the cached portion (CachedInputTokens is that
// subset), and TotalTokens is the provider's own input+output sum.
type TokenUsage struct {
	InputTokens           *int64 `json:"input_tokens"`
	CachedInputTokens     *int64 `json:"cached_input_tokens"`
	OutputTokens          *int64 `json:"output_tokens"`
	ReasoningOutputTokens *int64 `json:"reasoning_output_tokens"`
	TotalTokens           *int64 `json:"total_tokens"`
}

// RateLimitWindow is one rolling quota window from the rollout's
// rate_limits block. LimitID is Auspex's stable name for the window slot
// ("primary" = the 5-hour window, "secondary" = the weekly window — the
// rollout's own key names); WindowMinutes carries the provider's declared
// width so downstream never has to hardcode that mapping.
type RateLimitWindow struct {
	LimitID       string
	UsedPercent   *float64
	WindowMinutes *int64
	ResetsAt      *time.Time // epoch seconds on the wire
}

// RolloutSnapshot is the numbers-only projection of the LAST token_count
// line in the rollout's tail window: the state of the session's token and
// quota accounting as of the just-finished turn.
type RolloutSnapshot struct {
	// Last is the final request's own usage (last_token_usage) — the
	// per-turn actual. nil when the line carried none.
	Last *TokenUsage
	// Total is the session-cumulative usage (total_token_usage). nil when
	// the line carried none.
	Total *TokenUsage
	// ModelContextWindow is the model's context window size in tokens.
	ModelContextWindow *int64
	// RateLimits carries every quota window present (primary/secondary
	// today; sorted by LimitID). Windows that measured nothing are
	// omitted, not zeroed.
	RateLimits []RateLimitWindow
	// PlanType is the account plan identifier the rate_limits block
	// carried (an enum id like "pro"/"plus", never free text); "" when
	// absent.
	PlanType string
}

// Read bounds, mirroring transcriptusage.go's rationale: the token_count
// line being attributed sits at the rollout's tail, so a larger file is
// scanned from (size - window) forward — bounded I/O regardless of session
// length, with per-line memory capped separately.
const (
	rolloutTailWindowBytes int64 = 32 << 20 // 32 MiB scanned at most
	rolloutMaxLineBytes    int   = 8 << 20  // lines beyond this are skipped, not parsed
)

// ReadRolloutSnapshot extracts the last token_count observation from the
// rollout file at path. ok=false means "nothing extractable" — for ANY
// reason (see the file doc comment's fail-open contract) — and the caller
// must add nothing to its event payloads. It never returns an error by
// design: no rollout condition may fail the Stop hook.
func ReadRolloutSnapshot(path string) (RolloutSnapshot, bool) {
	return readRolloutSnapshot(path, rolloutTailWindowBytes, rolloutMaxLineBytes)
}

// readRolloutSnapshot is ReadRolloutSnapshot with injectable bounds so
// tests can exercise the tail-window and oversized-line branches without
// multi-megabyte fixtures.
func readRolloutSnapshot(path string, tailWindow int64, maxLine int) (RolloutSnapshot, bool) {
	f, err := os.Open(path)
	if err != nil {
		return RolloutSnapshot{}, false
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return RolloutSnapshot{}, false
	}

	skipFirstLine := false
	if size := info.Size(); size > tailWindow {
		if _, err := f.Seek(size-tailWindow, io.SeekStart); err != nil {
			return RolloutSnapshot{}, false
		}
		// The seek almost certainly landed mid-line; the first "line" read
		// is a fragment and must not be parsed.
		skipFirstLine = true
	}

	r := bufio.NewReaderSize(f, 64<<10)
	var last *rawTokenCount
	for {
		line, tooLong, err := nextRolloutLine(r, maxLine)
		if err != nil && !errors.Is(err, io.EOF) {
			return RolloutSnapshot{}, false
		}
		if skipFirstLine {
			skipFirstLine = false
		} else if !tooLong && len(line) > 0 {
			if tc, ok := decodeTokenCount(line); ok {
				last = tc // last one in the file wins: it is the turn-final accounting
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	if last == nil {
		return RolloutSnapshot{}, false
	}
	return last.snapshot(), true
}

// nextRolloutLine reads one newline-delimited line, capping retained bytes
// at maxLine: a longer line is fully consumed from the reader but returned
// empty with tooLong=true. err is io.EOF exactly when the reader is
// exhausted (the final unterminated line, if any, is still returned).
// Byte-for-byte the transcriptusage.go helper; duplicated rather than
// exported from internal/telemetry/claude because neither package's public
// API should grow a shared line-reading utility for two private call sites.
func nextRolloutLine(r *bufio.Reader, maxLine int) (line []byte, tooLong bool, err error) {
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

// rawTokenCount is the minimal projection of one token_count rollout line —
// numbers and ids only; no content field is named, so message text on other
// line shapes (or a future field on this one) is never decoded into memory
// this package retains.
type rawTokenCount struct {
	Info *struct {
		TotalTokenUsage    *TokenUsage `json:"total_token_usage"`
		LastTokenUsage     *TokenUsage `json:"last_token_usage"`
		ModelContextWindow *int64      `json:"model_context_window"`
	} `json:"info"`
	RateLimits *struct {
		Primary   *rawRateWindow `json:"primary"`
		Secondary *rawRateWindow `json:"secondary"`
		PlanType  *string        `json:"plan_type"`
	} `json:"rate_limits"`
}

type rawRateWindow struct {
	UsedPercent   *float64 `json:"used_percent"`
	WindowMinutes *int64   `json:"window_minutes"`
	ResetsAt      *float64 `json:"resets_at"` // epoch seconds
}

// decodeTokenCount decodes line if and only if it is an
// event_msg/token_count rollout line; every other line shape (session_meta,
// turn_context, response_item, other event_msg kinds, non-JSON garbage)
// contributes nothing.
func decodeTokenCount(line []byte) (*rawTokenCount, bool) {
	var envelope struct {
		Type    string `json:"type"`
		Payload *struct {
			Type string `json:"type"`
			rawTokenCount
		} `json:"payload"`
	}
	if json.Unmarshal(line, &envelope) != nil {
		return nil, false
	}
	if envelope.Type != "event_msg" || envelope.Payload == nil || envelope.Payload.Type != "token_count" {
		return nil, false
	}
	tc := envelope.Payload.rawTokenCount
	return &tc, true
}

// snapshot projects the raw decode into the exported RolloutSnapshot,
// dropping windows that measured nothing (absence means unknown, not zero
// usage — the same rule claudeprovider.ParseStatusLine applies to its
// rate-limit windows).
func (tc *rawTokenCount) snapshot() RolloutSnapshot {
	var snap RolloutSnapshot
	if tc.Info != nil {
		snap.Last = tc.Info.LastTokenUsage
		snap.Total = tc.Info.TotalTokenUsage
		snap.ModelContextWindow = tc.Info.ModelContextWindow
	}
	if tc.RateLimits != nil {
		appendWindow := func(limitID string, w *rawRateWindow) {
			if w == nil {
				return
			}
			var resetsAt *time.Time
			if w.ResetsAt != nil {
				t := time.Unix(int64(*w.ResetsAt), 0).UTC()
				resetsAt = &t
			}
			if w.UsedPercent == nil && resetsAt == nil {
				return // a window that measured nothing observes nothing
			}
			snap.RateLimits = append(snap.RateLimits, RateLimitWindow{
				LimitID:       limitID,
				UsedPercent:   w.UsedPercent,
				WindowMinutes: w.WindowMinutes,
				ResetsAt:      resetsAt,
			})
		}
		appendWindow("primary", tc.RateLimits.Primary)
		appendWindow("secondary", tc.RateLimits.Secondary)
		if tc.RateLimits.PlanType != nil {
			snap.PlanType = *tc.RateLimits.PlanType
		}
	}
	sort.Slice(snap.RateLimits, func(i, j int) bool {
		return snap.RateLimits[i].LimitID < snap.RateLimits[j].LimitID
	})
	return snap
}

// DefaultSessionsDir returns the Codex sessions root for this host:
// $CODEX_HOME/sessions when CODEX_HOME is set (Codex's own home override,
// which is also what lets tests and the E2E smoke isolate completely), else
// ~/.codex/sessions. ok=false when no home directory can be resolved —
// callers fail open, per this file's contract.
func DefaultSessionsDir() (string, bool) {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return filepath.Join(home, "sessions"), true
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, ".codex", "sessions"), true
}

// FindRolloutPath locates the rollout file for sessionID under sessionsDir
// (layout: YYYY/MM/DD/rollout-<timestamp>-<sessionID>.jsonl) — the fallback
// for the documented case where a hook payload's transcript_path is null
// (codex-rs: hook_transcript_path is None until the rollout is
// materialized, and always None for remote/cloud threads). When multiple
// files match (a resumed session can be re-recorded), the lexicographically
// greatest path wins — the filename embeds the start timestamp, so that is
// the newest recording. ok=false when nothing matches; fail-open as ever.
func FindRolloutPath(sessionsDir string, sessionID string) (string, bool) {
	if sessionsDir == "" || sessionID == "" {
		return "", false
	}
	matches, err := filepath.Glob(filepath.Join(sessionsDir, "*", "*", "*", "rollout-*-"+sessionID+".jsonl"))
	if err != nil || len(matches) == 0 {
		return "", false
	}
	sort.Strings(matches)
	return matches[len(matches)-1], true
}
