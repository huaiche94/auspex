# ADR-0058 — Resume bootstrap: a constructed prompt from durable state, one builder with two profiles (§21.6 step 8; #9 final slice)

> 🌐 English | [繁體中文](0058-resume-bootstrap-prompt.zh-TW.md)

Status: Accepted
Date: 2026-07-29
Owner: lead-executed (PROPOSAL — prompt-contract commitment, owner sign-off at merge)
Tracking: issue #9 (M7 Phase 2, step-8 resume); ADD §20.10/§21.6 step 8/§21.8/§22.7, FR-151; builds on ADR-013 (App Server = primary managed path) and ADR-0056 (App Server source + `managed_app_server` semantics); forward-feeds #8 (M11 managed shell/one-shot) and #11 (M13 managed-run calibration data)

## Context

Slice C landed steps 1–7 of §21.6: the providerStopper seam arms the
M10 pause trigger on the App Server path (`turn/interrupt`, interrupted
terminal observed, checkpoints + wake job persisted). What fires at wake
today is only the state machine: the daemon worker claims the wake job,
`ValidateResume` runs, and the pause record CAS-advances to `Resumed` —
**nothing relaunches a provider**. `app.SessionResumer` is a frozen port
with no production implementation; `ResumeThread` has zero callers. Step
8 — "`thread/resume` and `turn/start` with bootstrap" — is one line of
ADD prose, and the bootstrap prompt's content is the open design.

Constraints the substrate imposes (verified against main):

1. **The raw user prompt is never persisted** (Constitution §7 privacy
   default; prompts are redacted everywhere). Whatever the resumed agent
   receives must be *constructed*, because there is nothing to replay.
2. **The Progress Tree is the canonical durable task state**
   (Constitution §6.1) and carries first-party text per node: `title`,
   `description`, `acceptance_json`, `next_action_json`
   (`0020_progress_nodes.sql`). Task framing can be reconstructed from
   it without touching provider transcripts.
3. **The checkpoint manifest has resume slots that pause never fills.**
   `ProviderInfo{Name, SessionID, TurnID, InvocationMode}`,
   `NextActionInfo`, `ResumeInfo{StrategyOrder, PermissionMode}` exist
   in `statecheckpoint/manifest.go`, but the pause-time `Create` path
   populates only the progress summary and artifacts. The Codex
   `thread_id` is durable *only* inside the `provider.session.started`
   event payload — attribution data, not a first-class locator.
4. **Wake jobs are payload-free by design** (`0051_wake_jobs.sql`:
   `UNIQUE(pause_id, job_kind)` is the exactly-once anchor; a job is a
   pause pointer plus lease/retry bookkeeping).
5. **Terminology collision:** D-07's `SessionBootstrapper` bootstraps
   hook-side session *records* from zero data. This ADR's artifact is
   the **resume bootstrap** — an outbound prompt. Distinct names,
   distinct code.

## Decision

1. **The bootstrap prompt is constructed at wake time, never replayed,
   and never persisted.** Sole inputs, all first-party durable state:
   the pause record + its `pauseContext` (`GitHeadBaseline`,
   `QuotaBaseline`, `PausedWorkPaths`), the latest State Checkpoint
   manifest, Progress Tree rows (title/description/acceptance/next
   action), the repository-checkpoint fingerprint, and the wake-time
   revalidated quota observation. No provider transcript text exists in
   any input, so none can leak into the prompt. The build is
   deterministic given its inputs — a retried wake job rebuilds the
   identical prompt instead of reading a stored copy, which also means
   quota/git numbers are *fresh at wake*, not stale from pause time.
   Telemetry records the prompt's byte length only (ADR-051/0056
   numbers-only precedent).

