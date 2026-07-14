package claude

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	claudehooks "github.com/huaiche94/auspex/internal/hooks/claude"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// TestPrivacy_UserPromptSubmit_RawPromptNeverInEvent is this node's
// privacy-assertion test (Constitution §7 rule 2; packet's Privacy and
// Tests sections: "raw-prompt absence assertion across persisted rows/log
// output"). It asserts the raw prompt text from the fixture never appears
// anywhere in the produced Event — not in a known field, and not
// incidentally via a full-struct dump — which is the strongest assertion
// available short of a reflection-based field enumeration.
func TestPrivacy_UserPromptSubmit_RawPromptNeverInEvent(t *testing.T) {
	const rawPrompt = "Refactor the checkpoint manifest writer to use atomic rename."

	n, clock := newTestNormalizer()
	parsed, err := claudehooks.ParseUserPromptSubmit(fixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatalf("ParseUserPromptSubmit: %v", err)
	}

	ev := n.NormalizeUserPromptSubmit(parsed, clock.Now())

	assertNoRawText(t, ev, rawPrompt, "user prompt")
}

// TestPrivacy_UserPromptSubmit_DerivedFeatureFieldsAreBoolsAndCountsOnly
// pins the issue-#42 payload's structural privacy contract (Constitution
// §7 rule 2: "only derived features and hashes"): every payload field on a
// provider.turn.started event must be a bool or an int except the two
// known string fields — prompt_sha256 (fixed-alphabet digest, can never
// spell prompt text) and cwd (a filesystem path from the hook envelope,
// not prompt content). A new string-typed payload field is a
// privacy-review event, not a routine change — strings are the only type
// that can carry raw prompt text, so this test failing means stop and
// review, not loosen the assertion.
func TestPrivacy_UserPromptSubmit_DerivedFeatureFieldsAreBoolsAndCountsOnly(t *testing.T) {
	n, clock := newTestNormalizer()
	parsed, err := claudehooks.ParseUserPromptSubmit(fixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatalf("ParseUserPromptSubmit: %v", err)
	}

	ev := n.NormalizeUserPromptSubmit(parsed, clock.Now())

	allowedStringKeys := map[string]bool{"prompt_sha256": true, "cwd": true}
	for k, v := range ev.Payload {
		switch v.(type) {
		case bool, int:
			// booleans and counts: the only shapes derived features may take.
		case string:
			if !allowedStringKeys[k] {
				t.Errorf("payload field %q is a string (%q) — only prompt_sha256 and cwd may be strings on this event (Constitution §7 rule 2)", k, v)
			}
		default:
			t.Errorf("payload field %q has unexpected type %T — derived prompt features must be bools or ints", k, v)
		}
	}

	// Falsifiability: the feature fields must actually be present — an
	// empty payload would make the type scan above vacuous.
	if _, ok := ev.Payload["has_refactor_verb"]; !ok {
		t.Fatal("has_refactor_verb missing from payload — the derived-feature persistence this test pins did not run")
	}
}

// TestPrivacy_StopFailure_RawErrorMessageNeverInEvent covers the analogous
// case for StopFailure: the packet's classifyFailure privacy note in
// internal/hooks/claude/stop.go says provider error messages can echo
// request content, so this normalizer must not leak the raw error message
// text either, even though the packet's Privacy section is written
// primarily about prompts.
func TestPrivacy_StopFailure_RawErrorMessageNeverInEvent(t *testing.T) {
	const rawErrorMessage = "This request would exceed the rate limit for your organization."

	n, clock := newTestNormalizer()
	parsed, err := claudehooks.ParseStopFailure(fixture(t, "stopfailure", "rate_limit.json"))
	if err != nil {
		t.Fatalf("ParseStopFailure: %v", err)
	}

	events := n.NormalizeStopFailure(parsed, clock.Now())
	for _, ev := range events {
		assertNoRawText(t, ev, rawErrorMessage, "error message")
	}
}

// assertNoRawText fails the test if needle appears anywhere in a full JSON
// serialization of ev (payload + envelope fields) or in ev's Go %#v dump.
// Serializing the whole event, not just inspecting known fields, guards
// against a future edit accidentally adding a new field that carries raw
// text without this test needing to be updated to look at it explicitly.
func assertNoRawText(t *testing.T, ev v1.Event, needle, label string) {
	t.Helper()

	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if strings.Contains(string(b), needle) {
		t.Errorf("raw %s text leaked into JSON-serialized Event: %s", label, string(b))
	}

	dump := fmt.Sprintf("%#v", ev)
	if strings.Contains(dump, needle) {
		t.Errorf("raw %s text leaked into Event Go representation: %s", label, dump)
	}

	for k, v := range ev.Payload {
		if s, ok := v.(string); ok && strings.Contains(s, needle) {
			t.Errorf("raw %s text leaked into Payload[%q] = %q", label, k, s)
		}
	}
}
