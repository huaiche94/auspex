// Package artifacts implements checkpoint role Part A's artifact validator
// layer (agents/checkpoint.md Part A deliverable #3): the concrete checks
// that turn a claimed piece of evidence into a verified one before
// internal/progress's ArtifactStore records it as validation_status=passed.
//
// "Completed means evidenced" (Constitution §6.2) is enforced here, not by
// trusting an agent's own claim: every validator in this package inspects
// the actual filesystem/content, never the caller's assertion about it.
//
// This package does not itself run inside CompleteNode's transaction
// (that orchestration is checkpoint-a04's job); it is the pure/IO-only
// validation seam that protocol calls into, mirroring
// internal/progress/statemachine.go's role for node transitions.
package artifacts

import (
	"context"
)

// Result is the outcome of running one validator against one candidate
// piece of evidence. A zero-value Result (Passed: false, no Reasons) is
// never returned by a validator in this package — Passed=false always
// carries at least one Reason, since a validator rejecting evidence with no
// explanation would defeat the point of validator-checked completion.
type Result struct {
	Passed  bool
	Reasons []string
}

// Failed builds a failing Result carrying one or more human-readable
// reasons.
func Failed(reasons ...string) Result {
	return Result{Passed: false, Reasons: reasons}
}

// Passed is the canonical successful Result.
var Passed = Result{Passed: true}

// Validator is the narrow interface every artifact validator in this
// package (and any custom validator a later role registers) implements.
// Kind identifies the validator for storage (artifacts.validator_id,
// migrations/0022_artifacts.sql) and for acceptance-criterion lookup
// (progress.AcceptanceCriterion.Kind); Validate performs the actual check.
//
// Validators are stateless and safe for concurrent use — Validate must not
// mutate the Candidate it is given.
type Validator interface {
	Kind() string
	Validate(ctx context.Context, candidate Candidate) (Result, error)
}

// Candidate is the input every validator receives: a claimed artifact plus
// whatever criterion parameter it was invoked with (e.g. the heading text
// for HeadingExistsValidator, the expected digest for
// ChecksumMatchesValidator). Not every field is meaningful to every
// validator — each validator's doc comment states which fields it reads.
type Candidate struct {
	// Path is the absolute or working-directory-relative filesystem path
	// to the artifact under evaluation. Required by FileExists,
	// ChecksumMatches, HeadingExists, and FenceBalance.
	Path string

	// ExpectedSHA256 is the lowercase hex digest ChecksumMatchesValidator
	// compares the file's actual content against.
	ExpectedSHA256 string

	// Heading is the exact heading line (including leading `#`s)
	// HeadingExistsValidator searches for, matching ADD §18.5's
	// acceptance-criterion shape (`heading_exists: "# 20. ..."`).
	Heading string
}
