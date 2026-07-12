package features

import "github.com/huaiche94/preflight/internal/domain"

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
	case pf.MentionsDocumentation && !pf.HasFixVerb && !pf.HasRefactorVerb:
		return classified(TaskClassDocumentationShort, ReasonDocIndicator)
	case pf.MentionsTests && !pf.HasFixVerb && !pf.HasRefactorVerb && !pf.HasImplementVerb:
		return classified(TaskClassTestOnly, ReasonTestOnlyIndicator)
	case pf.HasInvestigateVerb && pf.MentionsPerformance:
		return classified(TaskClassPerformanceInvestigation, ReasonInvestigateVerb, ReasonPerformanceIndicator)
	case pf.HasInvestigateVerb:
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
	default:
		// Weak indicator combinations with no actionable verb: do not guess.
		return Classification{
			Class:       TaskClassUnknown,
			Confidence:  domain.ConfidenceUnavailable,
			ReasonCodes: []string{ReasonInsufficientSignal},
		}
	}
}
