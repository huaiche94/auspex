// Package claude parses Claude Code's native lifecycle-hook stdin payloads
// (UserPromptSubmit, Stop, StopFailure) and encodes the provider-compatible
// stdout responses those hooks expect back. Like internal/providers/claude,
// this package stops at the parsing/encoding step for this wave; producing
// the frozen pkg/protocol/v1.Event envelope is claude-provider-04's job.
package claude

import (
	"encoding/json"
	"fmt"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/features"
)

// UserPromptSubmitEvent is the parsed, privacy-safe representation of a
// Claude Code UserPromptSubmit hook payload. Raw prompt text is NEVER
// stored here (Constitution §7 rule 2; packet Privacy section) — only a
// SHA-256 hash, size signals, and derived feature booleans/counts are
// retained, mirroring internal/app/ports.go's EvaluateTurnRequest.PromptHash
// convention.
type UserPromptSubmitEvent struct {
	SessionID      domain.SessionID
	TranscriptPath *string // metadata only; not permission to read the transcript.
	CWD            *string

	PromptSHA256       string
	PromptByteLength   int
	PromptApproxTokens int

	// Features is the full privacy-preserving derived feature set (issue
	// #42), computed by features.ExtractPromptFeatures at the single point
	// where the raw prompt text exists in memory — this hook process (or
	// `auspex evaluate`'s in-memory prompt), never the persistence layer.
	// PromptFeatures' own type contract forbids raw text or substrings
	// (its only string fields are the fixed-alphabet SHA-256 hex digest
	// and enum values; everything else is a count, flag, or score), so
	// carrying it here keeps this struct exactly as privacy-safe as
	// before while letting the classifier's verb/domain signals survive
	// past this stack frame. Before #42 these booleans were recomputed
	// downstream from the three size-only fields above — which can only
	// ever yield false, the TaskClassUnknown collapse documented in
	// internal/evaluation/datasource_sql.go.
	Features features.PromptFeatures
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

	ev := NewUserPromptSubmitEvent(domain.SessionID(r.SessionID), prompt)
	ev.TranscriptPath = r.TranscriptPath
	ev.CWD = r.CWD
	return ev, nil
}

// NewUserPromptSubmitEvent builds the privacy-safe UserPromptSubmitEvent
// directly from raw prompt text, deriving everything immediately so the
// raw string never survives past this function's stack frame — the exact
// same derivation ParseUserPromptSubmit applies to a real hook payload (it
// calls this). Exported for `auspex evaluate` (issue #14 deliverable 5),
// which receives prompt text from a file/stdin instead of a hook payload
// but MUST derive the identical hash/size/feature signals so an offline
// evaluation and a hook evaluation of the same prompt are indistinguishable
// downstream (same PromptHash on the persisted event and prediction row,
// same derived features for the classifier). Callers must not log or
// persist the input (Constitution §7 rule 2).
//
// All derivation is delegated to features.ExtractPromptFeatures (issue
// #42): PromptSHA256/PromptByteLength are byte-for-byte the same values
// the previous inline sha256/len derivation produced, and
// PromptApproxTokens is now the ADD §14.7 tokenizer-free estimate rather
// than the earlier bytes/4 coarse estimate — both were explicitly
// approximate (never exact counts), and §14.7 is the estimator
// features.ClassifyTask's own prompt_too_short gate was designed against,
// so hook-time extraction and read-back classification can never disagree
// on whether a prompt was too short to classify.
func NewUserPromptSubmitEvent(sessionID domain.SessionID, prompt string) UserPromptSubmitEvent {
	pf := features.ExtractPromptFeatures(prompt)
	return UserPromptSubmitEvent{
		SessionID:          sessionID,
		PromptSHA256:       pf.SHA256Hex,
		PromptByteLength:   pf.ByteLength,
		PromptApproxTokens: pf.ApproxTokens,
		Features:           pf,
	}
}

// HookDecision is the coarse allow/block decision Auspex's evaluation
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
// choose per Auspex convention).
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
// so Auspex only ever emits the block shape when it actually decided to
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
// Auspex itself fails internally (e.g. malformed hook payload). Per the
// packet's Tests section ("malformed payload produces typed error and valid
// hook fallback"), Auspex must never leave Claude Code hanging or emit
// invalid JSON on internal failure — fail open on parse/internal errors so
// a Auspex bug never blocks the user's actual work.
func FallbackAllowResponse() []byte {
	b, err := EncodeUserPromptSubmitResponse(UserPromptSubmitResponse{Decision: HookDecisionAllow})
	if err != nil {
		// EncodeUserPromptSubmitResponse cannot fail for a plain allow with
		// no additional context; this is an unreachable safety net.
		return []byte(`{}`)
	}
	return b
}
