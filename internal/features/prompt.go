package features

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strings"
	"unicode"

	"github.com/huaiche94/preflight/internal/domain"
)

// PromptFeatures holds ONLY signals derived from a prompt. It must never
// contain raw prompt text or any substring of it. The only string fields
// permitted are the fixed-alphabet SHA-256 hex digest and enum values;
// everything else is a count, flag, or score (ADD §14.2 "Prompt-derived").
type PromptFeatures struct {
	// Size signals.
	ByteLength int
	RuneCount  int
	LineCount  int

	// ApproxTokens is the tokenizer-free estimate from ADD §14.7:
	//   ceil(ASCII alphanumeric / 4.0) + ceil(CJK runes / 1.5)
	//   + ceil(other Unicode / 2.5) + ceil(punctuation / 3.0)
	// It is an estimate, never an exact count, so TokenConfidence is
	// always ConfidenceLow (source: estimated).
	ApproxTokens    int
	TokenConfidence domain.Confidence

	// SHA256Hex is the lowercase hex SHA-256 digest of the raw prompt
	// bytes — the only prompt-derived identity that may be persisted
	// (mirrors EvaluateTurnRequest.PromptHash / Authorization.PromptHash).
	SHA256Hex string

	// Structure signals.
	ExplicitPathCount       int // whitespace-delimited tokens that look like file paths
	ListItemCount           int // bullet or numbered list lines (deliverable proxy)
	AcceptanceCriteriaCount int // checkbox-style "- [ ]" / "- [x]" lines

	// Verb signals (ADD §14.2: fix/implement/refactor/investigate/migrate).
	HasFixVerb         bool
	HasImplementVerb   bool
	HasRefactorVerb    bool
	HasInvestigateVerb bool
	HasMigrateVerb     bool

	// Domain indicators.
	MentionsTests           bool
	MentionsSchemaOrAPI     bool
	MentionsSecurity        bool
	MentionsPerformance     bool
	MentionsDocumentation   bool
	LongDocumentIndicator   bool // chapter/section/report/design-document phrasing
	QuestionIndicator       bool
	OpenEndedIndicator      bool
	CrossLayerIndicator     bool
	RepositoryWideIndicator bool
}

// ExtractPromptFeatures is the single entry point through which raw prompt
// text crosses into this package. The raw string is consumed here and only
// derived signals are returned; callers must not log or persist the input
// (Constitution §7 rule 2). It is deterministic: identical input always
// yields identical output.
func ExtractPromptFeatures(raw string) PromptFeatures {
	sum := sha256.Sum256([]byte(raw))

	pf := PromptFeatures{
		ByteLength:      len(raw),
		RuneCount:       len([]rune(raw)),
		ApproxTokens:    approxTokens(raw),
		TokenConfidence: domain.ConfidenceLow,
		SHA256Hex:       hex.EncodeToString(sum[:]),
	}

	lines := strings.Split(raw, "\n")
	pf.LineCount = len(lines)
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if isChecklistLine(t) {
			pf.AcceptanceCriteriaCount++
			pf.ListItemCount++
		} else if isListLine(t) {
			pf.ListItemCount++
		}
	}

	lowered := strings.ToLower(raw)
	words := wordSet(lowered)
	has := func(ws ...string) bool {
		for _, w := range ws {
			if _, ok := words[w]; ok {
				return true
			}
		}
		return false
	}
	phrase := func(ps ...string) bool {
		for _, p := range ps {
			if strings.Contains(lowered, p) {
				return true
			}
		}
		return false
	}

	for _, tok := range strings.Fields(raw) {
		if looksLikePath(tok) {
			pf.ExplicitPathCount++
		}
	}

	pf.HasFixVerb = has("fix", "fixes", "fixing", "bug", "bugfix", "repair", "resolve")
	pf.HasImplementVerb = has("implement", "implements", "implementing", "add", "adds", "create", "creates", "build", "builds", "write", "introduce")
	pf.HasRefactorVerb = has("refactor", "refactors", "refactoring", "restructure", "rename", "extract", "simplify")
	pf.HasInvestigateVerb = has("investigate", "investigating", "debug", "diagnose", "why", "analyze", "analyse", "inspect", "explain")
	pf.HasMigrateVerb = has("migrate", "migrates", "migrating", "migration", "upgrade", "port")

	pf.MentionsTests = has("test", "tests", "testing", "unittest", "unittests", "spec", "specs") || phrase("unit test", "integration test")
	pf.MentionsSchemaOrAPI = has("schema", "api", "apis", "endpoint", "endpoints", "contract", "protocol")
	pf.MentionsSecurity = has("security", "auth", "authentication", "authorization", "secret", "secrets", "vulnerability", "cve", "injection", "sanitize", "csrf", "xss")
	pf.MentionsPerformance = has("performance", "slow", "latency", "profiling", "profile", "benchmark", "throughput", "regression")
	pf.MentionsDocumentation = has("document", "documentation", "readme", "docs", "changelog", "comment", "comments", "docstring")
	pf.LongDocumentIndicator = has("chapter", "chapters", "report", "whitepaper") ||
		(has("section", "sections") && pf.MentionsDocumentation) ||
		phrase("design document", "architecture document", "design doc")
	pf.QuestionIndicator = strings.Contains(raw, "?") || firstWordIn(lowered, "what", "why", "how", "where", "when", "which", "who", "does", "is", "are", "can", "should")
	pf.OpenEndedIndicator = has("improve", "better", "cleanup", "explore", "somehow", "etc") || phrase("clean up", "make it better")
	pf.CrossLayerIndicator = phrase("cross-layer", "end-to-end", "e2e", "full-stack", "frontend and backend") || has("across", "layers")
	pf.RepositoryWideIndicator = has("codebase", "everywhere", "monorepo") || phrase("repository-wide", "repo-wide", "entire repo", "whole repo", "all files", "every file")

	return pf
}

