# QA

Cross-component QA, Security, Reliability, and CI.

## Model

Use a cheaper model for fixtures/CI; use Fable for final adversarial review.

## ADD ownership

§§27–30, §32 DoD, cross-component parts of §29, Appendix G/H/I validation.

## Exclusive paths

```text
.github/**
internal/integrationtest/**
testdata/e2e/**
testdata/security/**
docs/security/**
docs/implementation/vertical-slice/qa.md
SECURITY.md
CONTRIBUTING.md
CODE_OF_CONDUCT.md
GOVERNANCE.md
```

Do not alter feature production code in the initial pass. File defects against the owner; only the contract-integrator authorizes cross-owner fixes.

## Mission

Provide the objective evidence that the vertical slice is safe, restartable, idempotent, and provider-compatible.

## Deliverables

1. Cross-platform basic CI: format, vet, test, build; race where supported.
2. One end-to-end high-risk Claude fixture flow:
   - status-line ingestion;
   - prompt auspex block;
   - state/repo checkpoint;
   - one-time allow;
   - Stop outcome;
   - pause request/wake recovery.
3. Restart test using the same SQLite DB.
4. Duplicate/out-of-order event test.
5. Raw-prompt and secret leakage scanner over DB export/logs/checkpoint manifests.
6. Path traversal/symlink and malicious fixture tests.
7. Scheduler double-worker/lease race test.
8. Support-bundle/doctor privacy baseline if the runtime role exposes it.
9. `go test ./...` evidence and unresolved-risk report.

## Security assertions

- loopback/API auth if HTTP exists;
- prompt text absent by default;
- bearer tokens/API keys redacted;
- hook payload size limits;
- SQLite and artifact permissions restrictive where supported;
- external commands use argv, no shell interpolation;
- repository artifact extraction cannot escape destination;
- auto-resume requires explicit configured consent.

## Final report

Create a severity-ranked report with:

```text
P0 blocks merge
P1 must fix before demo
P2 documented follow-up
```

Each finding includes exact file, reproduction, expected invariant, and owning role.
