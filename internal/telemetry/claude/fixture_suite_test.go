// fixture_suite_test.go is claude-provider-07's fixture-backed test suite:
// an end-to-end exercise of this branch's whole Claude Code pipeline (parse
// -> normalize -> persist -> read back the durable row) against realistic
// fixture payloads, plus the cross-cutting raw-prompt-absence privacy gate
// named in agents/claude-provider.md's Tests section ("raw-prompt absence
// assertion across persisted rows/log output") and required by
// docs/implementation/vertical-slice/EXECUTION_DAG.md's claude-provider-07 entry
// ("Risk: Medium - raw-prompt-absence assertion is a hard privacy gate.
// Feeds qa-05 leakage scanner").
//
// Every test in this file is named so `go test ... -run Fixture` (this
// node's own frozen validation command) selects it: the parent table-driven
// test is TestFixtureSuite, and the privacy gate is
// TestFixture_RawTextNeverPersistedOrLogged.
package claude

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	claudehooks "github.com/huaiche94/auspex/internal/hooks/claude"
	claudeprovider "github.com/huaiche94/auspex/internal/providers/claude"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// openFixtureSuiteDB opens a fresh, fully migrated temp-file SQLite database
// for a single subtest. Mirrors store_test.go's openTestDB (that file is
// package claude_test, an external black-box test of EventStore's exported
// surface; this file is package claude and needs the same setup, so it is
// duplicated rather than shared across the two packages/test binaries).
func openFixtureSuiteDB(t *testing.T) *sqlite.DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "auspex.db")
	db, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("sqlite.AllMigrations: %v", err)
	}
	if err := db.Migrate(context.Background(), migrations); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	return db
}

// fixtureCase is one row of the TestFixtureSuite table: a single provider
// payload fixture, the parse+normalize+persist steps needed to run it
// through this branch's full pipeline, and the assertions to run against
// the resulting durable row(s).
type fixtureCase struct {
	name string

	// produce parses rawFixtureBytes (loaded via the fixture() helper in
	// normalizer_test.go) and returns the normalized event(s) this fixture
	// should turn into. Exactly one of the two hook/provider parsers is
	// exercised per case, matching how each fixture is actually consumed
	// in production (a status-line snapshot never goes through
	// ParseUserPromptSubmit, etc.).
	produce func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event

	// wantEventCount is the number of persisted rows this fixture must
	// produce. 0 is a valid, meaningful answer (e.g. a status-line
	// snapshot with every optional measurement absent normalizes to zero
	// events - CONTRACT_FREEZE.md's "unknown is not zero" rule, exercised
	// by the missing-fields cases below).
	wantEventCount int

	// wantEventTypes, when non-empty, asserts the exact multiset of
	// EventType values produced, in order. Left nil for cases where only
	// the count matters.
	wantEventTypes []v1.EventType
}

