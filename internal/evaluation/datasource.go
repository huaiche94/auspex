package evaluation

import (
	"github.com/huaiche94/auspex/internal/app"
)

// DataSource resolves every input EvaluateTurn's pipeline needs beyond what
// the frozen app.EvaluateTurnRequest itself carries (CONTRACT_FREEZE.md's
// privacy contract: no raw prompt text ever reaches this package, only its
// hash).
//
// History: this began as a package-local interface because Bootstrap
// deliberately deferred a repository/session/progress feature lookup port
// ("What Bootstrap did NOT freeze"). ADR-044 (closing wave2-analysis
// REC-01) promoted the shape verbatim into the frozen contract as
// app.FeatureDataSource; these aliases keep every existing consumer,
// implementation (SQLDataSource), and test compiling unchanged while the
// canonical definition now lives in internal/app/ports.go.
//
// A zero-value/ok=false return from any method means "not available yet"
// (cold-start), not an error — EvaluateTurn degrades to the same
// Confidence/Calibrated discipline every pipeline stage already uses for a
// missing input, per ADD principle 1 ("unknown is not zero").
type DataSource = app.FeatureDataSource

// ResolvedSession is DataSource.Resolve's return value.
type ResolvedSession = app.ResolvedSession
