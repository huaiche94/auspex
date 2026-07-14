package features

import (
	"reflect"
	"testing"

	"github.com/huaiche94/auspex/internal/domain"
)

// TestClassifierConfidentClassification exercises prompts with enough
// signal that ClassifyTask should commit to a specific, non-unknown class.
func TestClassifierConfidentClassification(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
		want   TaskClass
	}{
		{
			name:   "fix verb -> bugfix-local",
			prompt: "fix the null pointer bug in internal/session/store.go",
			want:   TaskClassBugfixLocal,
		},
		{
			name:   "fix verb + cross-layer -> bugfix-cross-layer",
			prompt: "fix the end-to-end bug that spans frontend and backend",
			want:   TaskClassBugfixCrossLayer,
		},
		{
			name:   "implement verb -> feature-local",
			prompt: "implement a new caching layer for the session store",
			want:   TaskClassFeatureLocal,
		},
		{
			name:   "security keyword dominates -> security-sensitive",
			prompt: "implement a fix for the authentication vulnerability and sanitize inputs",
			want:   TaskClassSecuritySensitive,
		},
		{
			name:   "migrate verb -> migration",
			prompt: "migrate the schema to the new format and upgrade dependent tables",
			want:   TaskClassMigration,
		},
		{
			name:   "long document indicator -> documentation-long",
			prompt: "write a design document with several chapters and sections describing the architecture",
			want:   TaskClassDocumentationLong,
		},
		{
			name:   "question with no verb -> question",
			prompt: "does the session store evict entries early?",
			want:   TaskClassQuestion,
		},
		{
			name:   "investigate verb -> inspection",
			prompt: "investigate why the retry loop spins forever",
			want:   TaskClassInspection,
		},
		// Issue #42 acceptance examples: ordinary prompts must not
		// collapse to unknown once real derived features reach the
		// classifier.
		{
			name:   "issue #42 acceptance: fix typo in README -> bugfix-local",
			prompt: "fix typo in README",
			want:   TaskClassBugfixLocal,
		},
		{
			name:   "issue #42 acceptance: refactor the policy engine -> refactor-local",
			prompt: "refactor the policy engine",
			want:   TaskClassRefactorLocal,
		},
		// Issue #42 widened vocabulary: everyday synonyms mapped onto the
		// classes that already existed for their signal slot.
		{
			name:   "typo/broken vocabulary -> bugfix-local",
			prompt: "the exporter is broken and crashes on startup",
			want:   TaskClassBugfixLocal,
		},
		{
			name:   "consolidate vocabulary -> refactor-local",
			prompt: "consolidate the duplicated helpers into one package",
			want:   TaskClassRefactorLocal,
		},
		{
			name:   "troubleshoot vocabulary -> inspection",
			prompt: "troubleshoot the startup sequence in the runner",
			want:   TaskClassInspection,
		},
		{
			name:   "coverage vocabulary without impl verbs -> test-only",
			prompt: "improve coverage for the parser package",
			want:   TaskClassTestOnly,
		},
		{
			name:   "tutorial vocabulary -> documentation-short",
			prompt: "write a tutorial for the CLI onboarding flow",
			want:   TaskClassDocumentationShort,
		},
		{
			name:   "performance indicator without actionable verb -> performance-investigation",
			prompt: "optimize the query planner, it feels slow",
			want:   TaskClassPerformanceInvestigation,
		},
		// Issue #49: an action verb (fix/refactor/implement) co-occurring
		// with an investigate verb predicts the WORKLOAD — the action class
		// must win, not inspection. These are the verb-collision cases the
		// #42 vocabulary widening ("review", "understand", "audit") shipped
		// without coverage.
		{
			name:   "issue #49: review + refactor -> refactor-local, not inspection",
			prompt: "review and refactor the policy engine",
			want:   TaskClassRefactorLocal,
		},
		{
			name:   "issue #49: fix + review -> bugfix-local, not inspection",
			prompt: "fix the login bug, then review the diff with me",
			want:   TaskClassBugfixLocal,
		},
		{
			name:   "issue #49: implement + understand -> feature-local, not inspection",
			prompt: "implement the cache layer and help me understand the eviction flow",
			want:   TaskClassFeatureLocal,
		},
		{
			name:   "issue #49: investigate + performance with no action verb keeps performance-investigation",
			prompt: "review the slow dashboard queries",
			want:   TaskClassPerformanceInvestigation,
		},
		{
			name:   "issue #49: pure investigate verb still inspection",
			prompt: "audit the dependency graph of the scheduler",
			want:   TaskClassInspection,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := ClassifierInput{Prompt: ExtractPromptFeatures(c.prompt)}
			got := ClassifyTask(in)
			if got.Class != c.want {
				t.Fatalf("ClassifyTask(%q) class = %q, want %q (reasons=%v)", c.prompt, got.Class, c.want, got.ReasonCodes)
			}
			if got.Class == TaskClassUnknown {
				t.Fatalf("expected a confident classification, got unknown")
			}
			if len(got.ReasonCodes) == 0 {
				t.Fatalf("confident classification must carry at least one reason code")
			}
			if got.Confidence == domain.ConfidenceUnavailable {
				t.Fatalf("confident classification must not report ConfidenceUnavailable")
			}
		})
	}
}

