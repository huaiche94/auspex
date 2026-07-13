package gitx

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huaiche94/auspex/internal/domain"
)

// take is a test helper that takes a fingerprint and fails the test on error.
func take(t *testing.T, c *Client, path string) Fingerprint {
	t.Helper()
	fp, err := c.Fingerprint(context.Background(), path)
	if err != nil {
		t.Fatalf("Fingerprint(%s): %v", path, err)
	}
	return fp
}

func TestFingerprintDeterministic(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("a.txt", "hello\n")
	rb.git("add", "a.txt")
	rb.git("commit", "-q", "-m", "init")
	c := NewClient(ExecRunner{})

	fp1 := take(t, c, rb.dir)
	fp2 := take(t, c, rb.dir)

	if fp1.Digest == "" {
		t.Fatal("Digest is empty")
	}
	if fp1.Schema != FingerprintSchema {
		t.Fatalf("Schema = %q, want %q", fp1.Schema, FingerprintSchema)
	}
	if !fp1.Equal(fp2) {
		t.Fatalf("two fingerprints of unchanged repo differ:\n%s\n%s", fp1.Digest, fp2.Digest)
	}
	if got := fp1.ComputeDigest(); got != fp1.Digest {
		t.Fatalf("ComputeDigest() = %s, stored Digest = %s", got, fp1.Digest)
	}
	if fp1.Branch != "main" {
		t.Fatalf("Branch = %q, want main", fp1.Branch)
	}
	if fp1.WorktreeRoot != rb.dir {
		t.Fatalf("WorktreeRoot = %q, want %q", fp1.WorktreeRoot, rb.dir)
	}
	if fp1.Untracked != DefaultUntrackedPolicy() {
		t.Fatalf("Untracked = %+v, want default policy", fp1.Untracked)
	}
	head := strings.TrimSpace(string(rb.git("rev-parse", "HEAD").Stdout))
	if fp1.HeadOID != head {
		t.Fatalf("HeadOID = %q, want %q", fp1.HeadOID, head)
	}
}

func TestFingerprintDetectsWorktreeChange(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("a.txt", "one\ntwo\n")
	rb.git("add", "a.txt")
	rb.git("commit", "-q", "-m", "init")
	c := NewClient(ExecRunner{})

	base := take(t, c, rb.dir)

	rb.write("a.txt", "one\ntwo\nthree\n")
	dirty := take(t, c, rb.dir)
	if base.Equal(dirty) {
		t.Fatal("unstaged modification did not change the digest")
	}
	if len(dirty.WorktreeNumstat) != 1 {
		t.Fatalf("WorktreeNumstat = %+v, want exactly one entry", dirty.WorktreeNumstat)
	}
	ns := dirty.WorktreeNumstat[0]
	if ns.Path != "a.txt" || ns.Added != 1 || ns.Deleted != 0 || ns.Binary {
		t.Fatalf("WorktreeNumstat[0] = %+v, want {Added:1 Deleted:0 Path:a.txt}", ns)
	}
	if len(dirty.IndexNumstat) != 0 {
		t.Fatalf("IndexNumstat = %+v, want empty (nothing staged)", dirty.IndexNumstat)
	}

	// Reverting the file restores the exact original digest.
	rb.write("a.txt", "one\ntwo\n")
	reverted := take(t, c, rb.dir)
	if !base.Equal(reverted) {
		t.Fatalf("reverted repo digest %s != baseline %s", reverted.Digest, base.Digest)
	}
}

func TestFingerprintDetectsStagedChange(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("a.txt", "one\n")
	rb.git("add", "a.txt")
	rb.git("commit", "-q", "-m", "init")
	c := NewClient(ExecRunner{})

	rb.write("a.txt", "one\nmore\n")
	unstaged := take(t, c, rb.dir)

	rb.git("add", "a.txt")
	staged := take(t, c, rb.dir)

	if unstaged.Equal(staged) {
		t.Fatal("staging a change did not change the digest")
	}
	if len(staged.IndexNumstat) != 1 || staged.IndexNumstat[0].Path != "a.txt" || staged.IndexNumstat[0].Added != 1 {
		t.Fatalf("IndexNumstat = %+v, want one a.txt entry with Added:1", staged.IndexNumstat)
	}
	if len(staged.WorktreeNumstat) != 0 {
		t.Fatalf("WorktreeNumstat = %+v, want empty (fully staged)", staged.WorktreeNumstat)
	}
}

