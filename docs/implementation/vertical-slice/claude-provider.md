# claude-provider — Vertical-Slice Progress Artifact

Role: `claude-provider` (`agents/claude-provider.md`).
Wave: DAG parallel-root nodes `claude-provider-01`, `claude-provider-02`, `claude-provider-03` (per `docs/implementation/vertical-slice/EXECUTION_DAG.md`).
Branch: `vertical-slice/claude-provider` (local only — not pushed, not merged into `main`).

This file is the durable, validator-checked evidence for this wave per
Constitution §6 rule 7 ("this same discipline applies, by analogy, to the
meta-level progress artifacts"). It is updated, not conversation memory.

---

```yaml
node: claude-provider-01
status: completed
artifacts:
  - internal/providers/claude/statusline.go
  - internal/providers/claude/statusline_test.go
  - testdata/provider-events/claude/statusline/normal.json
  - testdata/provider-events/claude/statusline/missing_fields.json
  - testdata/provider-events/claude/statusline/unknown_fields.json
  - testdata/provider-events/claude/statusline/high_usage.json
  - testdata/provider-events/claude/statusline/malformed.json
validation:
  - "gofmt -l internal/providers/claude internal/hooks/claude  -> clean"
  - "go build ./internal/providers/claude/... ./internal/hooks/claude/...  -> ok"
  - "go vet ./internal/providers/claude/... ./internal/hooks/claude/...  -> ok"
  - "go test ./internal/providers/claude/... -run StatusLine -v  -> PASS (5 fixture subtests + 4 unit tests)"
commit: 69462cce853665262ead9dad5b7f998c00b9bcd4
next_action: claude-provider-04 (blocked - not started this wave; normalizes parser output into pkg/protocol/v1.Event, depends on all of 01/02/03)
assumptions:
  - "No live Claude Code account was available this wave (per task brief). All 5 status-line fixtures were hand-constructed from agents/claude-provider.md and Auspex_ADD.md §22.5/§22.10's described field names (session_id, model.id/display_name, workspace.current_dir/project_dir, context_window.total_input_tokens/total_output_tokens/context_window_size/used_percentage, cost.total_cost_usd/total_duration_ms/total_api_duration_ms/total_lines_added/total_lines_removed, rate_limits.five_hour/seven_day.used_percentage/resets_at) rather than captured from a real session. Field names may not match the live API exactly; ADD Appendix H's official doc URLs (code.claude.com/docs/en/statusline) were not fetched (no external network access this wave) and could not be cross-checked."
  - "The 5 fixture files (normal, missing_fields, unknown_fields, high_usage, malformed) were found already present and untracked in the worktree from a previously interrupted attempt. They were inspected, found to align with ADD §22.5's field list exactly, and kept as-is rather than replaced."
  - "Confidence classification for ContextObservation/QuotaObservation (exact/high/medium/unavailable) is this role's own heuristic - CONTRACT_FREEZE.md freezes the Measurement/Confidence *type*, not per-provider confidence assignment rules."
  - "Five-hour/seven-day quota observations return nil (not a zero-value struct) when both percentage and reset timestamp are absent, per ADD §22.10 (\"Absence => unknown\")."
blockers: []
```

```yaml
node: claude-provider-02
status: completed
artifacts:
  - internal/hooks/claude/userpromptsubmit.go
  - internal/hooks/claude/userpromptsubmit_test.go
  - testdata/provider-events/claude/userpromptsubmit/normal.json
  - testdata/provider-events/claude/userpromptsubmit/missing_fields.json
  - testdata/provider-events/claude/userpromptsubmit/unknown_fields.json
  - testdata/provider-events/claude/userpromptsubmit/empty_prompt.json
  - testdata/provider-events/claude/userpromptsubmit/malformed.json
  - testdata/provider-events/claude/userpromptsubmit/response_allow.golden.json
  - testdata/provider-events/claude/userpromptsubmit/response_block.golden.json
validation:
  - "gofmt -l internal/providers/claude internal/hooks/claude  -> clean"
  - "go build ./internal/providers/claude/... ./internal/hooks/claude/...  -> ok"
  - "go vet ./internal/providers/claude/... ./internal/hooks/claude/...  -> ok"
  - "go test ./internal/hooks/claude/... -run UserPromptSubmit -v  -> PASS (5 fixture subtests + 5 unit/golden tests, including raw-prompt-never-persisted privacy assertion)"
commit: 69462cce853665262ead9dad5b7f998c00b9bcd4
next_action: claude-provider-04 (blocked - not started this wave)
assumptions:
  - "Exact stdin field names for the UserPromptSubmit hook payload (session_id, transcript_path, cwd, hook_event_name, prompt) are constructed from Claude Code hook conventions described in the packet and ADD §22.3/Appendix E.3, not captured from a live payload. ADD Appendix H names code.claude.com/docs/en/hooks as the authoritative source but it was not fetched this wave (no external network access)."
  - "The block-response JSON shape (decision/reason/hookSpecificOutput.hookEventName/additionalContext) is taken verbatim from ADD §22.3's example. The allow-response shape (bare `{}`, omitting the decision key entirely) is this role's own interpretation of 'hook protocol convention of omitting fields that don't apply' since the ADD does not show an explicit allow example - flagged here as a judgment call, not a frozen contract, in case contract-integrator wants to pin it down explicitly before claude-provider-06 (the CLI wrapper) ships."
  - "Approximate token counting (~4 bytes/token) is a coarse, explicitly-approximate heuristic per the packet's Privacy section ('approximate token count only') - not calibrated against Claude's actual tokenizer."
  - "On internal/parse failure, the hook wrapper is expected to call FallbackAllowResponse() to fail open (never block the user due to a Auspex bug) - this fail-open behavior for a hook-path *operational* failure is consistent with CONTRACT_FREEZE.md's fail-open/fail-closed split (operational observation failures may fail open) but the fallback wiring itself belongs to claude-provider-06's CLI wrapper, not this node - only the primitive (FallbackAllowResponse) is delivered here."
blockers: []
```

```yaml
node: claude-provider-03
status: completed
artifacts:
  - internal/hooks/claude/stop.go
  - internal/hooks/claude/stop_test.go
  - testdata/provider-events/claude/stop/normal.json
  - testdata/provider-events/claude/stop/missing_fields.json
  - testdata/provider-events/claude/stop/unknown_fields.json
  - testdata/provider-events/claude/stop/malformed.json
  - testdata/provider-events/claude/stopfailure/rate_limit.json
  - testdata/provider-events/claude/stopfailure/overloaded.json
  - testdata/provider-events/claude/stopfailure/context_length.json
  - testdata/provider-events/claude/stopfailure/network.json
  - testdata/provider-events/claude/stopfailure/unknown_category.json
  - testdata/provider-events/claude/stopfailure/missing_fields.json
  - testdata/provider-events/claude/stopfailure/malformed.json
validation:
  - "gofmt -l internal/providers/claude internal/hooks/claude  -> clean"
  - "go build ./internal/providers/claude/... ./internal/hooks/claude/...  -> ok"
  - "go vet ./internal/providers/claude/... ./internal/hooks/claude/...  -> ok"
  - "go test ./internal/hooks/claude/... -run 'Stop|StopFailure' -v  -> PASS (4 Stop fixture subtests + 7 StopFailure fixture subtests + error-message-not-retained privacy test + classifier unit test)"
commit: 69462cce853665262ead9dad5b7f998c00b9bcd4
next_action: claude-provider-04 (blocked - not started this wave)
assumptions:
  - "Exact StopFailure error-object shape (error.type/message/status_code, modeled loosely on Anthropic API error conventions: rate_limit_error, overloaded_error, invalid_request_error, connection_error) is hand-constructed - no frozen contract or live payload exists for this. ADD Appendix H names code.claude.com/docs/en/hooks as authoritative but it was not fetched this wave."
  - "classifyFailure's mapping from provider error type/status code to domain.FailureClass is this role's own heuristic, not a frozen contract: rate_limit_error/429 -> FailureProviderRateLimit; overloaded_error/529/5xx/api_error -> FailureProviderInternal; message containing 'too long'/context-length phrasing -> FailureContext; connection_error/network keywords -> FailureNetwork; permission_error/403 and authentication_error/401 -> FailurePermission; timeout_error -> FailureTimeout; anything unmatched -> FailureUnknown. This mapping should be revisited once real StopFailure payloads are observed against a live account, per the packet's stretch-goal note."
  - "StopHookActive (stop_hook_active) uses *bool so 'field absent' (nil, unknown) is distinguishable from 'field present and false' - modeled after Claude Code's documented stop-hook-loop-prevention field, but not verified against a live payload."
  - "Raw error message text is deliberately never retained on StopFailureEvent (only ErrorMessageLen, an int) even though the packet's Privacy section is written primarily about prompts - applied the same discipline defensively since provider error messages can echo back request content."
blockers: []
```

---

## Wave 2

```yaml
node: claude-provider-04
status: completed
artifacts:
  - internal/telemetry/claude/normalizer.go
  - internal/telemetry/claude/normalizer_test.go
  - internal/telemetry/claude/privacy_test.go
validation:
  - "gofmt -l internal/telemetry/claude  -> clean"
  - "go build ./internal/telemetry/claude/...  -> ok"
  - "go vet ./internal/telemetry/claude/...  -> ok"
  - "go test ./internal/telemetry/claude/... -v  -> PASS (9 tests, incl. 5 StopFailure fixture subtests, idempotency-determinism test, duplicate-snapshot idempotency test, 2 privacy-assertion tests)"
  - "go build ./...  -> ok (no cross-package regressions)"
  - "go test ./internal/providers/claude/... ./internal/hooks/claude/... ./internal/telemetry/claude/...  -> ok (Wave-1 packages unaffected)"
commit: d4d2869c96fdcda49fb81cf5a927c7c3eb7c7f8e
next_action: claude-provider-06 (this wave; claude-provider-05/-07 out of scope for this wave per instructions)
assumptions:
  - "pkg/protocol/v1.EventType's frozen taxonomy (verified by reading pkg/protocol/v1/event.go directly, commit ac99215 base) already contains every event type this node needs: EventProviderContextObserved, EventProviderUsageObserved, EventProviderQuotaObserved (from StatusLineSnapshot), EventProviderTurnStarted (from UserPromptSubmitEvent), EventProviderTurnCompleted (from StopEvent), EventProviderTurnFailed + EventProviderRateLimitHit (from StopFailureEvent, the latter only when FailureClass == domain.FailureProviderRateLimit). No contract gap was found; no new EventType was added by this role."
  - "ADR-041 (predictor's Token/Quota Forecast layer, landed on main after this branch's Wave 1) was confirmed out of scope for this node per the task brief: pkg/protocol/v1.Event was untouched by ADR-041 and this branch intentionally did not merge/rebase onto main, so the frozen envelope read here is the same one contract-integrator-04 froze at Bootstrap (commit 4262b4b)."
  - "domain.IDGenerator and domain.Clock (internal/domain/clock.go, frozen at Bootstrap, contract-integrator-owned) are used as the Normalizer's injected dependencies for EventID generation and ObservedAt/OccurredAt timestamps, rather than this role calling crypto/rand or time.Now() directly. No concrete domain.IDGenerator implementation exists yet on this branch (foundation-06's internal/idgen is a later, unmerged node this branch does not depend on) — package tests supply a deterministic fake (seqIDs) instead of a real UUIDv7 generator. The real generator will be wired in by whichever role assembles the end-to-end hook-to-storage path (out of scope for claude-provider-04 itself, which only produces Event values, not a wired pipeline)."
  - "IdempotencyKey's exact digest algorithm was left to this role per CONTRACT_FREEZE.md ('Owning role... defines the exact digest algorithm'): a SHA-256 over event-kind-tag + session ID + (+ limit ID for quota events) + an observedAt timestamp, unit-separator-joined. This is a judgment call, not a frozen contract — a later role persisting these events (claude-provider-05, this branch's next wave, not started) may need a different granularity (e.g. incorporating a monotonic sequence number) if the observedAt-second granularity proves too coarse for true duplicate-vs-distinct-observation disambiguation; flagged here for that role."
  - "One StatusLineSnapshot can normalize into up to 4 events (context, usage, five-hour quota, seven-day quota) because each maps to a distinct frozen EventType and CONTRACT_FREEZE.md's 'unknown is not zero' rule means a wholly-absent measurement must not synthesize a placeholder event. This is this role's own design choice (the packet does not specify 1:1 vs 1:N struct-to-event mapping) — NormalizeStatusLine's doc comment explains the reasoning; contract-integrator was not blocking on this since pkg/protocol/v1.Event's shape does not constrain cardinality of struct-to-event mapping."
  - "StopFailureEvent's classified FailureClass == domain.FailureProviderRateLimit also emits a second EventProviderRateLimitHit event alongside the primary EventProviderTurnFailed event (both from a single hook payload) since the frozen taxonomy has both types and they are not mutually exclusive (a turn can fail because of a rate limit). This is a judgment call about fan-out, not a contract requirement — documented in NormalizeStopFailure's doc comment for predictor/qa roles that may consume these events downstream."
blockers: []
```

```yaml
node: claude-provider-06
status: completed
artifacts:
  - integrations/claude/plugin.json
  - integrations/claude/hooks.json
  - integrations/claude/README.md
validation:
  - "manual: python3 -m json.tool on both plugin.json and hooks.json -> valid JSON"
  - "manual: structural assertion script confirms UserPromptSubmit/Stop/StopFailure hook entries and the statusLine entry all invoke `auspex hook claude <subcommand>` with type=command, and plugin.json matches ADD Appendix E.2 verbatim"
  - "go build ./...  -> ok (no .go files added by this node; confirms no accidental breakage)"
next_action: none — Wave 2 scope (claude-provider-04, claude-provider-06) complete; claude-provider-05/-07 explicitly out of scope for this wave per task brief
commit: 0dbe22b9b15506eb61daf5d97cb4363fbb8c2ec0
assumptions:
  - "This is an explicitly forward-looking stub per the DAG's own note on claude-provider-06 ('stub acceptable before [runtime-b01]'): the `auspex` CLI binary and its `hook claude ...` subcommands do not exist on this branch (runtime-b01, a different role's later node, not built yet). The example configuration is syntactically valid and internally consistent with this role's Wave-1/Wave-2 primitives but is not exercisable end-to-end (`auspex hook claude user-prompt-submit < fixture` per the DAG's literal validation command cannot run — there is no `auspex` binary yet)."
  - "plugin.json is copied verbatim from Auspex_ADD.md Appendix E.2, which is explicitly this role's documented ownership ('Appendix E.2/E.3' in agents/claude-provider.md)."
  - "hooks.json's overall JSON shape ({\"hooks\": {\"<HookEventName>\": [{\"hooks\": [{\"type\": \"command\", \"command\": \"...\"}]}]}}) is taken from Auspex_ADD.md Appendix E.3. This role's hooks.json only wires the three hook events this role has actually built parsers/normalizers for (UserPromptSubmit, Stop, StopFailure) plus the statusLine entry (§22.5), deliberately omitting Appendix E.3's SessionStart/TaskCreated/TaskCompleted/PreCompact/PostToolUse/PostToolUseFailure entries since this role has not built parsers for those (packet's P0 deliverables list them as optional, 'when fixtures are available' — none were built this wave) and wiring a hook with no corresponding parser would be example configuration promising behavior that doesn't exist yet."
  - "GENUINE UNRESOLVED DOCUMENT CONFLICT (recorded per task instructions' STOP-and-report guidance, though this one did not block the node — a defensible default was available and taken): Auspex_ADD.md Appendix E.3 writes CLI subcommands in PascalCase (`auspex hook claude UserPromptSubmit`), matching Claude Code's own wire-level hook_event_name casing. agents/runtime.md's P0 commands list AND docs/implementation/vertical-slice/EXECUTION_DAG.md's own validation command for this exact node (claude-provider-06) both write kebab-case (`auspex hook claude user-prompt-submit`). Per Constitution §2's document priority order, the ADD (priority 2) would outrank agents/runtime.md (priority 4) if this is read as a real conflict rather than a casing typo in one of the two documents. This role does not own either document and cannot fix it. hooks.json here follows the DAG/runtime.md kebab-case form since it is this specific node's own frozen validation command; the discrepancy is written up in full in integrations/claude/README.md for contract-integrator to reconcile (likely by fixing Appendix E.3 to match standard kebab-case CLI convention, since agents/runtime.md's P0 command list reads as the more deliberate, recently-authored source for exact CLI spelling)."
  - "Auspex_ADD.md §22.6 ('Compose existing status line' — installer must read/preserve a pre-existing status-line command and combine output rather than overwrite it) is installer behavior, not expressible in a static example config file, and is explicitly out of scope for this node — noted in the README rather than silently ignored."
blockers: []
```

---

## Corrective note (post-Wave-2 lint pass)

A cross-role integration validation pass (golangci-lint run against the full
merged Wave 2 tree) surfaced 4 code issues plus 1 documentation whitespace
issue in files owned by this role. A corrective commit was made to fix these
findings in place:

- `internal/hooks/claude/stop_test.go` (lines 84, 163): replaced direct
  `err.(*domain.Error)` type assertions with `errors.As` (errorlint).
- `internal/hooks/claude/userpromptsubmit_test.go` (line 167): replaced the
  deprecated `reflect.Ptr` constant with `reflect.Pointer` (govet inline check).
- This file (formerly line 150): removed a trailing blank line at EOF
  (`git diff --check` whitespace finding).

This was a corrective fix to existing Wave 2 deliverables, not a new DAG node.
No test behavior/intent changed; `gofmt`, `go build`, `go vet`, and
`go test ./internal/hooks/claude/... -race` all pass after the fix.

## Corrective note (post-Wave-2 lint pass, round 2)

A second golangci-lint pass against the fully-integrated Wave 2 tree found 3
more errorlint findings in this role's files, the same pattern as the round-1
fix (direct `err.(*domain.Error)` type assertion instead of `errors.As`), at
locations not covered by round 1:

- `internal/hooks/claude/userpromptsubmit_test.go:106`: replaced the
  `derr, ok := err.(*domain.Error)` assertion in `TestParseUserPromptSubmit`'s
  `wantErr` branch with `var derr *domain.Error; errors.As(...)`. Added
  `"errors"` to this file's import block (not actually present despite round
  1 touching this file for an unrelated govet fix).
