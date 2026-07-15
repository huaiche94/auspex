package features

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/huaiche94/auspex/internal/domain"
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

	// #51: on the blocking UserPromptSubmit hook path a prompt may be
	// multi-MB (a pasted log or diff), so every size op below is
	// allocation-free. RuneCount uses utf8.RuneCountInString rather than
	// len([]rune(raw)) — same count for every input, invalid UTF-8 included
	// (Go guarantees the identity), without materializing a rune slice the
	// size of the prompt. LineCount is strings.Count+1 (Split-by-"\n" yields
	// count("\n")+1 segments) instead of allocating the full line slice.
	pf := PromptFeatures{
		ByteLength:      len(raw),
		RuneCount:       utf8.RuneCountInString(raw),
		LineCount:       strings.Count(raw, "\n") + 1,
		ApproxTokens:    approxTokens(raw),
		TokenConfidence: domain.ConfidenceLow,
		SHA256Hex:       hex.EncodeToString(sum[:]),
	}

	// Fused line scan (#51): the checklist/list detection walks each
	// "\n"-delimited segment in place — byte-identical to iterating
	// strings.Split(raw, "\n") — without allocating a slice of every line.
	pf.AcceptanceCriteriaCount, pf.ListItemCount = countListLines(raw)

	// Fused path scan (#51): count path-shaped whitespace-delimited tokens
	// of raw in place — byte-identical to ranging strings.Fields(raw) — but
	// without allocating a slice of every token in a multi-MB prompt.
	pf.ExplicitPathCount = countExplicitPaths(raw)

	lowered := strings.ToLower(raw)

	// Vocabulary scan (#51): the pre-#51 wordSet materialized a map of EVERY
	// distinct word in the prompt though only the fixed promptVocab (~130
	// words, the union of every has() list below) is ever tested. scanVocab
	// walks the lowered tokens ONCE and keeps only the vocab hits, so
	// retained memory is bounded by the vocabulary size, not the prompt
	// size. has(w) is then a lookup in that bounded found-set — identical
	// truth value to the old "w ∈ wordSet(lowered)" for every w, because
	// promptVocab is built from these exact same word lists (no drift is
	// possible: the literals feed both has() and the vocab).
	found := scanVocab(lowered, promptVocab)
	has := func(ws ...string) bool {
		for _, w := range ws {
			if _, ok := found[w]; ok {
				return true
			}
		}
		return false
	}
	// phrase() retains the multi-word strings.Contains scans (#51 item 5):
	// they scan the already-lowered copy, allocate nothing, and cover the
	// hyphenated/spaced indicators a word-token set cannot express.
	phrase := func(ps ...string) bool {
		for _, p := range ps {
			if strings.Contains(lowered, p) {
				return true
			}
		}
		return false
	}

	// Vocabulary widened per issue #42 (fix path step 3): ordinary prompts
	// were collapsing to TaskClassUnknown because these lists missed
	// everyday synonyms. Every word below is a clear synonym for a signal
	// slot that already existed — no new signal categories and no new
	// classes; genuinely ambiguous generic verbs ("update", "change",
	// "remove") are deliberately NOT mapped, because choosing a class for
	// them would be an ungrounded modeling decision (they stay
	// TaskClassUnknown, the classifier's designed answer for insufficient
	// signal). The word lists live in package-level slices (fixVerbWords
	// etc.) so ExtractPromptFeatures and promptVocab share one definition:
	// the #51 fused scan cannot drift from the signals it feeds.
	pf.HasFixVerb = has(fixVerbWords...)
	pf.HasImplementVerb = has(implementVerbWords...)
	pf.HasRefactorVerb = has(refactorVerbWords...)
	pf.HasInvestigateVerb = has(investigateVerbWords...)
	pf.HasMigrateVerb = has(migrateVerbWords...)

	pf.MentionsTests = has(testsWords...) || phrase("unit test", "integration test")
	pf.MentionsSchemaOrAPI = has(schemaAPIWords...)
	pf.MentionsSecurity = has(securityWords...)
	pf.MentionsPerformance = has(performanceWords...)
	pf.MentionsDocumentation = has(documentationWords...)
	pf.LongDocumentIndicator = has(longDocPrimaryWords...) ||
		(has(longDocSectionWords...) && pf.MentionsDocumentation) ||
		phrase("design document", "architecture document", "design doc")
	pf.QuestionIndicator = strings.Contains(raw, "?") || firstWordIn(lowered, "what", "why", "how", "where", "when", "which", "who", "does", "is", "are", "can", "should")
	pf.OpenEndedIndicator = has(openEndedWords...) || phrase("clean up", "make it better")
	pf.CrossLayerIndicator = phrase("cross-layer", "end-to-end", "e2e", "full-stack", "frontend and backend") || has(crossLayerWords...)
	pf.RepositoryWideIndicator = has(repoWideWords...) || phrase("repository-wide", "repo-wide", "entire repo", "whole repo", "all files", "every file")

	return pf
}