2. **One builder, two profiles, selected by the §20.10 strategy in
   play.** §20.10's literal template is the shared skeleton; both
   profiles end with its authority clause — *Git, tests, artifact
   hashes, and the Progress Tree are authoritative over conversation
   memory*.
   - **`re_entry`** (strategies 1–2: same session/thread — Codex
     `thread/resume` + `turn/start`, claude `--resume`): the provider
     retains its own conversation context, so the prompt is a delta
     brief and deliberately does **not** re-narrate the task:
     interruption fact + reason (runway threshold), pause→wake time
     gap, instruction to verify git status and artifact checksums
     against the checkpoint, active node + recorded next action, and
     the remaining runway/budget numbers. The authority clause is the
     defense against stale in-context beliefs.
   - **`cold_bootstrap`** (strategy 3: new session — §21.8 exec
     fallback, FR-151 `new_session_progress_bootstrap`): everything in
     `re_entry` plus task framing reconstructed from the Progress Tree
     itself — root/completed/active node titles, descriptions, and
     acceptance criteria. This is the only honest way to brief a blank
     context, because the original prompt does not exist anywhere.

   Both profiles are provider-neutral text. Provider adapters choose
   only the *delivery*: `turn/start` message (codex app-server), `-p`
   argument with `--resume` (claude, §22.7), initial prompt of a new
   managed run (exec fallback, #8 one-shot/shell). The builder is
   shared infrastructure, not Codex code — that is what #8 reuses.

3. **Locator durability: fill the manifest's designed slots at pause
   time.** The pause-path checkpoint gains `ProviderInfo` (provider
   name; claude session id / codex thread+turn ids; invocation mode),
   `NextActionInfo` (from the active node's `next_action_json`), and
   `ResumeInfo` (strategy order derived from declared capabilities;
   permission mode). No migration — `manifest_json` is schemaless — and
   no new tables. The event-payload `thread_id` remains as attribution
   but stops being the only durable copy.

4. **The executor is the first production `SessionResumer`.** Codex App
   Server implementation: `thread/resume(threadID)` → `turn/start` with
   the built prompt. The resumed run re-arms the providerStopper — a
   resumed run is again interruptible, so pause→resume cycles are
   idempotent by construction. Approval requests remain
   surfaced-never-auto-approved (ADR-0056 §4). A `ValidateResume`
   BLOCK means no thread is started (existing precedent). Wake jobs
   stay payload-free: the worker hands over `pause_id`; everything else
   is re-derived from durable state.

5. **Failure ladder = §20.10's, made operational.** Same-thread resume
   fails (thread evicted, protocol error) → fork if the capability
   exists (codex today: none) → `cold_bootstrap` new session → manual.
   Strategy downgrades ride the existing pause lifecycle events and the
   wake job's lease/backoff machinery; on exhaustion the job goes
   `dead` and the pause stays manually resumable. The strategy actually
   used is recorded as an enum on the pause record's metadata —
   numbers-and-enums only.

## Honest scope

- This ADR fixes the **prompt contract** (inputs, profiles, authority
  clause, privacy posture) and **locator durability**. Implementation
  slices: (i) pause-time manifest enrichment, (ii) builder + golden
  prompt fixtures, (iii) App Server `SessionResumer` + fake-server E2E,
  (iv) #8 reuse of the builder. Each cites this ADR.
- The claude `--resume` executor is #8/M11 scope; this ADR only
  requires the builder stay provider-neutral.
- Cross-provider resume (checkpoint written under codex, resumed on
  claude) is explicitly out — the capability abstraction may permit it
  later; nothing here forecloses it, nothing here builds it.
- The builder needs node text the thin `app.ProgressNode`
  (ID/Status/Kind) does not expose; the implementation slice adds an
  additive read path (new method or read-model, per CONTRACT_FREEZE
  additive rules) rather than widening the frozen struct.

## Consequences

- `SessionResumer` gets its first implementation with **no signature
  change**; CONTRACT_FREEZE.md needs no amendment for the port itself.
- Golden fixtures pin both profiles' rendered prompts; a determinism
  test asserts rebuild-equality on identical inputs; the fake App
  Server E2E extends to the full interrupt→wake→resume loop.
- Managed resumed runs start producing the pause→resume telemetry M13
  (#11) needs for duration/runway calibration — this is the data the
  calibration wall has been waiting on.
- "Resume bootstrap" and D-07's `SessionBootstrapper` are recorded as
  unrelated artifacts to prevent naming drift.

## Alternatives considered

- **Replay the original prompt.** Impossible and rejected: it is never
  persisted, and replaying would also re-run completed work — the
  Progress Tree, not the prompt, is the durable task truth.
- **Persist the constructed bootstrap at pause time.** Rejected: quota
  and git state move between pause and wake, so a stored prompt is
  stale by definition; rebuild-at-wake is equally deterministic and
  avoids persisting another text blob.
- **One prompt for both profiles.** Rejected: re-narrating the task
  into a live thread wastes context and invites re-planning; a blank
  session without the narration cannot act. The split is §8.8's
  degradation ladder made explicit at the prompt layer.
- **Payload-carrying wake jobs.** Rejected: duplicates durable state
  into the queue, goes stale, and complicates the exactly-once anchor
  for zero information gain.
- **A dedicated thread-locator column/table.** Rejected for now: the
  manifest's designed slots suffice and need no migration; revisit only
  if lookup-by-thread becomes a real query pattern.