- `internal/providers/claude/statusline_test.go:142` and `:164`: replaced
  both `derr, ok := err.(*domain.Error)` assertions (in
  `TestParseStatusLine`'s `wantErr` branch and in
  `TestParseStatusLine_EmptySessionID`) with `errors.As` equivalents. Added
  `"errors"` to this file's import block (was not previously present).

Per the correction instructions, `golangci-lint run
./internal/hooks/claude/... ./internal/providers/claude/...` was run after
the fix (not just spot-checked at the 3 listed lines) and reports 0 issues
for both packages — no further findings of the same or a different pattern
were found in this role's files.

This was a corrective fix to existing Wave 2 deliverables, not a new DAG
node. No test behavior/intent changed; `gofmt`, `go build`, `go vet`,
`go test ./internal/hooks/claude/... ./internal/providers/claude/... -race`,
and `golangci-lint run ./internal/hooks/claude/... ./internal/providers/claude/...`
(0 issues) all pass after the fix.

## Corrective note (post-Wave-2 lint pass, round 3)

One remaining instance of the same errorlint pattern (direct
`err.(*domain.Error)` type assertion instead of `errors.As`) was found at
`internal/hooks/claude/userpromptsubmit_test.go:206`, in
`TestEncodeUserPromptSubmitResponse_UnknownDecision`, not covered by round 1
or round 2. Fixed the same way: `derr, ok := err.(*domain.Error)` / `if !ok
|| derr.Code != domain.ErrCodeValidation` became `var derr *domain.Error` /
`if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation`.
`"errors"` was already imported in this file (added during round 2), so no
import change was needed.

