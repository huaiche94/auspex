// provider.go: the per-provider spec behind Runner.Run — issue #9 M7
// Phase 1's minimal generalization of the issue-#8 claude-only gate. One
// managed run's shape is provider-independent (gate -> spawn argv-only ->
// parse stdout -> persist terminal events); everything provider-SPECIFIC
// is pinned in one providerSpec value per provider: the default binary,
// the exact argv, the stream parser, and the terminal-event normalizer.
// Run consults the table and nothing else, so claude's exported behavior
// (argv shape, event set, error contract) is byte-identical to the
// pre-generalization runner, and adding a provider is adding a table row
// — never another branch in Run.
package managed

import (
	"io"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/orchestrator"
	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"
	codextelemetry "github.com/huaiche94/auspex/internal/telemetry/codex"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// ProviderCodex is the Codex managed one-shot provider (issue #9 M7
// Phase 1 — ADD §21.8's `codex exec --json` path).
const ProviderCodex = "codex"

// DefaultCodexBin is the provider binary spawned for ProviderCodex when
// Runner.ProviderBin is empty: the user's own `codex` CLI, resolved from
// PATH exactly like DefaultProviderBin.
const DefaultCodexBin = "codex"

// providerSpec is one provider's managed-run recipe. Every field is
// required; specFor is the only lookup path.
type providerSpec struct {
	// defaultBin is the binary spawned when Runner.ProviderBin is empty.
	defaultBin string

	// invocationMode is the provider_sessions.invocation_mode the
	// session bootstrap registers for this provider's managed runs. Both
	// current specs use InvocationModeManagedStreamJSON — ADD §16's
	// policy vocabulary ([managed_app_server, managed_stream_json])
	// deliberately describes HOW the session is driven (a managed
	// one-shot over a JSON-line stdout stream), not the provider's own
	// format name, and codex exec --json is exactly that; a future
	// app-server adapter is where a second value appears.
	invocationMode string

	// argv builds the full provider argv after argv[0] for one prompt —
	// always exec argv elements, never a shell string (Constitution §7
	// rule 5).
	argv func(prompt string) []string

	// consume parses the provider's stdout stream to EOF (relaying raw
	// lines to relay when non-nil) and records the provider-specific
	// summary onto the outcome — RunOutcome.Stream for claude,
	// RunOutcome.Codex for codex; each provider's field stays zero for
	// the other.
	consume func(r io.Reader, relay io.Writer, outcome *RunOutcome)

	// terminalEvents normalizes the finished (or failed-to-spawn) run
	// into its terminal event batch via the provider's own telemetry
	// normalizer — the sole path from that provider's payloads into the
	// frozen envelope (each telemetry package's own contract). clock/ids
	// come from Runner.Hooks and are validated non-nil before any spec
	// function runs.
	terminalEvents func(clock domain.Clock, ids domain.IDGenerator, req RunRequest, turnID domain.TurnID, exitCode int, outcome RunOutcome, spawnFailed bool) []v1.Event
}

// providerSpecs is the closed set of providers this increment supports.
var providerSpecs = map[string]providerSpec{
	ProviderClaude: {
		defaultBin:     DefaultProviderBin,
		invocationMode: orchestrator.InvocationModeManagedStreamJSON,
		// ADD §22.1's supported managed path:
		// `claude -p <prompt> --output-format stream-json --verbose`.
		argv: func(prompt string) []string {
			return []string{"-p", prompt, "--output-format", "stream-json", "--verbose"}
		},
		consume: func(r io.Reader, relay io.Writer, outcome *RunOutcome) {
			outcome.Stream = readStream(r, relay)
		},
		terminalEvents: claudeTerminalEvents,
	},
	ProviderCodex: {
		defaultBin:     DefaultCodexBin,
		invocationMode: orchestrator.InvocationModeManagedStreamJSON,
		// ADD §21.8's exact invocation: `codex exec --json "prompt"` —
		// the prompt as a single positional argv element (verified
		// against codex-cli v0.144.4's `codex exec --help`).
		argv: func(prompt string) []string {
			return []string{"exec", "--json", prompt}
		},
		consume: func(r io.Reader, relay io.Writer, outcome *RunOutcome) {
			outcome.Codex = readCodexStream(r, relay)
		},
		terminalEvents: codexTerminalEvents,
	},
}

// specFor resolves the providerSpec for provider; ok=false means the
// provider is not supported by this increment (Run's validation turns
// that into the typed error).
func specFor(provider string) (providerSpec, bool) {
	spec, ok := providerSpecs[provider]
	return spec, ok
}

// claudeTerminalEvents projects a claude managed run's outcome through
// internal/telemetry/claude.NormalizeManagedRun — the exact body the
// pre-generalization persistTerminal ran, moved verbatim behind the spec
// seam.
func claudeTerminalEvents(clock domain.Clock, ids domain.IDGenerator, req RunRequest, turnID domain.TurnID, exitCode int, outcome RunOutcome, spawnFailed bool) []v1.Event {
	o := claudetelemetry.ManagedRunOutcome{
		SessionID:   req.SessionID,
		TurnID:      turnID,
		WorktreeID:  req.WorktreeID,
		TaskID:      req.TaskID,
		ExitCode:    exitCode,
		SpawnFailed: spawnFailed,
		// The stream's own model declaration (system init line), "" when
		// never observed — the normalizer stamps it onto the usage event
		// so ADR-047's cohort ladder can family-label the token sample
		// without guessing.
		ModelID: outcome.Stream.Model,
	}
	if res := outcome.Stream.Result; res != nil {
		o.ResultSeen = true
		o.ResultSubtype = res.Subtype
		o.IsError = res.IsError
		o.DurationMs = res.DurationMs
		o.DurationAPIMs = res.DurationAPIMs
		o.NumTurns = res.NumTurns
		o.TotalCostUSD = res.TotalCostUSD
		o.ResultTextLen = res.ResultTextLen
		if u := res.Usage; u != nil {
			o.InputTokens = u.InputTokens
			o.OutputTokens = u.OutputTokens
			o.CacheReadInputTokens = u.CacheReadInputTokens
			o.CacheCreationInputTokens = u.CacheCreationInputTokens
		}
	}
	return claudetelemetry.NewNormalizer(clock, ids).NormalizeManagedRun(o, clock.Now())
}

// codexTerminalEvents projects a codex managed exec run's outcome through
// internal/telemetry/codex.NormalizeManagedExec (that package's mapping
// table documents every stream-event -> EventType decision).
func codexTerminalEvents(clock domain.Clock, ids domain.IDGenerator, req RunRequest, turnID domain.TurnID, exitCode int, outcome RunOutcome, spawnFailed bool) []v1.Event {
	o := codextelemetry.ManagedExecOutcome{
		SessionID:         req.SessionID,
		TurnID:            turnID,
		WorktreeID:        req.WorktreeID,
		TaskID:            req.TaskID,
		ExitCode:          exitCode,
		SpawnFailed:       spawnFailed,
		ThreadStartedSeen: outcome.Codex.ThreadStartedLines > 0,
		ThreadID:          outcome.Codex.ThreadID,
		TurnCompletedSeen: outcome.Codex.Completed != nil,
		TurnFailedSeen:    outcome.Codex.Failed != nil,
		ErrorEvents:       outcome.Codex.ErrorLines,
	}
	if failed := outcome.Codex.Failed; failed != nil {
		o.FailureMessageLen = failed.MessageLen
	}
	if completed := outcome.Codex.Completed; completed != nil && completed.Usage != nil {
		u := *completed.Usage
		o.Usage = &u
	}
	return codextelemetry.NewNormalizer(clock, ids).NormalizeManagedExec(o, clock.Now())
}
