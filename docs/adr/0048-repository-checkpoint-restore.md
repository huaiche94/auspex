# ADR-048 — Real repository checkpoint restore (issue #6)

Status: Accepted
Date: 2026-07-14
Owner: lead-executed
Tracking: issue #6; ends checkpoint-b08's "actual restore is stretch/deferred" vertical-slice deferral

## Context

Repository Checkpoints were capture-only: create, verify, and a full ADD
§19.6 dry-run existed (checkpoint-b04/b08), but nothing could put a
worktree back into a captured state — the continuity story's last link
(checkpoint → incident → restore) and Graceful Pause's "repo can return
to a known state" resume assumption were both open. Constitution
non-negotiable #9 (a checkpoint flow must never silently commit the
active branch) makes restore's mutation design an ADR-level decision.

## Decision

### Execution model

Restore replays the checkpoint through the narrowest mutating primitives
that exist:

1. **staged patch** → `git apply --binary --index` (index + worktree),
2. **unstaged patch** → `git apply --binary` (worktree only),
3. **untracked.zip** → per-entry extraction under capture's own
   path-safety rules plus a strict no-clobber rule.

`git apply` cannot move HEAD, switch branches, or create commits — the
no-ref-mutation guarantee is structural, not a convention. Restore never
runs checkout/reset/stash/commit. Tests assert HEAD, branch, and commit
count are byte-identical across a restore.

### Gate sequence (unchanged, now load-bearing)

`Service.Restore` runs the existing §19.6 dry-run first — checksum
verification, repository identity, dirty-target policy, `git apply
--check` on both patches — and the apply step is unreachable unless that
verdict is clean. Dry-run remains the DEFAULT: the frozen request gains
an additive `Apply bool`, whose zero value preserves the pre-ADR-048
behavior exactly (ADR-044's amendment discipline; CONTRACT_FREEZE.md
entry added). `RestoreResult` gains `SafetyCheckpointID` and
`UntrackedSkipped`, both additive.

### Dirty-target rule (ADD §19.6 "safety checkpoint/force")

- Dirty target without `AllowDirty` → rejected (unchanged).
- Dirty target with `AllowDirty` + `Apply` → a **safety checkpoint** of
  the pre-restore state is captured first, unconditionally, through the
  same `Create` path being restored from — the operator's undo handle,
  returned in the result and in any later error. A safety-capture
  failure aborts with nothing mutated (never "proceed uninsured").
- Clean target → no safety checkpoint: its state is HEAD, which restore
  cannot move.

### Never delete, never overwrite

ADD §19.6's "never delete extra files unless `--exact`" is enforced by
construction: restore deletes nothing, and untracked extraction skips
any destination that already exists (`O_EXCL`-backed, symlink-aware via
Lstat), disclosing every skip as `exists_not_overwritten` in the result.
No `--exact` mode is built; if one ever is, it revises this ADR.

### Hostile-archive defense (second line)

On the service path a tampered `untracked.zip` already fails checksum
verification before extraction. Extraction still independently enforces:
worktree containment (no `..`, no absolute paths, `.git` never touched),
regular-file-entries only (symlink/special entries skipped), pre-existing
symlinked-parent rejection, and capture's own size caps re-applied
against the actual decompressed stream (zip headers are
attacker-controlled). Adversarial tests drive extraction directly with
crafted archives.

### Partial-application honesty

ApplyCheck runs moments before apply, but the tree can change in
between. If the unstaged replay fails after the staged one landed (or
extraction fails after both patches), the error names exactly how far
the restore got and carries the safety checkpoint ID when one exists —
never a generic failure hiding a half-restored tree.

### CLI

`auspex checkpoint restore --id <id> [--apply] [--allow-dirty]` — dry-run
by default per ADD §19.6, schema-versioned JSON output
(`auspex.checkpoint-restore.v1`), wired through the existing
`CheckpointCreateDeps.RepositoryCheckpoint` service.

## Alternatives considered

- **`git stash` / `git checkout` based restore** — rejected: both move
  refs or stash state behind the operator's back; `git apply` is the
  only primitive whose blast radius is exactly the §19.2 captured scopes.
- **Safety checkpoint on every apply (clean targets too)** — rejected:
  a clean target's state is HEAD, already durable; an unconditional
  capture would double artifact volume for zero recovery value.
- **Refusing dirty targets entirely (no AllowDirty apply)** — rejected:
  ADD §19.6 explicitly provides the safety-checkpoint/force arm, and the
  pause/resume flow needs it (a paused session's tree is often dirty).
- **Deleting extra files by default ("exact" semantics)** — rejected
  outright by ADD §19.6's MUST.

## Consequences

- The continuity story closes: capture → verify → dry-run → real
  restore, all against the frozen port.
- Graceful Pause resume validation can now assume a mechanically
  restorable repository state.
- `RestoreResult.Applied` is no longer constitutionally false — callers
  that treated it as decoration should read it.
