// precompact.go parses Claude Code's PreCompact hook payload (and the
// ADD-specified PostCompact sibling) — issue #114's "wire the designed
// PreCompact path" (ADD §22.3 initial events, §22.4 "`PreCompact` 一律
// capture state checkpoint"). Like every parser in this package, it stops
// at producing a privacy-safe intermediate struct; the frozen
// pkg/protocol/v1.Event projection is internal/telemetry/claude's job
// (precompact.go there), and the checkpoint capture itself is
// internal/orchestrator's (hooksprecompact.go).
//
// Privacy (Constitution §7 rule 2): PreCompact's custom_instructions field
// is user-authored text (the argument to `/compact <instructions>`), so
// only its LENGTH survives this parser's stack frame — the same
// length-only discipline ParseStopFailure applies to raw error messages.
package claude

import (
	"encoding/json"
	"fmt"

	"github.com/huaiche94/auspex/internal/domain"
)

// Compaction trigger enum values (PreCompact's trigger field). Kept as
// plain strings so a value a future provider adds flows through untouched
// (the issue-#21 unknown-window lesson applied to enums, matching
// internal/hooks/codex.SessionStartSource).
const (
	// CompactTriggerManual: the user ran /compact themselves.
	CompactTriggerManual = "manual"
	// CompactTriggerAuto: the provider compacted because the context
	// window filled up.
	CompactTriggerAuto = "auto"
)

// PreCompactEvent is the parsed, privacy-safe representation of a Claude
// Code PreCompact hook payload — the moment BEFORE the provider summarizes
// the session context. Optional fields are pointers per the
// repository-wide rule: nil means the payload did not carry the field —
// unknown, never a substituted zero.
type PreCompactEvent struct {
	SessionID      domain.SessionID
	TranscriptPath *string
	CWD            *string

	// Trigger is the provider's compaction-trigger enum ("manual" |
	// "auto"). nil means the field was absent from the payload.
	Trigger *string

	// CustomInstructionsLen is the byte length of the payload's
	// custom_instructions field (user text — length only, see the file
	// doc comment). nil means the field was absent; a present-but-empty
	// field parses to a pointer to 0 (absent and empty are different
	// observations — unknown is not zero).
	CustomInstructionsLen *int
}

type rawPreCompact struct {
	SessionID          string  `json:"session_id"`
	TranscriptPath     *string `json:"transcript_path"`
	CWD                *string `json:"cwd"`
	HookEventName      string  `json:"hook_event_name"`
	Trigger            *string `json:"trigger"`
	CustomInstructions *string `json:"custom_instructions"`
}

// ParsePreCompact parses a Claude Code PreCompact hook stdin payload,
// tolerating unknown fields.
func ParsePreCompact(raw []byte) (PreCompactEvent, error) {
	var r rawPreCompact
	if err := json.Unmarshal(raw, &r); err != nil {
		return PreCompactEvent{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   fmt.Sprintf("claude precompact: invalid JSON: %v", err),
			Retryable: false,
		}
	}

	if r.SessionID == "" {
		return PreCompactEvent{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "claude precompact: missing session_id",
			Retryable: false,
		}
	}

	ev := PreCompactEvent{
		SessionID:      domain.SessionID(r.SessionID),
		TranscriptPath: r.TranscriptPath,
		CWD:            r.CWD,
		Trigger:        r.Trigger,
	}
	if r.CustomInstructions != nil {
		n := len(*r.CustomInstructions)
		ev.CustomInstructionsLen = &n
	}
	return ev, nil
}

// PostCompactEvent is the parsed representation of a PostCompact hook
// payload — the moment AFTER the provider replaced the session context
// with its summary. NOTE (issue #114 capability audit): Claude Code ships
// no dedicated PostCompact hook event today — post-compaction is
// observable there only as a SessionStart with source "compact". This
// parser implements the shape ADD §22.3 specifies so the
// `auspex hook claude post-compact` entry point is real and tested the
// day the provider ships the event; it is deliberately NOT registered in
// integrations/claude/hooks.json until then (do not fake capability).
type PostCompactEvent struct {
	SessionID      domain.SessionID
	TranscriptPath *string
	CWD            *string

	// Trigger mirrors PreCompactEvent.Trigger.
	Trigger *string
}

type rawPostCompact struct {
	SessionID      string  `json:"session_id"`
	TranscriptPath *string `json:"transcript_path"`
	CWD            *string `json:"cwd"`
	HookEventName  string  `json:"hook_event_name"`
	Trigger        *string `json:"trigger"`
}

// ParsePostCompact parses a PostCompact hook stdin payload, tolerating
// unknown fields.
func ParsePostCompact(raw []byte) (PostCompactEvent, error) {
	var r rawPostCompact
	if err := json.Unmarshal(raw, &r); err != nil {
		return PostCompactEvent{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   fmt.Sprintf("claude postcompact: invalid JSON: %v", err),
			Retryable: false,
		}
	}

	if r.SessionID == "" {
		return PostCompactEvent{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "claude postcompact: missing session_id",
			Retryable: false,
		}
	}

	return PostCompactEvent{
		SessionID:      domain.SessionID(r.SessionID),
		TranscriptPath: r.TranscriptPath,
		CWD:            r.CWD,
		Trigger:        r.Trigger,
	}, nil
}
