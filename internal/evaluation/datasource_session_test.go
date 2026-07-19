// datasource_session_test.go: SQLDataSource.Session's issue-#114
// CompactionCount population — the first SessionFeatures field with a real
// producer (provider.session.compacted events from the pre/post-compact
// hooks and codex's SessionStart source="compact"). Follows
// datasource_sql_test.go's seeded-real-DB discipline: correct real data
// when backing rows exist, honest ok=false when they don't.
package evaluation_test

import (
	"context"
	"testing"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/evaluation"
)

func TestSQLDataSource_Session_NoCompactionEventsIsColdStart(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	ds := evaluation.NewSQLDataSource(db)

	// Unrelated events must not count.
	insertEvent(t, db, "ev-1", ids.sessionID, "provider.turn.started", "2026-07-12T01:00:00Z", map[string]any{})

	feat, ok, err := ds.Session(context.Background(), domain.SessionID(ids.sessionID))
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if ok {
		t.Fatalf("ok = true with no compaction events, want honest cold-start false (got %+v)", feat)
	}
}

func TestSQLDataSource_Session_CountsCompactions(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	ds := evaluation.NewSQLDataSource(db)

	// Claude pre-compact events (phase "pre") — one per compaction.
	insertEvent(t, db, "ev-pre-1", ids.sessionID, "provider.session.compacted", "2026-07-12T01:00:00Z",
		map[string]any{"phase": "pre", "trigger": "auto", "checkpoint_captured": true})
	insertEvent(t, db, "ev-pre-2", ids.sessionID, "provider.session.compacted", "2026-07-12T02:00:00Z",
		map[string]any{"phase": "pre", "trigger": "manual"})
	// The codex SessionStart source="compact" shape: phase-less. Counts.
	insertEvent(t, db, "ev-codex", ids.sessionID, "provider.session.compacted", "2026-07-12T03:00:00Z",
		map[string]any{"source": "compact"})
	// A "post" phase event is the SECOND observation of a compaction whose
	// "pre" was already counted — must NOT double-count.
	insertEvent(t, db, "ev-post", ids.sessionID, "provider.session.compacted", "2026-07-12T02:10:00Z",
		map[string]any{"phase": "post"})
	// Another session's compaction must not leak into this one.
	insertEvent(t, db, "ev-other", "some-other-session", "provider.session.compacted", "2026-07-12T04:00:00Z",
		map[string]any{"phase": "pre"})

	feat, ok, err := ds.Session(context.Background(), domain.SessionID(ids.sessionID))
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true (compactions observed)")
	}
	if feat.CompactionCount != 3 {
		t.Errorf("CompactionCount = %d, want 3 (two pre + one phase-less; post excluded)", feat.CompactionCount)
	}
	if feat.SessionID != domain.SessionID(ids.sessionID) {
		t.Errorf("SessionID = %q, want %q", feat.SessionID, ids.sessionID)
	}
	if feat.Confidence != domain.ConfidenceLow {
		t.Errorf("Confidence = %q, want low", feat.Confidence)
	}

	// Every OTHER field must stay honestly unknown (nil pointers) — the
	// unknown-is-not-zero contract partial population depends on.
	if feat.RetryRate != nil || feat.TestFailureRate != nil ||
		feat.RecentTurnUsageP50 != nil || feat.RecentTurnUsageP80 != nil || feat.RecentTurnUsageP90 != nil ||
		feat.ChangedFilesRecentP50 != nil || feat.ChangedFilesRecentP90 != nil ||
		feat.ChangedLinesRecentP50 != nil || feat.ChangedLinesRecentP90 != nil ||
		feat.ToolOutputBytesP50 != nil || feat.ContextGrowthRateP50 != nil || feat.CheckpointAge != nil {
		t.Errorf("unmeasured fields must stay nil, got %+v", feat)
	}
}

func TestSQLDataSource_Session_UndecodablePayloadStillCounts(t *testing.T) {
	db := openMigratedDB(t)
	ids := seedRepoWorktreeSessionTask(t, db)
	ds := evaluation.NewSQLDataSource(db)

	// event_type alone attests the compaction; a mangled payload must not
	// erase the observation.
	exec(t, db, `
		INSERT INTO events (event_id, schema_version, event_type, occurred_at, observed_at, source, session_id, payload_json)
		VALUES ('ev-bad', 'auspex.event.v1', 'provider.session.compacted', '2026-07-12T01:00:00Z', '2026-07-12T01:00:00Z', 'hook', ?, '{oops')`,
		ids.sessionID)

	feat, ok, err := ds.Session(context.Background(), domain.SessionID(ids.sessionID))
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if !ok || feat.CompactionCount != 1 {
		t.Errorf("ok=%v CompactionCount=%d, want ok=true count=1", ok, feat.CompactionCount)
	}
}
