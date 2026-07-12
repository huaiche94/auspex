# testdata/repositories fixtures

This directory holds fixtures for `internal/repocheckpoint` and
`internal/gitx` tests that need example repository *content* rather than
an example checkpoint *manifest* (see `testdata/checkpoints/repository/`
for the latter).

checkpoint-b04 does not commit an actual `.git` directory here: a real Git
repository checked into another Git repository as a plain directory tree
causes tooling (and `git status` inside this very repo) to misbehave, and
Preflight's own test suite builds real temporary repositories on demand
instead (see `internal/gitx`'s `repoBuilder` and
`internal/repocheckpoint`'s equivalent test helper in `helpers_test.go`) —
that is the actual source of "real repository" coverage for this role's
tests, exercised fresh in temp directories for every test run rather than
frozen here as static fixture data that could drift from what the code
under test expects.

Later nodes in this role's scope (checkpoint-b05's binary-patch edge
cases, checkpoint-b06's secret/path filtering, checkpoint-b09's
path-traversal/symlink-escape security gate) may add concrete fixture
files here if a specific edge case genuinely needs a frozen, reviewable
example rather than a generated temp repo — e.g. a binary file with a
specific byte pattern, or a path containing characters that are awkward to
construct portably in Go test code. This file exists so that convention is
documented before those nodes add to it.