// Prompt vocabulary (#42 widening, #49 tuning). Each slice is the exact set
// of words one has() call tests; promptVocab is their union. Keeping the
// literals here — used by BOTH ExtractPromptFeatures' has() calls and
// promptVocab — means the #51 single-pass scan can never test against a
// different set than the signals compute over. "regression" is deliberately
// absent from performanceWords (#49): in a dev context it names a reappeared
// BUG far more often than a performance drop, and the classifier's verb-less
// rescue rule would misroute those bug reports to performance-investigation;
// genuine perf regressions still match via "performance"/"slow"/"latency".
var (
	fixVerbWords = []string{"fix", "fixes", "fixing", "bug", "bugfix", "repair", "resolve",
		"typo", "typos", "broken", "crash", "crashes", "patch", "hotfix"}
	implementVerbWords = []string{"implement", "implements", "implementing", "add", "adds", "create", "creates", "build", "builds", "write", "introduce",
		"develop", "generate", "scaffold"}
	refactorVerbWords = []string{"refactor", "refactors", "refactoring", "restructure", "rename", "extract", "simplify",
		"reorganize", "reorganise", "consolidate", "decouple", "deduplicate", "dedupe", "modularize", "modularise"}
	investigateVerbWords = []string{"investigate", "investigating", "debug", "diagnose", "why", "analyze", "analyse", "inspect", "explain",
		"troubleshoot", "understand", "review", "audit"}
	migrateVerbWords = []string{"migrate", "migrates", "migrating", "migration", "upgrade", "port"}

	testsWords       = []string{"test", "tests", "testing", "unittest", "unittests", "spec", "specs", "coverage"}
	schemaAPIWords   = []string{"schema", "api", "apis", "endpoint", "endpoints", "contract", "protocol"}
	securityWords    = []string{"security", "auth", "authentication", "authorization", "secret", "secrets", "vulnerability", "cve", "injection", "sanitize", "csrf", "xss"}
	performanceWords = []string{"performance", "slow", "latency", "profiling", "profile", "benchmark", "throughput",
		"optimize", "optimise", "optimization", "optimisation", "speedup", "faster", "perf"}
	documentationWords = []string{"document", "documentation", "readme", "docs", "changelog", "comment", "comments", "docstring",
		"tutorial", "guide", "guides", "documenting"}
	longDocPrimaryWords = []string{"chapter", "chapters", "report", "whitepaper"}
	longDocSectionWords = []string{"section", "sections"}
	openEndedWords      = []string{"improve", "better", "cleanup", "explore", "somehow", "etc"}
	crossLayerWords     = []string{"across", "layers"}
	repoWideWords       = []string{"codebase", "everywhere", "monorepo"}
)

// promptVocab is the union of every has() word list — the ONLY words
// scanVocab needs to retain from a prompt (#51). Built once at package init;
// its size (not the prompt's) bounds the per-call found-set.
var promptVocab = buildVocab(
	fixVerbWords, implementVerbWords, refactorVerbWords, investigateVerbWords, migrateVerbWords,
	testsWords, schemaAPIWords, securityWords, performanceWords, documentationWords,
	longDocPrimaryWords, longDocSectionWords, openEndedWords, crossLayerWords, repoWideWords,
)