func TestFingerprintDetectsUntracked(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("a.txt", "hello\n")
	rb.git("add", "a.txt")
	rb.git("commit", "-q", "-m", "init")
	c := NewClient(ExecRunner{})

	base := take(t, c, rb.dir)

	rb.write("new/deep/file.txt", "untracked\n")
	with := take(t, c, rb.dir)
	if base.Equal(with) {
		t.Fatal("adding an untracked file did not change the digest")
	}
	found := false
	for _, e := range with.Entries {
		if e.Kind == KindUntracked && e.Path == "new/deep/file.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Entries = %+v, want untracked new/deep/file.txt listed individually", with.Entries)
	}

	rb.remove("new/deep/file.txt")
	after := take(t, c, rb.dir)
	if !base.Equal(after) {
		t.Fatalf("removing the untracked file did not restore baseline digest: %s != %s", after.Digest, base.Digest)
	}
}

func TestFingerprintDetectsHeadChange(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("a.txt", "v1\n")
	rb.git("add", "a.txt")
	rb.git("commit", "-q", "-m", "one")
	c := NewClient(ExecRunner{})

	before := take(t, c, rb.dir)

	rb.write("a.txt", "v2\n")
	rb.git("add", "a.txt")
	rb.git("commit", "-q", "-m", "two")
	after := take(t, c, rb.dir)

	if before.Equal(after) {
		t.Fatal("a new commit did not change the digest")
	}
	if before.HeadOID == after.HeadOID {
		t.Fatal("HeadOID did not change across a commit")
	}
	// Both trees are clean; only HEAD identity differs.
	if len(after.Entries) != 0 || len(after.IndexNumstat) != 0 || len(after.WorktreeNumstat) != 0 {
		t.Fatalf("clean repo after commit reports changes: %+v", after)
	}
}

func TestFingerprintRename(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("old.txt", "same content, long enough for rename detection\n")
	rb.git("add", "old.txt")
	rb.git("commit", "-q", "-m", "init")
	c := NewClient(ExecRunner{})

	rb.git("mv", "old.txt", "new.txt")
	fp := take(t, c, rb.dir)

	var renamed *Entry
	for i := range fp.Entries {
		if fp.Entries[i].Kind == KindRenamed {
			renamed = &fp.Entries[i]
		}
	}
	if renamed == nil || renamed.Path != "new.txt" || renamed.OrigPath != "old.txt" {
		t.Fatalf("Entries = %+v, want a renamed old.txt -> new.txt entry", fp.Entries)
	}
	if len(fp.IndexNumstat) != 1 {
		t.Fatalf("IndexNumstat = %+v, want exactly one entry", fp.IndexNumstat)
	}
	ns := fp.IndexNumstat[0]
	if ns.Path != "new.txt" || ns.OrigPath != "old.txt" || ns.Added != 0 || ns.Deleted != 0 {
		t.Fatalf("IndexNumstat[0] = %+v, want rename old.txt -> new.txt with 0/0 counts", ns)
	}
}

func TestFingerprintBinaryNumstat(t *testing.T) {
	rb := newRepoBuilder(t)
	// Content with NUL bytes so git classifies the file as binary.
	rb.write("blob.bin", "v1\x00\x01\x02binary\x00")
	rb.git("add", "blob.bin")
	rb.git("commit", "-q", "-m", "init")
	c := NewClient(ExecRunner{})

	rb.write("blob.bin", "v2\x00\x03\x04binary\x00changed")
	fp := take(t, c, rb.dir)

	if len(fp.WorktreeNumstat) != 1 {
		t.Fatalf("WorktreeNumstat = %+v, want exactly one entry", fp.WorktreeNumstat)
	}
	ns := fp.WorktreeNumstat[0]
	if !ns.Binary || ns.Path != "blob.bin" || ns.Added != 0 || ns.Deleted != 0 {
		t.Fatalf("WorktreeNumstat[0] = %+v, want binary blob.bin with zero counts", ns)
	}
}

