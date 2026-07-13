# ADR-045 — Rename the product from Preflight to Auspex (supersedes ADR-001)

Status: Accepted
Date: 2026-07-13
Owner: lead
Approved by: repository owner, 2026-07-13 (naming decision session)

## Context

ADR-001 named the product Preflight. The issue-#16 pre-release naming
audit (2026-07-13) found the name effectively unbrandable:

- **preflight.sh** — an active, recently launched pre-deploy scanning CLI
  with Claude Code/Cursor agent-skill integration: nearly the same niche.
- The **`preflight` binary already ships on many developers' PATHs** via
  Replicated troubleshoot and Red Hat openshift-preflight (both actively
  released Kubernetes tooling).
- The VS Code display name "Preflight" is taken by a same-niche AI
  code-review extension; the GitHub handle and all three candidate
  domains (preflight.dev / preflight.sh / getpreflight.com) are
  registered; "preflight" is a 30-year generic term in prepress, CORS,
  Tailwind, and CI vocabulary — near-zero distinctiveness.

## Decision

Rename the product, binary, module, protocol prefix, and state directory
to **Auspex** / `auspex`:

- Latin *auspex*: the diviner who reads bird omens **before an
  undertaking begins** and rules whether it may proceed — the product's
  one-line positioning ("should we even start this turn?") in a single
  word. English *auspices* ("under the auspices of") descends from it.
- Candidate-name audit (GitHub/npm/Homebrew/.dev RDAP, 2026-07-13):
  only small unrelated projects (largest 42⭐), no Homebrew formula, no
  same-niche product, no binary-on-PATH collision. A full #16-grade
  registry/trademark audit runs alongside this rename; §4.4 keeps the
  publication checklist.

Scope of the rename executed with this ADR:

1. Go module path `github.com/huaiche94/preflight` →
   `github.com/huaiche94/auspex`; GitHub repo renamed (old URLs
   redirect).
2. Binary `preflight` → `auspex`; `cmd/preflight` → `cmd/auspex`.
3. Frozen schema-version strings re-prefixed (`preflight.error.v1` →
   `auspex.error.v1`, etc.) — permissible only because the project is
   pre-release with zero external consumers; recorded as a
   CONTRACT_FREEZE.md amendment. After first public release this class
   of change would be forbidden.
4. OS user-data directory `preflight/` → `auspex/` (pre-release: no
   migration shipped; existing local databases are abandoned in place).
5. Authoritative docs renamed (`Preflight_ADD.md` → `Auspex_ADD.md`,
   execution plan, predictor supplement, methodology).
6. `docs/archive/` and git history are NOT rewritten — they are
   historical record and retain the old name; ADR-001's §33 entry is
   marked superseded rather than edited to pretend Auspex was always the
   name.

## Consequences

- ADR-001 is superseded. ADD §4.4's checklist now tracks the Auspex
  registry confirmations; domain strategy shifts to compound domains
  (auspex.tools / getauspex.dev), since auspex.dev is registered.
- The statusline glyph prefix changes `pf✈` → `ax✈` (the bird stays —
  auspicium was literally bird-watching).
- Historical artifacts (issue titles, wave-era progress docs in
  `docs/archive/`, commit messages) intentionally still say Preflight.
