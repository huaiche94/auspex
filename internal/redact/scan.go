// scan.go: the top-level scanning API this package exposes to callers
// (internal/repocheckpoint's untracked-archive policy, primarily). Ties
// together filename.go's name-pattern check and patterns.go's content
// detectors into one Scan call, with the same size-capping discipline
// internal/repocheckpoint applies everywhere else in this codebase (never
// read an unbounded amount of untrusted file content into memory).
package redact

import (
	"bytes"
	"os"
)

// MaxContentScanBytes is the amount of a candidate file's content this
// package will read and scan for content-based secret patterns. Content
// beyond this cap is not scanned — documented here, not silent: a secret
// that only appears after the first 1 MiB of an unusually large text file
// is a gap in this package's coverage (see doc.go's scope note), not a
// bug, and qa-05's independent leakage scan is the cross-cutting backstop
// for exactly this kind of single-layer gap.
const MaxContentScanBytes = 1 << 20 // 1 MiB

// Finding is one detector match against one candidate file.
type Finding struct {
	// Detector is the Name of the content.Detector that matched, or the
	// literal "filename" for a name-pattern match (never both reported as
	// one Finding — Scan returns one Finding per match, name-pattern and
	// content matches alike, so a caller can tell them apart by this
	// field without a separate boolean).
	Detector string
}

// Result is Scan's return value: whether anything matched, and which
// detector(s) fired. A candidate can match on filename alone, content
// alone, both, or neither.
type Result struct {
	Findings []Finding
}

// Matched reports whether Scan found anything at all.
func (r Result) Matched() bool { return len(r.Findings) > 0 }

// ScanPath evaluates path's filename against the name-pattern list, then
// — unless the file looks binary — reads up to MaxContentScanBytes of its
// content and runs every content detector against it. path must be a real,
// readable file; ScanPath returns an error only for an I/O failure (a
// filename-pattern match with an unreadable file is impossible to reach
// content-scan for, so that case still reports the filename Finding and a
// non-nil error — callers should check Findings even on error, matching
// the same "fail closed but still report what is known" posture
// internal/repocheckpoint's own security checks use).
func ScanPath(path string) (Result, error) {
	var result Result
	if MatchesSecretFilename(path) {
		result.Findings = append(result.Findings, Finding{Detector: "filename"})
	}

	f, err := os.Open(path)
	if err != nil {
		return result, err
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, MaxContentScanBytes)
	n, readErr := f.Read(buf)
	if readErr != nil && n == 0 {
		// A zero-byte file, or a read error on the very first read (e.g.
		// permission changed mid-scan) — either way there is no content
		// to scan; the filename Finding above (if any) still stands.
		return result, nil
	}
	content := buf[:n]

	if IsLikelyBinary(content) {
		// Binary content does not carry plaintext secrets in the shapes
		// this package's detectors match (see doc.go); scanning it would
		// only produce noise, not signal.
		return result, nil
	}

	result.Findings = append(result.Findings, ScanContent(content)...)
	return result, nil
}

// ScanContent runs every content detector against content directly, for
// callers that already have bytes in hand (e.g. a test, or a caller
// scanning a buffer rather than a file). It does not check
// MaxContentScanBytes itself — that cap is ScanPath's responsibility, so a
// caller who explicitly wants to scan more may do so deliberately.
func ScanContent(content []byte) []Finding {
	var findings []Finding
	for _, d := range detectors {
		if _, _, ok := d.Find(content); ok {
			findings = append(findings, Finding{Detector: d.Name})
		}
	}
	return findings
}

// IsLikelyBinary applies Git's own heuristic for "is this a binary file":
// the presence of a NUL byte in the first chunk of content. This is the
// same test `git diff`/`git grep` use internally (documented in Git's own
// source, xdiff-interface.c's buffer_is_binary check) — deliberately not a
// more elaborate content-type sniff, since this package only needs to
// decide "worth running text-pattern regexes over this," not classify the
// file's actual type.
func IsLikelyBinary(content []byte) bool {
	return bytes.IndexByte(content, 0) != -1
}
