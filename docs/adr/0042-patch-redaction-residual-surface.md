# ADR-042 — Patch redaction excludes filenames and binary-diff headers: accepted residual surface

Status: Accepted
Date: 2026-07-13
Owner: checkpoint (Part B / repocheckpoint), decision recorded by lead
Approved by: repository owner, 2026-07-13 (issue #5 decision session)

## Context

qa-09's final severity report (P2 #2,
`docs/implementation/vertical-slice/qa.md`) records that
`internal/repocheckpoint/patchredact.go` redacts secret-shaped content
only on `+`/`-` **line bodies** of staged/unstaged patches. It never
rewrites:

- file paths in `diff --git a/... b/...`, `--- a/...`, `+++ b/...`
  header lines (a path could itself be secret-shaped);
- binary-diff header/marker lines (`Binary files a/X and b/Y differ`,
  `GIT binary patch` and its base85 payload);
- context lines (leading space) — already separately pinned, because
  context must match the target file byte-for-byte for `git apply`.

qa-09 flagged this as a theoretical residual surface, not a confirmed
leak: no test constructs a secret-shaped filename, and no real instance
has been observed.

## Decision

**Accept the residual surface. Do not extend redaction to patch headers,
filenames, or binary-diff payloads.**

Rationale, in decreasing order of weight:

1. **Patch validity is load-bearing.** Restore dry-run (checkpoint-b08)
   and any future real restore (issue #6) depend on `git apply --check`
   against these patches. Rewriting paths or binary payloads produces a
   patch that no longer applies to the repository it was captured from —
   destroying the evidentiary value of the entire checkpoint to remove a
   secret that, in the filename case, is *already public in the user's
   own working tree and `git status` output*.
2. **Different threat model.** The redaction pass exists to stop secret
   *values* (tokens, keys) from being copied into durable checkpoint
   artifacts. A secret-shaped *filename* is not a credential at rest in
   the artifact; it is repository metadata the user chose. The
   untracked-archive path (checkpoint-b06) — where whole file *contents*
   are copied — retains its full per-file secret scan and skip ledger.
3. **Prevalence.** Secret-shaped filenames are vanishingly rare in
   practice; every observed real leak class (qa-05's tracked-diff P1,
   since fixed at `f981bde`) involved line content, which is covered.

## Consequences

- The boundary is pinned by tests so any silent behavior change is
  caught: `TestRedactPatchSecrets_ContextLine_NeverModified`,
  `TestRedactPatchSecrets_FileHeaderLines_NeverTreatedAsContent`, and
  (added with this ADR)
  `TestRedactPatchSecrets_ADR042_SecretShapedFilenameAndBinaryHeaders_AcceptedBoundary`
  in `internal/repocheckpoint/patchredact_internal_test.go`.
- `patchredact.go`'s doc comment already documents the line-scope rules;
  it now has a canonical decision record to cite.

## Revisit triggers

Reopen this decision if any of the following occurs:

1. A real (non-theoretical) leak through a filename or binary-diff
   header is observed or reported.
2. `internal/redact` gains structural patch rewriting (e.g. re-hunking)
   that could redact headers *without* breaking `git apply --check`.
3. Checkpoint artifacts gain an export/share path that leaves the local
   machine by default (today they are local-only under the user data
   directory), which would change the exposure model.
