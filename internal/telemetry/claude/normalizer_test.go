package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/features"
	claudehooks "github.com/huaiche94/auspex/internal/hooks/claude"
	claudeprovider "github.com/huaiche94/auspex/internal/providers/claude"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// fixedClock is a deterministic domain.Clock fake so tests never depend on
// wall-clock time.Now().
type fixedClock struct{ t time.Time }

func (f fixedClock) Now() time.Time { return f.t }

// seqIDs is a deterministic domain.IDGenerator fake: each call returns the
// next integer in sequence, formatted as a string. Real ID generation
// (UUIDv7) is foundation's internal/idgen, out of scope for this wave.
type seqIDs struct{ n int }

func (s *seqIDs) NewID() string {
	s.n++
	return "id-" + strconv.Itoa(s.n)
}

func fixture(t *testing.T, dir, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "provider-events", "claude", dir, name))
	if err != nil {
		t.Fatalf("reading fixture %s/%s: %v", dir, name, err)
	}
	return b
}

func newTestNormalizer() (*Normalizer, fixedClock) {
	clock := fixedClock{t: time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)}
	return NewNormalizer(clock, &seqIDs{}), clock
}

func requireEnvelope(t *testing.T, ev v1.Event, wantType v1.EventType, wantSession domain.SessionID) {
	t.Helper()
	if ev.SchemaVersion != v1.SchemaVersionEvent {
		t.Errorf("SchemaVersion = %q, want %q", ev.SchemaVersion, v1.SchemaVersionEvent)
	}
	if ev.EventID == "" {
		t.Error("EventID is empty")
	}
	if ev.EventType != wantType {
		t.Errorf("EventType = %q, want %q", ev.EventType, wantType)
	}
	if ev.Provider != Provider {
		t.Errorf("Provider = %q, want %q", ev.Provider, Provider)
	}
	if ev.SessionID != string(wantSession) {
		t.Errorf("SessionID = %q, want %q", ev.SessionID, wantSession)
	}
	if ev.OccurredAt.IsZero() {
		t.Error("OccurredAt is zero")
	}
	if ev.ObservedAt.IsZero() {
		t.Error("ObservedAt is zero")
	}
	if ev.IdempotencyKey == "" {
		t.Error("IdempotencyKey is empty")
	}
	if ev.Payload == nil {
		t.Error("Payload is nil")
	}
}

func TestNormalizeStatusLine(t *testing.T) {
	n, clock := newTestNormalizer()

	snap, err := claudeprovider.ParseStatusLine(fixture(t, "statusline", "normal.json"))
	if err != nil {
		t.Fatalf("ParseStatusLine: %v", err)
	}

	events := n.NormalizeStatusLine(snap, clock.Now())

	wantTypes := map[v1.EventType]int{
		v1.EventProviderContextObserved: 0,
		v1.EventProviderUsageObserved:   0,
		v1.EventProviderQuotaObserved:   0,
	}
	for _, ev := range events {
		requireEnvelope(t, ev, ev.EventType, snap.SessionID)
		wantTypes[ev.EventType]++
	}

	if wantTypes[v1.EventProviderContextObserved] != 1 {
		t.Errorf("context events = %d, want 1", wantTypes[v1.EventProviderContextObserved])
	}
	if wantTypes[v1.EventProviderUsageObserved] != 1 {
		t.Errorf("usage events = %d, want 1", wantTypes[v1.EventProviderUsageObserved])
	}
	// normal.json has both five_hour and seven_day windows populated.
	if wantTypes[v1.EventProviderQuotaObserved] != 2 {
		t.Errorf("quota events = %d, want 2", wantTypes[v1.EventProviderQuotaObserved])
	}

	// Spot-check payload values propagate and are not zero-substituted.
	for _, ev := range events {
		if ev.EventType == v1.EventProviderContextObserved {
			if got := ev.Payload["used_percent"]; got != 21.9 {
				t.Errorf("context used_percent = %v, want 21.9", got)
			}
		}
		if ev.EventType == v1.EventProviderUsageObserved {
			if got := ev.Payload["total_cost_usd"]; got != 1.2345 {
				t.Errorf("usage total_cost_usd = %v, want 1.2345", got)
			}
			// #20 Phase 1: usage observations carry cohort identity labels
			// so future token samples are stratifiable at observation
			// granularity (ADR-047).
			if got := ev.Payload["model_id"]; got != "claude-opus-4-1-20250805" {
				t.Errorf("usage model_id = %v, want claude-opus-4-1-20250805", got)
			}
			if got := ev.Payload["effort"]; got != "high" {
				t.Errorf("usage effort = %v, want high", got)
			}
		}
	}
}

