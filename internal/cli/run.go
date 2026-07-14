// run.go: the REAL `auspex run` command (issue #8's managed one-shot
// MVP, ADD §8.1). Shape: `auspex run --provider claude --session-id <id>
// --worktree-id <id> [--task-id <id>] [--provider-bin <path>] --
// <prompt>`. All orchestration lives in internal/managed.Runner (gate ->
// spawn -> parse -> persist); this file only maps flags/args to a
// RunRequest and renders the outcome, per ADD §10.1's "business logic
// stays out of RunE handlers".
//
// Output discipline (agents/runtime.md Part B "JSON and errors"): stdout
// carries EXACTLY one thing — the schema-versioned `auspex.run.v1`
// attribution JSON printed after the provider exits. Everything
// human-facing (the runner's decision line, the forecast card, the
// provider's relayed stream-json lines and its own stderr) goes to
// stderr, so scripts can pipe stdout without scraping. A BLOCK decision
// or spawn failure surfaces as the typed error envelope on stderr (via
// WithJSONErrorRendering) with NO attribution JSON — there is no run to
// attribute.
//
// Privacy (Constitution §7 rule 2): the prompt is taken from argv (the
// user's own command line), handed to managed.Runner which hashes it for
// the gate and passes it argv-only to the provider process; it is never
// logged, never echoed into any output this command writes, and never
// persisted.
package cli

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/managed"
	"github.com/huaiche94/auspex/internal/orchestrator"
)

// NewRunCmd builds the REAL `auspex run` command, wired against deps
// (the same orchestrator.HookDeps the hook/evaluate surfaces share —
// deliberately, so the managed gate is the same production evaluation
// path). This is the constructor internal/app/wiring.App.RootCmd() swaps
// in for root.go's `run` stub, the same stub-then-swap pattern
// `evaluate`/`hook`/`progress` follow. Exported for the same reason as
// NewEvaluateCmd: internal/app/wiring is a different package.
func NewRunCmd(deps orchestrator.HookDeps) *cobra.Command {
	var provider, sessionID, worktreeID, taskID, providerBin string
	cmd := &cobra.Command{
		Use:   "run [flags] -- <prompt>",
		Short: "Run a provider one-shot prompt under Auspex's managed gate",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if sessionID == "" {
				return &domain.Error{Code: domain.ErrCodeValidation, Message: "run: --session-id is required", Retryable: false}
			}
			if worktreeID == "" {
				return &domain.Error{Code: domain.ErrCodeValidation, Message: "run: --worktree-id is required", Retryable: false}
			}

			// The provider runs where the user ran auspex; an unresolvable
			// cwd degrades to "" (bootstrap and Dir-dependent behavior
			// skip, per managed.RunRequest's contract) rather than
			// failing a run the OS would have started anyway.
			dir, err := os.Getwd()
			if err != nil {
				dir = ""
			}

			var tid *domain.TaskID
			if taskID != "" {
				t := domain.TaskID(taskID)
				tid = &t
			}

			runner := &managed.Runner{Hooks: deps, ProviderBin: providerBin}
			outcome, err := runner.Run(cmd.Context(), managed.RunRequest{
				Provider:   provider,
				SessionID:  domain.SessionID(sessionID),
				WorktreeID: domain.WorktreeID(worktreeID),
				TaskID:     tid,
				// Cobra hands everything after `--` through as args;
				// joining with single spaces accepts both a single quoted
				// prompt argument and unquoted prose.
				Prompt:         strings.Join(args, " "),
				Dir:            dir,
				StreamRelay:    cmd.ErrOrStderr(),
				ProviderStderr: cmd.ErrOrStderr(),
				HumanLog:       cmd.ErrOrStderr(),
			})
			if err != nil {
				return err
			}

			body, err := marshalOrError("run", buildRunOutput(sessionID, outcome))
			if err != nil {
				return err
			}
			return writeJSON(cmd, body)
		},
	}
	cmd.Flags().StringVar(&provider, "provider", managed.ProviderClaude, "Provider to run (only \"claude\" is supported by this increment)")
	cmd.Flags().StringVar(&sessionID, "session-id", "", "Session ID to attribute this run to")
	cmd.Flags().StringVar(&worktreeID, "worktree-id", "", "Worktree ID to attribute this run to")
	cmd.Flags().StringVar(&taskID, "task-id", "", "Optional task ID to attribute this run to")
	cmd.Flags().StringVar(&providerBin, "provider-bin", managed.DefaultProviderBin, "Provider binary to spawn (argv only, resolved via PATH)")
	return cmd
}

// runOutput is `auspex run`'s schema-versioned wire shape
// ("auspex.run.v1", issue #8 outcome attribution). Nullable-by-design
// fields are pointers WITHOUT omitempty, per this package's established
// explicit-null discipline (evaluateOutput.Probability): a degraded gate
// serializes `"evaluation_id": null` / `"decision": null`, and a run
// whose stream carried no result line serializes null usage figures —
// absence is load-bearing information, never a fabricated zero (ADD
// principle 1).
type runOutput struct {
	SchemaVersion   string   `json:"schema_version"`
	SessionID       string   `json:"session_id"`
	TurnID          string   `json:"turn_id"`
	EvaluationID    *string  `json:"evaluation_id"`
	Decision        *string  `json:"decision"`
	ExitCode        int      `json:"exit_code"`
	IsError         *bool    `json:"is_error"`
	TotalCostUSD    *float64 `json:"total_cost_usd"`
	DurationMs      *int64   `json:"duration_ms"`
	EventsPersisted int      `json:"events_persisted"`
}

func buildRunOutput(sessionID string, outcome managed.RunOutcome) runOutput {
	out := runOutput{
		SchemaVersion:   "auspex.run.v1",
		SessionID:       sessionID,
		TurnID:          string(outcome.TurnID),
		ExitCode:        outcome.ExitCode,
		EventsPersisted: outcome.EventsPersisted,
	}
	if !outcome.GateDegraded {
		eid := string(outcome.EvaluationID)
		decision := string(outcome.Decision)
		out.EvaluationID = &eid
		out.Decision = &decision
	}
	if res := outcome.Stream.Result; res != nil {
		out.IsError = res.IsError
		out.TotalCostUSD = res.TotalCostUSD
		out.DurationMs = res.DurationMs
	}
	return out
}
