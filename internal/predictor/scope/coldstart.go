package scope

import "github.com/huaiche94/auspex/internal/features"

// coldStartDefault is one row of the ADD §14.6 bootstrap table: a per-task-class
// files-changed and lines-changed P50/P90 pair. These are explicitly *not*
// a universal benchmark (ADD §14.6: "這些是 bootstrap，不得宣稱為 universal
// benchmark") — they exist so a brand-new deployment with zero session
// history can still produce a bounded, explainable estimate instead of
// refusing to answer.
type coldStartDefault struct {
	FilesChangedP50 int64
	FilesChangedP90 int64
	LinesChangedP50 int64
	LinesChangedP90 int64
}

// coldStartDefaults is the ADD §14.6 table verbatim. Task classes not
// listed here (question, inspection, test-only, bugfix-cross-layer,
// refactor-local, performance-investigation, security-sensitive, unknown)
// fall back to coldStartFallback — the table only names 8 of the 16 §14.3
// classes, and the ADD gives no fallback rule beyond "bootstrap defaults",
// so this implementation picks the nearest-neighbor class deliberately
// (documented per mapping below) rather than inventing new numbers.
var coldStartDefaults = map[features.TaskClass]coldStartDefault{
	features.TaskClassDocumentationShort: {FilesChangedP50: 1, FilesChangedP90: 4, LinesChangedP50: 30, LinesChangedP90: 180},
	features.TaskClassDocumentationLong:  {FilesChangedP50: 3, FilesChangedP90: 12, LinesChangedP50: 500, LinesChangedP90: 5000},
	features.TaskClassBugfixLocal:        {FilesChangedP50: 2, FilesChangedP90: 6, LinesChangedP50: 70, LinesChangedP90: 280},
	features.TaskClassFeatureLocal:       {FilesChangedP50: 4, FilesChangedP90: 10, LinesChangedP50: 180, LinesChangedP90: 650},
	features.TaskClassFeatureCrossLayer:  {FilesChangedP50: 7, FilesChangedP90: 18, LinesChangedP50: 350, LinesChangedP90: 1400},
	features.TaskClassRefactorWide:       {FilesChangedP50: 12, FilesChangedP90: 35, LinesChangedP50: 700, LinesChangedP90: 3500},
	features.TaskClassMigration:          {FilesChangedP50: 8, FilesChangedP90: 24, LinesChangedP50: 450, LinesChangedP90: 2200},
	features.TaskClassRepositoryWide:     {FilesChangedP50: 20, FilesChangedP90: 60, LinesChangedP50: 1000, LinesChangedP90: 6000},
}

// coldStartFallback covers §14.3 classes the ADD §14.6 table does not name.
// Each maps to its nearest documented neighbor by scope shape, smallest
// first so an unrecognized/unknown class never over-estimates:
//   - question, unknown: no code change expected -> smallest possible (0/1 files, 0/10 lines)
//   - inspection, performance-investigation: read-heavy, near-zero write -> tiny (0/2 files, 0/40 lines)
//   - test-only: bounded to test files -> half of bugfix-local
//   - bugfix-cross-layer: bugfix-local's line/file shape but wider -> treat as feature-cross-layer's smaller sibling
//   - refactor-local: local, non-wide -> half of refactor-wide
//   - security-sensitive: treated like bugfix-local (localized, careful) per ADD's own security_sensitive flag
//     capturing the risk signal separately from scope magnitude
var coldStartFallback = map[features.TaskClass]coldStartDefault{
	features.TaskClassQuestion:                 {FilesChangedP50: 0, FilesChangedP90: 1, LinesChangedP50: 0, LinesChangedP90: 10},
	features.TaskClassUnknown:                  {FilesChangedP50: 0, FilesChangedP90: 1, LinesChangedP50: 0, LinesChangedP90: 10},
	features.TaskClassInspection:               {FilesChangedP50: 0, FilesChangedP90: 2, LinesChangedP50: 0, LinesChangedP90: 40},
	features.TaskClassPerformanceInvestigation: {FilesChangedP50: 0, FilesChangedP90: 2, LinesChangedP50: 0, LinesChangedP90: 40},
	features.TaskClassTestOnly:                 {FilesChangedP50: 1, FilesChangedP90: 3, LinesChangedP50: 35, LinesChangedP90: 140},
	features.TaskClassBugfixCrossLayer:         {FilesChangedP50: 4, FilesChangedP90: 10, LinesChangedP50: 150, LinesChangedP90: 600},
	features.TaskClassRefactorLocal:            {FilesChangedP50: 6, FilesChangedP90: 18, LinesChangedP50: 350, LinesChangedP90: 1750},
	features.TaskClassSecuritySensitive:        {FilesChangedP50: 2, FilesChangedP90: 6, LinesChangedP50: 70, LinesChangedP90: 280},
}

// lookupColdStart returns the cold-start default for class, preferring the
// ADD §14.6 table and falling back to the documented nearest-neighbor
// table above. Every TaskClass (including future additions) resolves to
// some entry: an unrecognized class falls through to the same bucket as
// TaskClassUnknown.
func lookupColdStart(class features.TaskClass) coldStartDefault {
	if d, ok := coldStartDefaults[class]; ok {
		return d
	}
	if d, ok := coldStartFallback[class]; ok {
		return d
	}
	return coldStartFallback[features.TaskClassUnknown]
}
