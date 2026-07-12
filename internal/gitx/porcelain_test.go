package gitx

import (
	"context"
	"strings"
	"testing"
)

// findEntry returns the first entry whose Path (or, for renames, OrigPath)
// matches path.
func findEntry(entries []Entry, path string) (Entry, bool) {
	for _, e := range entries {
		if e.Path == path || (e.Kind == KindRenamed && e.OrigPath == path) {
			return e, true
		}
	}
	return Entry{}, false
}

// TestPorcelainStatusScenarios drives a real temporary Git repository
// through the standard lifecycle (tracked/staged/unstaged/untracked/
// rename/delete) and asserts the parsed Status reflects each case
// correctly. This is the integration-level required-test coverage from
// agents/checkpoint.md Part B.
func TestPorcelainStatusScenarios(t *testing.T) {
	rb := newRepoBuilder(t)
	client := NewClient(ExecRunner{})

	// Seed a committed baseline so we have "tracked, unmodified" files to
	// mutate: one that will get an unstaged edit, one staged edit, one
	// deleted, one renamed.
	rb.write("tracked.txt", "tracked and unmodified\n")
	rb.write("to-edit-unstaged.txt", "original content\n")
	rb.write("to-edit-staged.txt", "original content\n")
	rb.write("to-delete.txt", "will be deleted\n")
	rb.write("to-rename-old.txt", "rename me\n")
	rb.git("add", ".")
	rb.git("commit", "-q", "-m", "baseline")

	// Unstaged modification.
	rb.write("to-edit-unstaged.txt", "modified content\n")

	// Staged modification.
	rb.write("to-edit-staged.txt", "modified content staged\n")
	rb.git("add", "to-edit-staged.txt")

	// Unstaged delete.
	rb.remove("to-delete.txt")

	// Rename (staged, detected via -M).
	rb.git("mv", "to-rename-old.txt", "to-rename-new.txt")

	// New untracked file.
	rb.write("untracked.txt", "brand new\n")

	status, err := client.Status(context.Background(), rb.dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	t.Run("branch header parsed", func(t *testing.T) {
		if status.Branch.Head != "main" {
			t.Errorf("Branch.Head = %q, want %q", status.Branch.Head, "main")
		}
		if status.Branch.OID == "" || status.Branch.OID == "(initial)" {
			t.Errorf("Branch.OID = %q, want a resolved commit hash", status.Branch.OID)
		}
	})

	t.Run("tracked unmodified file is absent from status", func(t *testing.T) {
		if _, ok := findEntry(status.Entries, "tracked.txt"); ok {
			t.Errorf("tracked.txt is unmodified and should not appear in status entries")
		}
	})

	t.Run("unstaged modification", func(t *testing.T) {
		e, ok := findEntry(status.Entries, "to-edit-unstaged.txt")
		if !ok {
			t.Fatal("to-edit-unstaged.txt not found in status entries")
		}
		if e.Kind != KindChanged {
			t.Errorf("Kind = %q, want KindChanged", e.Kind)
		}
		if e.Index != Unmodified {
			t.Errorf("Index = %q, want unmodified '.'", string(e.Index))
		}
		if e.Worktree != 'M' {
			t.Errorf("Worktree = %q, want 'M'", string(e.Worktree))
		}
	})

	t.Run("staged modification", func(t *testing.T) {
		e, ok := findEntry(status.Entries, "to-edit-staged.txt")
		if !ok {
			t.Fatal("to-edit-staged.txt not found in status entries")
		}
		if e.Kind != KindChanged {
			t.Errorf("Kind = %q, want KindChanged", e.Kind)
		}
		if e.Index != 'M' {
			t.Errorf("Index = %q, want 'M'", string(e.Index))
		}
		if e.Worktree != Unmodified {
			t.Errorf("Worktree = %q, want unmodified '.'", string(e.Worktree))
		}
		if e.HashHead == "" || e.HashIndex == "" {
			t.Errorf("expected non-empty HashHead/HashIndex, got %q/%q", e.HashHead, e.HashIndex)
		}
	})

	t.Run("unstaged delete", func(t *testing.T) {
		e, ok := findEntry(status.Entries, "to-delete.txt")
		if !ok {
			t.Fatal("to-delete.txt not found in status entries")
		}
		if e.Kind != KindChanged {
			t.Errorf("Kind = %q, want KindChanged", e.Kind)
		}
		if e.Index != Unmodified {
			t.Errorf("Index = %q, want unmodified '.'", string(e.Index))
		}
		if e.Worktree != 'D' {
			t.Errorf("Worktree = %q, want 'D'", string(e.Worktree))
		}
	})

	t.Run("staged rename", func(t *testing.T) {
		e, ok := findEntry(status.Entries, "to-rename-new.txt")
		if !ok {
			t.Fatal("to-rename-new.txt not found in status entries")
		}
		if e.Kind != KindRenamed {
			t.Fatalf("Kind = %q, want KindRenamed", e.Kind)
		}
		if e.OrigPath != "to-rename-old.txt" {
			t.Errorf("OrigPath = %q, want %q", e.OrigPath, "to-rename-old.txt")
		}
		if e.RenameOp != 'R' {
			t.Errorf("RenameOp = %q, want 'R'", string(e.RenameOp))
		}
		if e.RenameScore != 100 {
			t.Errorf("RenameScore = %d, want 100 (identical content)", e.RenameScore)
		}
	})

	t.Run("untracked file", func(t *testing.T) {
		e, ok := findEntry(status.Entries, "untracked.txt")
		if !ok {
			t.Fatal("untracked.txt not found in status entries")
		}
		if e.Kind != KindUntracked {
			t.Errorf("Kind = %q, want KindUntracked", e.Kind)
		}
		// Untracked entries carry no XY field.
		if e.Index != 0 || e.Worktree != 0 {
			t.Errorf("untracked entry should have zero Index/Worktree, got %q/%q", string(e.Index), string(e.Worktree))
		}
	})

	t.Run("entry count matches exactly the mutated set", func(t *testing.T) {
		// 5 entries: unstaged edit, staged edit, delete, rename, untracked.
		if len(status.Entries) != 5 {
			paths := make([]string, len(status.Entries))
			for i, e := range status.Entries {
				paths[i] = e.Path
			}
			t.Errorf("len(Entries) = %d, want 5; paths = %v", len(status.Entries), paths)
		}
	})
}

func TestPorcelainCleanWorktree(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("a.txt", "a\n")
	rb.git("add", "a.txt")
	rb.git("commit", "-q", "-m", "initial")

	client := NewClient(ExecRunner{})
	status, err := client.Status(context.Background(), rb.dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.Entries) != 0 {
		t.Errorf("clean worktree should have zero entries, got %d", len(status.Entries))
	}
}

// TestParsePorcelainV2Fixtures is a unit-level test of the parser against
// hand-constructed byte sequences, independent of a real git binary, so
// record-shape edge cases (submodule field, conflict stages, ignored
// entries) are covered even where they're awkward to provoke from a live
// repo.
func TestParsePorcelainV2Fixtures(t *testing.T) {
	tests := []struct {
		name    string
		record  string // '\x00'-joined tokens; NUL terminator appended automatically
		wantErr bool
		check   func(t *testing.T, st Status)
	}{
		{
			name:   "empty output",
			record: "",
			check: func(t *testing.T, st Status) {
				if len(st.Entries) != 0 {
					t.Errorf("expected zero entries, got %d", len(st.Entries))
				}
			},
		},
		{
			name: "branch headers only",
			record: strings.Join([]string{
				"# branch.oid abc123",
				"# branch.head main",
				"# branch.upstream origin/main",
				"# branch.ab +2 -1",
			}, "\x00"),
			check: func(t *testing.T, st Status) {
				if st.Branch.OID != "abc123" || st.Branch.Head != "main" || st.Branch.Upstream != "origin/main" {
					t.Fatalf("unexpected branch info: %+v", st.Branch)
				}
				if !st.Branch.HasAheadBehind || st.Branch.Ahead != 2 || st.Branch.Behind != 1 {
					t.Fatalf("unexpected ahead/behind: %+v", st.Branch)
				}
			},
		},
		{
			name:   "ignored entry",
			record: "! build/output.log",
			check: func(t *testing.T, st Status) {
				if len(st.Entries) != 1 || st.Entries[0].Kind != KindIgnored || st.Entries[0].Path != "build/output.log" {
					t.Fatalf("unexpected entries: %+v", st.Entries)
				}
			},
		},
		{
			name:   "unmerged conflict entry",
			record: "u UU N... 100644 100644 100644 100644 aaa111 bbb222 ccc333 conflicted.txt",
			check: func(t *testing.T, st Status) {
				if len(st.Entries) != 1 {
					t.Fatalf("expected 1 entry, got %d", len(st.Entries))
				}
				e := st.Entries[0]
				if e.Kind != KindUnmerged || e.Index != 'U' || e.Worktree != 'U' || e.Path != "conflicted.txt" {
					t.Fatalf("unexpected unmerged entry: %+v", e)
				}
				if e.ConflictHashes != [3]string{"aaa111", "bbb222", "ccc333"} {
					t.Fatalf("unexpected conflict hashes: %+v", e.ConflictHashes)
				}
			},
		},
		{
			name:    "unknown record type rejected",
			record:  "X garbage",
			wantErr: true,
		},
		{
			name:    "malformed changed entry rejected",
			record:  "1 M. N... 100644",
			wantErr: true,
		},
		{
			name:    "rename entry missing orig-path token rejected",
			record:  "2 R. N... 100644 100644 100644 aaa bbb R100 new.txt",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var raw []byte
			if tc.record != "" {
				raw = []byte(tc.record + "\x00")
			}
			st, err := ParsePorcelainV2(raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParsePorcelainV2(%q): expected error, got nil", tc.record)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePorcelainV2(%q): unexpected error: %v", tc.record, err)
			}
			if tc.check != nil {
				tc.check(t, st)
			}
		})
	}
}
