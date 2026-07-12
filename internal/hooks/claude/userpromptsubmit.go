// Package claude parses Claude Code's native lifecycle-hook stdin payloads
// (UserPromptSubmit, Stop, StopFailure) and encodes the provider-compatible
// stdout responses those hooks expect back. Like internal/providers/claude,
// this package stops at the parsing/encoding step for this wave; producing
// the frozen pkg/protocol/v1.Event envelope is claude-provider-04's job.
package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/huaiche94/preflight/internal/domain"
)

// UserPromptSubmitEvent is the parsed, privacy-safe representation of a
// Claude Code UserPromptSubmit hook payload. Raw prompt text is NEVER
// stored here (Constitution §7 rule 2; packet Privacy section) — only a
// SHA-256 hash, byte length, and a coarse approximate token count are
// retained, mirroring internal/app/ports.go's EvaluateTurnRequest.PromptHash
// convention.
type UserPromptSubmitEvent struct {
	SessionID      domain.SessionID
	TranscriptPath *string // metadata only; not permission to read the transcript.
	CWD            *string

	PromptSHA256       string
	PromptByteLength   int
	PromptApproxTokens int
}

type rawUserPromptSubmit struct {
	SessionID      string  `json:"session_id"`
	TranscriptPath *string `json:"transcript_path"`
	CWD            *string `json:"cwd"`
	HookEventName  string  `json:"hook_event_name"`
	Prompt         *string `json:"prompt"`
}

// ParseUserPromptSubmit parses a Claude Code UserPromptSubmit hook stdin
// payload. It tolerates unknown fields and hashes the prompt immediately
// so the raw text never survives past this function's stack frame into any
// returned/persisted struct.
func ParseUserPromptSubmit(raw []byte) (UserPromptSubmitEvent, error) {
	var r rawUserPromptSubmit
	if err := json.Unmarshal(raw, &r); err != nil {
		return UserPromptSubmitEvent{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   fmt.Sprintf("claude userpromptsubmit: invalid JSON: %v", err),
			Retryable: false,
		}
	}

	if r.SessionID == "" {
		return UserPromptSubmitEvent{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "claude userpromptsubmit: missing session_id",
			Retryable: false,
		}
	}

	var prompt string
	if r.Prompt != nil {
		prompt = *r.Prompt
	}

	sum := sha256.Sum256([]byte(prompt))

	return UserPromptSubmitEvent{
		SessionID:          domain.SessionID(r.SessionID),
		TranscriptPath:     r.TranscriptPath,
		CWD:                r.CWD,
		PromptSHA256:       hex.EncodeToString(sum[:]),
		PromptByteLength:   len(prompt),
		PromptApproxTokens: approxTokenCount(prompt),
	}, nil
}

// approxTokenCount is a coarse, provider-agnostic estimate (roughly 4 bytes
// per token for English-like text). It is explicitly approximate — never
// treated as an exact token count. Empty input yields 0 tokens (a true zero,
// not an "unknown" case, since an empty prompt IS zero-length by definition).
func approxTokenCount(prompt string) int {
	if len(prompt) == 0 {
		return 0
	}
	const approxBytesPerToken = 4
	tokens := len(prompt) / approxBytesPerToken
	if tokens == 0 {
		tokens = 1
	}
	return tokens
}

// HookDecision is the coarse allow/block decision Preflight's evaluation
// path renders for a UserPromptSubmit hook (ADD §22.3).
type HookDecision string

const (
	HookDecisionAllow HookDecision = "allow"
	HookDecisionBlock HookDecision = "block"
)

// UserPromptSubmitResponse is the provider-compatible response Claude
// Code's hook protocol expects on stdout for UserPromptSubmit (ADD §22.3).
// On block, Reason is shown to the user/model and AdditionalContext is
// injected via hookSpecificOutput.
type UserPromptSubmitResponse struct {
	Decision          HookDecision
	Reason            string
	AdditionalContext string
}

// wireUserPromptSubmitResponse mirrors Claude Code's expected on-wire JSON
// shape exactly (field names/casing are provider-dictated, not ours to
// choose per Preflight convention).
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
// for a UserPromptSubmit hook response. An empty/zero-value Decision is
// treated as allow-with-no-opinion: Claude Code's hook protocol allows the
// prompt through by default when a hook emits no explicit "block" decision,
// so Preflight only ever emits the block shape when it actually decided to
// block; a pure allow renders as `{}` (no decision key), matching
// hook-protocol convention of omitting fields that don't apply.
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
			Message:   fmt.Sprintf("claude userpromptsubmit: unknown decision %q", resp.Decision),
			Retryable: false,
		}
	}

	b, err := json.Marshal(wire)
	if err != nil {
		return nil, &domain.Error{
			Code:      domain.ErrCodeInternal,
			Message:   fmt.Sprintf("claude userpromptsubmit: encode response: %v", err),
			Retryable: false,
		}
	}
	return b, nil
}

// FallbackAllowResponse is the safe, minimal allow response emitted when
// Preflight itself fails internally (e.g. malformed hook payload). Per the
// packet's Tests section ("malformed payload produces typed error and valid
// hook fallback"), Preflight must never leave Claude Code hanging or emit
// invalid JSON on internal failure — fail open on parse/internal errors so
// a Preflight bug never blocks the user's actual work.
func FallbackAllowResponse() []byte {
	b, err := EncodeUserPromptSubmitResponse(UserPromptSubmitResponse{Decision: HookDecisionAllow})
	if err != nil {
		// EncodeUserPromptSubmitResponse cannot fail for a plain allow with
		// no additional context; this is an unreachable safety net.
		return []byte(`{}`)
	}
	return b
}
