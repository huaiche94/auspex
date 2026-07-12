package features

// TaskClass is the fixed task taxonomy from ADD §14.3. TaskClassUnknown is
// a first-class, expected value — the classifier returns it whenever signal
// is insufficient instead of guessing (predictor cold-start contract).
type TaskClass string

const (
	TaskClassQuestion                 TaskClass = "question"
	TaskClassInspection               TaskClass = "inspection"
	TaskClassDocumentationShort       TaskClass = "documentation-short"
	TaskClassDocumentationLong        TaskClass = "documentation-long"
	TaskClassTestOnly                 TaskClass = "test-only"
	TaskClassBugfixLocal              TaskClass = "bugfix-local"
	TaskClassBugfixCrossLayer         TaskClass = "bugfix-cross-layer"
	TaskClassFeatureLocal             TaskClass = "feature-local"
	TaskClassFeatureCrossLayer        TaskClass = "feature-cross-layer"
	TaskClassRefactorLocal            TaskClass = "refactor-local"
	TaskClassRefactorWide             TaskClass = "refactor-wide"
	TaskClassMigration                TaskClass = "migration"
	TaskClassSecuritySensitive        TaskClass = "security-sensitive"
	TaskClassPerformanceInvestigation TaskClass = "performance-investigation"
	TaskClassRepositoryWide           TaskClass = "repository-wide"
	TaskClassUnknown                  TaskClass = "unknown"
)

// AllTaskClasses lists every valid wire value, in ADD §14.3 order.
func AllTaskClasses() []TaskClass {
	return []TaskClass{
		TaskClassQuestion,
		TaskClassInspection,
		TaskClassDocumentationShort,
		TaskClassDocumentationLong,
		TaskClassTestOnly,
		TaskClassBugfixLocal,
		TaskClassBugfixCrossLayer,
		TaskClassFeatureLocal,
		TaskClassFeatureCrossLayer,
		TaskClassRefactorLocal,
		TaskClassRefactorWide,
		TaskClassMigration,
		TaskClassSecuritySensitive,
		TaskClassPerformanceInvestigation,
		TaskClassRepositoryWide,
		TaskClassUnknown,
	}
}

// Valid reports whether c is one of the fixed ADD §14.3 values.
func (c TaskClass) Valid() bool {
	for _, k := range AllTaskClasses() {
		if c == k {
			return true
		}
	}
	return false
}
