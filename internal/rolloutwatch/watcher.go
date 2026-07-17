// Package rolloutwatch is the issue-#92 Codex rollout-tailing watcher: a
// poll-based scanner over $CODEX_HOME/sessions/YYYY/MM/DD/rollout-*.jsonl
// that guarantees usage capture from ANY Codex surface — the VS Code
// plugin, alpha runtimes that lag hook support, and subagent threads,
// which get rollout files but no hooks at all (36/79 rollouts on the #91
// recon machine) — independent of hook delivery.
//
// Division of labor: ALL rollout-format knowledge (line classification,
// numbers-only projection, privacy discipline) lives in
// internal/telemetry/codex (rolloutscan.go, beside the Stop hook's own
// rollout reader), and event persistence goes through the SAME
// provider-agnostic EventStore the hook path writes to. This package owns
// only the scanning mechanics: file discovery, per-file read offsets,
// bounded work per tick, and the poll loop.
//
// # Dedupe / restart design (no new state, no migration)
//
// Per-file offsets are in-memory only. Durability comes from determinism
// instead of persistence: every event the watcher emits derives its
// idempotency key from rollout CONTENT (session id + the provider's own
// turn_id, or the task_complete line's own timestamp when a legacy rollout
// carries no turn ids — see codex.NormalizeRolloutTurnComplete), so a
// restart simply re-scans recent files from byte 0 and every re-emitted
// event dedupes in the store (events.idempotency_key UNIQUE, insert-or-
// ignore). The re-scan is bounded by Config.Lookback: files whose mtime
// predates the lookback window are marked read-at-current-size on first
// sight and only their future appends are parsed. The same construction is
// what makes hook+watcher double-capture safe: the rollout's
// task_complete.turn_id IS the Stop hook payload's turn_id, so both paths
// produce identical keys for the same turn.
//
// # Fail-open contract
//
// Watching is telemetry enrichment, never a gate: every per-file error
// (unreadable file, malformed line, torn tail write, failed persist) is
// absorbed into ScanStats.Errors and retried naturally on a later tick —
// offsets only advance after a successful persist, so nothing is lost,
// and no error ever stops the loop.
package rolloutwatch

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	codexhooks "github.com/huaiche94/auspex/internal/hooks/codex"
	codextelemetry "github.com/huaiche94/auspex/internal/telemetry/codex"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// Persister is the narrow store seam the watcher writes through —
// the same shape as internal/orchestrator.EventPersister, redeclared
// locally (Go's small-consumer-side-interface convention) so this package
// depends on the store contract, not on the orchestrator. The production
// value is internal/telemetry/claude.EventStore, the exact instance the
// hook path persists through — that shared UNIQUE idempotency index is
// half of the dedupe-by-construction story.
type Persister interface {
	PersistAll(ctx context.Context, runner app.TxRunner, evs []v1.Event) error
}

// Defaults. Interval trades capture latency against wasted stats on idle
// machines — 20s keeps the watcher near-real-time (rollouts are appended
// per event) at ~3 stat sweeps a minute. Lookback bounds the cold-start /
// restart re-scan; 14 days comfortably covers a vacation gap while keeping
// the first pass small (rollouts are per-session files, tens of MiB at the
// extreme). MaxBytesPerTick bounds one tick's parse work; anything left
// over is picked up by the next tick via the offsets.
const (
	DefaultInterval        = 20 * time.Second
	DefaultLookback        = 14 * 24 * time.Hour
	DefaultMaxBytesPerTick = 32 << 20 // 32 MiB

	// maxLineBytes mirrors rolloutusage.go's per-line cap: longer lines
	// are consumed and skipped, never parsed or retained.
	maxLineBytes = 8 << 20
)

// Config carries the watcher's tunables. SessionsDir is required; zero
// values elsewhere take the documented defaults.
type Config struct {
	// SessionsDir is the Codex sessions root to scan
	// ($CODEX_HOME/sessions layout: YYYY/MM/DD/rollout-*.jsonl).
	SessionsDir string
	// Interval is the poll interval for Run (DefaultInterval when 0).
	Interval time.Duration
	// Lookback bounds which previously-unseen files are parsed from byte
	// 0: files whose mtime is older are skipped to their current size
	// (DefaultLookback when 0).
	Lookback time.Duration
	// MaxBytesPerTick caps the bytes parsed across all files in one scan
	// pass (DefaultMaxBytesPerTick when 0).
	MaxBytesPerTick int64
}

// Deps carries the watcher's collaborators. All four are required — the
// watcher's entire job is persisting normalized events, so unlike the
// hook path there is no meaningful nil-Persister degrade.
type Deps struct {
	Clock     domain.Clock
	IDs       domain.IDGenerator
	Persister Persister
	TxRunner  app.TxRunner
}

