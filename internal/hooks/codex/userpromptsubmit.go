package codex

import (
	"encoding/json"
	"fmt"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/features"
)

// UserPromptSubmitEvent is the parsed, privacy-safe representation of a
// Codex UserPromptSubmit hook payload. Raw prompt text is NEVER stored here
// (Constitution §7 rule 2) — only the SHA-256 hash, size signals, and the
// derived feature set, exactly the discipline
// internal/hooks/claude.UserPromptSubmitEvent established. TurnID is
// Codex's own extension ("expose the active turn id to internal turn-scoped
// hooks", user-prompt-submit.command.input schema): the provider-stable
// turn identifier the telemetry layer stamps onto the frozen envelope's
// TurnID field, sparing Auspex the mint-and-resolve dance claude's
// Stop path needs (orchestrator's OpenTurnResolver).
type UserPromptSubmitEvent struct {
	SessionID      domain.SessionID
	TurnID         domain.TurnID // "" when the payload carried none
	TranscriptPath *string
	CWD            *string
	Model          *string
	PermissionMode *string

	PromptSHA256       string
	PromptByteLength   int
	PromptApproxTokens int

	// Features is the full privacy-preserving derived feature set (issue
	// #42), computed by features.ExtractPromptFeatures at the single point
	// where the raw prompt text exists in memory — this hook process. See
	// internal/hooks/claude.UserPromptSubmitEvent.Features for the type-
	// level privacy contract this relies on (only fixed-alphabet digest and
	// enum strings; everything else counts/flags/scores).
	Features features.PromptFeatures
}

type rawUserPromptSubmit struct {
	SessionID      string  `json:"session_id"`
	HookEventName  string  `json:"hook_event_name"`
	TurnID         *string `json:"turn_id"`
	TranscriptPath *string `json:"transcript_path"`
	CWD            *string `json:"cwd"`
	Model          *string `json:"model"`
	PermissionMode *string `json:"permission_mode"`
	Prompt         *string `json:"prompt"`
}

// ParseUserPromptSubmit parses a Codex UserPromptSubmit hook stdin payload.
// It tolerates unknown fields and hashes the prompt immediately so the raw
// text never survives past this function's stack frame into any returned or
// persisted struct (the same single-point-of-derivation rule
// internal/hooks/claude.ParseUserPromptSubmit follows, via the shared
// features.ExtractPromptFeatures — so a Codex prompt and a Claude prompt
// with identical text derive byte-identical hash/size/feature signals).
func ParseUserPromptSubmit(raw []byte) (UserPromptSubmitEvent, error) {
	var r rawUserPromptSubmit
	if err := json.Unmarshal(raw, &r); err != nil {
		return UserPromptSubmitEvent{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   fmt.Sprintf("codex userpromptsubmit: invalid JSON: %v", err),
			Retryable: false,
		}
	}
	if r.SessionID == "" {
		return UserPromptSubmitEvent{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "codex userpromptsubmit: missing session_id",
			Retryable: false,
		}
	}

	var prompt string
	if r.Prompt != nil {
		prompt = *r.Prompt
	}
	pf := features.ExtractPromptFeatures(prompt)

	ev := UserPromptSubmitEvent{
		SessionID:          domain.SessionID(r.SessionID),
		TranscriptPath:     r.TranscriptPath,
		CWD:                r.CWD,
		Model:              r.Model,
		PermissionMode:     r.PermissionMode,
		PromptSHA256:       pf.SHA256Hex,
		PromptByteLength:   pf.ByteLength,
		PromptApproxTokens: pf.ApproxTokens,
		Features:           pf,
	}
	if r.TurnID != nil {
		ev.TurnID = domain.TurnID(*r.TurnID)
	}
	return ev, nil
}

// HookDecision is the coarse allow/block decision Auspex's evaluation
// path renders for a Codex UserPromptSubmit hook. Codex's
// user-prompt-submit.command.output schema admits exactly one decision
// value, "block" (BlockDecisionWire) — allow is expressed by omitting the
// key, identical to Claude Code's convention.
type HookDecision string

const (
	HookDecisionAllow HookDecision = "allow"
	HookDecisionBlock HookDecision = "block"
)

// UserPromptSubmitResponse is the provider-compatible response Codex's hook
// protocol expects on stdout for UserPromptSubmit. On block, Reason is
// required by Codex's output parser ("Claude requires `reason` when
// `decision` is `block`; we enforce that semantic rule during output
// parsing" — codex's own schema note) and AdditionalContext is injected via
// hookSpecificOutput.
type UserPromptSubmitResponse struct {
	Decision          HookDecision
	Reason            string
	AdditionalContext string
}

// wireUserPromptSubmitResponse mirrors Codex's expected on-wire JSON shape
// exactly (field names/casing are provider-dictated). Codex validates hook
// stdout against a schema with additionalProperties:false, so this struct
// must emit ONLY keys that schema declares — pinned by the golden-fixture
// tests against testdata/provider-events/codex/userpromptsubmit/.
type wireUserPromptSubmitResponse struct {
	Decision           string                     `json:"decision,omitempty"`
	Reason             string                     `json:"reason,omitempty"`
	HookSpecificOutput *wireHookSpecificOutputUPS `json:"hookSpecificOutput,omitempty"`
}

type wireHookSpecificOutputUPS struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// EncodeUserPromptSubmitResponse renders the provider-compatible JSON body
// for a Codex UserPromptSubmit hook response. A pure allow renders as `{}`
// (no decision key): Codex's output schema has no "allow" decision value at
// all — absence of "block" IS allow, exactly Claude Code's convention.
func EncodeUserPromptSubmitResponse(resp UserPromptSubmitResponse) ([]byte, error) {
	wire := wireUserPromptSubmitResponse{}

	switch resp.Decision {
	case HookDecisionBlock:
		wire.Decision = "block"
		wire.Reason = resp.Reason
		if resp.AdditionalContext != "" {
			wire.HookSpecificOutput = &wireHookSpecificOutputUPS{
				HookEventName:     "UserPromptSubmit",
				AdditionalContext: resp.AdditionalContext,
			}
		}
	case HookDecisionAllow, "":
		if resp.AdditionalContext != "" {
			wire.HookSpecificOutput = &wireHookSpecificOutputUPS{
				HookEventName:     "UserPromptSubmit",
				AdditionalContext: resp.AdditionalContext,
			}
		}
	default:
		return nil, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   fmt.Sprintf("codex userpromptsubmit: unknown decision %q", resp.Decision),
			Retryable: false,
		}
	}

	b, err := json.Marshal(wire)
	if err != nil {
		return nil, &domain.Error{
			Code:      domain.ErrCodeInternal,
			Message:   fmt.Sprintf("codex userpromptsubmit: encode response: %v", err),
			Retryable: false,
		}
	}
	return b, nil
}

// FallbackAllowResponse is the safe, minimal allow response emitted when
// Auspex itself fails internally (e.g. malformed hook payload) — the
// same fail-open contract internal/hooks/claude.FallbackAllowResponse
// carries: never leave the provider hanging, never emit invalid JSON, never
// let an Auspex bug block the user's actual work.
func FallbackAllowResponse() []byte {
	b, err := EncodeUserPromptSubmitResponse(UserPromptSubmitResponse{Decision: HookDecisionAllow})
	if err != nil {
		// Unreachable safety net: a plain allow cannot fail to encode.
		return []byte(`{}`)
	}
	return b
}
