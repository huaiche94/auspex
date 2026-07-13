// patchredact.go: corrective fix for the gap qa-05's leakage scanner
// (internal/integrationtest/leakage_scanner_test.go,
// TestLeakageScanner_KnownGap_SecretInTrackedFileDiffIsNotFiltered) and this
// package's own checkpoint-b06-era untracked_test.go
// (TestCapture_Untracked_SecretScan_NeverAppliesToTrackedDiffContent)
// independently documented: internal/redact's secret scan was wired only
// into the untracked-file archive path (archive.go's buildUntrackedArchive)
// and never applied to the staged/unstaged patch content Capture generates
// via gitx.Client.DiffPatch. A secret-shaped string staged into a TRACKED
// file (e.g. an API key typo'd into a config file before a commit) was
// captured verbatim, unredacted, into staged.patch.gz/unstaged.patch.gz.
//
// # Design choice: redact-in-place, not skip-with-manifest-annotation
//
// ADD §19.3's Capture sequence lists "secret/size/symlink filters" as step
// 8, positioned generally between diff generation (steps 5-6) and archival
// (step 9) — it is not scoped narrowly to untracked files the way §19.5's
// own "Untracked policy" section states its "secret scan" bullet. That
// general placement is this package's basis for extending the filter to the
// patch content too, rather than treating the boundary as accepted.
//
// Two ways to close the gap were considered:
//
//  1. Redact detected secret spans within the patch content itself before
//     archiving (this file's approach).
//  2. Refuse to include a patch that contains a detected secret and record
//     a SkipReason/annotation in the manifest, mirroring the untracked-
//     archive skip-ledger pattern.
//
// Redact-in-place was chosen because:
//
//   - §19.6 Restore (dry-run and any future real restore) requires
//     `git apply --check` against the staged/unstaged patches. Option 2
//     would mean a checkpoint containing so much as one secret-shaped
//     staged line has NO patch to restore-check against at all — the
//     entire staged or unstaged changeset's evidence is lost, not just the
//     one sensitive line. Redaction preserves the patch's file headers and
//     hunk structure so restore dry-run (checkpoint-b08) remains
//     meaningful for the rest of the changeset.
//   - A staged/unstaged diff is one unified patch blob per side (unlike
//     untracked files, which are individually excludable candidates in a
//     zip archive). Skipping the whole patch on one detected secret line
//     would discard every unrelated legitimate change in that patch too —
//     a much larger loss of evidence than the untracked-archive path's
//     per-file skip.
//   - Redaction can be applied conservatively (see below) without
//     invalidating the patch's applicability, which the untracked-archive
//     skip-ledger approach cannot offer any equivalent of (there is no
//     partial-file capture there either).
//
// # Redaction scope: added/removed lines only, never context lines
//
// A unified diff's context lines (leading space) must match the target
// file's actual content exactly for `git apply` to locate the hunk;
// rewriting a context line's content would silently corrupt the patch's
// own applicability. Only "+" (added) and "-" (removed) line bodies are
// scanned and, if they match internal/redact's detectors, replaced
// wholesale with a fixed placeholder — this changes what the hunk records
// as added/removed content (the point: the secret must not survive into
// the archive) while leaving line count, hunk headers (@@ ... @@), file
// headers (diff --git/index/---/+++), and every context line byte-for-byte
// intact, so the patch remains structurally valid and still applies
// (checkpoint-b08's dry-run `git apply --check` continues to succeed
// against the redacted patch, since redaction never touches context lines
// or hunk arithmetic).
//
// Binary patches (`GIT binary patch` directives) are left untouched:
// internal/redact's detectors are plaintext pattern matches and binary
// literal-diff content already contains no such shapes worth scanning
// (mirrors internal/redact's own IsLikelyBinary skip for the untracked
// path); a binary blob also cannot be partially redacted without producing
// an undecodable patch.
package repocheckpoint

import (
	"bytes"

	"github.com/huaiche94/auspex/internal/redact"
)

