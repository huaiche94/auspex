package appserver

import (
	"context"
	"encoding/json"
)

// This file types the STABLE SUBSET of the App Server protocol Auspex
// consumes (ADD §21.2/§21.7), grounded field-for-field in the vendored
// schema (testdata/codex-schema, generated from codex-cli 0.144.5).
// Unknown fields are ignored by encoding/json by design; a struct here
// names ONLY identifiers, numbers, and enumerated statuses. Text-bearing
// protocol fields (diff bodies, plan step wording, item content) are
// deliberately not decoded — where a notification's only payload is
// text, the typed view carries its byte length (unknown is not zero;
// content is never persisted — Constitution §7).

// --- handshake -----------------------------------------------------------

// ClientInfo identifies this client in the initialize handshake.
type ClientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

// InitializeResult is the initialize response's stable subset.
type InitializeResult struct {
	UserAgent string `json:"userAgent"`
	CodexHome string `json:"codexHome"`
}

// Initialize performs the §21.2 handshake: the initialize request
// followed by the `initialized` notification.
func (c *Client) Initialize(ctx context.Context, info ClientInfo) (InitializeResult, error) {
	var res InitializeResult
	if err := c.Call(ctx, "initialize", map[string]any{"clientInfo": info}, &res); err != nil {
		return InitializeResult{}, err
	}
	if err := c.Notify("initialized", nil); err != nil {
		return InitializeResult{}, err
	}
	return res, nil
}

// --- thread/turn lifecycle -------------------------------------------------

// ThreadStartParams starts a fresh thread. Zero-value fields are omitted
// so the server's own defaults apply (schema: every field optional).
type ThreadStartParams struct {
	Cwd   string `json:"cwd,omitempty"`
	Model string `json:"model,omitempty"`
}

// Thread is the stable identity subset of the server's Thread object.
type Thread struct {
	ID string `json:"id"`
}

// ThreadStartResult carries the started thread plus the effective
// model/provider the server resolved (cohort labels for #20).
type ThreadStartResult struct {
	Thread        Thread `json:"thread"`
	Model         string `json:"model"`
	ModelProvider string `json:"modelProvider"`
}

// StartThread calls thread/start.
func (c *Client) StartThread(ctx context.Context, p ThreadStartParams) (ThreadStartResult, error) {
	var res ThreadStartResult
	err := c.Call(ctx, "thread/start", p, &res)
	return res, err
}

// ThreadResumeParams resumes an existing thread by id.
type ThreadResumeParams struct {
	ThreadID string `json:"threadId"`
	Cwd      string `json:"cwd,omitempty"`
	Model    string `json:"model,omitempty"`
}

// ResumeThread calls thread/resume. The response mirrors thread/start's
// shape (stable subset).
func (c *Client) ResumeThread(ctx context.Context, p ThreadResumeParams) (ThreadStartResult, error) {
	var res ThreadStartResult
	err := c.Call(ctx, "thread/resume", p, &res)
	return res, err
}

// UserInputText is the text variant of the turn/start input union.
type UserInputText struct {
	Type string `json:"type"` // always "text"
	Text string `json:"text"`
}

// TextInput builds the single-text-item input array turn/start expects.
func TextInput(text string) []UserInputText {
	return []UserInputText{{Type: "text", Text: text}}
}

// TurnStartParams starts one turn on a thread.
type TurnStartParams struct {
	ThreadID string          `json:"threadId"`
	Input    []UserInputText `json:"input"`
}

// TurnStatus is the server's turn status enum (schema TurnStatus).
type TurnStatus string

const (
	TurnStatusInProgress  TurnStatus = "inProgress"
	TurnStatusCompleted   TurnStatus = "completed"
	TurnStatusInterrupted TurnStatus = "interrupted"
	TurnStatusFailed      TurnStatus = "failed"
)

// Turn is the stable subset of the server's Turn object: identity,
// status, timing. Items are counted, never decoded (18-variant union
// whose members carry content text).
type Turn struct {
	ID          string          `json:"id"`
	Status      TurnStatus      `json:"status"`
	StartedAt   *int64          `json:"startedAt"`
	CompletedAt *int64          `json:"completedAt"`
	DurationMs  *int64          `json:"durationMs"`
	Items       json.RawMessage `json:"items"` // raw; use ItemCount
	Error       *TurnError      `json:"error"`
}

// TurnError keeps only the failure message's byte length — the message
// text itself is provider content and never persisted (same rule as
// managed/codexstream's MessageLen).
type TurnError struct {
	MessageLen int
}

// UnmarshalJSON measures the message without retaining it.
func (e *TurnError) UnmarshalJSON(b []byte) error {
	var raw struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	e.MessageLen = len(raw.Message)
	return nil
}

// ItemCount reports how many items the turn carried (0 for absent/
// undecodable — the count is a health signal, not an accounting claim).
func (t Turn) ItemCount() int {
	if len(t.Items) == 0 {
		return 0
	}
	var items []json.RawMessage
	if err := json.Unmarshal(t.Items, &items); err != nil {
		return 0
	}
	return len(items)
}

// TurnStartResult is turn/start's response.
type TurnStartResult struct {
	Turn Turn `json:"turn"`
}

// StartTurn calls turn/start.
func (c *Client) StartTurn(ctx context.Context, p TurnStartParams) (TurnStartResult, error) {
	var res TurnStartResult
	err := c.Call(ctx, "turn/start", p, &res)
	return res, err
}

// InterruptTurn calls turn/interrupt (ADD §21.6 step 5). The
// interrupted outcome arrives as a turn/completed notification with
// status "interrupted", not on this response.
func (c *Client) InterruptTurn(ctx context.Context, threadID, turnID string) error {
	return c.Call(ctx, "turn/interrupt", map[string]string{"threadId": threadID, "turnId": turnID}, nil)
}

