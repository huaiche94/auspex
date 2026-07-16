# Auspex

> 🌐 English | [繁體中文](README.zh-TW.md)

## Why Auspex, when agents already manage their own context?

They do — and increasingly well. Claude Code ships layered compaction:
bulky tool outputs are offloaded to disk early, the conversation is
auto-summarized near the context limit, and recent files and todos are
rehydrated afterward so the session keeps its momentum. Codex CLI
compacts server-side through a dedicated endpoint and re-reads recently
edited files after each pass. Both vendors now expose compaction directly
in their APIs. Recycling a full context window is a solved — and rapidly
commoditizing — problem.

Auspex doesn't compete with any of that. It covers the three things
native compaction doesn't:

**1. Quota is not context.**
Compaction keeps a session alive past the context ceiling, but it does
nothing when your usage window runs dry at 2 a.m. The session simply
dies, and every hour until you wake up is wasted. Auspex's Graceful Pause
watches quota runway, finds a safe stopping point before the wall,
checkpoints progress, and writes a wake task to local SQLite. The daemon
revalidates quota and repo state, then resumes the run — surviving
crashes and reboots along the way. Native compaction has no answer here;
this is Auspex's home turf.

**2. Compaction is lossy, and nobody audits the result.**
Every summarization pass drops detail the earlier turns had accumulated —
that's inherent to compression, not a bug to fix. The native mechanisms
trust their own summaries; no independent check verifies that the agent
stayed on course afterward. Auspex doesn't try to make the summary
perfect. It makes the loss survivable: State Checkpointing requires
verifiable evidence — test artifacts, checksums, Git snapshots — before
any work unit can be marked completed. An agent that drifts after a
compaction fails the evidence gate instead of silently shipping
regressions. The gate lives outside the context window, so it never
forgets.

**3. Sessions end. Work shouldn't.**
Native context management lives and dies with the process. Auspex
persists progress trees, wake tasks, and decisions in SQLite, so an
interrupted run — quota exhaustion, crash, reboot — picks up where it
left off instead of starting over.

**The one-line version**

Auspex doesn't do compaction. It supervises it.

The agent handles the page-turn. Auspex makes sure state is solidified
before the page turns (`CHECKPOINT_AND_RUN`), that output after the turn
still clears the evidence gate, and that quota interruptions become
pauses instead of dead sessions. This makes Auspex complementary to the
vendors' roadmaps, not a race against them: the better native compaction
gets, the longer the tasks people dare to run unattended — and the more a
supervision layer matters.

**Auspex is a local-first predictive runtime guard for AI coding agents.**
Before each turn with a provider like Claude Code or Codex CLI, it
estimates what the turn will cost — scope, tokens, quota fit,
blast-radius risk — and applies policy: run it, warn, require a
checkpoint first, split it, pause gracefully, or block it.

Checkpoint/resume/memory tools answer *"how do we continue?"*. Auspex
answers the question that comes first: **"should we even start this
turn?"** (The Latin *auspex* is the diviner who reads the omens before an
undertaking begins and rules whether it may proceed.)

## What it does, in one session

