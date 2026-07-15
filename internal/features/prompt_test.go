package features

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode"

	"github.com/huaiche94/auspex/internal/domain"
)

// Sentinel tokens deliberately avoid the hex alphabet [0-9a-f] so they can
// never collide with the SHA-256 digest field.
const leakSentinel = "zqxSENTINELzz_ROTATE_THIS_KEY_zzJKLMzqx"

var leakProbePrompt = "Please " + leakSentinel + " fix the login bug in internal/auth/session.go\n" +
	"- [ ] must not zzSENTINELTWOzz leak\n" +
	"密碼是 zzzSENTINELTHREEzzz 請勿外洩\n" +
	"api_key=zqxSUPERSECRETVALUEzqx"

// TestPromptFeaturesNoRawTextLeak is the Constitution §7 privacy assertion:
// raw prompt text must never appear anywhere in the extractor's output
// struct — not in any string field (checked by reflection walk) and not in
// any serialized form of the whole struct.
func TestPromptFeaturesNoRawTextLeak(t *testing.T) {
	pf := ExtractPromptFeatures(leakProbePrompt)

	sentinels := []string{
		leakSentinel,
		"zzSENTINELTWOzz",
		"zzzSENTINELTHREEzzz",
		"zqxSUPERSECRETVALUEzqx",
		"密碼是",
		leakProbePrompt,
	}
	// Every raw line of the prompt is also treated as a sentinel.
	for _, line := range strings.Split(leakProbePrompt, "\n") {
		if strings.TrimSpace(line) != "" {
			sentinels = append(sentinels, line)
		}
	}

	// 1. Reflection walk: no string field may contain any sentinel.
	v := reflect.ValueOf(pf)
	tp := reflect.TypeOf(pf)
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).Kind() != reflect.String {
			continue
		}
		val := v.Field(i).String()
		for _, s := range sentinels {
			if strings.Contains(val, s) {
				t.Fatalf("field %s leaks raw prompt content %q", tp.Field(i).Name, s)
			}
		}
	}

	// 2. Whole-struct serialization must not contain raw prompt content.
	blob, err := json.Marshal(pf)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, s := range sentinels {
		if strings.Contains(string(blob), s) {
			t.Fatalf("serialized PromptFeatures leaks raw prompt content %q: %s", s, blob)
		}
	}

	// 3. Structural guard: the only string-kind fields allowed on
	// PromptFeatures are the SHA-256 digest and the Confidence enum.
	// A new string field is a privacy-review event, not a routine change.
	allowed := map[string]bool{"SHA256Hex": true, "TokenConfidence": true}
	for i := 0; i < tp.NumField(); i++ {
		if tp.Field(i).Type.Kind() == reflect.String && !allowed[tp.Field(i).Name] {
			t.Fatalf("unexpected string-kind field %q on PromptFeatures; string fields can carry raw prompt text and require privacy review (Constitution §7)", tp.Field(i).Name)
		}
	}
}

func TestPromptFeaturesHashAndSizes(t *testing.T) {
	prompt := "fix the bug in internal/auth/session.go"
	pf := ExtractPromptFeatures(prompt)

	want := sha256.Sum256([]byte(prompt))
	if pf.SHA256Hex != hex.EncodeToString(want[:]) {
		t.Fatalf("SHA256Hex = %q, want digest of raw prompt", pf.SHA256Hex)
	}
	if pf.ByteLength != len(prompt) {
		t.Fatalf("ByteLength = %d, want %d", pf.ByteLength, len(prompt))
	}
	if pf.RuneCount != len([]rune(prompt)) {
		t.Fatalf("RuneCount = %d, want %d", pf.RuneCount, len([]rune(prompt)))
	}
	if pf.LineCount != 1 {
		t.Fatalf("LineCount = %d, want 1", pf.LineCount)
	}
	if pf.ExplicitPathCount != 1 {
		t.Fatalf("ExplicitPathCount = %d, want 1", pf.ExplicitPathCount)
	}
	if !pf.HasFixVerb {
		t.Fatal("HasFixVerb = false, want true")
	}
}