Unlike rounds 1 and 2, this round's authoritative check was a repo-wide
grep (`grep -rn '\.(\*domain\.Error)' --include="*.go" .`) rather than relying
on golangci-lint alone, to positively confirm no further instances of this
pattern remain anywhere in the repository. The grep returned no output after
the fix. `gofmt`, `go build`, `go vet`,
`go test ./internal/hooks/claude/... -race`, and `golangci-lint run
./internal/hooks/claude/... ./internal/providers/claude/...` (0 issues) all
pass after the fix.

---

## Wave 4

### Pre-work: merge main (Wave 3 / foundation-06 sync)

Before starting `claude-provider-05`, this branch fast-forward-merged
`origin/main` at commit `ca7062f` ("Integrate Wave 3: foundation-06/08,
predictor-05b, runtime-b01, qa-01/08") — the branch's own HEAD (`b5b606b`)
was already an ancestor of `origin/main`, so `git merge origin/main` was a
clean fast-forward (no merge commit, no conflicts). `go build ./...` and
`go test ./...` both passed cleanly against the merged tree before any
Wave-4 code was written, per the task's step-1 requirement. This pulled in
`internal/storage/sqlite/{db.go,migrate.go,migrations/0001-0004_*.sql}`
(foundation's connection/pragma/transaction engine and its own migration
range), `internal/idgen` (the real UUIDv7 `domain.IDGenerator`), and
several other roles' Wave 3 work (predictor forecast layer, runtime-b01
CLI skeleton, qa docs) — none of which this node depends on directly
except `internal/storage/sqlite`.

