// Package idgen provides the real implementation of internal/domain's
// IDGenerator interface. Per docs/implementation/vertical-slice/CONTRACT_FREEZE.md,
// all Auspex-owned entity IDs are UUIDv7 at generation time, generated
// here and never parsed for meaning by callers.
package idgen

import (
	"github.com/google/uuid"

	"github.com/huaiche94/auspex/internal/domain"
)

// UUIDv7 is the real IDGenerator implementation backed by
// github.com/google/uuid's UUIDv7 generator. It carries no state and is
// safe for concurrent use.
type UUIDv7 struct{}

// New returns an IDGenerator that produces UUIDv7 string IDs.
func New() domain.IDGenerator {
	return UUIDv7{}
}

// NewID returns a new, lowercase, hyphenated UUIDv7 string, per
// domain.IDGenerator. UUIDv7 is time-ordered, which keeps SQLite primary
// key/index locality reasonable for the storage layer built in later
// foundation nodes.
func (UUIDv7) NewID() string {
	id, err := uuid.NewV7()
	if err != nil {
		// uuid.NewV7 only errors if the runtime's crypto/rand source
		// fails, which is an unrecoverable environment fault, not a
		// condition callers can meaningfully handle via a returned
		// error (domain.IDGenerator.NewID has no error return, per the
		// frozen contract in internal/domain/clock.go). Fail loudly.
		panic("idgen: failed to generate UUIDv7: " + err.Error())
	}
	return id.String()
}