// redactedLinePlaceholder replaces an entire added/removed line's content
// when that line matches internal/redact's secret detectors. Fixed and
// generic (never echoes any part of the matched text back — same
// discipline internal/redact.Detector.Find itself documents: a partial
// secret excerpt is itself a leak vector).
const redactedLinePlaceholder = "[REDACTED: secret-shaped content removed by auspex checkpoint capture]"

// redactPatchSecrets scans a binary-safe patch (gitx.Client.DiffPatch
// output) line by line and replaces the body of any added ("+") or removed
// ("-") line whose content matches internal/redact's filename-independent
// content detectors with redactedLinePlaceholder. It reports whether any
// redaction occurred so the caller can record that fact in the manifest
// (Recoverability.Warnings), mirroring the untracked-archive path's
// "skipped reasons recorded" discipline even though nothing here is
// skipped — something was altered, and that is disclosed the same way.
//
// File header lines (diff --git, index, ---, +++, new/deleted file mode,
// rename from/to, similarity index), hunk headers (@@ ... @@), the
// "\ No newline at end of file" marker, and context lines (leading space)
// are never modified — only "+"/"-" line bodies are candidates, and only
// within a text hunk (a line belonging to a `GIT binary patch` block is
// never a "+"/"-" prefixed line in the first place, so this scan is
// naturally a no-op there without needing to special-case binary
// detection explicitly).
func redactPatchSecrets(patch []byte) (redacted []byte, hadSecret bool) {
	if len(patch) == 0 {
		return patch, false
	}

	lines := splitPatchLines(patch)
	for i, line := range lines {
		body, prefix, ok := diffLineBody(line)
		if !ok {
			continue
		}
		if len(redact.ScanContent(body)) == 0 {
			continue
		}
		hadSecret = true
		// Preserve the line's own trailing terminator (a "\n", or nothing
		// for a final line with no trailing newline) — only the content
		// between the prefix and the terminator is replaced.
		terminator := ""
		if bytes.HasSuffix(body, []byte("\n")) {
			terminator = "\n"
		}
		lines[i] = append(append([]byte{}, prefix...), []byte(redactedLinePlaceholder+terminator)...)
	}

	if !hadSecret {
		return patch, false
	}
	return joinPatchLines(lines), true
}

// diffLineBody reports whether line is a "+"-or-"-"-prefixed content line
// (as opposed to a file header, hunk header, or context line) and, if so,
// returns its body (everything after the single prefix byte) and the
// prefix byte itself for reassembly. "+++"/"---" file-header lines are
// excluded explicitly since they are three-character markers, not a
// one-character prefix followed by content.
func diffLineBody(line []byte) (body []byte, prefix []byte, ok bool) {
	if len(line) == 0 {
		return nil, nil, false
	}
	switch line[0] {
	case '+':
		if bytes.HasPrefix(line, []byte("+++")) {
			return nil, nil, false
		}
		return line[1:], line[:1], true
	case '-':
		if bytes.HasPrefix(line, []byte("---")) {
			return nil, nil, false
		}
		return line[1:], line[:1], true
	default:
		return nil, nil, false
	}
}

// splitPatchLines splits patch into lines, preserving each line's trailing
// "\n" (if any) so joinPatchLines can reassemble byte-identical output for
// every unmodified line. Git patch output always ends the final line with
// "\n" unless the diffed file itself lacks a trailing newline (signaled by
// the separate "\ No newline at end of file" marker line, which this
// function treats as an ordinary line like any other — it is never a
// "+"/"-" prefixed line so redactPatchSecrets never touches it).
func splitPatchLines(patch []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range patch {
		if b == '\n' {
			lines = append(lines, patch[start:i+1])
			start = i + 1
		}
	}
	if start < len(patch) {
		lines = append(lines, patch[start:])
	}
	return lines
}

// joinPatchLines is splitPatchLines's inverse.
func joinPatchLines(lines [][]byte) []byte {
	var buf bytes.Buffer
	for _, l := range lines {
		buf.Write(l)
	}
	return buf.Bytes()
}
