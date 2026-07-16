package codex

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

// rawNormalPrompt is the prompt text embedded verbatim in
// userpromptsubmit/normal.json — the privacy needle for this package's
// raw-text-absence assertions.
const rawNormalPrompt = "Refactor the rollout reader to use a bounded tail window."

func TestParseUserPromptSubmit_Normal(t *testing.T) {
	ev, err := ParseUserPromptSubmit(fixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatalf("ParseUserPromptSubmit: %v", err)
	}
	if ev.SessionID != "019f0000-1111-7aaa-8bbb-ccccdddd0001" {
		t.Errorf("SessionID = %q", ev.SessionID)
	}
	if ev.TurnID != "019f0000-2222-7aaa-8bbb-ccccdddd0101" {
		t.Errorf("TurnID = %q, want the payload's turn_id", ev.TurnID)
	}
	if ev.Model == nil || *ev.Model != "gpt-5.2-codex" {
		t.Errorf("Model = %v", ev.Model)
	}

	wantHash := sha256.Sum256([]byte(rawNormalPrompt))
	if ev.PromptSHA256 != hex.EncodeToString(wantHash[:]) {
		t.Errorf("PromptSHA256 = %q, want the sha256 of the fixture prompt", ev.PromptSHA256)
	}
	if ev.PromptByteLength != len(rawNormalPrompt) {
		t.Errorf("PromptByteLength = %d, want %d", ev.PromptByteLength, len(rawNormalPrompt))
	}
	if ev.PromptApproxTokens <= 0 {
		t.Errorf("PromptApproxTokens = %d, want > 0", ev.PromptApproxTokens)
	}
	if ev.Features.SHA256Hex != ev.PromptSHA256 {
		t.Error("Features must carry the extraction marker matching PromptSHA256")
	}
}

func TestParseUserPromptSubmit_RawPromptNeverRetained(t *testing.T) {
	ev, err := ParseUserPromptSubmit(fixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatalf("ParseUserPromptSubmit: %v", err)
	}
	dump := fmt.Sprintf("%#v", ev)
	if strings.Contains(dump, rawNormalPrompt) {
		t.Errorf("raw prompt text leaked into parsed struct: %s", dump)
	}
}

func TestParseUserPromptSubmit_MissingFields(t *testing.T) {
	ev, err := ParseUserPromptSubmit(fixture(t, "userpromptsubmit", "missing_fields.json"))
	if err != nil {
		t.Fatalf("ParseUserPromptSubmit: %v", err)
	}
	if ev.TurnID != "" {
		t.Errorf("TurnID = %q, want empty for an absent turn_id", ev.TurnID)
	}
	if ev.CWD != nil || ev.Model != nil {
		t.Errorf("absent fields must stay nil: CWD=%v Model=%v", ev.CWD, ev.Model)
	}
}

func TestParseUserPromptSubmit_EmptyPrompt(t *testing.T) {
	ev, err := ParseUserPromptSubmit(fixture(t, "userpromptsubmit", "empty_prompt.json"))
	if err != nil {
		t.Fatalf("ParseUserPromptSubmit: %v", err)
	}
	if ev.PromptByteLength != 0 {
		t.Errorf("PromptByteLength = %d, want 0", ev.PromptByteLength)
	}
	// ExtractPromptFeatures sets the marker for every input, even "".
	if ev.Features.SHA256Hex == "" {
		t.Error("Features.SHA256Hex empty — extraction must run even for an empty prompt")
	}
}

func TestParseUserPromptSubmit_UnknownFieldsTolerated(t *testing.T) {
	if _, err := ParseUserPromptSubmit(fixture(t, "userpromptsubmit", "unknown_fields.json")); err != nil {
		t.Fatalf("ParseUserPromptSubmit (must tolerate unknown fields): %v", err)
	}
}

func TestParseUserPromptSubmit_Malformed(t *testing.T) {
	_, err := ParseUserPromptSubmit(fixture(t, "userpromptsubmit", "malformed.json"))
	assertValidationError(t, err)
}

func TestParseUserPromptSubmit_MissingSessionID(t *testing.T) {
	_, err := ParseUserPromptSubmit([]byte(`{"hook_event_name":"UserPromptSubmit","prompt":"hi"}`))
	assertValidationError(t, err)
}

// --- response encoding, pinned by golden fixtures --------------------------

func TestEncodeUserPromptSubmitResponse_AllowMatchesGolden(t *testing.T) {
	body, err := EncodeUserPromptSubmitResponse(UserPromptSubmitResponse{Decision: HookDecisionAllow})
	if err != nil {
		t.Fatalf("encode allow: %v", err)
	}
	golden := bytes.TrimSpace(fixture(t, "userpromptsubmit", "response_allow.golden.json"))
	if !bytes.Equal(body, golden) {
		t.Errorf("allow response = %s, want golden %s", body, golden)
	}
}

func TestEncodeUserPromptSubmitResponse_BlockMatchesGolden(t *testing.T) {
	body, err := EncodeUserPromptSubmitResponse(UserPromptSubmitResponse{
		Decision:          HookDecisionBlock,
		Reason:            "Auspex evaluation eval-1 requires a checkpoint or explicit override before this task starts.",
		AdditionalContext: "forecast: sample",
	})
	if err != nil {
		t.Fatalf("encode block: %v", err)
	}
	golden := bytes.TrimSpace(fixture(t, "userpromptsubmit", "response_block.golden.json"))
	if !bytes.Equal(body, golden) {
		t.Errorf("block response = %s, want golden %s", body, golden)
	}
}

func TestEncodeUserPromptSubmitResponse_EmptyDecisionIsAllow(t *testing.T) {
	body, err := EncodeUserPromptSubmitResponse(UserPromptSubmitResponse{})
	if err != nil {
		t.Fatalf("encode zero-value: %v", err)
	}
	if string(body) != "{}" {
		t.Errorf("zero-value response = %s, want {}", body)
	}
}

func TestEncodeUserPromptSubmitResponse_AllowWithContext(t *testing.T) {
	body, err := EncodeUserPromptSubmitResponse(UserPromptSubmitResponse{
		Decision:          HookDecisionAllow,
		AdditionalContext: "forecast: sample",
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	want := `{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":"forecast: sample"}}`
	if string(body) != want {
		t.Errorf("allow-with-context = %s, want %s", body, want)
	}
}

func TestEncodeUserPromptSubmitResponse_UnknownDecisionRejected(t *testing.T) {
	_, err := EncodeUserPromptSubmitResponse(UserPromptSubmitResponse{Decision: "maybe"})
	assertValidationError(t, err)
}

func TestFallbackAllowResponse_IsValidMinimalJSON(t *testing.T) {
	if got := string(FallbackAllowResponse()); got != "{}" {
		t.Errorf("FallbackAllowResponse = %s, want {}", got)
	}
}