func buildVocab(lists ...[]string) map[string]struct{} {
	v := make(map[string]struct{})
	for _, list := range lists {
		for _, w := range list {
			v[w] = struct{}{}
		}
	}
	return v
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

// countListLines walks each "\n"-delimited segment of raw in place and
// applies the checklist/list rules, returning the acceptance-criteria and
// list-item counts. It is behavior-identical to the pre-#51 loop over
// strings.Split(raw, "\n") — Split yields one segment per "\n" plus a
// trailing segment, which this loop reproduces exactly (process the run
// before each '\n', then the final run) — but allocates no per-line slice
// for a many-line prompt. Splitting on the '\n' byte is correct for UTF-8
// (0x0A never occurs inside a multibyte rune) and, like the old code, leaves
// any trailing '\r' for TrimSpace to strip, so CRLF input behaves the same
// (windows-portable).
func countListLines(raw string) (acceptance, listItems int) {
	classify := func(segment string) {
		t := strings.TrimSpace(segment)
		if isChecklistLine(t) {
			acceptance++
			listItems++
		} else if isListLine(t) {
			listItems++
		}
	}
	start := 0
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\n' {
			classify(raw[start:i])
			start = i + 1
		}
	}
	classify(raw[start:])
	return acceptance, listItems
}

// countExplicitPaths counts path-shaped whitespace-delimited tokens of raw.
// It is behavior-identical to the pre-#51 loop over strings.Fields(raw) —
// each maximal run of non-space runes is one token, leading/trailing/
// repeated whitespace collapses, empty input yields zero — but walks the
// tokens in place instead of allocating a slice of every token in a
// multi-MB prompt (#51).
func countExplicitPaths(raw string) int {
	count := 0
	start := -1
	for i, r := range raw {
		if unicode.IsSpace(r) {
			if start >= 0 {
				if looksLikePath(raw[start:i]) {
					count++
				}
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 && looksLikePath(raw[start:]) {
		count++
	}
	return count
}

// scanVocab walks the lowered prompt's word tokens ONCE and returns only the
// vocab members it saw (#51). Tokenization matches the pre-#51 wordSet
// exactly: a token is a maximal run of letters/digits, everything else is a
// separator (strings.FieldsFunc with unicode.IsLetter||IsDigit). Because
// every word ever tested via has() is in vocab, "w ∈ result" has the same
// truth value as the old "w ∈ wordSet(lowered)" for every such w — but the
// returned set is bounded by vocab size, not by the number of distinct words
// in the prompt. Substrings taken for the membership probe share raw's
// backing array (no copy); only the bounded hits are retained.
func scanVocab(lowered string, vocab map[string]struct{}) map[string]struct{} {
	found := make(map[string]struct{})
	start := -1
	for i, r := range lowered {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			if tok := lowered[start:i]; inVocab(vocab, tok) {
				found[tok] = struct{}{}
			}
			start = -1
		}
	}
	if start >= 0 {
		if tok := lowered[start:]; inVocab(vocab, tok) {
			found[tok] = struct{}{}
		}
	}
	return found
}

func inVocab(vocab map[string]struct{}, tok string) bool {
	_, ok := vocab[tok]
	return ok
}

// firstWordIn reports whether the first whitespace-delimited word of lowered
// (with surrounding light punctuation trimmed) equals any candidate. It
// extracts only that first field — behavior-identical to
// strings.Trim(strings.Fields(lowered)[0], ...) — without allocating a slice
// of every field of a possibly-multi-MB prompt (#51). Field boundaries use
// unicode.IsSpace, matching strings.Fields.
func firstWordIn(lowered string, candidates ...string) bool {
	start := -1
	end := len(lowered)
	for i, r := range lowered {
		if unicode.IsSpace(r) {
			if start >= 0 {
				end = i
				break
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start < 0 {
		return false // no non-space content: strings.Fields would be empty
	}
	first := strings.Trim(lowered[start:end], ",.!?:;")
	for _, c := range candidates {
		if first == c {
			return true
		}
	}
	return false
}
