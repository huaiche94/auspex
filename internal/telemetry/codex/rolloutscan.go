// rolloutscan.go: incremental, line-at-a-time rollout decoding for the
// issue-#92 rollout-tailing watcher (internal/rolloutwatch) — the sibling
// of rolloutusage.go's tail-window snapshot reader. Where the Stop hook
// reads the rollout ONCE at turn end (ReadRolloutSnapshot), the watcher
// re-reads only appended bytes on a poll interval and needs to classify
// each new line itself: session_meta for attribution, turn_context for the
// model id, token_count for usage/quota/context numbers, and
// task_started/task_complete for turn boundaries. That format knowledge —
// and the privacy discipline that goes with it — belongs in THIS package,
// beside the existing rollout reader, so the watcher package handles only
// file discovery/offsets/persistence and never touches rollout JSON.
//
// # Privacy (Constitution §7 rule 2 — same posture as rolloutusage.go)
//
// Rollout files carry full conversation text (user_message/agent_message
// event_msg lines, response_item message lines) and task_complete lines
// carry last_agent_message (raw response text). No decode struct in this
// file names ANY content field, so encoding/json skips that text without
// copying it into any Go value this package returns: DecodeRolloutLine
// classifies a line by its type tags alone and extracts only ids,
// enums, timestamps, paths (cwd — the same allowance claude's turn.started
// payload has), and the token/quota numbers rolloutusage.go already
// projects. session_meta's base_instructions / agent_nickname / agent_path
// / git blocks are deliberately not named either — attribution needs only
// ids and enum labels (pinned by the watcher's privacy tests against the
// watch/with_message_text fixture).
package codex