// TestPromptFeaturesTokenApproximation checks the ADD §14.7 formula on
// inputs with a known category decomposition, plus non-negativity and the
// always-low confidence label (an estimate is never exact).
func TestPromptFeaturesTokenApproximation(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
		want   int
	}{
		{"empty", "", 0},
		// 8 ASCII alphanumerics -> ceil(8/4) = 2
		{"ascii only", "abcdefgh", 2},
		// 3 CJK runes -> ceil(3/1.5) = 2
		{"cjk only", "你好嗎", 2},
		// 3 ASCII punctuation -> ceil(3/3) = 1
		{"punct only", "!?.", 1},
		// whitespace is not counted
		{"whitespace only", " \n\t  ", 0},
		// 4 alnum (ceil 1) + 2 CJK (ceil 2) + 1 punct (ceil 1) = 4
		{"mixed", "ab12 你好!", 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pf := ExtractPromptFeatures(c.prompt)
			if pf.ApproxTokens != c.want {
				t.Fatalf("ApproxTokens(%q) = %d, want %d", c.prompt, pf.ApproxTokens, c.want)
			}
			if pf.ApproxTokens < 0 {
				t.Fatalf("ApproxTokens negative: %d", pf.ApproxTokens)
			}
			if pf.TokenConfidence != domain.ConfidenceLow {
				t.Fatalf("TokenConfidence = %q, want %q (approximation is never exact)", pf.TokenConfidence, domain.ConfidenceLow)
			}
		})
	}
}

func TestPromptFeaturesDeterministic(t *testing.T) {
	prompt := "Refactor internal/policy across layers.\n- [ ] add tests\n- update docs\n1. migrate schema"
	a := ExtractPromptFeatures(prompt)
	b := ExtractPromptFeatures(prompt)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("extraction is not deterministic:\n%+v\n%+v", a, b)
	}
}

func TestPromptFeaturesStructureSignals(t *testing.T) {
	prompt := "Implement feature X\n- [ ] acceptance one\n- [x] acceptance two\n- plain bullet\n1. numbered item\n2) other numbered"
	pf := ExtractPromptFeatures(prompt)
	if pf.AcceptanceCriteriaCount != 2 {
		t.Fatalf("AcceptanceCriteriaCount = %d, want 2", pf.AcceptanceCriteriaCount)
	}
	if pf.ListItemCount != 5 {
		t.Fatalf("ListItemCount = %d, want 5", pf.ListItemCount)
	}
	if !pf.HasImplementVerb {
		t.Fatal("HasImplementVerb = false, want true")
	}
}