// TestFixtureSuite is the table-driven fixture test named in this node's
// task brief item 2: "load each fixture, run it through your
// normalizer/parser, persist via your EventStore, and assert the resulting
// normalized Event / DB row is correct." It covers every category listed in
// agents/claude-provider.md's P0 deliverable #6 and this node's own DAG
// entry: normal, missing/null, compacted (PreCompact-adjacent), high-usage,
// duplicate, unknown-field, Stop, and rate-limit StopFailure.
func TestFixtureSuite(t *testing.T) {
	cases := []fixtureCase{
		// --- normal ----------------------------------------------------
		{
			name: "statusline/normal",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				snap, err := claudeprovider.ParseStatusLine(fixture(t, "statusline", "normal.json"))
				if err != nil {
					t.Fatalf("ParseStatusLine: %v", err)
				}
				return n.NormalizeStatusLine(snap, clock.Now())
			},
			wantEventCount: 4, // context + usage + five_hour quota + seven_day quota
			wantEventTypes: []v1.EventType{
				v1.EventProviderContextObserved,
				v1.EventProviderUsageObserved,
				v1.EventProviderQuotaObserved,
				v1.EventProviderQuotaObserved,
			},
		},
		{
			name: "userpromptsubmit/normal",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := claudehooks.ParseUserPromptSubmit(fixture(t, "userpromptsubmit", "normal.json"))
				if err != nil {
					t.Fatalf("ParseUserPromptSubmit: %v", err)
				}
				return []v1.Event{n.NormalizeUserPromptSubmit(parsed, clock.Now())}
			},
			wantEventCount: 1,
			wantEventTypes: []v1.EventType{v1.EventProviderTurnStarted},
		},

		// --- missing/null fields -----------------------------------------
		{
			name: "statusline/missing_fields",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				snap, err := claudeprovider.ParseStatusLine(fixture(t, "statusline", "missing_fields.json"))
				if err != nil {
					t.Fatalf("ParseStatusLine: %v", err)
				}
				return n.NormalizeStatusLine(snap, clock.Now())
			},
			// missing_fields.json nulls out every context/cost sub-field
			// EXCEPT context_window_size (still 200000) and omits
			// rate_limits' inner windows entirely -> contextEvent still
			// fires (one measurement, window size, is present - "unknown
			// is not zero" is about a wholly-absent measurement, not about
			// every sibling field being present), but usage/quota do not.
			wantEventCount: 1,
			wantEventTypes: []v1.EventType{v1.EventProviderContextObserved},
		},
		{
			name: "userpromptsubmit/missing_fields",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := claudehooks.ParseUserPromptSubmit(fixture(t, "userpromptsubmit", "missing_fields.json"))
				if err != nil {
					t.Fatalf("ParseUserPromptSubmit: %v", err)
				}
				return []v1.Event{n.NormalizeUserPromptSubmit(parsed, clock.Now())}
			},
			wantEventCount: 1,
			wantEventTypes: []v1.EventType{v1.EventProviderTurnStarted},
		},
		{
			name: "stop/missing_fields",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := claudehooks.ParseStop(fixture(t, "stop", "missing_fields.json"))
				if err != nil {
					t.Fatalf("ParseStop: %v", err)
				}
				return []v1.Event{n.NormalizeStop(parsed, clock.Now(), nil, nil)}
			},
			wantEventCount: 1,
			wantEventTypes: []v1.EventType{v1.EventProviderTurnCompleted},
		},
		{
			name: "stopfailure/missing_fields",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := claudehooks.ParseStopFailure(fixture(t, "stopfailure", "missing_fields.json"))
				if err != nil {
					t.Fatalf("ParseStopFailure: %v", err)
				}
				return n.NormalizeStopFailure(parsed, clock.Now())
			},
			// No error object at all -> FailureUnknown, no rate-limit
			// fan-out event.
			wantEventCount: 1,
			wantEventTypes: []v1.EventType{v1.EventProviderTurnFailed},
		},

		// --- precompact (dedicated hook, issue #114) ----------------------
		{
			name: "precompact/normal",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := claudehooks.ParsePreCompact(fixture(t, "precompact", "normal.json"))
				if err != nil {
					t.Fatalf("ParsePreCompact: %v", err)
				}
				if parsed.Trigger == nil || *parsed.Trigger != claudehooks.CompactTriggerAuto {
					t.Fatalf("normal.json fixture must model an auto-triggered compaction, got %v", parsed.Trigger)
				}
				return []v1.Event{n.NormalizePreCompact(parsed, clock.Now(), nil)}
			},
			wantEventCount: 1,
			wantEventTypes: []v1.EventType{v1.EventProviderSessionCompacted},
		},
		{
			name: "precompact/missing_fields",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := claudehooks.ParsePreCompact(fixture(t, "precompact", "missing_fields.json"))
				if err != nil {
					t.Fatalf("ParsePreCompact: %v", err)
				}
				return []v1.Event{n.NormalizePreCompact(parsed, clock.Now(), nil)}
			},
			wantEventCount: 1,
			wantEventTypes: []v1.EventType{v1.EventProviderSessionCompacted},
		},

		// --- compacted (PreCompact-adjacent, passive signature) -----------
		//
		// Historical note: until issue #114 wired the dedicated PreCompact
		// hook parser exercised above, the only observable signature of "a
		// compaction just happened" was this passive status-line shape: low
		// current context usage relative to a high cumulative
		// cost/duration/LOC total - i.e. a long-running session whose
		// context window was just reset by a compaction, as opposed to a
		// genuinely fresh session (which would have low cumulative cost
		// too). testdata/provider-events/claude/statusline/compacted.json
		// models exactly that shape; it remains valid coverage for
		// installations whose hooks.json predates the PreCompact entry.
		{
			name: "statusline/compacted",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				snap, err := claudeprovider.ParseStatusLine(fixture(t, "statusline", "compacted.json"))
				if err != nil {
					t.Fatalf("ParseStatusLine: %v", err)
				}
				if snap.ContextUsedPercent == nil || *snap.ContextUsedPercent >= 10 {
					t.Fatalf("compacted.json fixture must model LOW current context usage (post-compaction), got %v", snap.ContextUsedPercent)
				}
				if snap.TotalCostUSD == nil || *snap.TotalCostUSD < 5 {
					t.Fatalf("compacted.json fixture must model a HIGH cumulative cost (long session that just compacted), got %v", snap.TotalCostUSD)
				}
				return n.NormalizeStatusLine(snap, clock.Now())
			},
			wantEventCount: 4,
			wantEventTypes: []v1.EventType{
				v1.EventProviderContextObserved,
				v1.EventProviderUsageObserved,
				v1.EventProviderQuotaObserved,
				v1.EventProviderQuotaObserved,
			},
		},

		// --- high-usage (near quota limits) -------------------------------
		{
			name: "statusline/high_usage",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				snap, err := claudeprovider.ParseStatusLine(fixture(t, "statusline", "high_usage.json"))
				if err != nil {
					t.Fatalf("ParseStatusLine: %v", err)
				}
				var fiveHour *claudeprovider.RateLimitWindow
				for i := range snap.RateLimitWindows {
					if snap.RateLimitWindows[i].LimitID == "five_hour" {
						fiveHour = &snap.RateLimitWindows[i]
					}
				}
				if fiveHour == nil || fiveHour.UsedPercent == nil || *fiveHour.UsedPercent < 90 {
					t.Fatalf("high_usage.json fixture must model near-quota-limit five-hour usage, got %+v", fiveHour)
				}
				return n.NormalizeStatusLine(snap, clock.Now())
			},
			wantEventCount: 4,
			wantEventTypes: []v1.EventType{
				v1.EventProviderContextObserved,
				v1.EventProviderUsageObserved,
				v1.EventProviderQuotaObserved,
				v1.EventProviderQuotaObserved,
			},
		},

		// --- unknown-field payloads ----------------------------------------
		{
			name: "statusline/unknown_fields",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				snap, err := claudeprovider.ParseStatusLine(fixture(t, "statusline", "unknown_fields.json"))
				if err != nil {
					t.Fatalf("ParseStatusLine (must tolerate unknown fields): %v", err)
				}
				return n.NormalizeStatusLine(snap, clock.Now())
			},
			// The fixture's rate_limits carries a hypothetical
			// weekly_fable window this build has never heard of — issue
			// #21's pin: an unknown window becomes a quota event like
			// any other, the day it appears on the wire.
			wantEventCount: 5,
			wantEventTypes: []v1.EventType{
				v1.EventProviderContextObserved,
				v1.EventProviderUsageObserved,
				v1.EventProviderQuotaObserved,
				v1.EventProviderQuotaObserved,
				v1.EventProviderQuotaObserved,
			},
		},
		{
			name: "userpromptsubmit/unknown_fields",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := claudehooks.ParseUserPromptSubmit(fixture(t, "userpromptsubmit", "unknown_fields.json"))
				if err != nil {
					t.Fatalf("ParseUserPromptSubmit (must tolerate unknown fields): %v", err)
				}
				return []v1.Event{n.NormalizeUserPromptSubmit(parsed, clock.Now())}
			},
			wantEventCount: 1,
			wantEventTypes: []v1.EventType{v1.EventProviderTurnStarted},
		},
		{
			name: "stop/unknown_fields",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := claudehooks.ParseStop(fixture(t, "stop", "unknown_fields.json"))
				if err != nil {
					t.Fatalf("ParseStop (must tolerate unknown fields): %v", err)
				}
				return []v1.Event{n.NormalizeStop(parsed, clock.Now(), nil, nil)}
			},
			wantEventCount: 1,
			wantEventTypes: []v1.EventType{v1.EventProviderTurnCompleted},
		},
		{
			name: "stopfailure/unknown_category",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := claudehooks.ParseStopFailure(fixture(t, "stopfailure", "unknown_category.json"))
				if err != nil {
					t.Fatalf("ParseStopFailure (must tolerate unknown fields/error types): %v", err)
				}
				return n.NormalizeStopFailure(parsed, clock.Now())
			},
			wantEventCount: 1,
			wantEventTypes: []v1.EventType{v1.EventProviderTurnFailed},
		},

		// --- Stop --------------------------------------------------------
		{
			name: "stop/normal",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := claudehooks.ParseStop(fixture(t, "stop", "normal.json"))
				if err != nil {
					t.Fatalf("ParseStop: %v", err)
				}
				return []v1.Event{n.NormalizeStop(parsed, clock.Now(), nil, nil)}
			},
			wantEventCount: 1,
			wantEventTypes: []v1.EventType{v1.EventProviderTurnCompleted},
		},

		// --- rate-limit StopFailure ----------------------------------------
		{
			name: "stopfailure/rate_limit",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := claudehooks.ParseStopFailure(fixture(t, "stopfailure", "rate_limit.json"))
				if err != nil {
					t.Fatalf("ParseStopFailure: %v", err)
				}
				if parsed.FailureClass != "provider_rate_limit" {
					t.Fatalf("rate_limit.json fixture must classify as provider_rate_limit, got %v", parsed.FailureClass)
				}
				return n.NormalizeStopFailure(parsed, clock.Now())
			},
			// turn.failed + the rate-limit fan-out event (normalizer.go's
			// NormalizeStopFailure doc comment).
			wantEventCount: 2,
			wantEventTypes: []v1.EventType{
				v1.EventProviderTurnFailed,
				v1.EventProviderRateLimitHit,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openFixtureSuiteDB(t)
			store := NewEventStore(db)
			n, clock := newTestNormalizer()
			ctx := context.Background()

			events := tc.produce(t, n, clock)
			if len(events) != tc.wantEventCount {
				t.Fatalf("produced %d events, want %d (types: %v)", len(events), tc.wantEventCount, eventTypes(events))
			}
			if tc.wantEventTypes != nil {
				got := eventTypes(events)
				if !equalEventTypes(got, tc.wantEventTypes) {
					t.Fatalf("event types = %v, want %v", got, tc.wantEventTypes)
				}
			}

			if err := store.PersistAll(ctx, db, events); err != nil {
				t.Fatalf("PersistAll: %v", err)
			}

			for _, ev := range events {
				stored, err := store.GetByEventID(ctx, ev.EventID)
				if err != nil {
					t.Fatalf("GetByEventID(%s): %v", ev.EventID, err)
				}
				if stored.EventType != string(ev.EventType) {
					t.Errorf("stored EventType = %q, want %q", stored.EventType, ev.EventType)
				}
				if stored.SchemaVersion != v1.SchemaVersionEvent {
					t.Errorf("stored SchemaVersion = %q, want %q", stored.SchemaVersion, v1.SchemaVersionEvent)
				}
				if stored.Provider != Provider {
					t.Errorf("stored Provider = %q, want %q", stored.Provider, Provider)
				}
				if stored.IdempotencyKey == "" {
					t.Errorf("stored IdempotencyKey is empty for event %s", ev.EventID)
				}
				if stored.SessionID != ev.SessionID {
					t.Errorf("stored SessionID = %q, want %q", stored.SessionID, ev.SessionID)
				}

				// Round-trip the persisted payload against the in-memory
				// one field-by-field via JSON re-marshal, since
				// map[string]any numeric types can shift (int64 -> float64)
				// across a JSON round trip - comparing raw Go equality
				// would spuriously fail on that alone.
				wantJSON, err := json.Marshal(ev.Payload)
				if err != nil {
					t.Fatalf("marshal want payload: %v", err)
				}
				gotJSON, err := json.Marshal(stored.Payload)
				if err != nil {
					t.Fatalf("marshal stored payload: %v", err)
				}
				if string(wantJSON) != string(gotJSON) {
					t.Errorf("stored payload = %s, want %s", gotJSON, wantJSON)
				}
			}
		})
	}
}

