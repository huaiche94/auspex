// fixture_suite_test.go is the codex adapter's fixture-backed suite
// (issue #9 Phase 1; Constitution §5 rule 4 "fixtures first") — the
// analog of internal/telemetry/claude/fixture_suite_test.go: every
// provider payload fixture runs the full parse -> normalize -> persist ->
// read-back pipeline. Persistence deliberately goes through
// internal/telemetry/claude.EventStore, pinning this branch's storage
// decision: the store is provider-agnostic and codex REUSES it, no
// codex-side twin, no new migration.
package codex

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	codexhooks "github.com/huaiche94/auspex/internal/hooks/codex"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

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

type fixtureCase struct {
	name           string
	produce        func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event
	wantEventCount int
	wantEventTypes []v1.EventType
}

func TestFixtureSuite(t *testing.T) {
	cases := []fixtureCase{
		// --- normal ----------------------------------------------------
		{
			name: "sessionstart/normal",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := codexhooks.ParseSessionStart(fixture(t, "sessionstart", "normal.json"))
				if err != nil {
					t.Fatalf("ParseSessionStart: %v", err)
				}
				return []v1.Event{n.NormalizeSessionStart(parsed, clock.Now())}
			},
			wantEventCount: 1,
			wantEventTypes: []v1.EventType{v1.EventProviderSessionStarted},
		},
		{
			name: "sessionstart/resume",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := codexhooks.ParseSessionStart(fixture(t, "sessionstart", "resume.json"))
				if err != nil {
					t.Fatalf("ParseSessionStart: %v", err)
				}
				return []v1.Event{n.NormalizeSessionStart(parsed, clock.Now())}
			},
			wantEventCount: 1,
			wantEventTypes: []v1.EventType{v1.EventProviderSessionResumed},
		},
		{
			name: "sessionstart/compact",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := codexhooks.ParseSessionStart(fixture(t, "sessionstart", "compact.json"))
				if err != nil {
					t.Fatalf("ParseSessionStart: %v", err)
				}
				return []v1.Event{n.NormalizeSessionStart(parsed, clock.Now())}
			},
			wantEventCount: 1,
			wantEventTypes: []v1.EventType{v1.EventProviderSessionCompacted},
		},
		{
			name: "precompact/normal (issue #114; ADD-specified, not yet provider-verified)",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := codexhooks.ParsePreCompact(fixture(t, "precompact", "normal.json"))
				if err != nil {
					t.Fatalf("ParsePreCompact: %v", err)
				}
				return []v1.Event{n.NormalizePreCompact(parsed, clock.Now(), nil)}
			},
			wantEventCount: 1,
			wantEventTypes: []v1.EventType{v1.EventProviderSessionCompacted},
		},
		{
			name: "userpromptsubmit/normal",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := codexhooks.ParseUserPromptSubmit(fixture(t, "userpromptsubmit", "normal.json"))
				if err != nil {
					t.Fatalf("ParseUserPromptSubmit: %v", err)
				}
				return []v1.Event{n.NormalizeUserPromptSubmit(parsed, clock.Now())}
			},
			wantEventCount: 1,
			wantEventTypes: []v1.EventType{v1.EventProviderTurnStarted},
		},
		{
			name: "stop/normal+rollout",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := codexhooks.ParseStop(fixture(t, "stop", "normal.json"))
				if err != nil {
					t.Fatalf("ParseStop: %v", err)
				}
				return n.NormalizeStop(parsed, clock.Now(), normalSnapshot(t))
			},
			wantEventCount: 4,
			wantEventTypes: []v1.EventType{
				v1.EventProviderTurnCompleted,
				v1.EventProviderContextObserved,
				v1.EventProviderQuotaObserved,
				v1.EventProviderQuotaObserved,
			},
		},

		// --- missing/null fields ------------------------------------------
		{
			name: "sessionstart/missing_fields",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := codexhooks.ParseSessionStart(fixture(t, "sessionstart", "missing_fields.json"))
				if err != nil {
					t.Fatalf("ParseSessionStart: %v", err)
				}
				return []v1.Event{n.NormalizeSessionStart(parsed, clock.Now())}
			},
			wantEventCount: 1,
			wantEventTypes: []v1.EventType{v1.EventProviderSessionStarted},
		},
		{
			name: "userpromptsubmit/missing_fields",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := codexhooks.ParseUserPromptSubmit(fixture(t, "userpromptsubmit", "missing_fields.json"))
				if err != nil {
					t.Fatalf("ParseUserPromptSubmit: %v", err)
				}
				return []v1.Event{n.NormalizeUserPromptSubmit(parsed, clock.Now())}
			},
			wantEventCount: 1,
			wantEventTypes: []v1.EventType{v1.EventProviderTurnStarted},
		},
		{
			name: "stop/missing_fields (no rollout)",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := codexhooks.ParseStop(fixture(t, "stop", "missing_fields.json"))
				if err != nil {
					t.Fatalf("ParseStop: %v", err)
				}
				return n.NormalizeStop(parsed, clock.Now(), nil)
			},
			wantEventCount: 1,
			wantEventTypes: []v1.EventType{v1.EventProviderTurnCompleted},
		},

		// --- unknown-field payloads ----------------------------------------
		{
			name: "sessionstart/unknown_fields",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := codexhooks.ParseSessionStart(fixture(t, "sessionstart", "unknown_fields.json"))
				if err != nil {
					t.Fatalf("ParseSessionStart (must tolerate unknown fields): %v", err)
				}
				return []v1.Event{n.NormalizeSessionStart(parsed, clock.Now())}
			},
			wantEventCount: 1,
			wantEventTypes: []v1.EventType{v1.EventProviderSessionStarted},
		},
		{
			name: "userpromptsubmit/unknown_fields",
			produce: func(t *testing.T, n *Normalizer, clock fixedClock) []v1.Event {
				parsed, err := codexhooks.ParseUserPromptSubmit(fixture(t, "userpromptsubmit", "unknown_fields.json"))
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
				parsed, err := codexhooks.ParseStop(fixture(t, "stop", "unknown_fields.json"))
				if err != nil {
					t.Fatalf("ParseStop (must tolerate unknown fields): %v", err)
				}
				return n.NormalizeStop(parsed, clock.Now(), nil)
			},
			wantEventCount: 1,
			wantEventTypes: []v1.EventType{v1.EventProviderTurnCompleted},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openFixtureSuiteDB(t)
			store := claudetelemetry.NewEventStore(db)
			n, clock := newTestNormalizer()
			ctx := context.Background()

			events := tc.produce(t, n, clock)
			if len(events) != tc.wantEventCount {
				t.Fatalf("produced %d events, want %d", len(events), tc.wantEventCount)
			}
			for i, want := range tc.wantEventTypes {
				if events[i].EventType != want {
					t.Fatalf("event[%d] type = %q, want %q", i, events[i].EventType, want)
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
					t.Errorf("stored SchemaVersion = %q", stored.SchemaVersion)
				}
				if stored.Provider != Provider {
					t.Errorf("stored Provider = %q, want %q", stored.Provider, Provider)
				}
				if stored.IdempotencyKey == "" {
					t.Errorf("stored IdempotencyKey empty for event %s", ev.EventID)
				}
				if stored.SessionID != ev.SessionID {
					t.Errorf("stored SessionID = %q, want %q", stored.SessionID, ev.SessionID)
				}
				if stored.TurnID != ev.TurnID {
					t.Errorf("stored TurnID = %q, want %q", stored.TurnID, ev.TurnID)
				}

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

// TestFixture_DuplicateEvents_Idempotent proves duplicate hook delivery is
// a durable no-op for the codex pipeline — and does so around the codex
// adapter's stronger dedupe property: turn-scoped events (turn.started,
// turn.completed, and the rollout observations) key on codex's
// provider-stable turn_id, so even deliveries at DIFFERENT wall-clock
// instants dedupe (claude's time-scoped keys only dedupe identical
// instants).
func TestFixture_DuplicateEvents_Idempotent(t *testing.T) {
	db := openFixtureSuiteDB(t)
	store := claudetelemetry.NewEventStore(db)
	ctx := context.Background()

	parsed, err := codexhooks.ParseStop(fixture(t, "stop", "normal.json"))
	if err != nil {
		t.Fatal(err)
	}
	snap := normalSnapshot(t)

	clock1 := fixedClock{t: time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)}
	first := NewNormalizer(clock1, &seqIDs{}).NormalizeStop(parsed, clock1.Now(), snap)
	clock2 := fixedClock{t: clock1.t.Add(5 * time.Second)}
	second := NewNormalizer(clock2, &seqIDs{n: 1000}).NormalizeStop(parsed, clock2.Now(), snap)

	if err := store.PersistAll(ctx, db, first); err != nil {
		t.Fatalf("PersistAll(first): %v", err)
	}
	if err := store.PersistAll(ctx, db, second); err != nil {
		t.Fatalf("PersistAll(second/duplicate): %v", err)
	}

	for i, ev := range first {
		if ev.IdempotencyKey != second[i].IdempotencyKey {
			t.Fatalf("key[%d] not stable across deliveries: %q vs %q", i, ev.IdempotencyKey, second[i].IdempotencyKey)
		}
		count, err := store.CountByIdempotencyKey(ctx, ev.IdempotencyKey)
		if err != nil {
			t.Fatalf("CountByIdempotencyKey[%d]: %v", i, err)
		}
		if count != 1 {
			t.Errorf("row count for event[%d] = %d, want 1", i, count)
		}
		if _, err := store.GetByEventID(ctx, second[i].EventID); err == nil {
			t.Errorf("duplicate delivery's event[%d] landed as a distinct row", i)
		}
	}
}
