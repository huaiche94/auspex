// Package clock provides the real, wall-clock implementation of
// internal/domain's Clock interface. Production code depends on
// domain.Clock, not this package directly, so tests can substitute a fake.
package clock

import (
	"time"

	"github.com/huaiche94/preflight/internal/domain"
)

// System is the real Clock implementation backed by time.Now(). It carries
// no state and is safe for concurrent use.
type System struct{}

// New returns a Clock backed by the system wall clock.
func New() domain.Clock {
	return System{}
}

// Now returns the current local time, per domain.Clock.
func (System) Now() time.Time {
	return time.Now()
}