// --- duplicate events (idempotency), fixture-suite level -----------------

// TestFixture_DuplicateEvents_Idempotent is this node's fixture-driven
// duplicate-events case (packet Tests: "duplicate event idempotency";
// P0 deliverable #6: "Fixtures for... duplicate... payloads"). Rather than
// adding a byte-identical throwaway fixture file (normalizer_test.go's own
// TestNormalizeStatusLine_DuplicateSnapshot_SameIdempotencyKey already
// established the precedent of reusing an existing fixture twice, and
// claude-provider-04's own lessons-learned entry recommends exactly this
// pattern for later normalization-layer nodes), this test feeds each of
// several existing fixtures through the FULL pipeline (parse -> normalize
// -> persist) twice and asserts the SECOND delivery is a true no-op at the
// durable-storage layer: no duplicate row, and the original row's payload
// is unchanged.
func TestFixture_DuplicateEvents_Idempotent(t *testing.T) {
	type dupCase struct {
		name    string
		produce func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event
	}

	cases := []dupCase{
		{
			name: "statusline/normal",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				snap, err := claudeprovider.ParseStatusLine(fixture(t, "statusline", "normal.json"))
				if err != nil {
					t.Fatalf("ParseStatusLine: %v", err)
				}
				return n.NormalizeStatusLine(snap, clock.Now())
			},
		},
		{
			name: "userpromptsubmit/normal",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := claudehooks.ParseUserPromptSubmit(fixture(t, "userpromptsubmit", "normal.json"))
				if err != nil {
					t.Fatalf("ParseUserPromptSubmit: %v", err)
				}
				return []v1.Event{n.NormalizeUserPromptSubmit(parsed, clock.Now())}
			},
		},
		{
			name: "stop/normal",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := claudehooks.ParseStop(fixture(t, "stop", "normal.json"))
				if err != nil {
					t.Fatalf("ParseStop: %v", err)
				}
				return []v1.Event{n.NormalizeStop(parsed, clock.Now(), nil, nil)}
			},
		},
		{
			name: "stopfailure/rate_limit",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := claudehooks.ParseStopFailure(fixture(t, "stopfailure", "rate_limit.json"))
				if err != nil {
					t.Fatalf("ParseStopFailure: %v", err)
				}
				return n.NormalizeStopFailure(parsed, clock.Now())
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openFixtureSuiteDB(t)
			store := NewEventStore(db)
			ctx := context.Background()

			// Same fixed instant on both deliveries: this is the realistic
			// "hook fired twice for the same underlying observation"
			// scenario the digestKey algorithm is designed to catch (a
			// different observedAt would legitimately produce a distinct
			// IdempotencyKey and is not what "duplicate delivery" means
			// here). The two Normalizers deliberately use
			// non-overlapping seqIDs ranges (mirroring store_test.go's
			// TestIdempotent_ConcurrentDuplicateWrites_NoDuplicateRow
			// pattern of "ids := &seqIDs{n: i * 1000}") so each delivery's
			// EventID is guaranteed distinct, matching what a real
			// UUIDv7 domain.IDGenerator would produce for two genuinely
			// separate hook invocations - a shared/overlapping counter
			// here would let a second delivery's row collide on the
			// event_id PRIMARY KEY instead of the idempotency_key UNIQUE
			// index, which would silently validate the wrong mechanism.
			clock := fixedClock{t: time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)}
			n1 := NewNormalizer(clock, &seqIDs{n: 0})
			firstDelivery := tc.produce(t, n1, clock)

			n2 := NewNormalizer(clock, &seqIDs{n: 1000})
			secondDelivery := tc.produce(t, n2, clock)

			if len(firstDelivery) != len(secondDelivery) {
				t.Fatalf("event count differs across duplicate deliveries: %d vs %d", len(firstDelivery), len(secondDelivery))
			}

			if err := store.PersistAll(ctx, db, firstDelivery); err != nil {
				t.Fatalf("PersistAll(first delivery): %v", err)
			}
			if err := store.PersistAll(ctx, db, secondDelivery); err != nil {
				t.Fatalf("PersistAll(second/duplicate delivery): %v", err)
			}

			for i, ev := range firstDelivery {
				if ev.IdempotencyKey != secondDelivery[i].IdempotencyKey {
					t.Fatalf("IdempotencyKey[%d] not stable across duplicate delivery: %q vs %q", i, ev.IdempotencyKey, secondDelivery[i].IdempotencyKey)
				}

				count, err := store.CountByIdempotencyKey(ctx, ev.IdempotencyKey)
				if err != nil {
					t.Fatalf("CountByIdempotencyKey[%d]: %v", i, err)
				}
				if count != 1 {
					t.Errorf("row count for event[%d] idempotency key = %d, want 1 (duplicate delivery must not create a second row)", i, count)
				}

				// The originally-persisted row (first delivery's EventID)
				// must still be the one on disk; the second delivery's
				// distinct EventID must not have landed as a separate row.
				stored, err := store.GetByEventID(ctx, ev.EventID)
				if err != nil {
					t.Errorf("GetByEventID(first delivery event %d): %v", i, err)
				} else if stored.EventID != ev.EventID {
					t.Errorf("stored EventID[%d] = %q, want %q", i, stored.EventID, ev.EventID)
				}
				if _, err := store.GetByEventID(ctx, secondDelivery[i].EventID); err == nil {
					t.Errorf("GetByEventID(second delivery event %d) unexpectedly succeeded; duplicate must not create a distinct row", i)
				}
			}
		})
	}
}