Once wired into Claude Code or Codex CLI (see
[Quick start](#quick-start)), every prompt you submit is evaluated
before it runs. This is real output from
one of this repository's own development sessions — Auspex dogfoods
itself daily:

```text
Auspex forecast (uncalibrated estimate — scores are not probabilities):
  scope: ~1–4 files changed, ~30–180 lines, ~2–6 files read (P50–P90)
  tokens: P50 3782 / P80 5732 / P90 7564
  cost: ~$0.04–$0.38 USD (estimate)
  context: P90 ~3% of window (projected)
  risk: 0.50/1.00 overall — QUOTA_UNKNOWN, PREDICTION_COLD_START
  policy: WARN
```

The evaluation feeds a policy engine with **eight frozen actions**
(`RUN`, `WARN`, `REQUIRE_CONFIRMATION`, `CHECKPOINT_AND_RUN`, `SPLIT`,
`PAUSE`, `PAUSE_AND_AUTO_RESUME`, `BLOCK`). The decision returns to the
agent through the hook response — an allowed prompt passes through, a
blocked one carries a machine-readable reason the agent itself can act
on. Alongside the per-prompt gate, Auspex maintains:

- **A Progress Tree** — the canonical, durable task state. A node may
  not be marked complete without validator-checked evidence (a file, DB
  record, checksum, or Git snapshot); "the agent said it's done" never
  counts.
- **State + repository checkpoints** — every node completion creates a
  state checkpoint atomically; repository checkpoints capture the
  worktree (with secret redaction) without ever committing your branch.
- **Graceful Pause** — when the quota window is about to run out,
  Auspex checkpoints, interrupts at a safe point, and persists a durable
  wake job in SQLite. The daemon (`auspex daemon`) executes due wake
  jobs unattended; resume re-verifies repository, quota, session, and
  authorization before running.

Everything is local: one static Go binary, one SQLite database under
your OS user-data directory, no cloud services. Raw prompt text and tool
output are never persisted by default — only extracted features.

## Quick start

Requires Go 1.26.5 (pinned in `go.mod`); no CGO, no external services.

```bash
go build -o auspex ./cmd/auspex
./auspex version
./auspex doctor      # creates + migrates the SQLite DB, then verifies it
```

`doctor` is meaningful immediately after building: the first run creates
the database under the OS user-data directory (macOS:
`~/Library/Application Support/auspex/`, Linux: `$XDG_DATA_HOME/auspex/`)
and reports each check (`database`, `config`, …) with a per-check status.

To wire it into Claude Code, follow
[`integrations/claude/`](integrations/claude/README.md): it ships the
`hooks.json`/`plugin.json` examples that route Claude Code's
UserPromptSubmit / Stop / StopFailure / statusline events through
`auspex hook claude <event>`, plus `auspex init` to register the current
repository. Codex CLI is wired the same way:
[`integrations/codex/hooks.json`](integrations/codex/hooks.json) routes
its SessionStart / UserPromptSubmit / Stop events through
`auspex hook codex <event>` (hook argv is kebab-case, ADR-050). In both
cases the Stop-side capture records exact per-turn token usage — all
four token classes, Claude from the session transcript (ADR-051), Codex
from the session rollout JSONL — numbers only, never prompt or output
text. The hooks **fail open** — an Auspex crash never blocks your
session; run `auspex evaluate` directly to surface real errors.

### The command tree

```text
auspex evaluate               estimate a prompt before running it (--json)
auspex decision allow|deny    consume a one-time authorization (replays rejected)
auspex checkpoint create      state + repository checkpoint (never commits your branch)
auspex progress ...           inspect the Progress Tree; evidence-gated completion
auspex pause request|cancel   safe-point pause with a durable wake job
auspex resume                 re-verified resume
auspex scheduler run-once     execute due wake jobs without the daemon
auspex daemon ...             background daemon + authenticated loopback HTTP API
auspex run ...                one-shot prompt under the managed gate (claude|codex)
auspex init                   register the current repository/session
auspex status | doctor        session/checkpoint/pause state; environment health
auspex gc                     tiered telemetry retention (90-day default, ADR-046)
auspex export                 de-identified datasets for offline analysis
auspex hook claude <event>    the four hook entrypoints Claude Code calls
auspex hook codex <event>     the Codex CLI hook entrypoints (same gate)
auspex hook codex status      stdin-less status line for tmux/scripts (--cwd DIR)
```

Every command speaks schema-versioned JSON on stdout (`--json`, FR-160)
and fails with one typed error shape, so both humans and agents can
consume it:

```json
{"schema_version":"auspex.error.v1","code":"validation",
 "message":"pause request: --reason must be one of \"calibrated_hit_probability\", \"emergency_uncalibrated\"",
 "retryable":false,"details":{"reason":"quota_hit"}}
```

A VS Code companion extension ([`vscode/`](vscode/README.md)) renders
the daemon's per-session status view — risk, runway, quota freshness,
progress, checkpoints, and pause state, where unknown renders as
"unknown", never as a fabricated zero — plus the wake-job queue with an
inline cancel button for scheduled resumes; it is used from source or a
locally packaged VSIX until the marketplace publisher is registered
([#18](https://github.com/huaiche94/auspex/issues/18)).

## Project status

The full vertical slice — 85/85 DAG nodes across seven roles, Bootstrap
through the Stage-5 integration gate — is integrated on `main`, followed
by the post-slice backlog: the daemon with its authenticated loopback
API ([#7](https://github.com/huaiche94/auspex/issues/7)), native-hook
session bootstrap ([#17](https://github.com/huaiche94/auspex/issues/17)),
the per-prompt forecast surface
([#14](https://github.com/huaiche94/auspex/issues/14)), tiered telemetry
retention (ADR-046), real repository-checkpoint restore
([#6](https://github.com/huaiche94/auspex/issues/6)), and the VS Code
companion ([#10](https://github.com/huaiche94/auspex/issues/10)), now
fed by a daemon session-status API (`GET /v1/session/status`,
`auspex.daemon.session_status.v1`). Codex CLI is a first-class second
provider ([#9](https://github.com/huaiche94/auspex/issues/9)): both
native hooks (`auspex hook codex <event>`) and the managed one-shot
(`auspex run --provider codex`, over `codex exec --json`) ship; what
remains in #9 is the M7 Phase-2 tail — app-server subscription,
graceful interrupt, `codex exec resume`. Native-hook sessions capture
exact per-turn token usage for both providers — Claude from the Stop
transcript (ADR-051), Codex from the session rollout JSONL — and live
runway forecasts computed from that real quota telemetry feed the
policy's runway reason codes plus an in-horizon statusline hint
(`⏳ runway ~Ns`). This repository's own sessions feed telemetry into a
local Auspex daily.

**The honest caveat:** every forecast is still produced by cold-start
rules, not calibrated models. Scores are not probabilities and are
labeled that way on every surface (Constitution §7 rule 7). The token
forecast in particular barely responds to the prompt yet
([#42](https://github.com/huaiche94/auspex/issues/42)). The calibration
*rails* are all in place — predicted-vs-actual pairs for cost, duration,
and exact tokens accumulate from normal use, and the first field
dataset already quantifies the gap: the cold-start cost forecast
under-forecasts real cost roughly 7–9× at the median, driven by
cache-read-blind pricing
([#66](https://github.com/huaiche94/auspex/issues/66)). The
fit-and-feed-back step that turns those pairs into calibrated forecasts
is the open M13 milestone
([#11](https://github.com/huaiche94/auspex/issues/11)). External research
backs this stance rather than undercutting it: a study of eight frontier
agents on SWE-bench (Bai et al.,
[arXiv:2604.22750](https://arxiv.org/abs/2604.22750), 2026) finds token
use can vary up to 30× across identical runs and that models predict
their own cost only weakly (correlation ≤ 0.39, systematically low) — so
a coarse, uncalibrated range is the honest ceiling, not a temporary one.
Auspex's value is therefore in the **decision it gates** — checkpoint,
pause, resume, block — not in the precision of the number it prints.

Open roadmap milestones: the Codex M7 Phase-2 tail — app-server
subscription, graceful interrupt, `codex exec resume`
([#9](https://github.com/huaiche94/auspex/issues/9)); the managed shell
mode (M11, [#8](https://github.com/huaiche94/auspex/issues/8)); the
calibration fit-and-feed-back pipeline (M13,
[#11](https://github.com/huaiche94/auspex/issues/11)); pre-release
namespace claims ([#18](https://github.com/huaiche94/auspex/issues/18));
tool-operation capture with spin detection and phase-aware gating
([#67](https://github.com/huaiche94/auspex/issues/67)/[#68](https://github.com/huaiche94/auspex/issues/68),
ADR-gated); research-derived forecast upgrades
([#65](https://github.com/huaiche94/auspex/issues/65), the forecast half
of [#66](https://github.com/huaiche94/auspex/issues/66),
[#42](https://github.com/huaiche94/auspex/issues/42),
[#20](https://github.com/huaiche94/auspex/issues/20) — data-gated); and
a signal-handling fix for the managed runner
([#88](https://github.com/huaiche94/auspex/issues/88)). The
[issue tracker](https://github.com/huaiche94/auspex/issues) is the live
backlog. Work is milestone-gated: nothing is implemented ahead of its
milestone (`docs/design/Auspex_ADD.md` §31).

Research-grounded additions distilled from Bai et al. (above) — a
cache-aware four-class cost model (its capture half has landed; the
forecast half is open in #66), a repeated-file-operation risk signal
that catches a spinning turn by *observation* instead of prediction, and
phase-aware conditional forecasting — are captured as roadmap notes (as
external priors, never as fitted numbers) in
[`docs/backlog/token-cost-prediction-research.md`](docs/backlog/token-cost-prediction-research.md).

## Validate a change

The local pre-commit bar, and exactly what CI
([`.github/workflows/ci.yml`](.github/workflows/ci.yml)) runs on
ubuntu-latest, macos-latest, and windows-latest — all three
hard-blocking:

```bash
gofmt -l . && go build ./... && go vet ./...
go test ./... -race
golangci-lint run ./...
```

## Repository layout

```text
cmd/auspex/           CLI entrypoint (thin main; wiring in internal/app)
internal/             application core, domain model, adapters (Go)
pkg/protocol/v1/      public wire protocol types
integrations/claude/  Claude Code hook wiring (hooks.json / plugin.json)
integrations/codex/   Codex CLI hook wiring (hooks.json)
vscode/               VS Code companion extension (TypeScript)
schemas/              JSON Schemas for the frozen wire shapes
research/             offline Python analysis — never a runtime dependency
agents/               role definitions from the multi-agent build
docs/                 design docs, ADRs, decision log, build history
testdata/             cross-package fixtures (checkpoints, provider events)
```

Every folder has its own `README.md` introducing what lives there, and
every documentation file has a Traditional Chinese sibling
(`<name>.zh-TW.md`, ADR-049). The authoring language is normative:
English for everything except `docs/design/Auspex_ADD.md` and
`docs/DECISION_LOG.md`, which are authored in Traditional Chinese.

## Where to read next

| You want to… | Read |
|---|---|
| Understand the architecture | [`docs/design/Auspex_ADD.md`](docs/design/Auspex_ADD.md) — the single authoritative architecture/requirements spec, authored in Traditional Chinese (normative as written); amended only by ADRs under [`docs/adr/`](docs/adr/) |
| Contribute (human or agent) | [`CONSTITUTION.md`](CONSTITUTION.md) (process authority) → [`CONTRIBUTING.md`](CONTRIBUTING.md) → [`AGENTS.md`](AGENTS.md) |
| See how the predictor works | [`docs/design/Auspex_Predictor_Design_Supplement.md`](docs/design/Auspex_Predictor_Design_Supplement.md) + [`internal/predictor/`](internal/predictor/README.md) |
| Trace how this repo was built | [`docs/implementation/vertical-slice/`](docs/implementation/vertical-slice/README.md) — the execution DAG, wave-by-wave integration history, per-role progress logs |
| Reuse the multi-agent process | [`docs/methodology/`](docs/methodology/README.md) |
| Browse all documentation | [`docs/README.md`](docs/README.md) |

`CONSTITUTION.md` governs process; `docs/design/Auspex_ADD.md` governs
architecture. When any other document disagrees with them, those two
win (Constitution §1–§2).

## Tech stack & license

Go 1.26.5 single static binary with SQLite (WAL) · TypeScript only in
the VS Code extension · Python 3.12+ for offline research only ·
Apache-2.0 (see [`LICENSE`](LICENSE), [`NOTICE`](NOTICE)).