// ScanStats reports one scan pass, numbers only. EventsEmitted counts
// events handed to a PersistAll call that returned success — because the
// store deduplicates by idempotency key, this can exceed the number of
// rows actually inserted (a re-scan emits the same events again and the
// store ignores them; that is the design, not a leak).
type ScanStats struct {
	// FilesSeen is how many rollout files the discovery glob matched.
	FilesSeen int
	// FilesRead is how many files had appended bytes parsed this pass.
	FilesRead int
	// BytesRead is the total appended bytes parsed this pass.
	BytesRead int64
	// TurnsEmitted is how many task_complete boundaries produced events.
	TurnsEmitted int
	// EventsEmitted is how many events were successfully handed to the
	// store (see the type doc for dedupe semantics).
	EventsEmitted int
	// Errors counts files whose scan or persist failed this pass; their
	// offsets did not advance and the next tick retries.
	Errors int
	// Deferred counts files with pending appends left unread because the
	// pass's byte budget ran out; the next tick continues from their
	// offsets.
	Deferred int
}

// fileState is one rollout file's in-memory scan state. offset always
// points at a line boundary; the carry fields let a turn span ticks (its
// token_count lines may arrive in one tick and its task_complete in a
// later one).
type fileState struct {
	offset int64

	meta       codextelemetry.RolloutMeta
	metaSeen   bool // a session_meta line was decoded (first one wins)
	metaProbed bool // the lazy first-line probe ran (backlog-skipped files)

	// model is the latest turn_context model enum id, "" until seen.
	model string
	// pending is the last token_count snapshot since the current turn
	// began (task_started resets it; task_complete consumes it) — the
	// per-turn analog of ReadRolloutSnapshot's "last token_count wins".
	pending *codextelemetry.RolloutSnapshot
}

// Watcher scans a Codex sessions tree and persists normalized usage
// events. Not safe for concurrent use: one Watcher belongs to one
// goroutine (Run's loop, or a caller driving ScanOnce directly).
type Watcher struct {
	cfg   Config
	deps  Deps
	norm  *codextelemetry.Normalizer
	files map[string]*fileState
}

// New validates deps (fail-closed: a missing collaborator is a
// composition bug, the same discipline daemon.Worker applies) and applies
// Config defaults.
func New(cfg Config, deps Deps) (*Watcher, error) {
	if cfg.SessionsDir == "" || deps.Clock == nil || deps.IDs == nil || deps.Persister == nil || deps.TxRunner == nil {
		return nil, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "rolloutwatch: New requires Config.SessionsDir and all of Deps.Clock/IDs/Persister/TxRunner",
			Retryable: false,
		}
	}
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.Lookback <= 0 {
		cfg.Lookback = DefaultLookback
	}
	if cfg.MaxBytesPerTick <= 0 {
		cfg.MaxBytesPerTick = DefaultMaxBytesPerTick
	}
	return &Watcher{
		cfg:   cfg,
		deps:  deps,
		norm:  codextelemetry.NewNormalizer(deps.Clock, deps.IDs),
		files: map[string]*fileState{},
	}, nil
}

