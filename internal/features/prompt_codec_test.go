package features

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/huaiche94/auspex/internal/domain"
)

// TestPromptCodec_RoundTrip is the #50-item-1 keystone proof: Encode then
// Decode is the identity on realistic extracted features, BOTH in-process
// (the writer hands the reader a Go map directly) and across a JSON round
// trip (the production path, where numbers become float64 in storage and
// must survive back to int). If a key were dropped or mistyped on one side
// of the codec, the field it carries would not survive this round trip.
func TestPromptCodec_RoundTrip(t *testing.T) {
	prompts := []string{
		"Refactor internal/policy across layers.\n- [ ] add tests\n- update docs\n1. migrate schema",
		"fix the crash in internal/auth/session.go and add unit tests for the api",
		"why is the endpoint slow? profile the performance and optimize the schema",
		"", // empty prompt: still a real extraction (SHA256Hex set, all-false features)
		"See the design document for the whole repo security audit — sanitize every endpoint",
	}
	for _, p := range prompts {
		orig := ExtractPromptFeatures(p)

		if got := DecodePromptFeatures(EncodePromptFeatures(orig)); !reflect.DeepEqual(got, orig) {
			t.Errorf("in-process round trip changed features for %q:\n got: %+v\nwant: %+v", p, got, orig)
		}

		blob, err := json.Marshal(EncodePromptFeatures(orig))
		if err != nil {
			t.Fatalf("marshal encoded payload: %v", err)
		}
		var wire map[string]any
		if err := json.Unmarshal(blob, &wire); err != nil {
			t.Fatalf("unmarshal encoded payload: %v", err)
		}
		if got := DecodePromptFeatures(wire); !reflect.DeepEqual(got, orig) {
			t.Errorf("JSON round trip changed features for %q:\n got: %+v\nwant: %+v", p, got, orig)
		}
	}
}

// TestPromptCodec_VersionStamped pins issue #50 item 2: the codec stamps the
// extraction-era tag with the exact package constant, and an event that
// lacks the key is honestly reported as unknown-era (never silently treated
// as the current era). It also guards the privacy invariant that the version
// value is a fixed constant, not anything prompt-derived.
func TestPromptCodec_VersionStamped(t *testing.T) {
	enc := EncodePromptFeatures(ExtractPromptFeatures("fix the bug"))
	got, ok := PromptFeatureVersionFromPayload(enc)
	if !ok {
		t.Fatal("encoded payload is missing the prompt_feature_version key")
	}
	if got != PromptFeatureVersion {
		t.Errorf("prompt_feature_version = %q, want %q", got, PromptFeatureVersion)
	}
	if enc[PromptFeatureVersionKey] != PromptFeatureVersion {
		t.Errorf("version key %q = %v, want the constant", PromptFeatureVersionKey, enc[PromptFeatureVersionKey])
	}

	// A payload without the key predates #50's stamping: unknown-era.
	if v, ok := PromptFeatureVersionFromPayload(map[string]any{"prompt_sha256": "abc"}); ok {
		t.Errorf("PromptFeatureVersionFromPayload reported present=%v (value %q) for a payload with no version key", ok, v)
	}
}

// TestPromptCodec_DecodesFlatLegacyEvent is the BINDING historical-
// decodability proof (#50): events persisted BEFORE this change stored the
// keys FLAT at payload top level and carried NO version tag. The codec must
// keep decoding those correctly — a #47-widened refactor prompt must still
// read back has_refactor_verb=true, not collapse to all-false /
// TaskClassUnknown. This hand-builds such a legacy payload (not via Encode)
// so the test is a genuine wire-compatibility check, not a tautology.
func TestPromptCodec_DecodesFlatLegacyEvent(t *testing.T) {
	// JSON numbers decode as float64 — reproduce that exactly (a real legacy
	// payload always arrives via json.Unmarshal).
	legacy := map[string]any{
		"prompt_sha256":        "deadbeef",
		"prompt_byte_length":   float64(42),
		"prompt_approx_tokens": float64(9),
		"prompt_rune_count":    float64(40),
		"prompt_line_count":    float64(3),
		"explicit_path_count":  float64(1),
		"list_item_count":      float64(2),
		"has_refactor_verb":    true,
		"mentions_tests":       true,
		// deliberately NOT present: has_fix_verb, mentions_security, the
		// version tag, etc. — an old event that never measured them.
	}
	pf := DecodePromptFeatures(legacy)

	if pf.SHA256Hex != "deadbeef" {
		t.Errorf("SHA256Hex = %q, want deadbeef", pf.SHA256Hex)
	}
	if pf.ByteLength != 42 || pf.ApproxTokens != 9 || pf.RuneCount != 40 || pf.LineCount != 3 {
		t.Errorf("size fields decoded wrong: %+v", pf)
	}
	if pf.ExplicitPathCount != 1 || pf.ListItemCount != 2 {
		t.Errorf("count fields decoded wrong: paths=%d list=%d", pf.ExplicitPathCount, pf.ListItemCount)
	}
	if !pf.HasRefactorVerb || !pf.MentionsTests {
		t.Error("a flat legacy event lost its measured-true signals — historical decodability regressed")
	}
	// Absent keys must decode to the honest zero, never a fabricated default.
	if pf.HasFixVerb || pf.MentionsSecurity || pf.RepositoryWideIndicator {
		t.Error("absent keys decoded to true — unknown must not be fabricated")
	}
	// TokenConfidence is a fixed invariant restored by Decode, not stored.
	if pf.TokenConfidence != domain.ConfidenceLow {
		t.Errorf("TokenConfidence = %q, want %q", pf.TokenConfidence, domain.ConfidenceLow)
	}
}

// TestPromptCodec_DecodesPre42SizeOnlyEvent covers the oldest events, which
// carried only the size trio (no derived features at all). They must decode
// to their real sizes plus all-false features — exactly the TaskClassUnknown
// cold-start the classifier is designed to return, never an error.
func TestPromptCodec_DecodesPre42SizeOnlyEvent(t *testing.T) {
	pf := DecodePromptFeatures(map[string]any{
		"prompt_sha256":        "cafe",
		"prompt_byte_length":   float64(5),
		"prompt_approx_tokens": float64(1),
	})
	if pf.SHA256Hex != "cafe" || pf.ByteLength != 5 || pf.ApproxTokens != 1 {
		t.Errorf("size trio decoded wrong: %+v", pf)
	}
	want := PromptFeatures{
		SHA256Hex:       "cafe",
		ByteLength:      5,
		ApproxTokens:    1,
		TokenConfidence: domain.ConfidenceLow,
	}
	if !reflect.DeepEqual(pf, want) {
		t.Errorf("pre-#42 event did not decode to size + all-false:\n got: %+v\nwant: %+v", pf, want)
	}
}
