package features

import "github.com/huaiche94/auspex/internal/domain"

// Classification is the classifier's explainable output. Class is always a
// valid ADD §14.3 value; ReasonCodes records which signals produced it.
// Confidence is a measurement-quality label (domain.Confidence), never a
// probability (Constitution §7 rule 7).
type Classification struct {
	Class       TaskClass
	Confidence  domain.Confidence
	ReasonCodes []string
}

// Classifier reason codes (stable wire strings; golden-tested).
const (
	ReasonInsufficientSignal   = "insufficient_signal"
	ReasonPromptTooShort       = "prompt_too_short"
	ReasonSecurityIndicator    = "security_indicator"
	ReasonMigrationVerb        = "migration_verb"
	ReasonLongDocIndicator     = "long_document_indicator"
	ReasonDocIndicator         = "documentation_indicator"
	ReasonTestOnlyIndicator    = "test_only_indicator"
	ReasonPerformanceIndicator = "performance_indicator"
	ReasonInvestigateVerb      = "investigate_verb"
	ReasonQuestionIndicator    = "question_indicator"
	ReasonFixVerb              = "fix_verb"
	ReasonRefactorVerb         = "refactor_verb"
	ReasonImplementVerb        = "implement_verb"
	ReasonCrossLayerIndicator  = "cross_layer_indicator"
	ReasonRepositoryWide       = "repository_wide_indicator"
	ReasonDocumentSectionNode  = "document_section_node"
)

// ClassifyTask maps a ClassifierInput onto the ADD §14.3 taxonomy with a
// fixed rule precedence, so identical input always yields identical
// output. It operates on derived features only — raw prompt text never
// reaches the classifier (it isn't a field on PromptFeatures at all).
//
// Cold-start contract: when signal is insufficient it returns an explicit
// TaskClassUnknown with ConfidenceUnavailable — it never guesses a class.
func ClassifyTask(in ClassifierInput) Classification {
	pf := in.Prompt

	if pf.ApproxTokens < 2 {
		return Classification{
			Class:       TaskClassUnknown,
			Confidence:  domain.ConfidenceUnavailable,
			ReasonCodes: []string{ReasonPromptTooShort},
		}
	}

	hasVerb := pf.HasFixVerb || pf.HasImplementVerb || pf.HasRefactorVerb ||
		pf.HasInvestigateVerb || pf.HasMigrateVerb
	// hasActionVerb gates the investigate cases below (#49): fix/refactor/
	// implement predict the WORKLOAD, investigate only the framing — "review
	// and refactor the policy engine" is a refactor someone asked to be
	// careful about, not an inspection. Without this guard, #42's widened
	// investigate vocabulary ("review", "understand", "audit" — everyday
	// words inside action prompts) shadowed every action class and pushed
	// the heaviest turns onto inspection's smallest cold-start multiplier:
	// the worst under-forecast direction for a runway guard.
	hasActionVerb := pf.HasFixVerb || pf.HasImplementVerb || pf.HasRefactorVerb
	hasIndicator := pf.MentionsTests || pf.MentionsSecurity || pf.MentionsPerformance ||
		pf.MentionsDocumentation || pf.LongDocumentIndicator || pf.QuestionIndicator ||
		pf.RepositoryWideIndicator
	progressSaysDocSection := in.Progress != nil && in.Progress.IsDocumentSection

	if !hasVerb && !hasIndicator && !progressSaysDocSection {
		return Classification{
			Class:       TaskClassUnknown,
			Confidence:  domain.ConfidenceUnavailable,
			ReasonCodes: []string{ReasonInsufficientSignal},
		}
	}

	classified := func(class TaskClass, reasons ...string) Classification {
		return Classification{
			Class:       class,
			Confidence:  domain.ConfidenceLow, // day-one heuristic, never higher
			ReasonCodes: reasons,
		}
	}

	// Fixed precedence: the most consequential signal wins.
	switch {
	case pf.MentionsSecurity:
		return classified(TaskClassSecuritySensitive, ReasonSecurityIndicator)
	case pf.HasMigrateVerb:
		return classified(TaskClassMigration, ReasonMigrationVerb)
	case pf.LongDocumentIndicator:
		return classified(TaskClassDocumentationLong, ReasonLongDocIndicator)
	case progressSaysDocSection && pf.MentionsDocumentation:
		return classified(TaskClassDocumentationLong, ReasonDocumentSectionNode, ReasonDocIndicator)
	// Deliberately NOT guarded on HasImplementVerb (#49 finding 2, kept as
	// designed): "write a tutorial" / "add a README" are documentation work
	// phrased with implement verbs, and docs-beats-implement is pinned by
	// the #42 acceptance tests. The known cost is the rarer inverse ("add a
	// guide generator" — code work about docs) under-forecasting as
	// documentation-short; a boolean feature set cannot split these two, so
	// revisit only with calibration data (#11), not another vocabulary rule.
	case pf.MentionsDocumentation && !pf.HasFixVerb && !pf.HasRefactorVerb:
		return classified(TaskClassDocumentationShort, ReasonDocIndicator)
	case pf.MentionsTests && !pf.HasFixVerb && !pf.HasRefactorVerb && !pf.HasImplementVerb:
		return classified(TaskClassTestOnly, ReasonTestOnlyIndicator)
	case pf.HasInvestigateVerb && pf.MentionsPerformance && !hasActionVerb:
		return classified(TaskClassPerformanceInvestigation, ReasonInvestigateVerb, ReasonPerformanceIndicator)
	case pf.HasInvestigateVerb && !hasActionVerb:
		return classified(TaskClassInspection, ReasonInvestigateVerb)
	case pf.QuestionIndicator && !hasVerb:
		return classified(TaskClassQuestion, ReasonQuestionIndicator)
	case pf.HasFixVerb && pf.CrossLayerIndicator:
		return classified(TaskClassBugfixCrossLayer, ReasonFixVerb, ReasonCrossLayerIndicator)
	case pf.HasFixVerb:
		return classified(TaskClassBugfixLocal, ReasonFixVerb)
	case pf.HasRefactorVerb && pf.RepositoryWideIndicator:
		return classified(TaskClassRefactorWide, ReasonRefactorVerb, ReasonRepositoryWide)
	case pf.HasRefactorVerb:
		return classified(TaskClassRefactorLocal, ReasonRefactorVerb)
	case pf.HasImplementVerb && pf.CrossLayerIndicator:
		return classified(TaskClassFeatureCrossLayer, ReasonImplementVerb, ReasonCrossLayerIndicator)
	case pf.HasImplementVerb:
		return classified(TaskClassFeatureLocal, ReasonImplementVerb)
	case pf.RepositoryWideIndicator:
		return classified(TaskClassRepositoryWide, ReasonRepositoryWide)
	case pf.MentionsPerformance:
		// Issue #42 (fix path step 3): a performance-flavored prompt with
		// no actionable §14.2 verb ("the dashboard is slow", "optimize
		// the query planner") previously fell through to Unknown even
		// though the §14.3 taxonomy already has exactly one performance
		// class with a designed cold-start multiplier
		// (internal/predictor/token/coldstart.go). Placed after every
		// verb case and after repository-wide, so it only rescues prompts
		// no more-specific rule already classifies — it never re-routes a
		// prompt away from a verb-based class.
		return classified(TaskClassPerformanceInvestigation, ReasonPerformanceIndicator)
	default:
		// Weak indicator combinations with no actionable verb: do not guess.
		return Classification{
			Class:       TaskClassUnknown,
			Confidence:  domain.ConfidenceUnavailable,
			ReasonCodes: []string{ReasonInsufficientSignal},
		}
	}
}
