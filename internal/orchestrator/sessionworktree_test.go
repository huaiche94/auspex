// sessionworktree_test.go: issue #114 — SessionWorktreeStore's DB
// read-back against a real migrated DB (codexstatus_test.go's precedent
// for in-package hook-infrastructure stores).
package orchestrator_test

import (
	"context"
	"testing"

	"github.com/huaiche94/auspex/internal/orchestrator"
)

func TestSessionWorktreeStore_ResolvesBootstrapChain(t *testing.T) {
	db := openCodexStatusDB(t)
	seedCodexSession(t, db, "sess-1", "/home/dev/projects/sample", "gpt-5.2-codex", "2026-07-14T09:00:00Z")

	store := &orchestrator.SessionWorktreeStore{DB: db}
	worktreeID, ok := store.WorktreeForSession(context.Background(), "sess-1")
	if !ok {
		t.Fatal("WorktreeForSession = ok=false, want the seeded binding")
	}
	if worktreeID != "wt-_home_dev_projects_sample" {
		t.Errorf("worktreeID = %q, want the seeded worktree id", worktreeID)
	}
}

func TestSessionWorktreeStore_FailOpen(t *testing.T) {
	db := openCodexStatusDB(t)

	t.Run("unknown session", func(t *testing.T) {
		store := &orchestrator.SessionWorktreeStore{DB: db}
		if _, ok := store.WorktreeForSession(context.Background(), "nope"); ok {
			t.Error("unknown session must be ok=false")
		}
	})
	t.Run("empty session id", func(t *testing.T) {
		store := &orchestrator.SessionWorktreeStore{DB: db}
		if _, ok := store.WorktreeForSession(context.Background(), ""); ok {
			t.Error("empty session id must be ok=false")
		}
	})
	t.Run("nil receiver", func(t *testing.T) {
		var store *orchestrator.SessionWorktreeStore
		if _, ok := store.WorktreeForSession(context.Background(), "sess-1"); ok {
			t.Error("nil receiver must be ok=false")
		}
	})
	t.Run("nil DB", func(t *testing.T) {
		store := &orchestrator.SessionWorktreeStore{}
		if _, ok := store.WorktreeForSession(context.Background(), "sess-1"); ok {
			t.Error("nil DB must be ok=false")
		}
	})
}