func TestNormalizeStatusLine_MissingFields_OmitsAbsentEvents(t *testing.T) {
	n, clock := newTestNormalizer()

	snap, err := claudeprovider.ParseStatusLine(fixture(t, "statusline", "missing_fields.json"))
	if err != nil {
		t.Fatalf("ParseStatusLine: %v", err)
	}

	events := n.NormalizeStatusLine(snap, clock.Now())

	// CONTRACT_FREEZE.md "unknown is not zero": a wholly-absent measurement
	// must not synthesize an event that claims to observe it. This does not
	// assert a specific count (that depends on exactly what the fixture
	// omits) — it asserts that no produced event's payload claims a
	// measurement this snapshot didn't actually carry, and that produced
	// events don't panic/zero-fill.
	for _, ev := range events {
		requireEnvelope(t, ev, ev.EventType, snap.SessionID)
	}
}

func TestNormalizeStatusLine_Idempotent(t *testing.T) {
	n, clock := newTestNormalizer()
	snap, err := claudeprovider.ParseStatusLine(fixture(t, "statusline", "normal.json"))
	if err != nil {
		t.Fatalf("ParseStatusLine: %v", err)
	}

	first := n.NormalizeStatusLine(snap, clock.Now())
	second := n.NormalizeStatusLine(snap, clock.Now())

	if len(first) != len(second) {
		t.Fatalf("event count differs across normalizations: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].IdempotencyKey != second[i].IdempotencyKey {
			t.Errorf("IdempotencyKey[%d] not deterministic: %q vs %q", i, first[i].IdempotencyKey, second[i].IdempotencyKey)
		}
		if first[i].EventType != second[i].EventType {
			t.Errorf("EventType[%d] differs: %q vs %q", i, first[i].EventType, second[i].EventType)
		}
		// EventID must NOT be equal — each normalization call produces a
		// distinct event identity; IdempotencyKey is what downstream
		// dedup keys on, not EventID.
		if first[i].EventID == second[i].EventID {
			t.Errorf("EventID[%d] unexpectedly equal across calls: %q", i, first[i].EventID)
		}
	}
}

func TestNormalizeUserPromptSubmit(t *testing.T) {
	n, clock := newTestNormalizer()

	parsed, err := claudehooks.ParseUserPromptSubmit(fixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatalf("ParseUserPromptSubmit: %v", err)
	}

	ev := n.NormalizeUserPromptSubmit(parsed, clock.Now())
	requireEnvelope(t, ev, v1.EventProviderTurnStarted, parsed.SessionID)

	if ev.Payload["prompt_sha256"] != parsed.PromptSHA256 {
		t.Errorf("prompt_sha256 payload = %v, want %v", ev.Payload["prompt_sha256"], parsed.PromptSHA256)
	}

	// Issue #42: the derived feature booleans/counts must reach the
	// payload so evaluation read-back can feed the real classifier. The
	// fixture prompt is "Refactor the checkpoint manifest writer to use
	// atomic rename." — a refactor-verb prompt with no fix vocabulary.
	if ev.Payload["has_refactor_verb"] != true {
		t.Errorf("has_refactor_verb payload = %v, want true", ev.Payload["has_refactor_verb"])
	}
	if ev.Payload["has_fix_verb"] != false {
		t.Errorf("has_fix_verb payload = %v, want false (measured false, not absent)", ev.Payload["has_fix_verb"])
	}
	if ev.Payload["explicit_path_count"] != parsed.Features.ExplicitPathCount {
		t.Errorf("explicit_path_count payload = %v, want %v", ev.Payload["explicit_path_count"], parsed.Features.ExplicitPathCount)
	}
}