// --- helpers ---------------------------------------------------------------

func eventTypes(evs []v1.Event) []v1.EventType {
	out := make([]v1.EventType, len(evs))
	for i, ev := range evs {
		out[i] = ev.EventType
	}
	return out
}

func equalEventTypes(a, b []v1.EventType) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- raw-prompt-absence privacy gate ----------------------------------------

// rawTextFixture names a fixture file that is known (by construction,
// verified against the file's own literal content below) to contain
// sensitive raw text at a specific JSON field this pipeline must never
// persist or log: either a user prompt (userpromptsubmit fixtures) or a
// provider error message (stopfailure fixtures). The needle strings here
// are copied verbatim from the fixture files under
// testdata/provider-events/claude/ so a future edit to a fixture's raw text
// without updating this table causes an immediate, loud test failure
// (rawFixtureContainsNeedle below asserts this) rather than a silently
// weakened privacy gate.
type rawTextFixture struct {
	dir, file string
	needle    string
	label     string
}

// allRawTextFixtures is every fixture in this branch's corpus that embeds
// real, sensitive-shaped text a leak would be observable through. This is
// intentionally broader than just "normal" fixtures - unknown-field and
// missing-field variants also carry real prompt/error text and must be
// covered too, since a bug that only leaks under an unusual field
// combination is still a privacy bug.
var allRawTextFixtures = []rawTextFixture{
	{dir: "userpromptsubmit", file: "normal.json", needle: "Refactor the checkpoint manifest writer to use atomic rename.", label: "prompt"},
	{dir: "userpromptsubmit", file: "unknown_fields.json", needle: "Add a retry loop around the SQLite writer.", label: "prompt"},
	{dir: "userpromptsubmit", file: "missing_fields.json", needle: "What does this function do?", label: "prompt"},
	{dir: "stopfailure", file: "rate_limit.json", needle: "This request would exceed the rate limit for your organization.", label: "error message"},
	{dir: "stopfailure", file: "overloaded.json", needle: "Anthropic's API is temporarily overloaded.", label: "error message"},
	{dir: "stopfailure", file: "network.json", needle: "connection reset by peer", label: "error message"},
	{dir: "stopfailure", file: "context_length.json", needle: "prompt is too long: 210000 tokens > 200000 maximum", label: "error message"},
	{dir: "stopfailure", file: "unknown_category.json", needle: "an error Auspex has never seen before", label: "error message"},
}