func TestFingerprintPathWithSpaces(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("dir with space/file name.txt", "one\n")
	rb.git("add", "dir with space/file name.txt")
	rb.git("commit", "-q", "-m", "init")
	c := NewClient(ExecRunner{})

	base := take(t, c, rb.dir)
	rb.write("dir with space/file name.txt", "one\ntwo\n")
	dirty := take(t, c, rb.dir)

	if base.Equal(dirty) {
		t.Fatal("modification under a spaced path did not change the digest")
	}
	if len(dirty.WorktreeNumstat) != 1 || dirty.WorktreeNumstat[0].Path != "dir with space/file name.txt" {
		t.Fatalf("WorktreeNumstat = %+v, want the spaced path intact", dirty.WorktreeNumstat)
	}
}

func TestFingerprintUnbornBranch(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("a.txt", "staged before first commit\n")
	rb.git("add", "a.txt")
	c := NewClient(ExecRunner{})

	fp := take(t, c, rb.dir)
	if fp.HeadOID != "(initial)" {
		t.Fatalf("HeadOID = %q, want (initial) on an unborn branch", fp.HeadOID)
	}
	// --cached on an unborn branch diffs against the empty tree.
	if len(fp.IndexNumstat) != 1 || fp.IndexNumstat[0].Path != "a.txt" {
		t.Fatalf("IndexNumstat = %+v, want staged a.txt vs empty tree", fp.IndexNumstat)
	}
}

func TestFingerprintLinkedWorktree(t *testing.T) {
	rb := newRepoBuilder(t)
	rb.write("a.txt", "hello\n")
	rb.git("add", "a.txt")
	rb.git("commit", "-q", "-m", "init")

	linked := filepath.Join(rb.dir, "..", filepath.Base(rb.dir)+"-linked")
	rb.git("worktree", "add", "-q", "-b", "linked", linked)
	t.Cleanup(func() { rb.git("worktree", "remove", "--force", linked) })
	c := NewClient(ExecRunner{})

	main := take(t, c, rb.dir)
	lw := take(t, c, linked)

	if !lw.IsLinkedWorktree {
		t.Fatal("linked worktree fingerprint has IsLinkedWorktree=false")
	}
	if lw.CommonDir != main.CommonDir {
		t.Fatalf("CommonDir differs: %q vs %q", lw.CommonDir, main.CommonDir)
	}
	if lw.WorktreeRoot == main.WorktreeRoot {
		t.Fatal("linked worktree reports the main worktree root")
	}
	// Same commit, same content — but a different worktree is a different
	// repository state identity.
	if main.Equal(lw) {
		t.Fatal("fingerprints of two different worktrees compare equal")
	}
}

func TestFingerprintNotARepo(t *testing.T) {
	rb := newRepoBuilder(t) // for the git-availability skip check
	_ = rb
	c := NewClient(ExecRunner{})

	_, err := c.Fingerprint(context.Background(), t.TempDir())
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeNotFound {
		t.Fatalf("Fingerprint outside a repo: err = %v, want domain ErrCodeNotFound", err)
	}
}

func TestFingerprintDigestOrderIndependence(t *testing.T) {
	e1 := Entry{Kind: KindChanged, Index: 'M', Worktree: Unmodified, Submodule: "N...", ModeHead: "100644", ModeIndex: "100644", ModeWorktree: "100644", HashHead: "aaa", HashIndex: "bbb", Path: "a.txt"}
	e2 := Entry{Kind: KindUntracked, Path: "z.txt"}
	n1 := NumstatEntry{Added: 1, Deleted: 2, Path: "a.txt"}
	n2 := NumstatEntry{Binary: true, Path: "z.bin"}

	base := Fingerprint{
		Schema:       FingerprintSchema,
		WorktreeRoot: "/repo",
		CommonDir:    "/repo/.git",
		HeadOID:      "abc123",
		Branch:       "main",
		Entries:      []Entry{e1, e2},
		IndexNumstat: []NumstatEntry{n1, n2},
		Untracked:    DefaultUntrackedPolicy(),
	}
	shuffled := base
	shuffled.Entries = []Entry{e2, e1}
	shuffled.IndexNumstat = []NumstatEntry{n2, n1}

	if base.ComputeDigest() != shuffled.ComputeDigest() {
		t.Fatal("digest depends on slice ordering; canonical sort is broken")
	}
}

