// privacy_test.go is the codex adapter's raw-text-absence gate
// (Constitution §7 rule 2), mirroring internal/telemetry/claude's
// privacy_test.go: for every fixture that embeds sensitive-shaped raw text
// (prompt text in the hook payloads, assistant/user message text in the
// rollout), the produced events, their persisted rows, and the parsers'
// error strings must never carry that text. The rollout case is the one
// claude has no analog for — rollout files hold FULL conversation text,
// and this pipeline reads them at every Stop, so the reader's
// numbers-only projection is load-bearing privacy, not just tidiness.
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	codexhooks "github.com/huaiche94/auspex/internal/hooks/codex"
	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// Needles copied verbatim from the fixture files; the self-check in the
// test fails loudly if the fixtures drift so the gate never silently
// weakens.
const (
	needlePromptNormal   = "Refactor the rollout reader to use a bounded tail window."
	needlePromptUnknown  = "Add a retry loop around the rollout scanner."
	needleStopAssistant  = "Done - the rollout reader now scans a bounded tail window."
	needleRolloutUser    = "SECRET user prose that must never persist."
	needleRolloutAgent   = "SECRET assistant prose that must never persist."
	rolloutTextFixture   = "with_message_text.jsonl"
	fixtureNeedleMissing = "privacy fixture table is stale: %s no longer contains needle %q - update the table"
)

func TestPrivacy_UserPromptSubmit_RawPromptNeverInEventOrRow(t *testing.T) {
	cases := []struct{ file, needle string }{
		{"normal.json", needlePromptNormal},
		{"unknown_fields.json", needlePromptUnknown},
	}

	db := openFixtureSuiteDB(t)
	store := claudetelemetry.NewEventStore(db)
	ctx := context.Background()
	n, clock := newTestNormalizer() // shared: EventIDs must stay distinct across the loop

	for _, tc := range cases {
		raw := fixture(t, "userpromptsubmit", tc.file)
		if !strings.Contains(string(raw), tc.needle) {
			t.Fatalf(fixtureNeedleMissing, tc.file, tc.needle)
		}
		parsed, err := codexhooks.ParseUserPromptSubmit(raw)
		if err != nil {
			t.Fatalf("ParseUserPromptSubmit(%s): %v", tc.file, err)
		}
		if dump := fmt.Sprintf("%#v", parsed); strings.Contains(dump, tc.needle) {
			t.Errorf("%s: raw prompt leaked into parsed struct", tc.file)
		}

		ev := n.NormalizeUserPromptSubmit(parsed, clock.Now())
		assertNoRawText(t, ev, tc.needle, "user prompt")

		if err := store.PersistAll(ctx, db, []v1.Event{ev}); err != nil {
			t.Fatalf("PersistAll: %v", err)
		}
		assertStoredRowClean(t, ctx, store, ev.EventID, tc.needle, "user prompt")
	}
}

func TestPrivacy_UserPromptSubmit_PayloadFieldsAreBoolsCountsAndKnownStrings(t *testing.T) {
	n, clock := newTestNormalizer()
	parsed, err := codexhooks.ParseUserPromptSubmit(fixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatal(err)
	}
	ev := n.NormalizeUserPromptSubmit(parsed, clock.Now())

	// Same structural pin as claude's: strings are the only type that can
	// carry raw prompt text, so every string payload key must be on the
	// explicit allow-list — a new string key is a privacy-review event.
	allowedStringKeys := map[string]bool{
		"prompt_sha256":          true, // fixed-alphabet digest
		"cwd":                    true, // a path from the hook envelope
		"model_id":               true, // provider model identifier
		"prompt_feature_version": true, // fixed compile-time constant
		"task_class":             true, // features codec enum
	}
	for k, v := range ev.Payload {
		if s, isString := v.(string); isString && !allowedStringKeys[k] {
			t.Errorf("payload field %q is a string (%q) not on the privacy allow-list", k, s)
		}
	}
}

func TestPrivacy_Stop_LastAssistantMessageNeverInEventOrRow(t *testing.T) {
	raw := fixture(t, "stop", "normal.json")
	if !strings.Contains(string(raw), needleStopAssistant) {
		t.Fatalf(fixtureNeedleMissing, "stop/normal.json", needleStopAssistant)
	}
	parsed, err := codexhooks.ParseStop(raw)
	if err != nil {
		t.Fatal(err)
	}

	n, clock := newTestNormalizer()
	events := n.NormalizeStop(parsed, clock.Now(), normalSnapshot(t))

	db := openFixtureSuiteDB(t)
	store := claudetelemetry.NewEventStore(db)
	ctx := context.Background()
	if err := store.PersistAll(ctx, db, events); err != nil {
		t.Fatalf("PersistAll: %v", err)
	}
	for _, ev := range events {
		assertNoRawText(t, ev, needleStopAssistant, "assistant message")
		assertStoredRowClean(t, ctx, store, ev.EventID, needleStopAssistant, "assistant message")
	}
}