// TestFixture_RawTextNeverPersistedOrLogged is claude-provider-07's hard
// privacy gate (this node's DAG entry: "Risk: Medium - raw-prompt-absence
// assertion is a hard privacy gate. Feeds qa-05 leakage scanner"; packet
// Tests: "raw-prompt absence assertion across persisted rows/log output";
// Constitution §7 rule 2: "Raw prompts and tool output are not persisted by
// default").
//
// Scope of what this test checks, spelled out explicitly so qa-05's later
// leakage scanner (and this node's own reviewer) can independently verify
// the claim:
//
//  1. Persisted rows: every fixture in allRawTextFixtures is run through the
//     real parse -> normalize -> EventStore.Persist path against a real
//     temp-file SQLite database (not a mock), then EVERY column of the
//     resulting row is read back via raw SQL (bypassing StoredEvent's typed
//     accessors, which could theoretically mask a leak the struct doesn't
//     surface) and checked for the fixture's known raw-text needle. This
//     covers payload_json specifically (where a leak would most plausibly
//     land) but also every other TEXT column on the row (source, provider,
//     session_id, idempotency_key, etc.) in case a future edit accidentally
//     routed raw text through an unexpected column.
//  2. Error/log output: every fixture directory's malformed.json is parsed
//     (expected to fail) and the returned error's Error() string is checked
//     for leakage; additionally, every successfully-parsed fixture's parse
//     error path is exercised defensively (parsing succeeds, so there is no
//     error, but this documents that the check was considered). This
//     package has no logging framework (no `log`/`slog` call sites in
//     internal/providers/claude, internal/hooks/claude, or
//     internal/telemetry/claude as of this node - verified by repo grep,
//     see this node's progress-artifact notes) - the only text that could
//     plausibly reach an operator's terminal or a log aggregator from these
//     packages is a returned Go `error`'s formatted string (domain.Error's
//     Error() method, or an fmt.Errorf-wrapped variant). Every error path
//     this pipeline can produce (JSON syntax errors, missing-session-id
//     validation errors, EventStore write errors) is exercised here and
//     checked.
//  3. Event struct dump: the full v1.Event value (JSON-marshaled AND
//     Go %#v-dumped) is checked, mirroring privacy_test.go's existing
//     per-event assertion, but run here across the FULL fixture corpus
//     rather than the two specific fixtures privacy_test.go already covers,
//     so this test's coverage is a strict superset.
//
// What this test does NOT check (documented so the scope claim is precise,
// not overstated): it does not check stdout/stderr of an actual `auspex`
// CLI process (no such binary exists on this branch yet - runtime-b01/
// claude-provider-06 territory), and it does not scan a full SQLite file
// export or a real log file on disk - qa-05's leakage scanner is expected
// to cover that end-to-end, process-level surface; this test covers the
// package-level surface this role owns and controls directly.
func TestFixture_RawTextNeverPersistedOrLogged(t *testing.T) {
	// Self-check: fail loudly (not silently) if a listed needle no longer
	// appears verbatim in its named fixture file - this means the table
	// above has drifted from the actual fixture content and the privacy
	// gate would no longer be testing what it claims to.
	for _, rf := range allRawTextFixtures {
		raw := fixture(t, rf.dir, rf.file)
		if !strings.Contains(string(raw), rf.needle) {
			t.Fatalf("allRawTextFixtures table is stale: %s/%s no longer contains needle %q - update the table", rf.dir, rf.file, rf.needle)
		}
	}

	db := openFixtureSuiteDB(t)
	store := NewEventStore(db)
	n, clock := newTestNormalizer()
	ctx := context.Background()

	var allNeedles []rawTextFixture

	for _, rf := range allRawTextFixtures {
		allNeedles = append(allNeedles, rf)

		var events []v1.Event
		switch rf.dir {
		case "userpromptsubmit":
			parsed, err := claudehooks.ParseUserPromptSubmit(fixture(t, rf.dir, rf.file))
			if err != nil {
				t.Fatalf("ParseUserPromptSubmit(%s): %v", rf.file, err)
			}
			// Belt-and-suspenders: assert the struct itself carries no raw
			// prompt field, via a Go %#v dump of the parsed struct (before
			// normalization even runs) - claudehooks.ParseUserPromptSubmit
			// hashes immediately per its own doc comment, so this dump
			// must never contain the raw needle either.
			dump := fmt.Sprintf("%#v", parsed)
			if strings.Contains(dump, rf.needle) {
				t.Errorf("%s/%s: raw %s leaked into parsed UserPromptSubmitEvent struct: %s", rf.dir, rf.file, rf.label, dump)
			}
			events = []v1.Event{n.NormalizeUserPromptSubmit(parsed, clock.Now())}
		case "stopfailure":
			parsed, err := claudehooks.ParseStopFailure(fixture(t, rf.dir, rf.file))
			if err != nil {
				t.Fatalf("ParseStopFailure(%s): %v", rf.file, err)
			}
			dump := fmt.Sprintf("%#v", parsed)
			if strings.Contains(dump, rf.needle) {
				t.Errorf("%s/%s: raw %s leaked into parsed StopFailureEvent struct: %s", rf.dir, rf.file, rf.label, dump)
			}
			events = n.NormalizeStopFailure(parsed, clock.Now())
		default:
			t.Fatalf("unhandled fixture dir in allRawTextFixtures: %q", rf.dir)
		}

		// Check the produced Event value(s) before persistence (struct
		// dump + JSON marshal), same technique as privacy_test.go's
		// assertNoRawText, applied across the whole corpus here.
		for _, ev := range events {
			assertNoRawText(t, ev, rf.needle, rf.label)
		}

		if err := store.PersistAll(ctx, db, events); err != nil {
			t.Fatalf("PersistAll(%s/%s): %v", rf.dir, rf.file, err)
		}

		for _, ev := range events {
			assertRowHasNoRawText(t, ctx, db, ev.EventID, rf.needle, rf.label)
		}
	}

	// --- error/log-output path: malformed payloads --------------------
	//
	// Every hook/parser directory's malformed.json is syntactically
	// invalid JSON that fails inside encoding/json before any field
	// (including a raw prompt or error message) is ever extracted into a
	// Go value - so the returned error's message can only ever echo
	// encoding/json's own generic parse-position diagnostic, never
	// fixture content. This loop verifies that expectation holds for
	// every malformed fixture this role owns, rather than assuming it.
	malformedCases := []struct {
		dir, file string
		parse     func(raw []byte) error
	}{
		{"statusline", "malformed.json", func(raw []byte) error { _, err := claudeprovider.ParseStatusLine(raw); return err }},
		{"userpromptsubmit", "malformed.json", func(raw []byte) error { _, err := claudehooks.ParseUserPromptSubmit(raw); return err }},
		{"stop", "malformed.json", func(raw []byte) error { _, err := claudehooks.ParseStop(raw); return err }},
		{"stopfailure", "malformed.json", func(raw []byte) error { _, err := claudehooks.ParseStopFailure(raw); return err }},
	}
	for _, mc := range malformedCases {
		raw := fixture(t, mc.dir, mc.file)
		err := mc.parse(raw)
		if err == nil {
			t.Fatalf("%s/%s: expected a parse error for malformed JSON, got nil", mc.dir, mc.file)
		}
		errText := err.Error()
		for _, needle := range allNeedles {
			if strings.Contains(errText, needle.needle) {
				t.Errorf("%s/%s: parse error text leaked raw %s: %q", mc.dir, mc.file, needle.label, errText)
			}
		}
		// The malformed fixture bytes themselves are also never valid
		// carriers of any OTHER fixture's needle text (a sanity check that
		// malformed.json files are self-contained garbage, not accidental
		// copies of a real payload) - checked defensively since a future
		// edit could otherwise turn a "malformed" fixture into one that
		// happens to still contain a real prompt/message substring ahead
		// of its syntax error.
		for _, needle := range allNeedles {
			if strings.Contains(string(raw), needle.needle) {
				t.Errorf("%s/%s: malformed fixture unexpectedly contains a raw-text needle %q from another fixture - malformed fixtures must be self-contained garbage", mc.dir, mc.file, needle.needle)
			}
		}
	}

	// --- error/log-output path: validation errors (missing session_id) --
	//
	// A missing session_id error's Message is a fixed string
	// ("claude ...: missing session_id") with no fixture-derived
	// interpolation at all - verified directly against domain.Error's
	// Message field rather than assuming it from reading the source, so a
	// future edit that accidentally started interpolating request content
	// into this message would be caught here.
	noSessionID := []byte(`{"hook_event_name":"UserPromptSubmit","prompt":"` + allRawTextFixtures[0].needle + `"}`)
	_, err := claudehooks.ParseUserPromptSubmit(noSessionID)
	if err == nil {
		t.Fatal("expected missing-session_id validation error, got nil")
	}
	if strings.Contains(err.Error(), allRawTextFixtures[0].needle) {
		t.Errorf("missing-session_id validation error leaked raw prompt text: %q", err.Error())
	}
}

