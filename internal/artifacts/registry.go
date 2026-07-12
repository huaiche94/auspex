// registry.go: the optional custom validator interface (agents/checkpoint.md
// Part A deliverable #3's fifth item, "optional custom validator
// interface"). Registry lets a caller (a later role, or a future
// provider-specific acceptance criterion) plug in additional Validator
// implementations beyond this package's four built-ins, keyed by
// Validator.Kind() — the same string progress.AcceptanceCriterion.Kind and
// artifacts.validator_id already carry, so a registered custom validator
// slots into the existing acceptance-criterion vocabulary without any
// schema change.
package artifacts

import (
	"context"
	"fmt"
	"sync"
)

// Registry holds a set of Validators keyed by their Kind() and dispatches
// Validate calls to the matching one. It is safe for concurrent use.
type Registry struct {
	mu         sync.RWMutex
	validators map[string]Validator
}

// NewRegistry returns a Registry pre-populated with this package's four
// built-in validators (file_exists, checksum_matches, heading_exists,
// fence_balance), so a caller gets the standard set for free and only
// needs to call Register for anything additional.
func NewRegistry() *Registry {
	r := &Registry{validators: make(map[string]Validator)}
	for _, v := range []Validator{
		FileExistsValidator{},
		ChecksumMatchesValidator{},
		HeadingExistsValidator{},
		FenceBalanceValidator{},
	} {
		r.mustRegister(v)
	}
	return r
}

// Register adds v to the registry under v.Kind(). Registering a second
// Validator under an already-used Kind is a caller error (it would make
// dispatch ambiguous and silently shadow the previous validator) and
// returns an error rather than overwriting silently.
func (r *Registry) Register(v Validator) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.validators[v.Kind()]; exists {
		return fmt.Errorf("artifacts: validator kind %q already registered", v.Kind())
	}
	r.validators[v.Kind()] = v
	return nil
}

// mustRegister is Register without the error return, used only for this
// package's own known-unique built-in Kind() values at construction time.
func (r *Registry) mustRegister(v Validator) {
	if err := r.Register(v); err != nil {
		panic(err) // unreachable: NewRegistry's built-in kinds are distinct by construction
	}
}

// Lookup returns the Validator registered under kind, or false if none is.
func (r *Registry) Lookup(kind string) (Validator, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.validators[kind]
	return v, ok
}

// Validate dispatches to the Validator registered under kind. An unknown
// kind is a validation failure (Result, not an error) — an acceptance
// criterion naming a validator this registry doesn't know about must be
// treated the same as any other failed check (evidence not established),
// not crash the caller.
func (r *Registry) Validate(ctx context.Context, kind string, candidate Candidate) (Result, error) {
	v, ok := r.Lookup(kind)
	if !ok {
		return Failed(fmt.Sprintf("artifacts: unknown validator kind %q", kind)), nil
	}
	return v.Validate(ctx, candidate)
}
