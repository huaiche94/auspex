// patchredact_internal_test.go: direct unit coverage for patchredact.go's
// redactPatchSecrets, exercising the exact line-scope rules documented
// there (added/removed lines only, never context/header lines) without
// needing a real Git repository. Named "_internal_test" and package
// repocheckpoint (not repocheckpoint_test) to reach the unexported
// redactPatchSecrets function directly, mirroring security_internal_test.go's
// existing convention in this package for unexported-surface tests.
package repocheckpoint

import (
	"strings"
	"testing"
)

const fakeGitHubToken = "ghp_1234567890abcdefghijklmnopqrstuvwxyz12"

func TestRedactPatchSecrets_AddedLineWithSecret_IsRedacted(t *testing.T) {
	patch := []byte(strings.Join([]string{
		"diff --git a/config.txt b/config.txt",
		"index e69de29..1234567 100644",
		"--- a/config.txt",
		"+++ b/config.txt",
		"@@ -1 +1 @@",
		"-placeholder",
		"+github_token: " + fakeGitHubToken,
		"",
	}, "\n"))

	got, hadSecret := redactPatchSecrets(patch)
	if !hadSecret {
		t.Fatal("expected hadSecret=true for a secret-shaped added line")
	}
	if strings.Contains(string(got), fakeGitHubToken) {
		t.Fatalf("expected the secret token to be redacted, got: %s", got)
	}
	if !strings.Contains(string(got), redactedLinePlaceholder) {
		t.Fatalf("expected the redaction placeholder to appear, got: %s", got)
	}
	// File headers and hunk header must survive byte-for-byte.
	for _, want := range []string{
		"diff --git a/config.txt b/config.txt",
		"index e69de29..1234567 100644",
		"--- a/config.txt",
		"+++ b/config.txt",
		"@@ -1 +1 @@",
		"-placeholder",
	} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("expected unmodified line %q to survive redaction, got: %s", want, got)
		}
	}
}

func TestRedactPatchSecrets_RemovedLineWithSecret_IsRedacted(t *testing.T) {
	patch := []byte(strings.Join([]string{
		"diff --git a/config.txt b/config.txt",
		"index 1234567..e69de29 100644",
		"--- a/config.txt",
		"+++ b/config.txt",
		"@@ -1 +1 @@",
		"-github_token: " + fakeGitHubToken,
		"+placeholder",
		"",
	}, "\n"))

	got, hadSecret := redactPatchSecrets(patch)
	if !hadSecret {
		t.Fatal("expected hadSecret=true for a secret-shaped removed line")
	}
	if strings.Contains(string(got), fakeGitHubToken) {
		t.Fatalf("expected the secret token to be redacted from the removed line, got: %s", got)
	}
}

func TestRedactPatchSecrets_ContextLine_NeverModified(t *testing.T) {
	// A secret-shaped string appearing in a CONTEXT line (unchanged
	// content, leading space) must never be rewritten: context lines must
	// match the target file exactly for `git apply` to locate the hunk.
	patch := []byte(strings.Join([]string{
		"diff --git a/config.txt b/config.txt",
		"index 1234567..89abcde 100644",
		"--- a/config.txt",
		"+++ b/config.txt",
		"@@ -1,2 +1,2 @@",
		" github_token: " + fakeGitHubToken,
		"-old line",
		"+new line",
		"",
	}, "\n"))

	got, hadSecret := redactPatchSecrets(patch)
	if hadSecret {
		t.Fatal("expected hadSecret=false: the only secret-shaped line is a context line, which must never be redacted")
	}
	if !strings.Contains(string(got), fakeGitHubToken) {
		t.Fatalf("expected the context line's secret-shaped content to survive untouched, got: %s", got)
	}
	if string(got) != string(patch) {
		t.Fatalf("expected patch to be returned byte-identical when no +/- line matches, got: %s", got)
	}
}

func TestRedactPatchSecrets_FileHeaderLines_NeverTreatedAsContent(t *testing.T) {
	// "---"/"+++" file-header lines must never be mistaken for a
	// removed/added content line even though they start with the same
	// byte, and must never be redacted even if a path coincidentally looks
	// secret-shaped.
	patch := []byte(strings.Join([]string{
		"diff --git a/sk-ant-file.txt b/sk-ant-file.txt",
		"index 1234567..89abcde 100644",
		"--- a/sk-ant-file.txt",
		"+++ b/sk-ant-file.txt",
		"@@ -1 +1 @@",
		"-old",
		"+new",
		"",
	}, "\n"))

	got, hadSecret := redactPatchSecrets(patch)
	if hadSecret {
		t.Fatalf("expected no redaction: no +/- content line contains a secret shape, got hadSecret=true: %s", got)
	}
	if string(got) != string(patch) {
		t.Fatalf("expected byte-identical passthrough, got: %s", got)
	}
}

func TestRedactPatchSecrets_NoSecret_ReturnsOriginalBytesUnchanged(t *testing.T) {
	patch := []byte(strings.Join([]string{
		"diff --git a/notes.txt b/notes.txt",
		"index 1234567..89abcde 100644",
		"--- a/notes.txt",
		"+++ b/notes.txt",
		"@@ -1 +1 @@",
		"-first line",
		"+second line",
		"",
	}, "\n"))

	got, hadSecret := redactPatchSecrets(patch)
	if hadSecret {
		t.Fatal("expected hadSecret=false for an ordinary patch with no secret shapes")
	}
	if string(got) != string(patch) {
		t.Fatal("expected the original bytes back unchanged when nothing matched")
	}
}

func TestRedactPatchSecrets_EmptyPatch_NoOp(t *testing.T) {
	got, hadSecret := redactPatchSecrets(nil)
	if hadSecret {
		t.Fatal("expected hadSecret=false for an empty patch")
	}
	if len(got) != 0 {
		t.Fatalf("expected empty output for empty input, got: %q", got)
	}
}

func TestRedactPatchSecrets_MultipleHunksAndFiles_OnlyOffendingLinesRedacted(t *testing.T) {
	patch := []byte(strings.Join([]string{
		"diff --git a/clean.txt b/clean.txt",
		"index 1234567..89abcde 100644",
		"--- a/clean.txt",
		"+++ b/clean.txt",
		"@@ -1 +1 @@",
		"-old clean",
		"+new clean",
		"diff --git a/dirty.txt b/dirty.txt",
		"index 1234567..89abcde 100644",
		"--- a/dirty.txt",
		"+++ b/dirty.txt",
		"@@ -1 +1 @@",
		"-old dirty",
		"+token: " + fakeGitHubToken,
		"",
	}, "\n"))

	got, hadSecret := redactPatchSecrets(patch)
	if !hadSecret {
		t.Fatal("expected hadSecret=true: dirty.txt's added line contains a secret shape")
	}
	if !strings.Contains(string(got), "-old clean") || !strings.Contains(string(got), "+new clean") {
		t.Fatalf("expected clean.txt's hunk to survive untouched, got: %s", got)
	}
	if !strings.Contains(string(got), "-old dirty") {
		t.Fatalf("expected dirty.txt's removed line (no secret) to survive untouched, got: %s", got)
	}
	if strings.Contains(string(got), fakeGitHubToken) {
		t.Fatalf("expected dirty.txt's added line to be redacted, got: %s", got)
	}
}
