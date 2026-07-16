// codexstatus_test.go: issue #9 Phase 1b — CodexStatusStore's DB read-back
// and HandleCodexStatus's line rendering, against a real migrated DB
// (openturn_test.go's precedent for in-package hook-infrastructure tests;
// this file is package orchestrator_test like the rest, using exported
// surfaces only).
package orchestrator_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

func openCodexStatusDB(t *testing.T) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "auspex.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}
	if err := db.Migrate(context.Background(), migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

// seedCodexSession writes the repositories -> worktrees -> provider_sessions
// chain the issue-#17 bootstrap would have created for a codex session
// rooted at rootPath.
func seedCodexSession(t *testing.T, db *sqlite.DB, sessionID, rootPath, model, startedAt string) {
	t.Helper()
	ctx := context.Background()
	exec := func(q string, args ...any) {
		if _, err := db.Conn().ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	// Repo/worktree identity keys off the root path (two sessions in the
	// same worktree share the chain, as the real bootstrap upserts do).
	pathKey := strings.NewReplacer("/", "_").Replace(rootPath)
	repoID := "repo-" + pathKey
	wtID := "wt-" + pathKey
	exec(`INSERT OR IGNORE INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
	      VALUES (?, ?, ?, ?, ?)`, repoID, rootPath, rootPath+"/.git", startedAt, startedAt)
	exec(`INSERT OR IGNORE INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
	      VALUES (?, ?, ?, ?, ?, ?)`, wtID, repoID, rootPath, rootPath+"/.git", startedAt, startedAt)
	exec(`INSERT INTO provider_sessions (id, worktree_id, provider, provider_session_id, invocation_mode, model, started_at)
	      VALUES (?, ?, 'codex', ?, 'native-hook', ?, ?)`, sessionID, wtID, sessionID, model, startedAt)
}

func persistCodexObservation(t *testing.T, db *sqlite.DB, ev v1.Event) {
	t.Helper()
	store := claudetelemetry.NewEventStore(db)
	if err := store.PersistAll(context.Background(), db, []v1.Event{ev}); err != nil {
		t.Fatalf("PersistAll: %v", err)
	}
}

func codexObservation(id, sessionID string, eventType v1.EventType, observedAt time.Time, payload map[string]any) v1.Event {
	return v1.Event{
		SchemaVersion:  v1.SchemaVersionEvent,
		EventID:        id,
		EventType:      eventType,
		OccurredAt:     observedAt,
		ObservedAt:     observedAt,
		IdempotencyKey: "key-" + id,
		Source:         "provider_event",
		Provider:       "codex",
		SessionID:      sessionID,
		Payload:        payload,
	}
}

func TestCodexStatusStore_ResolvesLatestSessionAndObservations(t *testing.T) {
	db := openCodexStatusDB(t)
	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)

	seedCodexSession(t, db, "codex-old", "/repo/a", "gpt-5.1-codex", "2026-07-14T10:00:00Z")
	seedCodexSession(t, db, "codex-new", "/repo/a", "gpt-5.2-codex", "2026-07-14T11:00:00Z")

	// Older context observation, then a newer one — the newer must win.
	persistCodexObservation(t, db, codexObservation("ev-ctx-1", "codex-new", v1.EventProviderContextObserved, base.Add(-time.Minute), map[string]any{
		"used_tokens": 10000, "window_tokens": 100000,
	}))
	persistCodexObservation(t, db, codexObservation("ev-ctx-2", "codex-new", v1.EventProviderContextObserved, base, map[string]any{
		"used_tokens": 44374, "window_tokens": 353400,
	}))
	// Quota: a primary (ignored by the weekly segment) and a secondary.
	persistCodexObservation(t, db, codexObservation("ev-q-1", "codex-new", v1.EventProviderQuotaObserved, base, map[string]any{
		"limit_id": "primary", "used_percent": 13.0,
	}))
	persistCodexObservation(t, db, codexObservation("ev-q-2", "codex-new", v1.EventProviderQuotaObserved, base, map[string]any{
		"limit_id": "secondary", "used_percent": 49.2,
	}))

	store := &orchestrator.CodexStatusStore{DB: db}

	// A cwd INSIDE the worktree resolves to the latest session.
	snap, ok := store.LatestCodexStatus(context.Background(), "/repo/a/deep/subdir")
	if !ok {
		t.Fatal("ok = false, want the seeded session")
	}
	if snap.SessionID != "codex-new" {
		t.Errorf("SessionID = %q, want the newest session", snap.SessionID)
	}
	if snap.Model != "gpt-5.2-codex" {
		t.Errorf("Model = %q", snap.Model)
	}
	if snap.ContextUsedPercent == nil {
		t.Fatal("ContextUsedPercent = nil")
	}
	wantPct := 44374.0 / 353400.0 * 100
	if diff := *snap.ContextUsedPercent - wantPct; diff > 0.001 || diff < -0.001 {
		t.Errorf("ContextUsedPercent = %v, want ~%v", *snap.ContextUsedPercent, wantPct)
	}
	if snap.WeeklyUsedPercent == nil || *snap.WeeklyUsedPercent != 49.2 {
		t.Errorf("WeeklyUsedPercent = %v, want 49.2 (the secondary window)", snap.WeeklyUsedPercent)
	}

	// A cwd outside every worktree resolves nothing.
	if _, ok := store.LatestCodexStatus(context.Background(), "/elsewhere"); ok {
		t.Error("ok = true for an unrelated cwd")
	}
	// The prefix match must not treat /repo/ab as inside /repo/a.
	if _, ok := store.LatestCodexStatus(context.Background(), "/repo/ab"); ok {
		t.Error("ok = true for a sibling path sharing a prefix")
	}
	// Empty cwd falls back to the latest codex session anywhere.
	if snap, ok := store.LatestCodexStatus(context.Background(), ""); !ok || snap.SessionID != "codex-new" {
		t.Errorf("empty-cwd resolve = (%+v, %v), want the newest session", snap, ok)
	}
}

func TestCodexStatusStore_MissingObservationsStayNil(t *testing.T) {
	db := openCodexStatusDB(t)
	seedCodexSession(t, db, "codex-bare", "/repo/b", "", "2026-07-14T10:00:00Z")

	store := &orchestrator.CodexStatusStore{DB: db}
	snap, ok := store.LatestCodexStatus(context.Background(), "/repo/b")
	if !ok {
		t.Fatal("ok = false")
	}
	if snap.ContextUsedPercent != nil || snap.WeeklyUsedPercent != nil {
		t.Errorf("percentages fabricated with no observations: %+v", snap)
	}
	if snap.Model != "" {
		t.Errorf("Model = %q, want empty for a NULL column", snap.Model)
	}
}

func TestCodexStatusStore_NilReceiverAndNilDBFailOpen(t *testing.T) {
	var nilStore *orchestrator.CodexStatusStore
	if _, ok := nilStore.LatestCodexStatus(context.Background(), "/x"); ok {
		t.Error("nil receiver must be ok=false")
	}
	if _, ok := (&orchestrator.CodexStatusStore{}).LatestCodexStatus(context.Background(), "/x"); ok {
		t.Error("nil DB must be ok=false")
	}
}

// --- HandleCodexStatus ---------------------------------------------------------

func TestHandleCodexStatus_RendersLineFromStore(t *testing.T) {
	db := openCodexStatusDB(t)
	base := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	seedCodexSession(t, db, "codex-line", "/repo/c", "gpt-5.2-codex", "2026-07-14T10:00:00Z")
	persistCodexObservation(t, db, codexObservation("ev-ctx", "codex-line", v1.EventProviderContextObserved, base, map[string]any{
		"used_tokens": 44374, "window_tokens": 353400,
	}))
	persistCodexObservation(t, db, codexObservation("ev-q", "codex-line", v1.EventProviderQuotaObserved, base, map[string]any{
		"limit_id": "secondary", "used_percent": 49.2,
	}))

	deps := baseHookDeps()
	deps.CodexStatus = &orchestrator.CodexStatusStore{DB: db}

	line, err := orchestrator.HandleCodexStatus(context.Background(), deps, "/repo/c")
	if err != nil {
		t.Fatalf("HandleCodexStatus: %v", err)
	}
	if !strings.Contains(line, "ax»") {
		t.Errorf("line = %q, want the ax» head", line)
	}
	if !strings.Contains(line, "gpt-5.2-codex") {
		t.Errorf("line = %q, want the model", line)
	}
	if !strings.Contains(line, "weekly ~49%") {
		t.Errorf("line = %q, want the weekly quota segment", line)
	}
	if !strings.Contains(line, "context") || !strings.Contains(line, "12.6%") {
		t.Errorf("line = %q, want the measured context segment", line)
	}
}

func TestHandleCodexStatus_NoReaderOrNoSession_BareLineNeverError(t *testing.T) {
	deps := baseHookDeps() // CodexStatus nil
	line, err := orchestrator.HandleCodexStatus(context.Background(), deps, "/anywhere")
	if err != nil {
		t.Fatalf("nil reader must fail open: %v", err)
	}
	if !strings.Contains(line, "ax»") {
		t.Errorf("line = %q, want the bare head", line)
	}

	deps.CodexStatus = &orchestrator.CodexStatusStore{DB: openCodexStatusDB(t)} // empty DB
	line, err = orchestrator.HandleCodexStatus(context.Background(), deps, "/anywhere")
	if err != nil {
		t.Fatalf("no-session must fail open: %v", err)
	}
	if !strings.Contains(line, "ax»") {
		t.Errorf("line = %q, want the bare head", line)
	}
}