// --- account / rate limits ---------------------------------------------

// RateLimitWindow is one rolling quota window (§21.5's field set;
// usedPercent is the only required field — everything else nullable,
// unknown is not zero).
type RateLimitWindow struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins *int64  `json:"windowDurationMins"`
	ResetsAt           *int64  `json:"resetsAt"`
}

// RateLimitSnapshot is the stable subset of the server's rate-limit
// state: both windows plus identity/plan labels, all optional.
type RateLimitSnapshot struct {
	LimitID              *string          `json:"limitId"`
	LimitName            *string          `json:"limitName"`
	Primary              *RateLimitWindow `json:"primary"`
	Secondary            *RateLimitWindow `json:"secondary"`
	PlanType             *string          `json:"planType"`
	RateLimitReachedType *string          `json:"rateLimitReachedType"`
}

// ReadRateLimits calls account/rateLimits/read (§21.5).
func (c *Client) ReadRateLimits(ctx context.Context) (RateLimitSnapshot, error) {
	var res struct {
		RateLimits RateLimitSnapshot `json:"rateLimits"`
	}
	err := c.Call(ctx, "account/rateLimits/read", nil, &res)
	return res.RateLimits, err
}

// --- notifications (decode helpers) --------------------------------------

// Notification method names of the stable subset (§21.2).
const (
	MethodTurnStarted            = "turn/started"
	MethodTurnCompleted          = "turn/completed"
	MethodThreadTokenUsage       = "thread/tokenUsage/updated"
	MethodAccountRateLimits      = "account/rateLimits/updated"
	MethodItemStarted            = "item/started"
	MethodItemCompleted          = "item/completed"
	MethodTurnDiffUpdated        = "turn/diff/updated"
	MethodTurnPlanUpdated        = "turn/plan/updated"
	MethodRemoteControlStatus    = "remoteControl/status/changed" // observed on 0.144.5; tolerated, unused
	MethodAccountLoginCompleted  = "account/login/completed"      // tolerated, unused
	MethodThreadClosed           = "thread/closed"
	MethodContextCompactionStart = "thread/compact/started" // best-effort; absence tolerated
)

// TurnNotification is the shared shape of turn/started and
// turn/completed.
type TurnNotification struct {
	ThreadID string `json:"threadId"`
	Turn     Turn   `json:"turn"`
}

// TokenUsageBreakdown mirrors the schema's TokenUsageBreakdown — the
// same five counters the rollout JSONL carries, camelCased. Codex
// semantics: InputTokens INCLUDES CachedInputTokens; OutputTokens
// includes ReasoningOutputTokens (normalization into the frozen envelope
// vocabulary happens in internal/telemetry/codex, not here).
type TokenUsageBreakdown struct {
	InputTokens           int64 `json:"inputTokens"`
	CachedInputTokens     int64 `json:"cachedInputTokens"`
	OutputTokens          int64 `json:"outputTokens"`
	ReasoningOutputTokens int64 `json:"reasoningOutputTokens"`
	TotalTokens           int64 `json:"totalTokens"`
}

// ThreadTokenUsageNotification is thread/tokenUsage/updated — the live
// per-turn usage stream (the LiveTokenUsage capability's data source).
type ThreadTokenUsageNotification struct {
	ThreadID   string `json:"threadId"`
	TurnID     string `json:"turnId"`
	TokenUsage struct {
		Last               TokenUsageBreakdown `json:"last"`
		Total              TokenUsageBreakdown `json:"total"`
		ModelContextWindow *int64              `json:"modelContextWindow"`
	} `json:"tokenUsage"`
}

// RateLimitsNotification is account/rateLimits/updated.
type RateLimitsNotification struct {
	RateLimits RateLimitSnapshot `json:"rateLimits"`
}

// ItemNotification is the shared identity subset of item/started and
// item/completed. The item body is a content-bearing 18-variant union;
// only its type tag and id are decoded.
type ItemNotification struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	Item     struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	} `json:"item"`
	StartedAtMs   *int64 `json:"startedAtMs"`
	CompletedAtMs *int64 `json:"completedAtMs"`
}

// TurnDiffNotification is turn/diff/updated with the diff body measured,
// never retained (repository state belongs to git observation, not to a
// persisted provider payload).
type TurnDiffNotification struct {
	ThreadID    string
	TurnID      string
	DiffByteLen int
}

// UnmarshalJSON measures diff length without retaining content.
func (n *TurnDiffNotification) UnmarshalJSON(b []byte) error {
	var raw struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Diff     string `json:"diff"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	n.ThreadID, n.TurnID, n.DiffByteLen = raw.ThreadID, raw.TurnID, len(raw.Diff)
	return nil
}

// TurnPlanNotification is turn/plan/updated with steps counted, never
// decoded (step wording is content; §21.3 maps plans to PROPOSED
// Progress Tree nodes downstream, which needs counts and ids only at
// this layer).
type TurnPlanNotification struct {
	ThreadID  string
	TurnID    string
	PlanSteps int
}

// UnmarshalJSON counts plan steps without retaining their text.
func (n *TurnPlanNotification) UnmarshalJSON(b []byte) error {
	var raw struct {
		ThreadID string            `json:"threadId"`
		TurnID   string            `json:"turnId"`
		Plan     []json.RawMessage `json:"plan"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	n.ThreadID, n.TurnID, n.PlanSteps = raw.ThreadID, raw.TurnID, len(raw.Plan)
	return nil
}
