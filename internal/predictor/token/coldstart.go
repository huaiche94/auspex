package token

import "github.com/huaiche94/auspex/internal/features"

// baseTurnTokens is the cold-start "typical turn" token cost before any
// task-class relative multiplier is applied. ADD §14.6's table gives a
// *relative* multiplier per class (e.g. bugfix-local = 1.0), not an
// absolute token count, so this package anchors that relative scale to a
// single bootstrap absolute value. 6000 tokens approximates a small/medium
// local turn (a few files of exploration + a modest edit + one
// verification pass) under §15.1's decomposition; like every other number
// in the §14.6 table, it is a bootstrap starting point, not a measured
// universal benchmark, and is expected to be replaced by empirical
// per-deployment quantiles once >=8 similar-turn samples exist (§15.2).
const baseTurnTokens = 6000.0

// coldStartMultiplier is the ADD §14.6 "relative token multiplier" column,
// verbatim, keyed by task class.
var coldStartMultiplier = map[features.TaskClass]float64{
	features.TaskClassDocumentationShort: 0.6,
	features.TaskClassDocumentationLong:  1.8,
	features.TaskClassBugfixLocal:        1.0,
	features.TaskClassFeatureLocal:       1.4,
	features.TaskClassFeatureCrossLayer:  2.0,
	features.TaskClassRefactorWide:       2.8,
	features.TaskClassMigration:          2.5,
	features.TaskClassRepositoryWide:     3.5,
}

// coldStartMultiplierFallback covers the §14.3 task classes ADD §14.6's
// table does not name, using the same nearest-neighbor mapping rationale
// already documented in internal/predictor/scope/coldstart.go (kept
// independent here rather than importing across packages, since the two
// tables measure different quantities — files/lines vs. relative token
// cost — and a future change to one must not silently ripple into the
// other):
//   - question, unknown: minimal turn, mostly a short answer -> well below bugfix-local
//   - inspection, performance-investigation: read-heavy, low edit cost -> below bugfix-local
//   - test-only: bounded to test files -> below bugfix-local
//   - bugfix-cross-layer: wider than bugfix-local, narrower than feature-cross-layer
//   - refactor-local: local, non-wide -> between feature-local and refactor-wide
//   - security-sensitive: careful/localized but with extra verification overhead -> slightly above bugfix-local
var coldStartMultiplierFallback = map[features.TaskClass]float64{
	features.TaskClassQuestion:                 0.3,
	features.TaskClassUnknown:                  0.5,
	features.TaskClassInspection:               0.5,
	features.TaskClassPerformanceInvestigation: 0.7,
	features.TaskClassTestOnly:                 0.8,
	features.TaskClassBugfixCrossLayer:         1.6,
	features.TaskClassRefactorLocal:            1.7,
	features.TaskClassSecuritySensitive:        1.2,
}

// lookupColdStartMultiplier returns the ADD §14.6 relative token multiplier
// for class, preferring the table verbatim and falling back to the
// documented nearest-neighbor table above. Every TaskClass resolves to some
// value: an unrecognized class falls through to TaskClassUnknown's entry.
func lookupColdStartMultiplier(class features.TaskClass) float64 {
	if m, ok := coldStartMultiplier[class]; ok {
		return m
	}
	if m, ok := coldStartMultiplierFallback[class]; ok {
		return m
	}
	return coldStartMultiplierFallback[features.TaskClassUnknown]
}