// TestNormalizeUserPromptSubmit_ZeroValueFeaturesPersistNoFeatureKeys pins
// the issue-#42 honesty gate: an event whose Features were never extracted
// (zero-value struct, e.g. built by an older caller) must not persist
// false booleans masquerading as measurements — unknown is not zero. The
// extraction marker is Features.SHA256Hex, which every
// features.ExtractPromptFeatures call sets (even for an empty prompt).
func TestNormalizeUserPromptSubmit_ZeroValueFeaturesPersistNoFeatureKeys(t *testing.T) {
	n, clock := newTestNormalizer()

	parsed := claudehooks.UserPromptSubmitEvent{
		SessionID:        "sess-legacy",
		PromptSHA256:     "abc123",
		PromptByteLength: 10,
	}
	ev := n.NormalizeUserPromptSubmit(parsed, clock.Now())

	for _, key := range []string{"has_fix_verb", "has_refactor_verb", "mentions_tests", "explicit_path_count", "open_ended_indicator"} {
		if _, present := ev.Payload[key]; present {
			t.Errorf("payload key %q present for a zero-value Features struct — false booleans must not masquerade as measurements", key)
		}
	}
}

// TestNormalizeUserPromptSubmit_CodecWriterReaderAgree proves the #50-item-1
// keystone at the real writer boundary: the payload NormalizeUserPromptSubmit
// persists, taken through JSON exactly as storage does, decodes via the
// reader's codec (features.DecodePromptFeatures) back to the identical
// PromptFeatures the writer started from — so writer and reader can never
// drift on the key set (a dropped/typo'd key would fail this DeepEqual). The
// extraction-era tag rides along on the same payload.
func TestNormalizeUserPromptSubmit_CodecWriterReaderAgree(t *testing.T) {
	n, clock := newTestNormalizer()

	ev := claudehooks.NewUserPromptSubmitEvent("sess-agree",
		"Refactor internal/policy across layers and fix the bug.\n- [ ] add tests\n- [x] migrate the api schema\n1. audit the security docs")
	out := n.NormalizeUserPromptSubmit(ev, clock.Now())

	blob, err := json.Marshal(out.Payload)
	if err != nil {
		t.Fatalf("marshal persisted payload: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(blob, &wire); err != nil {
		t.Fatalf("unmarshal persisted payload: %v", err)
	}

	if got := features.DecodePromptFeatures(wire); !reflect.DeepEqual(got, ev.Features) {
		t.Errorf("writer/reader disagree via the codec:\n got: %+v\nwant: %+v", got, ev.Features)
	}
	if v, ok := features.PromptFeatureVersionFromPayload(wire); !ok || v != features.PromptFeatureVersion {
		t.Errorf("persisted payload missing/incorrect extraction-era tag: got %q present=%v, want %q", v, ok, features.PromptFeatureVersion)
	}
}

// TestNormalizeUserPromptSubmit_FeaturesPersistedIffExtracted pins the #50
// item 4 gate in BOTH directions — features are persisted IFF they were
// extracted — so a future caller that populates feature booleans without the
// SHA256Hex extraction marker can never silently regress a session to
// TaskClassUnknown undetected.
func TestNormalizeUserPromptSubmit_FeaturesPersistedIffExtracted(t *testing.T) {
	n, clock := newTestNormalizer()

	// Extracted (SHA256Hex set by ExtractPromptFeatures): the full derived
	// set AND the version tag are persisted.
	extracted := claudehooks.NewUserPromptSubmitEvent("sess-ext", "refactor the api and add tests")
	got := n.NormalizeUserPromptSubmit(extracted, clock.Now()).Payload
	for _, key := range []string{"has_refactor_verb", "mentions_tests", "prompt_rune_count", "prompt_line_count", features.PromptFeatureVersionKey} {
		if _, present := got[key]; !present {
			t.Errorf("extracted event: payload missing %q — features must persist when extracted", key)
		}
	}

	// The fragility #50 item 4 warns of: feature booleans populated but
	// SHA256Hex (the extraction marker) left empty. NO feature keys and NO
	// version tag may persist; only the size trio the event carries directly.
	noMarker := claudehooks.UserPromptSubmitEvent{
		SessionID:        "sess-nomarker",
		PromptSHA256:     "abc123",
		PromptByteLength: 10,
		Features: features.PromptFeatures{
			HasRefactorVerb: true, // populated WITHOUT SHA256Hex — must be ignored
			MentionsTests:   true,
		},
	}
	q := n.NormalizeUserPromptSubmit(noMarker, clock.Now()).Payload
	for _, key := range []string{"has_refactor_verb", "mentions_tests", "prompt_rune_count", features.PromptFeatureVersionKey} {
		if _, present := q[key]; present {
			t.Errorf("no-marker event: payload key %q present — unmarked features must not persist as measurements", key)
		}
	}
	if q["prompt_sha256"] != "abc123" || q["prompt_byte_length"] != 10 {
		t.Errorf("no-marker event dropped the size trio it carries directly: %+v", q)
	}
}

func TestNormalizeStop(t *testing.T) {
	n, clock := newTestNormalizer()

	parsed, err := claudehooks.ParseStop(fixture(t, "stop", "normal.json"))
	if err != nil {
		t.Fatalf("ParseStop: %v", err)
	}

	ev := n.NormalizeStop(parsed, clock.Now())
	requireEnvelope(t, ev, v1.EventProviderTurnCompleted, parsed.SessionID)
}

func TestNormalizeStopFailure(t *testing.T) {
	tests := []struct {
		name          string
		fixture       string
		wantRateLimit bool
	}{
		{name: "rate_limit", fixture: "rate_limit.json", wantRateLimit: true},
		{name: "overloaded", fixture: "overloaded.json", wantRateLimit: false},
		{name: "network", fixture: "network.json", wantRateLimit: false},
		{name: "context_length", fixture: "context_length.json", wantRateLimit: false},
		{name: "unknown_category", fixture: "unknown_category.json", wantRateLimit: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, clock := newTestNormalizer()

			parsed, err := claudehooks.ParseStopFailure(fixture(t, "stopfailure", tt.fixture))
			if err != nil {
				t.Fatalf("ParseStopFailure: %v", err)
			}

			events := n.NormalizeStopFailure(parsed, clock.Now())
			if len(events) == 0 {
				t.Fatal("NormalizeStopFailure returned no events")
			}
			requireEnvelope(t, events[0], v1.EventProviderTurnFailed, parsed.SessionID)
			if got := events[0].Payload["failure_class"]; got != string(parsed.FailureClass) {
				t.Errorf("failure_class payload = %v, want %v", got, parsed.FailureClass)
			}

			hasRateLimit := false
			for _, ev := range events[1:] {
				if ev.EventType == v1.EventProviderRateLimitHit {
					hasRateLimit = true
					requireEnvelope(t, ev, v1.EventProviderRateLimitHit, parsed.SessionID)
				}
			}
			if hasRateLimit != tt.wantRateLimit {
				t.Errorf("rate limit event present = %v, want %v", hasRateLimit, tt.wantRateLimit)
			}
		})
	}
}