// Run executes ScanOnce on Config.Interval until ctx is cancelled,
// returning ctx.Err() (the caller decides whether cancellation is an
// error — the CLI treats it as a clean shutdown). onScan, when non-nil,
// receives each pass's stats. Per-file errors never stop the loop; they
// are already absorbed into the stats per this package's fail-open
// contract.
func (w *Watcher) Run(ctx context.Context, onScan func(ScanStats)) error {
	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()
	for {
		stats := w.ScanOnce(ctx)
		if onScan != nil {
			onScan(stats)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Drain runs ScanOnce until the tree is caught up — no pass defers work
// and the last pass read nothing new — and returns the aggregated stats
// (FilesSeen/Deferred are the final pass's snapshot; the counters sum).
// This is the `--once` mode's engine: a fresh process has no offsets, so
// a single budget-bounded pass over a large backlog could exit before
// reaching every file, and a later fresh run would start over; draining
// inside one process lets the in-memory offsets carry progress across
// passes. Still bounded: total work ≤ the lookback window's appended
// bytes, each pass ≤ MaxBytesPerTick, and a pass that makes no progress
// (everything erroring, nothing readable) ends the drain rather than
// spinning.
func (w *Watcher) Drain(ctx context.Context) ScanStats {
	var total ScanStats
	for {
		pass := w.ScanOnce(ctx)
		total.FilesSeen = pass.FilesSeen
		total.Deferred = pass.Deferred
		total.FilesRead += pass.FilesRead
		total.BytesRead += pass.BytesRead
		total.TurnsEmitted += pass.TurnsEmitted
		total.EventsEmitted += pass.EventsEmitted
		total.Errors += pass.Errors
		if ctx.Err() != nil || pass.Deferred == 0 || pass.BytesRead == 0 {
			return total
		}
	}
}

// ScanOnce performs one bounded scan pass: discover rollout files, read
// each file's appended bytes (within the pass's byte budget), and persist
// the events those bytes complete. It never returns an error — every
// failure is a ScanStats.Errors increment and the next pass retries.
func (w *Watcher) ScanOnce(ctx context.Context) ScanStats {
	var stats ScanStats

	matches, err := filepath.Glob(filepath.Join(w.cfg.SessionsDir, "*", "*", "*", "rollout-*.jsonl"))
	if err != nil || len(matches) == 0 {
		w.prune(nil)
		return stats
	}
	sort.Strings(matches)
	stats.FilesSeen = len(matches)

	cutoff := w.deps.Clock.Now().Add(-w.cfg.Lookback)
	budget := w.cfg.MaxBytesPerTick

	seen := make(map[string]bool, len(matches))
	for _, path := range matches {
		seen[path] = true
		if ctx.Err() != nil {
			break
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}

		st, known := w.files[path]
		if !known {
			st = &fileState{}
			if info.ModTime().Before(cutoff) {
				// Backlog older than the lookback window: never parsed.
				// Only future appends will be, with attribution lazily
				// recovered from the file's leading session_meta line.
				st.offset = info.Size()
			}
			w.files[path] = st
		}
		if info.Size() == st.offset {
			continue
		}
		if budget <= 0 {
			stats.Deferred++
			continue
		}

		if st.offset > 0 && !st.metaSeen && !st.metaProbed {
			// First growth of a backlog-skipped (or truncation-reset)
			// file: recover attribution from line 1, which is always the
			// session_meta line, without parsing the skipped backlog.
			w.probeLeadingMeta(path, st)
		}

		res, scanErr := w.scanFile(ctx, path, st, budget)
		budget -= res.bytesRead
		stats.BytesRead += res.bytesRead
		if res.bytesRead > 0 {
			stats.FilesRead++
		}
		if res.deferred {
			stats.Deferred++
		}
		if scanErr != nil {
			stats.Errors++
			continue
		}
		stats.TurnsEmitted += res.turns
		stats.EventsEmitted += res.events
	}

	w.prune(seen)
	return stats
}

// prune drops state for files the discovery pass no longer sees (deleted
// or moved rollouts), keeping the state map proportional to the live
// tree. seen == nil means "nothing matched".
func (w *Watcher) prune(seen map[string]bool) {
	for path := range w.files {
		if !seen[path] {
			delete(w.files, path)
		}
	}
}

// probeLeadingMeta reads only the file's first line looking for the
// session_meta attribution. Best-effort and once-only per file
// (metaProbed): any failure just leaves attribution to the filename-derived
// session id.
func (w *Watcher) probeLeadingMeta(path string, st *fileState) {
	st.metaProbed = true
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReaderSize(f, 64<<10)
	line, _, terminated, tooLong, _ := readLine(r, maxLineBytes)
	if !terminated || tooLong || len(line) == 0 {
		return
	}
	if rl, ok := codextelemetry.DecodeRolloutLine(line); ok && rl.Meta != nil {
		st.meta = *rl.Meta
		st.metaSeen = true
	}
}

// fileScanResult is scanFile's per-pass accounting. deferred reports that
// the pass's byte budget ran out with readable bytes still remaining in
// THIS file — the caller counts it so ScanStats.Deferred honestly covers
// partially-read files, not just files never reached (Drain's stop
// condition depends on that honesty).
type fileScanResult struct {
	bytesRead int64
	turns     int
	events    int
	deferred  bool
}

// scanFile parses the file's appended complete lines (up to budget bytes),
// persists the events they produce in ONE transaction, and only then
// commits the advanced offset and carry state — so a failed persist
// re-parses the same bytes next tick and the deterministic idempotency
// keys make the retry loss-free and duplicate-free. An unterminated final
// line (a torn tail write racing the Codex process) is left unconsumed for
// the next tick.
func (w *Watcher) scanFile(ctx context.Context, path string, st *fileState, budget int64) (fileScanResult, error) {
	var res fileScanResult

	f, err := os.Open(path)
	if err != nil {
		return res, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return res, errors.New("rolloutwatch: not a regular file")
	}
	if info.Size() < st.offset {
		// The file shrank (rotation/replacement — not a documented Codex
		// behavior, but never trust an offset past EOF): reset and
		// re-scan from byte 0; idempotency keys absorb the re-emission.
		*st = fileState{}
	}
	if _, err := f.Seek(st.offset, io.SeekStart); err != nil {
		return res, err
	}

	// Work on a scratch copy so a failed persist rolls the carry state
	// back along with the offset.
	work := *st
	var events []v1.Event
	turns := 0

	r := bufio.NewReaderSize(f, 64<<10)
	var consumed int64
	exhausted := false
	for {
		if consumed >= budget {
			exhausted = true
			break
		}
		line, n, terminated, tooLong, rerr := readLine(r, maxLineBytes)
		if !terminated {
			// Torn tail write or EOF mid-line: do not consume; the next
			// tick re-reads from this line's start.
			break
		}
		consumed += int64(n)
		if !tooLong && len(line) > 0 {
			w.applyLine(&work, path, line, &events, &turns)
		}
		if rerr != nil {
			break
		}
	}
	res.bytesRead = consumed
	res.deferred = exhausted && st.offset+consumed < info.Size()

	if len(events) > 0 {
		if err := w.deps.Persister.PersistAll(ctx, w.deps.TxRunner, events); err != nil {
			return res, err
		}
	}
	work.offset = st.offset + consumed
	*st = work
	res.turns = turns
	res.events = len(events)
	return res, nil
}

// applyLine folds one classified rollout line into the scan state,
// appending the turn-terminal event set when the line is a task_complete
// boundary. Unclassifiable lines (content lines, malformed JSON, unknown
// shapes) contribute nothing.
func (w *Watcher) applyLine(st *fileState, path string, line []byte, events *[]v1.Event, turns *int) {
	rl, ok := codextelemetry.DecodeRolloutLine(line)
	if !ok {
		return
	}
	switch {
	case rl.Meta != nil:
		// First meta wins: a resumed/forked recording appends another
		// session_meta into the same file, but the file's identity (and
		// its filename id) is the original session's.
		if !st.metaSeen {
			st.meta = *rl.Meta
			st.metaSeen = true
		}
	case rl.TurnContext != nil:
		if rl.TurnContext.Model != "" {
			st.model = rl.TurnContext.Model
		}
	case rl.TokenCount != nil:
		st.pending = rl.TokenCount
	case rl.TaskStarted != nil:
		// Turn boundary: token_count lines before the task belong to
		// session setup/compaction, not to this turn.
		st.pending = nil
	case rl.TaskComplete != nil:
		snap := st.pending
		st.pending = nil

		sessionID := st.meta.SessionID
		if sessionID == "" {
			sessionID = sessionIDFromPath(path)
		}
		turnID := rl.TaskComplete.TurnID
		if sessionID == "" || (turnID == "" && rl.Timestamp.IsZero()) {
			// No attributable session, or no deterministic turn ref at
			// all (no provider turn_id AND no line timestamp): emitting
			// would risk key collisions or restart duplicates — skip,
			// fail-open.
			return
		}
		completedAt := rl.Timestamp
		if completedAt.IsZero() {
			completedAt = w.deps.Clock.Now()
		}

		stop := codexhooks.StopEvent{
			SessionID: domain.SessionID(sessionID),
			TurnID:    domain.TurnID(turnID),
		}
		if st.model != "" {
			model := st.model
			stop.Model = &model
		}
		attr := codextelemetry.RolloutAttribution{
			Originator:      st.meta.Originator,
			Surface:         st.meta.Surface,
			ParentSessionID: st.meta.ParentSessionID,
		}
		*events = append(*events, w.norm.NormalizeRolloutTurnComplete(stop, completedAt, snap, attr)...)
		*turns++
	}
}

// sessionIDFromPath derives the session id from the rollout filename
// (rollout-<timestamp>-<uuid>.jsonl) — the fallback for files whose
// session_meta line was skipped (backlog) or unreadable. "" when the name
// does not carry a plausible id.
func sessionIDFromPath(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	const uuidLen = 36
	if len(base) <= uuidLen || !strings.HasPrefix(base, "rollout-") {
		return ""
	}
	id := base[len(base)-uuidLen:]
	if strings.Count(id, "-") != 4 {
		return ""
	}
	return id
}

// readLine reads one newline-delimited line from r, capping retained bytes
// at maxLine (longer lines are consumed but returned empty with
// tooLong=true — rolloutusage.go's nextRolloutLine discipline, extended
// with the two accounting facts the offset tracker needs: how many bytes
// the line consumed, and whether it was actually newline-terminated).
// terminated=false means the reader ended mid-line; the returned count
// must NOT be committed to the offset.
func readLine(r *bufio.Reader, maxLine int) (line []byte, consumed int, terminated bool, tooLong bool, err error) {
	var buf []byte
	for {
		chunk, cerr := r.ReadSlice('\n')
		consumed += len(chunk)
		if !tooLong {
			if len(buf)+len(chunk) > maxLine {
				tooLong = true
				buf = nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		if errors.Is(cerr, bufio.ErrBufferFull) {
			continue // mid-line; keep consuming
		}
		if len(chunk) > 0 && chunk[len(chunk)-1] == '\n' {
			terminated = true
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
			}
		}
		return buf, consumed, terminated, tooLong, cerr
	}
}
