// toolops.go: per-turn file-operation aggregation (issue #67 slice 3a,
// approved by ADR-052) — the five aggregates of research doc §7.3 stamped
// on provider.turn.completed:
//
//	distinct_files_touched, total_file_ops, repeated_ops,
//	repeat_rate (omitted when total_file_ops = 0), max_ops_on_one_file
//
// # Where path identity is resolved (the design's load-bearing decision)
//
// ADR-052's binding privacy invariant: raw file paths are never persisted
// in any form — not raw, not hashed; a path is interned to an opaque
// per-turn ordinal in PROCESS MEMORY for counting and discarded. Each
// PostToolUse hook invocation is a separate short-lived process, so
// cross-invocation interning is impossible without persisting something
// path-derived — which the invariant forbids (a keyed digest is still a
// hash; a crash mid-turn would strand it durably). Identity must
// therefore be resolved inside ONE process, and the only process that can
// see the whole turn is the Stop hook: it replays the just-ended turn's
// tool_use entries from the session transcript — the SAME already-read
// enrichment source #72's usage extraction established (ADR-051 covered
// the transcript as a source; this reads a different field of it on the
// same fail-open, undocumented-surface terms, Constitution §7 rule 4) —
// interning paths in memory and keeping only the counts.
//
// The PostToolUse hook remains the capture step (§7.2 "native
// PostToolUse (primary)"): each invocation records one counter increment
// in the toolop_scratch table (migration 0011 — ordinals/counters only,
// no path bytes CAN exist there), which (a) gates stamping on the capture
// actually having run — no registered hook, no aggregate fields, absence
// stays honest — and (b) supplies an exact hook-observed total_file_ops
// when the transcript replay is unavailable, so capture degrades to a
// partial (total-only) aggregate rather than silently disagreeing
// numbers. When the replay succeeds, all five fields come from it — one
// internally consistent set (the replay is main-chain only, matching the
// usage extraction's attribution model).
//
// # Aggregate definitions
//
//   - distinct_files_touched — number of distinct paths this turn
//   - total_file_ops — total view+modify ops (§7.2 classification:
//     view = Read; modify = Edit, Write, MultiEdit, NotebookEdit)
//   - repeated_ops — ops that REVISIT an already-touched file: a file
//     touched k times contributes k-1, so repeated_ops =
//     total_file_ops - distinct_files_touched (e.g. 3 ops on 2 distinct
//     files = 1 repeat, repeat_rate 1/3)
//   - repeat_rate = repeated_ops / total_file_ops, omitted when
//     total_file_ops = 0 (a rate over nothing is not a measurement)
//   - max_ops_on_one_file — the worst single-file churn
package claude

import (
	"encoding/json"

	claudehooks "github.com/huaiche94/auspex/internal/hooks/claude"
)

// TurnToolOps is the numbers-only per-turn file-operation aggregate.
// Fields are pointers per the repository-wide rule: nil means that
// aggregate could not be measured (unknown, never a substituted zero).
// The full-replay path sets all four; the hook-count degrade path sets
// TotalFileOps only.
type TurnToolOps struct {
	DistinctFilesTouched *int64
	TotalFileOps         *int64
	RepeatedOps          *int64
	MaxOpsOnOneFile      *int64
}

// RepeatRate returns repeated_ops / total_file_ops. ok=false when either
// half is unknown or total_file_ops is 0 — per §7.3 the rate is omitted,
// not fabricated, on an op-less turn.
func (o TurnToolOps) RepeatRate() (float64, bool) {
	if o.RepeatedOps == nil || o.TotalFileOps == nil || *o.TotalFileOps <= 0 {
		return 0, false
	}
	return float64(*o.RepeatedOps) / float64(*o.TotalFileOps), true
}

// HookCountedToolOps builds the transcript-less degrade aggregate: the
// PostToolUse hook's own exact invocation count as total_file_ops, with
// the identity-dependent fields honestly absent (distinct/repeated/max
// need per-file identity, which without a readable transcript exists
// nowhere the privacy invariant permits).
func HookCountedToolOps(totalFileOps int64) TurnToolOps {
	return TurnToolOps{TotalFileOps: &totalFileOps}
}

// ReadTurnToolOps replays the LAST turn's file-touching tool_use entries
// from the session transcript at path and reduces them to the five-field
// aggregate. ok=false means the turn's ops could not be replayed — for
// ANY reason (missing/unreadable file, no in-window prompt boundary, an
// unrecognized schema) — and the caller falls back to the hook-counted
// degrade. Fail-open by construction, exactly like ReadTurnUsage: no
// transcript condition may fail the Stop hook.
//
// ok=true with all-zero counts is a real measurement: the turn was
// bounded and its main chain performed no file operations.
func ReadTurnToolOps(path string) (TurnToolOps, bool) {
	return readTurnToolOps(path, transcriptTailWindowBytes, transcriptMaxLineBytes)
}

