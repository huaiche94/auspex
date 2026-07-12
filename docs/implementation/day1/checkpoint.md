# checkpoint — Progress Artifact

Role covers two internal sub-components (Part A: Progress Tree / State
Checkpointing; Part B: Repository Checkpoint), kept separate per
`agents/checkpoint.md`. This wave, only one node was unblocked and assigned:
`checkpoint-b02`. `checkpoint-a01` and `checkpoint-b01` remain queued behind
`foundation-06` (core SQLite migration harness), which was not complete this
wave — per the frozen `EXECUTION_DAG.md`, both were correctly withheld
rather than started against an incomplete dependency.

```yaml
node: checkpoint-b02
status: completed
artifacts:
  - internal/gitx/runner.go        # domain.ProcessRunner exec.Command-backed impl (argv-only, never a shell string)
  - internal/gitx/client.go        # Client wrapping ProcessRunner with git-specific ops (Status)
  - internal/gitx/resolver.go      # RepoInfo + ResolveRepo: worktree root / git-dir / common-dir resolution via `git rev-parse --path-format=absolute`
  - internal/gitx/porcelain.go     # ParsePorcelainV2: `git status --porcelain=v2 -z` parser (changed/renamed/unmerged/untracked/ignored + branch headers)
  - internal/gitx/gitx_test.go     # shared repoBuilder test scaffolding (real temp git repos via ExecRunner, argv-only)
  - internal/gitx/resolver_test.go # main worktree, linked worktree, not-a-repo, nonexistent-path cases
  - internal/gitx/porcelain_test.go # tracked/staged/unstaged/untracked/rename/delete scenarios + parser fixture edge cases
validation:
  - "gofmt -l internal/gitx                                                     # empty output"
  - "go build ./internal/gitx/...                                               # OK"
  - "go vet ./internal/gitx/...                                                 # OK"
  - "go test ./internal/gitx/... -run 'Porcelain|Fingerprint|Resolver' -v       # PASS, all subtests green (Fingerprint has no matches yet — that's checkpoint-b03 scope)"
  - "go test ./internal/gitx/... -race -v                                       # PASS"
  - "go build ./... && go vet ./...                                             # whole-repo build/vet unaffected"
commit: 9b222d0
next_action: checkpoint-a01 and checkpoint-b01 (both blocked on foundation-06, not started this wave)
assumptions:
  - "Status() pins --branch --untracked-files=all --find-renames on top of --porcelain=v2 -z so output is deterministic regardless of the caller's git config (status.renames, status.showUntrackedFiles). This is additive to the exact flag string in the DAG's validation command and does not change the parser's contract."
  - "ParsePorcelainV2 fails closed (returns a domain.Error with ErrCodeValidation) on any record shape it doesn't recognize, rather than silently skipping it — this parser feeds the Repository Checkpoint integrity boundary (Constitution §6), so an unintelligible status must not be treated as 'no changes.'"
  - "ResolveRepo requires git >= 2.31 for `rev-parse --path-format=absolute`; not verified against older git in this environment. Local git version used for tests: 2.37.3."
  - "Unmerged (conflict) entries and ignored entries are parsed by the porcelain layer (fixture-tested) but have no live-repo integration test in this wave, since provoking a real merge conflict/ignore rule in a throwaway temp repo added setup complexity out of proportion to checkpoint-b02's scope; they are covered by TestParsePorcelainV2Fixtures instead. checkpoint-b03/b04 should add a live-repo conflict case if the fingerprint or checkpoint-create logic branches on unmerged state."
blockers:
  - "checkpoint-a01 and checkpoint-b01 blocked pending foundation-06 (core SQLite migration harness) — not started this wave, per explicit wave assignment."
```
