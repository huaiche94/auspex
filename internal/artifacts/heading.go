package artifacts

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
)

// HeadingExistsValidator checks that Candidate.Path's Markdown content
// contains a heading line exactly matching Candidate.Heading (e.g.
// `# 18. Progress Tree 與 State Checkpointing`, ADD §18.5's
// `heading_exists:` acceptance-criterion shape). This is the "document
// section actually has the heading it claims" check — a section that
// merely contains the heading TEXT somewhere in prose (not as a heading
// line) does not satisfy it.
//
// Matching rules:
//   - the candidate heading is compared against each line, after trimming
//     only trailing carriage returns/whitespace (CRLF-safe) — leading
//     whitespace is NOT trimmed, since 4+ leading spaces would make a line
//     an indented code block in CommonMark, not a heading, and this
//     validator must not treat those as equivalent;
//   - a line only counts as a heading candidate if it starts with 1-6 '#'
//     characters followed by a space (CommonMark ATX heading syntax) —
//     this excludes false positives like a code fence line that happens to
//     contain the same text;
//   - lines inside fenced code blocks (``` or ~~~) are skipped, so an
//     example heading shown inside a fence in the document (e.g. this very
//     package's fixtures, which quote ADD §18.5's YAML block) is not
//     mistaken for the section's real heading.
type HeadingExistsValidator struct{}

// Kind returns this validator's stable identifier.
func (HeadingExistsValidator) Kind() string { return "heading_exists" }

// Validate reports whether Candidate.Heading appears as an actual ATX
// heading line (outside any fenced code block) in Candidate.Path.
func (HeadingExistsValidator) Validate(_ context.Context, c Candidate) (Result, error) {
	if c.Path == "" {
		return Failed("heading_exists: candidate has no Path"), nil
	}
	if strings.TrimSpace(c.Heading) == "" {
		return Failed("heading_exists: candidate has no Heading to search for"), nil
	}

	f, err := os.Open(c.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return Failed(fmt.Sprintf("heading_exists: %s does not exist", c.Path)), nil
		}
		return Result{}, fmt.Errorf("artifacts: heading_exists: open %s: %w", c.Path, err)
	}
	defer func() { _ = f.Close() }()

	want := strings.TrimRight(c.Heading, "\r\n")
	var open *fence

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")

		if marker, isFence := fenceRun(line); isFence {
			switch {
			case open == nil:
				open = &marker
			case marker.char == open.char && marker.n >= open.n:
				open = nil
			}
			continue
		}
		if open != nil {
			continue
		}
		if !isATXHeadingLine(line) {
			continue
		}
		if line == want {
			return Passed, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return Result{}, fmt.Errorf("artifacts: heading_exists: scan %s: %w", c.Path, err)
	}

	return Failed(fmt.Sprintf("heading_exists: heading %q not found in %s", c.Heading, c.Path)), nil
}

// isATXHeadingLine reports whether line is a CommonMark ATX heading: 1-6
// '#' characters, then a space, then the heading text (or a closing-only
// '#' line, which is also excluded here since it never has a following
// space and thus never anchors real content).
func isATXHeadingLine(line string) bool {
	i := 0
	for i < len(line) && line[i] == '#' {
		i++
	}
	if i == 0 || i > 6 {
		return false
	}
	return i < len(line) && line[i] == ' '
}

var _ Validator = HeadingExistsValidator{}