// referenceExtractPromptFeatures is the PRE-#51 extraction logic preserved
// verbatim as a behavioral oracle: the full-rune-slice RuneCount, the
// strings.Split line pass, the strings.Fields path pass, the wordSet map of
// EVERY distinct word, and the strings.Fields(lowered) first-word check. It
// shares the production word-list slices (fixVerbWords etc.) so the ONLY
// thing under test is the scan MECHANISM, not the vocabulary content.
// TestExtractPromptFeatures_MatchesReference asserts the fused
// ExtractPromptFeatures produces byte-identical output to this for a corpus
// of edge cases (BINDING: #51 must be behavior-preserving).
func referenceExtractPromptFeatures(raw string) PromptFeatures {
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
	ws := make(map[string]struct{})
	for _, w := range strings.FieldsFunc(lowered, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		ws[w] = struct{}{}
	}
	has := func(list []string) bool {
		for _, w := range list {
			if _, ok := ws[w]; ok {
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
	fields := strings.Fields(lowered)
	firstWord := func(cands ...string) bool {
		if len(fields) == 0 {
			return false
		}
		first := strings.Trim(fields[0], ",.!?:;")
		for _, c := range cands {
			if first == c {
				return true
			}
		}
		return false
	}
	pf.HasFixVerb = has(fixVerbWords)
	pf.HasImplementVerb = has(implementVerbWords)
	pf.HasRefactorVerb = has(refactorVerbWords)
	pf.HasInvestigateVerb = has(investigateVerbWords)
	pf.HasMigrateVerb = has(migrateVerbWords)
	pf.MentionsTests = has(testsWords) || phrase("unit test", "integration test")
	pf.MentionsSchemaOrAPI = has(schemaAPIWords)
	pf.MentionsSecurity = has(securityWords)
	pf.MentionsPerformance = has(performanceWords)
	pf.MentionsDocumentation = has(documentationWords)
	pf.LongDocumentIndicator = has(longDocPrimaryWords) ||
		(has(longDocSectionWords) && pf.MentionsDocumentation) ||
		phrase("design document", "architecture document", "design doc")
	pf.QuestionIndicator = strings.Contains(raw, "?") || firstWord("what", "why", "how", "where", "when", "which", "who", "does", "is", "are", "can", "should")
	pf.OpenEndedIndicator = has(openEndedWords) || phrase("clean up", "make it better")
	pf.CrossLayerIndicator = phrase("cross-layer", "end-to-end", "e2e", "full-stack", "frontend and backend") || has(crossLayerWords)
	pf.RepositoryWideIndicator = has(repoWideWords) || phrase("repository-wide", "repo-wide", "entire repo", "whole repo", "all files", "every file")
	return pf
}

// TestExtractPromptFeatures_MatchesReference is the #51 behavior-preservation
// proof: the fused single-pass extractor must yield byte-identical
// PromptFeatures to the pre-#51 multi-pass logic for every input, especially
// the edge cases where a hand-rolled scan can drift from the standard-library
// one — CRLF and trailing/blank lines (Split semantics), consecutive and
// leading whitespace (Fields semantics), a vocab token at the very end with
// no trailing separator, CJK/multibyte runes, first-word punctuation, and
// invalid UTF-8.
func TestExtractPromptFeatures_MatchesReference(t *testing.T) {
	corpus := []string{
		"",
		"   \n\t  ",
		"fix",
		"api",
		"the api", // vocab token flush at end, no trailing separator
		"fix the bug in internal/auth/session.go", // path + fix verb
		"refactor\n",         // trailing newline (Split yields "")
		"a\n\n\nb",           // consecutive blank lines
		"fix    the   bug",   // consecutive spaces (Fields collapse)
		"   what is this?",   // leading whitespace + first-word question
		"why, is this slow?", // first word with trailing punctuation
		"Refactor internal/policy across layers.\n- [ ] add tests\n- update docs\n1. migrate schema",
		"Implement feature X\n- [ ] acceptance one\n- [x] acceptance two\n- plain bullet\n1. numbered item\n2) other numbered",
		"修復 the bug 密碼 optimize performance",           // CJK interleaved with vocab
		"why?! debug... the-api and the schema",        // punctuation-heavy separators
		"internal/a.go internal/b.go pkg/c.ts main.py", // path-heavy
		"See the design document and the architecture document for the whole repo audit",
		"CLEAN UP and MAKE IT BETTER across frontend and backend end-to-end", // uppercase + phrases
		"please review and understand the codebase everywhere",
		"a\r\nfix the bug\r\n- [ ] windows line\r\n", // CRLF (windows-portable)
		"valid \xff\xfe invalid utf8 bytes fix",      // invalid UTF-8
		benchLogPrompt[:50000],                       // a slice of the multi-MB log prompt
	}
	for i, raw := range corpus {
		got := ExtractPromptFeatures(raw)
		want := referenceExtractPromptFeatures(raw)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("case %d: fused output differs from reference\n got: %+v\nwant: %+v\ninput: %q", i, got, want, raw)
		}
	}
}

// benchLogPrompt is a multi-MB pasted-log-style prompt — the worst case #51
// targets: a user pasting a large log or diff, which
// NewUserPromptSubmitEvent runs ExtractPromptFeatures over SYNCHRONOUSLY on
// the UserPromptSubmit hook path before every prompt. It is built once so
// BenchmarkExtractPromptFeatures measures extraction, not string assembly.
var benchLogPrompt = strings.Repeat(
	"2026-07-14T12:00:00.123Z INFO  internal/evaluation/service.go:214 "+
		"forecast token_p50=1234 refactor the schema and fix the bug in "+
		"internal/auth/session.go please add tests and migrate the api\n",
	20000, // ~140 bytes/line * 20000 ≈ 2.8 MB
)

// BenchmarkExtractPromptFeatures pins the #51 hot-path cost on a multi-MB
// prompt. Report -benchmem: the fused implementation must show far fewer
// allocations and bytes/op than the pre-#51 version, which materialized a
// full rune slice, a map of EVERY distinct word, and full field/line slices.
func BenchmarkExtractPromptFeatures(b *testing.B) {
	b.SetBytes(int64(len(benchLogPrompt)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ExtractPromptFeatures(benchLogPrompt)
	}
}