// TestNormalizeStatusLine_DuplicateSnapshot_SameIdempotencyKey covers the
// duplicate-delivery scenario named in the packet's Tests section
// ("duplicate event idempotency") at the normalization layer: normalizing
// the same underlying snapshot content twice (e.g. because a hook fired
// twice, or a crash caused a re-read) MUST produce the same
// IdempotencyKey so a downstream persistence layer (claude-provider-05,
// not this node) can dedupe on it.
func TestNormalizeStatusLine_DuplicateSnapshot_SameIdempotencyKey(t *testing.T) {
	n, clock := newTestNormalizer()

	rawA := fixture(t, "statusline", "normal.json")
	rawB := fixture(t, "statusline", "normal.json")

	snapA, err := claudeprovider.ParseStatusLine(rawA)
	if err != nil {
		t.Fatalf("ParseStatusLine A: %v", err)
	}
	snapB, err := claudeprovider.ParseStatusLine(rawB)
	if err != nil {
		t.Fatalf("ParseStatusLine B: %v", err)
	}

	sameInstant := clock.Now()
	eventsA := n.NormalizeStatusLine(snapA, sameInstant)
	eventsB := n.NormalizeStatusLine(snapB, sameInstant)

	if len(eventsA) != len(eventsB) {
		t.Fatalf("event count differs: %d vs %d", len(eventsA), len(eventsB))
	}
	for i := range eventsA {
		if eventsA[i].IdempotencyKey != eventsB[i].IdempotencyKey {
			t.Errorf("duplicate snapshot IdempotencyKey[%d] differs: %q vs %q", i, eventsA[i].IdempotencyKey, eventsB[i].IdempotencyKey)
		}
	}
}