import (
	"encoding/json"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	codexhooks "github.com/huaiche94/auspex/internal/hooks/codex"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// RolloutMeta is the numbers/ids-only projection of a rollout session_meta
// line: everything the watcher needs to attribute the file's events, and
// nothing else (no instructions, no agent nicknames/paths).
type RolloutMeta struct {
	// SessionID is the rollout's own session id (meta `id`) — the same id
	// the filename embeds and the same id Codex hook payloads carry as
	// session_id (verified against live capture: rollout task turn ids and
	// hook turn ids share one id space, and so do session ids).
	SessionID string
	// Originator is Codex's own client enum ("codex_vscode", "codex-tui",
	// "codex_cli_rs", ...); "" when absent.
	Originator string
	// Surface is Auspex's normalized launch-surface label derived from the
	// meta's source/thread_source/originator fields: "cli", "vscode", or
	// "subagent"; "" when none could be derived (unknown is not guessed).
	Surface string
	// ParentSessionID links a subagent rollout to the session that spawned
	// it (meta parent_thread_id, falling back to the source object's
	// thread_spawn.parent_thread_id); "" for top-level sessions.
	ParentSessionID string
	// CWD is the session's working directory — a path, same allowance as
	// the session-start hook payload's cwd.
	CWD string
}

// RolloutTask is the id-only projection of a task_started / task_complete
// event_msg line. TurnID is Codex's own turn id ("" on older rollouts that
// predate the field); the task_complete wire shape also carries
// last_agent_message, which is deliberately never decoded (see the file
// doc comment).
type RolloutTask struct {
	TurnID string
}

// RolloutTurnContext is the id-only projection of a turn_context line:
// the model enum id the upcoming turn runs with. Other turn_context fields
// (cwd, effort, approval policy...) are not needed by the watcher and are
// left undecoded.
type RolloutTurnContext struct {
	Model string
}

// RolloutLine is one classified rollout line. Exactly one of the pointer
// fields is non-nil for a line DecodeRolloutLine reports ok=true for;
// Timestamp is the line envelope's own timestamp (zero when absent or
// unparseable — callers must treat zero as unknown, never as 1970).
type RolloutLine struct {
	Timestamp    time.Time
	Meta         *RolloutMeta
	TurnContext  *RolloutTurnContext
	TokenCount   *RolloutSnapshot
	TaskStarted  *RolloutTask
	TaskComplete *RolloutTask
}

// DecodeRolloutLine classifies one rollout JSONL line into the watcher's
// numbers/ids-only projection. ok=false means the line contributes nothing
// to the watcher — malformed JSON, a content-bearing line
// (user_message/agent_message/response_item), or any other line shape —
// and per this package's fail-open contract the caller simply moves on.
//
// The decode is two-phase: a first pass reads only the envelope's
// timestamp/type discriminators (payload.type included), then the matching
// narrow struct decodes the one interesting payload shape. Content-bearing
// lines are rejected at phase one without any payload field being decoded.
func DecodeRolloutLine(line []byte) (RolloutLine, bool) {
	var envelope struct {
		Timestamp string `json:"timestamp"`
		Type      string `json:"type"`
		Payload   struct {
			Type string `json:"type"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &envelope) != nil {
		return RolloutLine{}, false
	}

	out := RolloutLine{}
	if ts, err := time.Parse(time.RFC3339, envelope.Timestamp); err == nil {
		out.Timestamp = ts.UTC()
	}

	switch envelope.Type {
	case "session_meta":
		meta, ok := decodeSessionMeta(line)
		if !ok {
			return RolloutLine{}, false
		}
		out.Meta = meta
		return out, true
	case "turn_context":
		var raw struct {
			Payload struct {
				Model *string `json:"model"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &raw) != nil {
			return RolloutLine{}, false
		}
		tc := &RolloutTurnContext{}
		if raw.Payload.Model != nil {
			tc.Model = *raw.Payload.Model
		}
		out.TurnContext = tc
		return out, true
	case "event_msg":
		switch envelope.Payload.Type {
		case "token_count":
			tc, ok := decodeTokenCount(line)
			if !ok {
				return RolloutLine{}, false
			}
			snap := tc.snapshot()
			out.TokenCount = &snap
			return out, true
		case "task_started", "task_complete":
			task, ok := decodeTaskEvent(line)
			if !ok {
				return RolloutLine{}, false
			}
			if envelope.Payload.Type == "task_started" {
				out.TaskStarted = task
			} else {
				out.TaskComplete = task
			}
			return out, true
		}
	}
	return RolloutLine{}, false
}

// decodeTaskEvent extracts the turn id from a task_started/task_complete
// line. The struct names ONLY turn_id: task_complete's last_agent_message
// (raw response text) is skipped inside encoding/json, never copied into a
// value this package retains — the same not-even-named discipline
// codexhooks.ParseStop applies to last_assistant_message.
func decodeTaskEvent(line []byte) (*RolloutTask, bool) {
	var raw struct {
		Payload struct {
			TurnID *string `json:"turn_id"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &raw) != nil {
		return nil, false
	}
	task := &RolloutTask{}
	if raw.Payload.TurnID != nil {
		task.TurnID = *raw.Payload.TurnID
	}
	return task, true
}

// rawMetaSource is session_meta's polymorphic `source` field: a plain
// string on top-level sessions ("cli", "vscode") and an object on
// subagent threads ({"subagent":{"thread_spawn":{"parent_thread_id":...}}}
// or {"subagent":{"other":...}}). The custom unmarshaler names only the
// subagent linkage fields — thread_spawn's agent_nickname/agent_path are
// skipped undecoded — and is fail-open: an unrecognized future shape
// contributes nothing rather than failing the line.
type rawMetaSource struct {
	label          string
	subagent       bool
	parentThreadID string
}

func (s *rawMetaSource) UnmarshalJSON(b []byte) error {
	var str string
	if json.Unmarshal(b, &str) == nil {
		s.label = str
		return nil
	}
	var obj struct {
		Subagent *struct {
			ThreadSpawn *struct {
				ParentThreadID *string `json:"parent_thread_id"`
			} `json:"thread_spawn"`
		} `json:"subagent"`
	}
	if json.Unmarshal(b, &obj) == nil && obj.Subagent != nil {
		s.subagent = true
		if obj.Subagent.ThreadSpawn != nil && obj.Subagent.ThreadSpawn.ParentThreadID != nil {
			s.parentThreadID = *obj.Subagent.ThreadSpawn.ParentThreadID
		}
	}
	return nil
}

// decodeSessionMeta projects a session_meta line into RolloutMeta. Only
// ids, enum labels, and the cwd path are named; base_instructions,
// agent_nickname, agent_path, git, and every other block are skipped
// undecoded.
func decodeSessionMeta(line []byte) (*RolloutMeta, bool) {
	var raw struct {
		Payload struct {
			ID             *string       `json:"id"`
			CWD            *string       `json:"cwd"`
			Originator     *string       `json:"originator"`
			ThreadSource   *string       `json:"thread_source"`
			ParentThreadID *string       `json:"parent_thread_id"`
			Source         rawMetaSource `json:"source"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &raw) != nil {
		return nil, false
	}
	p := raw.Payload

	meta := &RolloutMeta{}
	if p.ID != nil {
		meta.SessionID = *p.ID
	}
	if p.CWD != nil {
		meta.CWD = *p.CWD
	}
	if p.Originator != nil {
		meta.Originator = *p.Originator
	}
	if p.ParentThreadID != nil && *p.ParentThreadID != "" {
		meta.ParentSessionID = *p.ParentThreadID
	} else if p.Source.parentThreadID != "" {
		meta.ParentSessionID = p.Source.parentThreadID
	}
	threadSource := ""
	if p.ThreadSource != nil {
		threadSource = *p.ThreadSource
	}
	meta.Surface = deriveSurface(threadSource, p.Source, meta.Originator)
	return meta, true
}

// deriveSurface normalizes the meta's three provenance signals into the
// closed label set {"cli","vscode","subagent"} the issue-#92 reports split
// on. Precedence: an explicit subagent marker wins (that is the coverage
// gap the watcher exists for), then the source string's own enum, then a
// best-effort mapping of known originator enums. "" when nothing matched —
// unknown is not guessed.
func deriveSurface(threadSource string, source rawMetaSource, originator string) string {
	if source.subagent || threadSource == "subagent" {
		return "subagent"
	}
	switch source.label {
	case "vscode":
		return "vscode"
	case "cli":
		return "cli"
	}
	switch originator {
	case "codex_vscode":
		return "vscode"
	case "codex-tui", "codex_cli_rs", "codex_exec":
		return "cli"
	}
	return ""
}

// RolloutAttribution carries the per-file provenance labels the watcher
// stamps onto the events it emits, so reports can split usage by launch
// surface (issue #92's CLI/IDE/subagent attribution requirement). All
// fields are enum ids or session ids — never free text.
type RolloutAttribution struct {
	Originator      string
	Surface         string
	ParentSessionID string
}

// NormalizeRolloutTurnComplete projects one rollout task_complete boundary
// into the SAME terminal event set NormalizeStop produces for the Stop
// hook — turn.completed (+ context.observed / quota.observed when snap
// yields them) — which is exactly what makes hook+watcher double-capture
// dedupe by construction: the idempotency keys are NormalizeStop's own,
// unchanged ("codex.stop"/"codex.rollout.context"/"codex.rollout.quota"
// digests over the session id and turn ref), and the rollout's
// task_complete turn_id IS the Stop hook payload's turn_id (one provider
// id space, verified against live capture), so whichever path persists
// second is a no-op in the store.
//
// completedAt is the task_complete LINE's own timestamp: it becomes
// OccurredAt (honest — the rollout records when the turn actually ended,
// unlike the hook path which only knows its own delivery instant), and,
// for older rollouts whose task events carry no turn_id, it doubles as
// NormalizeStop's deterministic time-based key fallback — derived from
// file content, so a restart's re-scan of the same bytes reproduces the
// same keys (the watcher's no-migration restart contract).
//
// Two deliberate differences from the hook-path events, applied AFTER
// NormalizeStop so the keys cannot drift:
//
//   - Source is domain.SourceProviderEvent on every event (the observation
//     came from a rollout line, not a hook delivery — NormalizeStop's own
//     context/quota events already say so; this extends the same honesty
//     to the watcher's turn.completed).
//   - attr's originator/surface/parent_session_id labels are stamped into
//     every event payload (enum ids only). On a hook-covered turn the
//     hook's row usually lands first and wins, losing these labels for
//     that row — acceptable: hook-covered sessions are attributable via
//     provider_sessions already; the labels matter precisely on the
//     surfaces hooks never fire for (subagent threads, laggy IDE alphas).
func (n *Normalizer) NormalizeRolloutTurnComplete(ev codexhooks.StopEvent, completedAt time.Time, snap *RolloutSnapshot, attr RolloutAttribution) []v1.Event {
	events := n.NormalizeStop(ev, completedAt, snap)
	for i := range events {
		events[i].Source = string(domain.SourceProviderEvent)
		payload := events[i].Payload
		if attr.Originator != "" {
			payload["originator"] = attr.Originator
		}
		if attr.Surface != "" {
			payload["surface"] = attr.Surface
		}
		if attr.ParentSessionID != "" {
			payload["parent_session_id"] = attr.ParentSessionID
		}
	}
	return events
}