// TestClassifierReturnsUnknownWithInsufficientSignal is the cold-start-safe
// assertion: when there isn't enough signal to commit to a class, the
// classifier must return TaskClassUnknown explicitly rather than guessing.
func TestClassifierReturnsUnknownWithInsufficientSignal(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
	}{
		{name: "empty prompt", prompt: ""},
		{name: "single word, no verb or indicator", prompt: "hello"},
		{name: "very short prompt", prompt: "ok"},
		{name: "neutral filler with no actionable signal", prompt: "please and thank you very much indeed"},
		// Issue #42: generic edit verbs ("update", "change", "remove")
		// are deliberately NOT mapped to a class — picking one would be
		// an ungrounded modeling decision (an "update" is equally
		// plausibly a bugfix, a feature, or a migration), so they stay
		// unknown by design. See ExtractPromptFeatures' vocabulary
		// comment.
		{name: "generic update verb stays unknown by design", prompt: "update the dependency to the latest release"},
		// Issue #49: "regression" left the performance vocabulary — in a dev
		// context it names a reappeared bug, not a perf drop, and the
		// verb-less rescue rule was routing bug reports to
		// performance-investigation.
		{name: "bare regression report stays unknown", prompt: "there was a regression after the last release"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := ClassifierInput{Prompt: ExtractPromptFeatures(c.prompt)}
			got := ClassifyTask(in)
			if got.Class != TaskClassUnknown {
				t.Fatalf("ClassifyTask(%q) class = %q, want %q (must not guess)", c.prompt, got.Class, TaskClassUnknown)
			}
			if got.Confidence != domain.ConfidenceUnavailable {
				t.Fatalf("ClassifyTask(%q) confidence = %q, want %q", c.prompt, got.Confidence, domain.ConfidenceUnavailable)
			}
			if len(got.ReasonCodes) == 0 {
				t.Fatalf("unknown classification must still carry a reason code explaining why")
			}
		})
	}
}

func TestClassifierDeterministic(t *testing.T) {
	prompt := "refactor the payment module across layers and add tests"
	in := ClassifierInput{Prompt: ExtractPromptFeatures(prompt)}
	a := ClassifyTask(in)
	b := ClassifyTask(in)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("ClassifyTask is not deterministic: %+v vs %+v", a, b)
	}
}

func TestClassifierProgressDocumentSectionHint(t *testing.T) {
	prompt := "write documentation for this section"
	nodeKind := domain.NodeDocumentSection
	in := ClassifierInput{
		Prompt: ExtractPromptFeatures(prompt),
		Progress: &ProgressFeatures{
			CurrentNodeKind:   nodeKind,
			IsDocumentSection: true,
		},
	}
	got := ClassifyTask(in)
	if got.Class != TaskClassDocumentationLong {
		t.Fatalf("ClassifyTask with document-section progress hint = %q, want %q", got.Class, TaskClassDocumentationLong)
	}
}

func TestAllTaskClassesValid(t *testing.T) {
	for _, c := range AllTaskClasses() {
		if !c.Valid() {
			t.Fatalf("class %q reported invalid by its own Valid()", c)
		}
	}
	if TaskClass("not-a-real-class").Valid() {
		t.Fatal("bogus class reported valid")
	}
}