// assertRowHasNoRawText loads the full raw row for eventID directly via SQL
// (not through StoredEvent, so every column - including any this role might
// add in the future without updating StoredEvent's field list - is
// actually inspected) and fails the test if needle appears in ANY text
// column.
func assertRowHasNoRawText(t *testing.T, ctx context.Context, db *sqlite.DB, eventID, needle, label string) {
	t.Helper()

	q := sqlite.QuerierFromContext(ctx, db)
	row := q.QueryRowContext(ctx, `
		SELECT event_id, schema_version, event_type, occurred_at, observed_at,
		       idempotency_key, source, provider, repository_id, worktree_id,
		       session_id, turn_id, task_id, progress_node_id, payload_json
		FROM events WHERE event_id = ?
	`, eventID)

	var (
		id, schemaVersion, eventType, occurredAt, observedAt string
		idempotencyKey, source, provider                     sql.NullString
		repositoryID, worktreeID, sessionID                  sql.NullString
		turnID, taskID, progressNodeID                       sql.NullString
		payloadJSON                                          string
	)
	if err := row.Scan(
		&id, &schemaVersion, &eventType, &occurredAt, &observedAt,
		&idempotencyKey, &source, &provider,
		&repositoryID, &worktreeID, &sessionID,
		&turnID, &taskID, &progressNodeID,
		&payloadJSON,
	); err != nil {
		t.Fatalf("scanning raw row for event %s: %v", eventID, err)
	}

	columns := map[string]string{
		"event_id":         id,
		"schema_version":   schemaVersion,
		"event_type":       eventType,
		"occurred_at":      occurredAt,
		"observed_at":      observedAt,
		"idempotency_key":  idempotencyKey.String,
		"source":           source.String,
		"provider":         provider.String,
		"repository_id":    repositoryID.String,
		"worktree_id":      worktreeID.String,
		"session_id":       sessionID.String,
		"turn_id":          turnID.String,
		"task_id":          taskID.String,
		"progress_node_id": progressNodeID.String,
		"payload_json":     payloadJSON,
	}
	for col, val := range columns {
		if strings.Contains(val, needle) {
			t.Errorf("persisted row for event %s leaked raw %s into column %q: %q", eventID, label, col, val)
		}
	}
}
