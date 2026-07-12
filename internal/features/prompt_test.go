package features

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/huaiche94/preflight/internal/domain"
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
