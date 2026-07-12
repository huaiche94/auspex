package artifacts

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
)

// FenceBalanceValidator checks that every fenced code block in
// Candidate.Path's Markdown content is properly closed: each opening fence
// (``` or ~~~, CommonMark's two fence styles) has a matching closing fence
// of the same character and at-least-equal run length, per CommonMark's
// fenced-code-block spec. An unbalanced fence (opened but never closed, or
// closed with the wrong marker) silently corrupts every subsequent
// heading/paragraph in the rendered document — this is the
// "markdown-fence-balance" validator ADD §18.5's example acceptance
// contract names explicitly.
type FenceBalanceValidator struct{}

// Kind returns this validator's stable identifier.
func (FenceBalanceValidator) Kind() string { return "fence_balance" }

// fence records one open code-fence's marker character and run length, so
// a close can be matched against the correct entry when fences nest via
// mismatched-length reopening (CommonMark: a fence closes only on a line
// with the same character and a run length >= the opening run length).
type fence struct {
	char string // "`" or "~"
	n    int    // run length
}

// Validate scans Candidate.Path line by line and reports whether every
// opened fence is eventually closed by end of file.
func (FenceBalanceValidator) Validate(_ context.Context, c Candidate) (Result, error) {
	if c.Path == "" {
		return Failed("fence_balance: candidate has no Path"), nil
	}

	f, err := os.Open(c.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return Failed(fmt.Sprintf("fence_balance: %s does not exist", c.Path)), nil
		}
		return Result{}, fmt.Errorf("artifacts: fence_balance: open %s: %w", c.Path, err)
	}
	defer func() { _ = f.Close() }()

	var open *fence
	openedAtLine := 0

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimRight(scanner.Text(), "\r")
		marker, isFenceLine := fenceRun(line)
		if !isFenceLine {
			continue
		}

		switch {
		case open == nil:
			open = &marker
			openedAtLine = lineNo
		case marker.char == open.char && marker.n >= open.n:
			open = nil
			openedAtLine = 0
		default:
			// A fence line of a different character (or too-short same
			// character run) while already inside a fence is ordinary
			// fenced content (e.g. a ``` example shown inside a ~~~
			// block), not a close — leave `open` as-is.
		}
	}
	if err := scanner.Err(); err != nil {
		return Result{}, fmt.Errorf("artifacts: fence_balance: scan %s: %w", c.Path, err)
	}

	if open != nil {
		return Failed(fmt.Sprintf("fence_balance: unclosed code fence opened at line %d in %s", openedAtLine, c.Path)), nil
	}
	return Passed, nil
}

// fenceRun reports whether line is a fence delimiter line (optionally
// preceded by up to 3 leading spaces, per CommonMark) — a run of 3+ '`' or
// '~' characters, optionally followed by an info string. It returns the
// marker character and run length.
func fenceRun(line string) (fence, bool) {
	trimmed := strings.TrimLeft(line, " ")
	if len(trimmed) > len(line) {
		return fence{}, false
	}
	if len(line)-len(trimmed) > 3 {
		return fence{}, false // 4+ leading spaces is an indented code block, not a fence
	}
	if trimmed == "" {
		return fence{}, false
	}
	ch := trimmed[0]
	if ch != '`' && ch != '~' {
		return fence{}, false
	}
	n := 0
	for n < len(trimmed) && trimmed[n] == ch {
		n++
	}
	if n < 3 {
		return fence{}, false
	}
	// Backtick fences cannot have a backtick in their info string
	// (CommonMark); a run of backticks followed by more backticks after
	// non-fence characters would be a malformed line, but for balance
	// purposes we only need the leading run.
	return fence{char: string(ch), n: n}, true
}

var _ Validator = FenceBalanceValidator{}