// readTurnToolOps is ReadTurnToolOps with injectable bounds (tests).
func readTurnToolOps(path string, tailWindow int64, maxLine int) (TurnToolOps, bool) {
	acc := newToolOpsAccumulator()
	if !scanTranscriptTail(path, tailWindow, maxLine, acc.consume) {
		return TurnToolOps{}, false
	}
	return acc.result()
}

// toolOpsAccumulator folds transcript lines in stream order: every prompt
// boundary resets it, so at EOF it holds exactly the LAST turn's file
// operations. ordinals is the §7.3 intern map — path → opaque per-turn
// ordinal — which lives only in this process's memory and is discarded
// with the accumulator; only opsByOrdinal's counts reach the result.
type toolOpsAccumulator struct {
	sawBoundary  bool
	ordinals     map[string]int  // path -> ordinal (in-memory only, never persisted)
	opsByOrdinal []int64         // ops count per ordinal
	seenBlocks   map[string]bool // tool_use block IDs already counted (defensive re-stream dedupe)
}

func newToolOpsAccumulator() *toolOpsAccumulator {
	return &toolOpsAccumulator{
		ordinals:   map[string]int{},
		seenBlocks: map[string]bool{},
	}
}

// transcriptToolUseBlock is the minimal projection of one assistant
// content block: enough to recognize a tool_use, dedupe it, classify its
// tool, and read its file target. Like the hook parser's projection
// (claudehooks.ParsePostToolUse), content-bearing input fields are never
// extracted.
type transcriptToolUseBlock struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input *struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	} `json:"input"`
}

func (a *toolOpsAccumulator) consume(line []byte) {
	var e transcriptEntry
	if json.Unmarshal(line, &e) != nil {
		return // not a JSON object we understand: contributes nothing
	}
	if e.IsSidechain {
		return // main chain only — matches transcriptusage.go's attribution model
	}
	switch e.Type {
	case "user":
		if isPromptBoundary(e) {
			a.sawBoundary = true
			a.ordinals = map[string]int{}
			a.opsByOrdinal = nil
			a.seenBlocks = map[string]bool{}
		}
	case "assistant":
		if e.Message == nil || len(e.Message.Content) == 0 {
			return
		}
		var blocks []transcriptToolUseBlock
		if json.Unmarshal(e.Message.Content, &blocks) != nil {
			return // string/unknown content shape: no tool_use to count
		}
		for _, b := range blocks {
			a.consumeBlock(b)
		}
	}
}

func (a *toolOpsAccumulator) consumeBlock(b transcriptToolUseBlock) {
	if b.Type != "tool_use" || b.Input == nil {
		return
	}
	if claudehooks.ClassifyToolOp(b.Name) == claudehooks.ToolOpIgnored {
		return
	}
	path := b.Input.FilePath
	if path == "" {
		path = b.Input.NotebookPath
	}
	if path == "" {
		return // not a file-targeted op
	}
	if b.ID != "" {
		// One API call streams as several transcript lines; a tool_use
		// block observed twice under the same ID is the same operation.
		if a.seenBlocks[b.ID] {
			return
		}
		a.seenBlocks[b.ID] = true
	}
	ordinal, ok := a.ordinals[path]
	if !ok {
		ordinal = len(a.opsByOrdinal)
		a.ordinals[path] = ordinal
		a.opsByOrdinal = append(a.opsByOrdinal, 0)
	}
	a.opsByOrdinal[ordinal]++
}

// result reduces the interned counts to the five-field aggregate.
// ok=false when no prompt boundary was seen (the turn could not be
// bounded — e.g. it fell outside the tail window): an unbounded replay
// might blend turns, so nothing honest can be reported.
func (a *toolOpsAccumulator) result() (TurnToolOps, bool) {
	if !a.sawBoundary {
		return TurnToolOps{}, false
	}
	var total, maxOps int64
	distinct := int64(len(a.opsByOrdinal))
	for _, ops := range a.opsByOrdinal {
		total += ops
		if ops > maxOps {
			maxOps = ops
		}
	}
	repeated := total - distinct
	return TurnToolOps{
		DistinctFilesTouched: &distinct,
		TotalFileOps:         &total,
		RepeatedOps:          &repeated,
		MaxOpsOnOneFile:      &maxOps,
	}, true
}