```yaml
node: claude-provider-05
status: completed
artifacts:
  - internal/storage/sqlite/migrations/0010_events.sql
  - internal/telemetry/claude/store.go
  - internal/telemetry/claude/store_test.go
validation:
  - "gofmt -l internal/telemetry/claude internal/storage/sqlite/migrations -> clean"
  - "go build ./...  -> ok (whole repo, post-merge)"
  - "go vet ./...  -> ok"
  - "go test ./internal/telemetry/claude/... -run Idempotent -v  -> PASS (9 tests: 1 pre-existing normalizer-level idempotency test from claude-provider-04, plus 8 new EventStore-level tests covering duplicate-write no-op, replayed-call safety, out-of-order delivery, shuffled/repeated out-of-order redelivery, concurrent duplicate writes, distinct-key non-interference, and payload-non-corruption-on-duplicate)"
  - "go test ./internal/providers/claude/... ./internal/hooks/claude/... ./internal/telemetry/claude/... -race  -> ok (full owned-package regression suite, no failures)"
  - "golangci-lint run ./...  -> 0 issues (whole repo, after one self-caught-and-fixed gofmt violation in store.go)"
commit: <recorded at end of this wave, see final report>
next_action: claude-provider-07 (blocked — explicitly out of scope for this wave per task instructions; do not start)
assumptions:
  - "Persistence target is ADD §12.2's canonical, unscoped `events` table (not a claude-provider-specific table name like `provider_events`) - migrations/0010_events.sql creates it verbatim per the ADD's column set, remapped 1:1 onto pkg/protocol/v1.Event's frozen field names (schema_version, event_id, event_type, occurred_at, observed_at, sequence, idempotency_key, source, provider, repository_id, worktree_id, session_id, turn_id, task_id, progress_node_id, payload_json). This is claude-provider's own migration range (0010-0019) per CONTRACT_FREEZE.md, and the DAG's own node title for claude-provider-05 ('Migrations 0010-0019 + persist') and qa-04's dependency wiring ('claude-provider-05, checkpoint-a07' feeding a Duplicate/OutOfOrder integration test) both confirm this is the intended durable event log, not a provider-scoped turns/quota_observations/context_observations table set. No other role's packet or the DAG claims the `events` table name, so no collision risk was found."
  - "No FK constraints from `events` to repositories/worktrees/provider_sessions/tasks, matching ADD §12.2's own literal `CREATE TABLE events` (which also declares none). This is necessary, not just convenient: this role's own provider.turn.started/turn.completed/etc. events are produced before any `turns` row exists (no role has shipped a `turns` table migration yet - it is not in foundation's 0001-0004 range and is not yet claimed by checkpoint/predictor either per a grep of the DAG), so an FK against turns would make every write from this role fail until some other role's later migration lands. Session/task/worktree/repository IDs are carried as plain, unenforced TEXT columns, consistent with the ADD's own choice."
  - "Idempotency mechanism is a partial UNIQUE index (idx_events_idempotency, ADD §12.3 verbatim: `ON events(idempotency_key) WHERE idempotency_key IS NOT NULL`) plus `INSERT ... ON CONFLICT(idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING` on the write path. The WHERE clause has to be repeated verbatim on both the index definition and the ON CONFLICT clause - SQLite rejects `ON CONFLICT(col) DO NOTHING` against a partial unique index if the conflict target's WHERE clause doesn't match the index's WHERE clause exactly ('ON CONFLICT clause does not match any PRIMARY KEY or UNIQUE constraint'). This was not known in advance; it was discovered by the first real test run against this migration and is now documented in store.go's Persist doc comment so a future reader does not have to rediscover it by trial and error."
  - "Idempotent duplicate-write semantics are a true no-op (ON CONFLICT DO NOTHING), not an upsert/overwrite and not an error. A second Persist call with the same IdempotencyKey but a (hypothetically) different EventID or Payload leaves the FIRST-written row's data untouched - verified explicitly by TestIdempotent_PayloadNotCorruptedAfterDuplicateAttempt. This mirrors CONTRACT_FREEZE.md's CompleteNodeRequest.IdempotencyKey replay contract ('same key MUST return the same result... a different payload under the same key is a conflict, not a silent overwrite') by analogy, though this node does not implement a conflict-detection/rejection path for a genuinely different payload under the same key - the digest algorithm (claude-provider-04's normalizer.go digestKey, which incorporates an observation timestamp) is relied upon to make that scenario a normalizer bug rather than a legitimate case this storage layer needs to detect. Flagged here in case a future qa-04 test wants stricter same-key-different-payload conflict detection - it does not exist in this implementation."
  - "Out-of-order delivery safety follows directly from the schema design, not from any explicit sequencing logic: `events` has no mutable per-entity 'current state' row that an out-of-order write could clobber (unlike, e.g., a `quota_observations` table keyed by (session_id, limit_id) that stores only the latest observation) - every row is independently keyed by its own EventID (primary key) and de-duplicated only by its own IdempotencyKey, so there is no ordering precondition to violate. Verified by three tests: two logically-distinct events delivered in reverse-of-occurred-at order both persist independently (TestIdempotent_OutOfOrderDelivery_BothPersistIndependently); the same logical event delivered multiple times in a shuffled, repeated order still produces exactly one row (TestIdempotent_OutOfOrderDuplicateRedelivery_StillDeduplicates); and 8 goroutines racing to persist the same idempotency key concurrently (real transaction contention, not just sequential calls) still produce exactly one row (TestIdempotent_ConcurrentDuplicateWrites_NoDuplicateRow)."
  - "EventStore.Persist uses internal/storage/sqlite's QuerierFromContext(ctx, db) to resolve either the active *sql.Tx (when called from inside a WithTx callback) or the bare connection pool otherwise, per db.go's own documented pattern for storage code written by later roles. EventStore.PersistAll is the transaction-owning convenience wrapper this role's own callers (a future claude-provider-07 or the eventual hook-to-storage wiring) are expected to use for a normalizer call's whole event batch (e.g. NormalizeStatusLine's up to 4 events, NormalizeStopFailure's up to 2) - PersistAll's own WithTx call makes a partial-batch-durable-on-error impossible, and each individual Persist call inside it is still itself an idempotent no-op on replay. No concrete production wiring from a real hook invocation to EventStore exists yet on this branch (that end-to-end assembly is out of this node's scope per the packet - claude-provider-05's own task brief is 'idempotent telemetry persistence', not the hook-to-storage integration path itself)."
  - "GetByEventID/CountByIdempotencyKey/StoredEvent are test-support-oriented read helpers on EventStore, not a general read API - documented as such in store.go. They exist because this node's own idempotency tests need to assert against actual stored row state (row count per key, unmodified original payload after a duplicate write) without depending on another role's read-path/query-service abstractions, none of which exist yet for this table."
blockers:
  - "GENUINE CROSS-ROLE TEST FALLOUT (not a defect in this node's own deliverables, and not fixed by this node since the affected file is outside this role's exclusive paths): after migrations/0010_events.sql lands, three pre-existing tests in internal/storage/sqlite/migrate_test.go (foundation's file, not claude-provider's - agents/claude-provider.md's exclusive paths list only 'internal/storage/sqlite/migrations/0010-0019_*.sql', not migrate_test.go itself) start failing: TestAllMigrations_LoadsCoreSchemaFiles (hardcodes 'len(migrations) == 4'), TestCoreMigrations_FromEmptyDatabase and TestCoreMigrations_ReopenFromFile_AppliesOnce (both hardcode 'CurrentVersion == 4, the highest foundation-06 migration'). These assertions were written against a tree where foundation-06's own 0001-0004 range was the only migration range that existed yet; sqlite.AllMigrations()'s go:embed of migrations/*.sql is, by foundation's own design (migrate.go's doc comment: '...land as additional files under migrations/ in their own commits and are picked up automatically once present, with no change needed here'), meant to pick up every later role's migrations automatically - so this fallout is a structurally inevitable, one-time consequence of ANY role (claude-provider, checkpoint, predictor, or runtime) being the first to add a second migration range, not something specific to how this node built 0010_events.sql. Per Constitution §4 rule 4, this role does not edit a file it does not own to fix another role's test; it is reported here for contract-integrator/foundation to update migrate_test.go's hardcoded counts (e.g. to assert 'contains repositories/worktrees/provider_sessions/tasks/events' rather than an exact total count, or to scope TestAllMigrations_LoadsCoreSchemaFiles to foundation's own range specifically). Confirmed narrow blast radius: `go test ./...` repo-wide shows exactly these 3 failing tests, all in internal/storage/sqlite, and no other package regresses; this role's own required validation commands (`go test ./internal/telemetry/claude/... -run Idempotent` and the full owned-package suite with -race) both pass cleanly and do not touch the affected file or package."
```

---

## Wave 5

### Pre-work: merge main (Wave 4 / foundation test fix sync)

Before starting `claude-provider-07`, this branch fast-forward-merged
`origin/main` at commit `5470e4d` ("Record Bootstrap commit SHA in
CONTRACT_FREEZE.md and progress artifact" plus Wave 4's integrated state).
This branch's own HEAD (`ad98120`, claude-provider-05) was already an
ancestor of `origin/main`, so the merge was a clean fast-forward — no merge
commit, no conflicts. Confirmed the previously-reported
`internal/storage/sqlite/migrate_test.go` blocker (hardcoded migration
counts) was fixed on `main` in the interim: the hardcoded `len(migrations)
== 4`/`CurrentVersion == 4` assertions are gone; the file now asserts
range-agnostic conditions (e.g. `len(migrations) < len(want)`). `go build
./...` and `go test ./...` both passed cleanly against the merged tree
before any Wave-5 code was written.

```yaml
node: claude-provider-07
status: completed
artifacts:
  - internal/telemetry/claude/fixture_suite_test.go
  - testdata/provider-events/claude/statusline/compacted.json
validation:
  - "gofmt -l internal/providers/claude internal/hooks/claude internal/telemetry/claude -> clean"
  - "go build ./internal/providers/claude/... ./internal/hooks/claude/... ./internal/telemetry/claude/... -> ok"
  - "go vet ./internal/providers/claude/... ./internal/hooks/claude/... ./internal/telemetry/claude/... -> ok"
  - "go test ./internal/providers/claude/... ./internal/telemetry/claude/... -run Fixture -v -> PASS (TestFixtureSuite: 14 fixture subtests covering normal/missing-null/compacted/high-usage/unknown-field/Stop/rate-limit-StopFailure; TestFixture_DuplicateEvents_Idempotent: 4 duplicate-delivery subtests; TestFixture_RawTextNeverPersistedOrLogged: the privacy gate, 1 test covering 8 raw-text fixtures + 4 malformed-payload error paths + 1 validation-error path)"
  - "go test ./internal/providers/claude/... ./internal/hooks/claude/... ./internal/telemetry/claude/... -race -> ok (full owned-package regression suite, no failures)"
  - "go build ./... -> ok (whole repo)"
  - "go vet ./... -> ok (whole repo)"
  - "go test ./... -> ok (whole repo, no regressions in any other role's package)"
  - "golangci-lint run ./... -> 0 issues (whole repo)"
commit: <recorded at end of this wave, see final report>
next_action: none — claude-provider-07 was this role's only Wave 5 node; no further claude-provider DAG nodes assigned as of this wave
assumptions:
  - "PreCompact-adjacent 'compacted' fixture category: this branch has no dedicated PreCompact/PostCompact hook parser (agents/claude-provider.md P0 deliverable #1 lists PreCompact as optional, 'when fixtures are available' - none were built in Waves 1-4, and building one was explicitly out of scope for this node, which is a fixtures+tests node against EXISTING parsers only). The observable signature of 'a compaction just happened' that the EXISTING status-line parser can actually see is modeled instead: testdata/provider-events/claude/statusline/compacted.json has LOW current context usage (used_percentage: 4.53, small token counts) alongside a HIGH cumulative cost/duration/LOC total (total_cost_usd: 12.9, total_duration_ms: 5184320) - the signature of a long-running session whose context window was just reset by a compaction, as opposed to a genuinely fresh/short session (which would have low cumulative cost too). TestFixtureSuite's statusline/compacted subtest asserts both halves of this shape explicitly (context_used_percent < 10 AND total_cost_usd >= 5) so the fixture cannot silently drift into no longer representing a post-compaction snapshot. This is a documented scope decision, not an oversight - a future PreCompact/PostCompact hook parser node (if one is ever assigned) should add its own dedicated fixtures/tests rather than retrofitting this one."
  - "'Duplicate events' fixture category: rather than adding new byte-identical throwaway fixture files, this node reuses four existing normal.json fixtures (statusline, userpromptsubmit, stop, stopfailure/rate_limit) fed through the full parse->normalize->persist pipeline TWICE each, with the second delivery using a Normalizer whose seqIDs IDGenerator is seeded at a disjoint offset (mirroring store_test.go's own TestIdempotent_ConcurrentDuplicateWrites_NoDuplicateRow pattern) so the two deliveries produce genuinely distinct EventIDs sharing the same IdempotencyKey - matching what two real hook invocations of the same underlying observation, using a real UUIDv7 IDGenerator, would produce. This follows claude-provider-04's own Wave-2 lessons-learned recommendation ('recommend other normalization-layer nodes in future waves do the same rather than duplicating fixtures one layer up'). A self-inflicted test bug was caught and fixed during this node's own work: an early draft used two independently-constructed newTestNormalizer() calls, whose seqIDs counters both start at 0 - this made the two deliveries' EventIDs COINCIDENTALLY IDENTICAL, so the dedup assertion passed for the wrong reason (event_id PRIMARY KEY collision, not idempotency_key UNIQUE-index dedup). Caught by an explicit assertion that the second delivery's EventID must NOT be independently retrievable, which failed until the seqIDs offset fix was applied - documented here as a real pitfall for any future test that constructs two 'independent' fake ID generators and assumes they produce non-colliding output without arranging for it explicitly."
  - "Privacy gate (TestFixture_RawTextNeverPersistedOrLogged) scope, self-verified before reporting complete: this test was sanity-checked by deliberately injecting a raw-prompt-text leak into normalizer.go's NormalizeUserPromptSubmit payload (a throwaway 'debug_leak_sanity_check' field literally containing the known fixture's raw prompt string), confirming the test FAILS loudly at three independent points (JSON-marshaled Event, %#v Go-dumped Event, and the raw persisted SQLite row's payload_json column), then reverting the injection (git diff confirmed clean revert, no trace left in the committed code). This was done specifically because a privacy-gate test that only ever passes is unfalsifiable evidence - the DAG's own risk note ('hard privacy gate') warranted proving the gate actually gates something, not just trusting the assertions read correctly by inspection."
  - "The privacy gate's 8 raw-text fixtures (allRawTextFixtures table) were chosen as every fixture in this branch's existing corpus that embeds real, sensitive-shaped text (3 userpromptsubmit prompts across normal/unknown_fields/missing_fields; 5 stopfailure error messages across rate_limit/overloaded/network/context_length/unknown_category) - deliberately broader than just the 'normal' cases privacy_test.go already covered (claude-provider-04's own two tests), since a leak that only manifests under an unusual field combination (e.g. unknown_fields.json's extra top-level keys) is still a privacy bug this gate should catch. A self-check loop in the test itself (`strings.Contains(string(raw), rf.needle)`) fails loudly if a listed needle ever drifts out of sync with its fixture file's actual content, so the table cannot silently stop testing what it claims to test."
  - "Explicit scope boundary documented in the test's own doc comment (per the task's request to 'explicitly confirm the raw-prompt-absence test's scope... so the lead can independently verify'): checked = every column of the persisted `events` row (via raw SQL, not the typed StoredEvent struct, so a future schema/column addition is not silently skipped) for all 8 raw-text fixtures; the full v1.Event JSON marshal and Go %#v dump for the same 8 fixtures; the parsed intermediate struct's Go %#v dump (UserPromptSubmitEvent/StopFailureEvent) before normalization even runs; every malformed.json's returned parse-error Error() string across all 4 hook/provider directories; one explicit missing-session_id validation-error case. NOT checked (explicitly out of scope for this role/node, left for qa-05's later leakage scanner per this node's own DAG entry 'Feeds qa-05 leakage scanner'): actual process stdout/stderr of a running `auspex` CLI binary (does not exist yet on this branch), a full SQLite file export, or an on-disk log file - this node covers the package-level surface this role owns and controls directly, not the end-to-end process/file-system surface."
  - "This branch has no logging framework call sites (no `log`/`slog` imports) anywhere in internal/providers/claude, internal/hooks/claude, or internal/telemetry/claude as of this node - verified by repo grep before writing the privacy test's 'log output' coverage claim. The only text that could reach an operator's terminal or a log aggregator from these packages is a returned Go `error`'s formatted string (domain.Error.Error() or an fmt.Errorf-wrapped variant) - every error-producing path in this pipeline (JSON syntax errors via all 4 directories' malformed.json, the missing-session_id validation path) is exercised by this node's privacy test specifically because it is the only 'log output' surface that actually exists to check."
blockers: []
```
