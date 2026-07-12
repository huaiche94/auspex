# claude-provider — Day-1 progress artifact

Role: `claude-provider` (`agents/claude-provider.md`).
Wave: DAG parallel-root nodes `claude-provider-01`, `claude-provider-02`, `claude-provider-03` (per `docs/implementation/day1/EXECUTION_DAG.md`).
Branch: `day1/claude-provider` (local only — not pushed, not merged into `main`).

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
  - "No live Claude Code account was available this wave (per task brief). All 5 status-line fixtures were hand-constructed from agents/claude-provider.md and Preflight_ADD.md §22.5/§22.10's described field names (session_id, model.id/display_name, workspace.current_dir/project_dir, context_window.total_input_tokens/total_output_tokens/context_window_size/used_percentage, cost.total_cost_usd/total_duration_ms/total_api_duration_ms/total_lines_added/total_lines_removed, rate_limits.five_hour/seven_day.used_percentage/resets_at) rather than captured from a real session. Field names may not match the live API exactly; ADD Appendix H's official doc URLs (code.claude.com/docs/en/statusline) were not fetched (no external network access this wave) and could not be cross-checked."
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
  - "On internal/parse failure, the hook wrapper is expected to call FallbackAllowResponse() to fail open (never block the user due to a Preflight bug) - this fail-open behavior for a hook-path *operational* failure is consistent with CONTRACT_FREEZE.md's fail-open/fail-closed split (operational observation failures may fail open) but the fallback wiring itself belongs to claude-provider-06's CLI wrapper, not this node - only the primitive (FallbackAllowResponse) is delivered here."
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
  - "manual: structural assertion script confirms UserPromptSubmit/Stop/StopFailure hook entries and the statusLine entry all invoke `preflight hook claude <subcommand>` with type=command, and plugin.json matches ADD Appendix E.2 verbatim"
  - "go build ./...  -> ok (no .go files added by this node; confirms no accidental breakage)"
next_action: none — Wave 2 scope (claude-provider-04, claude-provider-06) complete; claude-provider-05/-07 explicitly out of scope for this wave per task brief
commit: 0dbe22b9b15506eb61daf5d97cb4363fbb8c2ec0
assumptions:
  - "This is an explicitly forward-looking stub per the DAG's own note on claude-provider-06 ('stub acceptable before [runtime-b01]'): the `preflight` CLI binary and its `hook claude ...` subcommands do not exist on this branch (runtime-b01, a different role's later node, not built yet). The example configuration is syntactically valid and internally consistent with this role's Wave-1/Wave-2 primitives but is not exercisable end-to-end (`preflight hook claude user-prompt-submit < fixture` per the DAG's literal validation command cannot run — there is no `preflight` binary yet)."
  - "plugin.json is copied verbatim from Preflight_ADD.md Appendix E.2, which is explicitly this role's documented ownership ('Appendix E.2/E.3' in agents/claude-provider.md)."
  - "hooks.json's overall JSON shape ({\"hooks\": {\"<HookEventName>\": [{\"hooks\": [{\"type\": \"command\", \"command\": \"...\"}]}]}}) is taken from Preflight_ADD.md Appendix E.3. This role's hooks.json only wires the three hook events this role has actually built parsers/normalizers for (UserPromptSubmit, Stop, StopFailure) plus the statusLine entry (§22.5), deliberately omitting Appendix E.3's SessionStart/TaskCreated/TaskCompleted/PreCompact/PostToolUse/PostToolUseFailure entries since this role has not built parsers for those (packet's P0 deliverables list them as optional, 'when fixtures are available' — none were built this wave) and wiring a hook with no corresponding parser would be example configuration promising behavior that doesn't exist yet."
  - "GENUINE UNRESOLVED DOCUMENT CONFLICT (recorded per task instructions' STOP-and-report guidance, though this one did not block the node — a defensible default was available and taken): Preflight_ADD.md Appendix E.3 writes CLI subcommands in PascalCase (`preflight hook claude UserPromptSubmit`), matching Claude Code's own wire-level hook_event_name casing. agents/runtime.md's P0 commands list AND docs/implementation/day1/EXECUTION_DAG.md's own validation command for this exact node (claude-provider-06) both write kebab-case (`preflight hook claude user-prompt-submit`). Per Constitution §2's document priority order, the ADD (priority 2) would outrank agents/runtime.md (priority 4) if this is read as a real conflict rather than a casing typo in one of the two documents. This role does not own either document and cannot fix it. hooks.json here follows the DAG/runtime.md kebab-case form since it is this specific node's own frozen validation command; the discrepancy is written up in full in integrations/claude/README.md for contract-integrator to reconcile (likely by fixing Appendix E.3 to match standard kebab-case CLI convention, since agents/runtime.md's P0 command list reads as the more deliberate, recently-authored source for exact CLI spelling)."
  - "Preflight_ADD.md §22.6 ('Compose existing status line' — installer must read/preserve a pre-existing status-line command and combine output rather than overwrite it) is installer behavior, not expressible in a static example config file, and is explicitly out of scope for this node — noted in the README rather than silently ignored."
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