// approxTokens implements the ADD §14.7 tokenizer-free approximation.
// Whitespace is not counted. Output is always a non-negative finite int.
func approxTokens(raw string) int {
	var ascii, cjk, punct, other int
	for _, r := range raw {
		switch {
		case unicode.IsSpace(r):
			// not counted
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			ascii++
		case isCJK(r):
			cjk++
		case r < 128: // remaining ASCII: punctuation and symbols
			punct++
		default:
			other++
		}
	}
	return int(math.Ceil(float64(ascii)/4.0)) +
		int(math.Ceil(float64(cjk)/1.5)) +
		int(math.Ceil(float64(other)/2.5)) +
		int(math.Ceil(float64(punct)/3.0))
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

func isChecklistLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "- [ ]") ||
		strings.HasPrefix(trimmed, "- [x]") ||
		strings.HasPrefix(trimmed, "- [X]") ||
		strings.HasPrefix(trimmed, "* [ ]") ||
		strings.HasPrefix(trimmed, "* [x]")
}

func isListLine(trimmed string) bool {
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
		return true
	}
	// Numbered list: one or more digits followed by "." or ")".
	i := 0
	for i < len(trimmed) && trimmed[i] >= '0' && trimmed[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(trimmed) {
		return false
	}
	return trimmed[i] == '.' || trimmed[i] == ')'
}

// looksLikePath reports whether a whitespace-delimited token resembles a
// file path or symbol path. Heuristic only; used as a count, never stored.
func looksLikePath(tok string) bool {
	tok = strings.Trim(tok, "`'\"()[]{},:;")
	if len(tok) < 3 {
		return false
	}
	if strings.ContainsAny(tok, " \t") {
		return false
	}
	if strings.Contains(tok, "/") && !strings.HasPrefix(tok, "http") {
		return true
	}
	for _, ext := range []string{".go", ".md", ".sql", ".json", ".yaml", ".yml", ".ts", ".py", ".cs", ".sh"} {
		if strings.HasSuffix(tok, ext) {
			return true
		}
	}
	return false
}

func wordSet(lowered string) map[string]struct{} {
	ws := make(map[string]struct{})
	for _, w := range strings.FieldsFunc(lowered, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		ws[w] = struct{}{}
	}
	return ws
}

func firstWordIn(lowered string, candidates ...string) bool {
	fields := strings.Fields(lowered)
	if len(fields) == 0 {
		return false
	}
	first := strings.Trim(fields[0], ",.!?:;")
	for _, c := range candidates {
		if first == c {
			return true
		}
	}
	return false
}
