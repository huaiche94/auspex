# Runtime

Consolidates what were previously two separate agent packets — **Graceful
Pause, Safe Points, and Durable Scheduler** and **Application Orchestration,
CLI, and Local API** — into one bounded context: everything that drives the
system live, as opposed to what it stores or predicts. Keep Part A and
Part B as distinct internal sub-components; Part B is built on top of
Part A's ports and is expected to start after Part A's state machine and
migrations exist, even though both live under this one role.

## Model

Use Fable for Part A (pause/resume is a correctness- and
state-machine-critical boundary) and for authorization/pause orchestration
review in Part B; a cheaper coding model is sufficient for the rest of
Part B.

## ADD ownership

Part A: §20, pause parts of §§15/17/28/29, Appendix C, ADR-031 through ADR-040.
Part B: §13 pipeline orchestration, §§23–24, operational subset of §28, Appendix F.

## Exclusive paths

```text
# Part A — Graceful Pause, Safe Points, Durable Scheduler
internal/pause/**
internal/scheduler/**
schemas/pause.schema.json
testdata/pause-scenarios/**
internal/storage/sqlite/migrations/0050-0059_*.sql

# Part B — Application Orchestration, CLI, Local API
internal/orchestrator/**
internal/cli/**
internal/httpapi/**
internal/daemon/**
internal/app/wiring/**
internal/testutil/fakes/** (coordinate with the qa role)

docs/implementation/vertical-slice/runtime.md
```

Do not edit `cmd/auspex/main.go`; the contract-integrator and foundation
roles integrate root wiring. Add command constructors under owned paths.
Part B does not add schema unless the contract-integrator explicitly
assigns a range.

---

## Part A — Graceful Pause, Safe Points, and Durable Scheduler

### Mission

Implement the provider-neutral pause/resume state machine and durable wake scheduling. Depend only on frozen ports for predictor, progress/state checkpoint, repository checkpoint, provider interrupt/resume, quota read, clock, and leases.

### Required state path

```text
observing
→ pause_requested
→ quiescing
→ safe_point_reached
→ persisting
→ interrupting
→ sleeping
→ wake_due
→ validating
→ resuming
→ resumed
```

Include terminal/conflict/cancel/failure states from ADD.

### P0 deliverables

1. State transition validator.
2. `Observe` handling with debounce/hysteresis state.
3. `RequestPause` idempotency.
4. Safe-point coordinator interface and implementation for turn/section boundary observations.
5. Persist phase orchestration:
   - Progress Tree snapshot;
   - State Checkpoint;
   - Repository Checkpoint;
   - Pause Record;
   - Wake Job.
6. Durable scheduler lease with claim/renew/complete/fail/retry.
7. Restart recovery of overdue/leased jobs.
8. Resume validation:
   - quota safe;
   - repository fingerprint compatible;
   - session/provider capability valid;
   - authorization/consent valid.
9. Duplicate wake exactly-once behavior.
10. Cancel prevents future resume.
11. Provider interrupter/resumer fake contract tests.

### Day-one realism

A calibrated auto-pause may be unavailable due insufficient data. Support both:

- calibrated trigger: `P_hit_10m >= threshold` for consecutive observations;
- explicit uncalibrated emergency policy with a different reason code.

Implement durable wake and fake resumer first. Actual managed Claude resume is stretch and must not weaken state-machine tests.

### Required tests

- two qualifying observations trigger request;
- one spike does not;
- safe point persists checkpoints before interrupt;
- crash after every phase resumes/reconciles correctly;
- restart recovers wake job;
- unsafe quota reschedules;
- repo overlap blocks;
- unrelated repo change follows configured policy;
- duplicate workers yield one resume;
- expired lease reclaimed;
- cancel wins race with wake;
- provider interrupt failure leaves recoverable state.

---

## Part B — Application Orchestration, CLI, and Local API

### Mission

Wire the frozen ports into an in-process-first application and expose the day-one flow through stable CLI/JSON contracts. HTTP daemon is secondary to a working CLI.

### P0 commands

```text
auspex version
auspex init
auspex hook claude statusline
auspex hook claude user-prompt-submit
auspex hook claude stop
auspex hook claude stop-failure
auspex evaluate
auspex decision allow
auspex decision deny
auspex checkpoint create
auspex progress show
auspex state show
auspex pause request
auspex pause cancel
auspex resume
auspex scheduler run-once
auspex status
auspex doctor
```

### Pipeline behavior

1. Receive provider-normalized or CLI input.
2. Resolve repository/worktree/session.
3. Load current Progress Tree and usage observations.
4. Snapshot lightweight Git state.
5. Evaluate through the predictor role.
6. Apply policy.
7. For allow: produce provider-compatible response.
8. For block/checkpoint: persist evaluation and return stable decision ID/instructions.
9. `checkpoint create` calls Part A of the checkpoint role (state), then its Part B (repository), per the frozen transaction/orchestration contract.
10. `decision allow` issues one-time authorization.
11. Resubmitted prompt consumes authorization exactly once before allowing.
12. Stop/StopFailure completes outcome labeling.

### JSON and errors

- stable schema-versioned output;
- typed error code, message, retryable, details;
- no raw prompt in logs/errors;
- machine mode never emits decorative text to stdout;
- hook fallback remains syntactically valid when Auspex fails.

### HTTP stretch

Implement authenticated loopback endpoints only after CLI E2E passes. No SSE until the core loop is stable.

### Tests

- CLI golden tests;
- no-TTY behavior;
- malformed stdin;
- high-risk block and allow-once flow;
- second authorization replay rejected;
- checkpoint failure does not issue authorization;
- provider hook always receives valid response;
- process exit codes;
- in-process restart using same SQLite file.