// TestPrivacy_Rollout_MessageTextNeverLeavesTheReader is the codex-specific
// gate: a rollout containing full user/assistant message text yields a
// snapshot, events, and rows carrying NONE of it.
func TestPrivacy_Rollout_MessageTextNeverLeavesTheReader(t *testing.T) {
	path := rolloutFixturePath(t, rolloutTextFixture)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{needleRolloutUser, needleRolloutAgent} {
		if !strings.Contains(string(raw), needle) {
			t.Fatalf(fixtureNeedleMissing, rolloutTextFixture, needle)
		}
	}

	snap, ok := ReadRolloutSnapshot(path)
	if !ok {
		t.Fatal("ReadRolloutSnapshot failed on the with_message_text fixture")
	}
	dump := fmt.Sprintf("%#v", snap)
	for _, needle := range []string{needleRolloutUser, needleRolloutAgent} {
		if strings.Contains(dump, needle) {
			t.Errorf("rollout message text leaked into RolloutSnapshot: %s", dump)
		}
	}

	parsed, err := codexhooks.ParseStop(fixture(t, "stop", "normal.json"))
	if err != nil {
		t.Fatal(err)
	}
	n, clock := newTestNormalizer()
	events := n.NormalizeStop(parsed, clock.Now(), &snap)

	db := openFixtureSuiteDB(t)
	store := claudetelemetry.NewEventStore(db)
	ctx := context.Background()
	if err := store.PersistAll(ctx, db, events); err != nil {
		t.Fatalf("PersistAll: %v", err)
	}
	for _, ev := range events {
		for _, needle := range []string{needleRolloutUser, needleRolloutAgent} {
			assertNoRawText(t, ev, needle, "rollout message")
			assertStoredRowClean(t, ctx, store, ev.EventID, needle, "rollout message")
		}
	}
}

// TestPrivacy_MalformedPayloads_ErrorsNeverEchoContent exercises the
// parsers' error strings — the only text these packages can emit toward a
// log — against every needle.
func TestPrivacy_MalformedPayloads_ErrorsNeverEchoContent(t *testing.T) {
	needles := []string{needlePromptNormal, needlePromptUnknown, needleStopAssistant}
	malformed := []struct {
		dir   string
		parse func(raw []byte) error
	}{
		{"sessionstart", func(raw []byte) error { _, err := codexhooks.ParseSessionStart(raw); return err }},
		{"userpromptsubmit", func(raw []byte) error { _, err := codexhooks.ParseUserPromptSubmit(raw); return err }},
		{"stop", func(raw []byte) error { _, err := codexhooks.ParseStop(raw); return err }},
	}
	for _, mc := range malformed {
		err := mc.parse(fixture(t, mc.dir, "malformed.json"))
		if err == nil {
			t.Fatalf("%s/malformed.json: expected a parse error", mc.dir)
		}
		for _, needle := range needles {
			if strings.Contains(err.Error(), needle) {
				t.Errorf("%s: parse error leaked raw text: %q", mc.dir, err.Error())
			}
		}
	}

	// Validation error for a payload that DOES carry a prompt: the fixed
	// missing-session_id message must not interpolate it.
	noSession := []byte(`{"hook_event_name":"UserPromptSubmit","prompt":` + jsonString(needlePromptNormal) + `}`)
	_, err := codexhooks.ParseUserPromptSubmit(noSession)
	if err == nil {
		t.Fatal("expected missing-session_id error")
	}
	if strings.Contains(err.Error(), needlePromptNormal) {
		t.Errorf("validation error leaked raw prompt: %q", err.Error())
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// assertNoRawText fails if needle appears anywhere in a full JSON
// serialization of ev or in its Go %#v dump — the whole-value technique
// claude's privacy_test.go established.
func assertNoRawText(t *testing.T, ev v1.Event, needle, label string) {
	t.Helper()
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if strings.Contains(string(b), needle) {
		t.Errorf("raw %s leaked into JSON-serialized Event: %s", label, b)
	}
	if dump := fmt.Sprintf("%#v", ev); strings.Contains(dump, needle) {
		t.Errorf("raw %s leaked into Event struct dump: %s", label, dump)
	}
}

// assertStoredRowClean reads the persisted row back (every logical column
// StoredEvent surfaces plus the payload) and checks for the needle.
func assertStoredRowClean(t *testing.T, ctx context.Context, store *claudetelemetry.EventStore, eventID, needle, label string) {
	t.Helper()
	stored, err := store.GetByEventID(ctx, eventID)
	if err != nil {
		t.Fatalf("GetByEventID(%s): %v", eventID, err)
	}
	dump := fmt.Sprintf("%#v", stored)
	if strings.Contains(dump, needle) {
		t.Errorf("persisted row for %s leaked raw %s: %s", eventID, label, dump)
	}
}