func TestFingerprintDigestSensitivity(t *testing.T) {
	base := Fingerprint{
		Schema:       FingerprintSchema,
		WorktreeRoot: "/repo",
		CommonDir:    "/repo/.git",
		HeadOID:      "abc123",
		Branch:       "main",
		Untracked:    DefaultUntrackedPolicy(),
	}
	mutate := map[string]func(*Fingerprint){
		"schema":        func(f *Fingerprint) { f.Schema = "auspex.gitx.fingerprint.v999" },
		"worktree root": func(f *Fingerprint) { f.WorktreeRoot = "/other" },
		"common dir":    func(f *Fingerprint) { f.CommonDir = "/other/.git" },
		"linked flag":   func(f *Fingerprint) { f.IsLinkedWorktree = true },
		"head oid":      func(f *Fingerprint) { f.HeadOID = "def456" },
		"branch":        func(f *Fingerprint) { f.Branch = "other" },
		"policy mode":   func(f *Fingerprint) { f.Untracked.Mode = "normal" },
		"policy ignore": func(f *Fingerprint) { f.Untracked.IncludeIgnored = true },
		"entry":         func(f *Fingerprint) { f.Entries = []Entry{{Kind: KindUntracked, Path: "x"}} },
		"numstat":       func(f *Fingerprint) { f.WorktreeNumstat = []NumstatEntry{{Added: 1, Path: "x"}} },
	}
	for name, fn := range mutate {
		fp := base
		fn(&fp)
		if fp.ComputeDigest() == base.ComputeDigest() {
			t.Errorf("mutating %s did not change the digest", name)
		}
	}

	// Informational fields must NOT move the digest: a remote fetch that
	// updates ahead/behind is not a repository state change.
	info := base
	info.Upstream = "origin/main"
	info.Ahead, info.Behind, info.HasAheadBehind = 3, 1, true
	if info.ComputeDigest() != base.ComputeDigest() {
		t.Error("upstream/ahead/behind changed the digest; they are informational only")
	}
}

func TestFingerprintEqualZeroValue(t *testing.T) {
	var a, b Fingerprint
	if a.Equal(b) {
		t.Fatal("two zero-value fingerprints compare equal; empty digest must fail closed")
	}
}

func TestFingerprintNumstatParse(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []NumstatEntry
	}{
		{"empty", "", nil},
		{"single", "1\t2\ta.txt\x00", []NumstatEntry{{Added: 1, Deleted: 2, Path: "a.txt"}}},
		{"no trailing NUL", "1\t2\ta.txt", []NumstatEntry{{Added: 1, Deleted: 2, Path: "a.txt"}}},
		{"binary", "-\t-\tblob.bin\x00", []NumstatEntry{{Binary: true, Path: "blob.bin"}}},
		{
			"rename",
			"0\t0\t\x00old.txt\x00new.txt\x00",
			[]NumstatEntry{{Path: "new.txt", OrigPath: "old.txt"}},
		},
		{
			"mixed",
			"3\t1\ta.txt\x000\t0\t\x00old.txt\x00new.txt\x00-\t-\tblob.bin\x00",
			[]NumstatEntry{
				{Added: 3, Deleted: 1, Path: "a.txt"},
				{Path: "new.txt", OrigPath: "old.txt"},
				{Binary: true, Path: "blob.bin"},
			},
		},
		{
			"path with tab and newline",
			"1\t0\tweird\npath\ttab.txt\x00",
			[]NumstatEntry{{Added: 1, Deleted: 0, Path: "weird\npath\ttab.txt"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseNumstatZ([]byte(tc.in))
			if err != nil {
				t.Fatalf("ParseNumstatZ(%q): %v", tc.in, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("entry %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestFingerprintNumstatParseRejects(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"missing fields", "1\ta.txt\x00"},
		{"non-numeric count", "x\t2\ta.txt\x00"},
		{"negative count", "-1\t2\ta.txt\x00"},
		{"mixed binary marker", "-\t2\ta.txt\x00"},
		{"rename missing dest", "0\t0\t\x00old.txt\x00"},
		{"rename empty source", "0\t0\t\x00\x00new.txt\x00"},
		{"empty record mid-stream", "\x001\t2\ta.txt\x00"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseNumstatZ([]byte(tc.in))
			var derr *domain.Error
			if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
				t.Fatalf("ParseNumstatZ(%q): err = %v, want domain ErrCodeValidation (fail closed)", tc.in, err)
			}
		})
	}
}
