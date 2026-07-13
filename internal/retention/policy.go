// policy.go: the retention window (ADR-046 tier 1, "hot raw window").
package retention

import "time"

// DefaultRetentionDays is the hot-window default (ADR-046): raw rows
// younger than this many days are never touched by a retention pass.
const DefaultRetentionDays = 90

// Policy is the retention configuration for one pass. One window covers
// every table class; per-class overrides are deliberately not offered
// until a real need surfaces (ADR-046 "no speculative abstraction" —
// mirroring the README/ADD contribution rule ADR-044 cites).
type Policy struct {
	// RetentionDays is the hot-window length in whole days. Zero (the
	// Go zero value) means DefaultRetentionDays; negative values are
	// rejected by Engine.Run as a validation error rather than silently
	// clamped, since "-1 days" is a caller bug, not a preference.
	RetentionDays int
}

// Days returns the effective window length (RetentionDays, defaulted).
func (p Policy) Days() int {
	if p.RetentionDays == 0 {
		return DefaultRetentionDays
	}
	return p.RetentionDays
}

// Cutoff returns the UTC instant separating the hot window from expired
// data: rows strictly OLDER than this are candidates. A row exactly AT
// the cutoff is retained (strict <, verified by the boundary tests) so
// the rule is unambiguous rather than dependent on timestamp precision.
func (p Policy) Cutoff(now time.Time) time.Time {
	return now.UTC().AddDate(0, 0, -p.Days())
}
