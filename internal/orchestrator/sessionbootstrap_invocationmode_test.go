// sessionbootstrap_invocationmode_test.go: coverage for the issue-#8
// addition to SessionBootstrap — the InvocationMode field the managed
// one-shot runner sets so provider_sessions honestly records HOW the
// session is driven (managed_stream_json vs. the native-hook default).
// A separate file from sessionbootstrap_test.go (whose harness helpers —
// openBootstrapDB, fakeRepoResolver, newBootstrapper — it reuses, same
// orchestrator_test package) so the addition stays trivially mergeable.
package orchestrator_test

import (
	"context"
	"testing"

	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

func sessionInvocationMode(t *testing.T, db *sqlite.DB, sessionID string) string {
	t.Helper()
	var mode string
	if err := db.Conn().QueryRowContext(context.Background(),
		`SELECT invocation_mode FROM provider_sessions WHERE id = ?`, sessionID,
	).Scan(&mode); err != nil {
		t.Fatalf("reading invocation_mode for %s: %v", sessionID, err)
	}
	return mode
}

func TestSessionBootstrapper_InvocationMode_DefaultsToNativeHook(t *testing.T) {
	db := openBootstrapDB(t)
	dir := t.TempDir()
	b := newBootstrapper(db, &fakeRepoResolver{info: mainWorktreeInfo(dir)}, bootstrapTestTime)

	if !b.Bootstrap(context.Background(), claudeBootstrap("sess-mode-default", dir, nil)) {
		t.Fatal("Bootstrap = false, want true")
	}
	if got := sessionInvocationMode(t, db, "sess-mode-default"); got != "native-hook" {
		t.Errorf("invocation_mode = %q, want %q — the empty field must keep every hook caller's exact prior behavior", got, "native-hook")
	}
}

func TestSessionBootstrapper_InvocationMode_ManagedStreamJSON(t *testing.T) {
	db := openBootstrapDB(t)
	dir := t.TempDir()
	b := newBootstrapper(db, &fakeRepoResolver{info: mainWorktreeInfo(dir)}, bootstrapTestTime)

	req := claudeBootstrap("sess-mode-managed", dir, nil)
	req.InvocationMode = orchestrator.InvocationModeManagedStreamJSON
	if !b.Bootstrap(context.Background(), req) {
		t.Fatal("Bootstrap = false, want true")
	}
	if got := sessionInvocationMode(t, db, "sess-mode-managed"); got != "managed_stream_json" {
		t.Errorf("invocation_mode = %q, want %q", got, "managed_stream_json")
	}
}

// TestSessionBootstrapper_InvocationMode_FirstObservationWins proves the
// exact sequence `auspex run` relies on (managed/run.go bootstraps with
// the managed mode BEFORE the shared gate path re-bootstraps with the
// hook default): the row stays managed_stream_json.
func TestSessionBootstrapper_InvocationMode_FirstObservationWins(t *testing.T) {
	db := openBootstrapDB(t)
	dir := t.TempDir()
	b := newBootstrapper(db, &fakeRepoResolver{info: mainWorktreeInfo(dir)}, bootstrapTestTime)

	managed := claudeBootstrap("sess-mode-fow", dir, nil)
	managed.InvocationMode = orchestrator.InvocationModeManagedStreamJSON
	if !b.Bootstrap(context.Background(), managed) {
		t.Fatal("first Bootstrap = false, want true")
	}
	// The gate's own re-bootstrap: same session, hook-default mode.
	if !b.Bootstrap(context.Background(), claudeBootstrap("sess-mode-fow", dir, nil)) {
		t.Fatal("second Bootstrap = false, want true")
	}
	if got := sessionInvocationMode(t, db, "sess-mode-fow"); got != "managed_stream_json" {
		t.Errorf("invocation_mode = %q, want managed_stream_json to survive the hook-default re-bootstrap (first observation wins)", got)
	}
}
