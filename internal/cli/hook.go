package cli

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/huaiche94/preflight/internal/hooks/claude"
	"github.com/huaiche94/preflight/internal/orchestrator"
)

// newHookCmd builds the standalone-stub `preflight hook claude
// {statusline,user-prompt-submit,stop,stop-failure}` subtree
// (agents/runtime.md Part B P0 command list). Every leaf here is a stub,
// used only by the bare NewRootCmd() tree (no wired services — see
// doc.go's package comment): a caller with no orchestrator.HookDeps to
// inject gets an honest "not yet available" rather than a silently
// no-op command. internal/app/wiring.App.RootCmd() replaces this subtree
// with NewHookClaudeCmd's real handlers once a wiring.App exists
// (runtime-b04) — see wiring.go.
//
// Subcommand casing: kebab-case ("user-prompt-submit", "stop-failure").
// See doc.go's package comment for the full naming-convention rationale —
// this repository has an unresolved discrepancy between
// Preflight_ADD.md Appendix E.3 (PascalCase) and three other frozen
// documents (kebab-case), tracked as ADR_Recommendations.md REC-03. This
// package follows agents/runtime.md's own P0 command list verbatim.
func newHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Provider hook entry points",
	}
	cmd.AddCommand(newHookClaudeStubCmd())
	return cmd
}

func newHookClaudeStubCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "claude",
		Short: "Claude Code hook entry points",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "statusline",
			Short: "Handle a Claude Code status-line hook event",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return notImplemented("hook claude statusline")
			},
		},
		&cobra.Command{
			Use:   "user-prompt-submit",
			Short: "Handle a Claude Code UserPromptSubmit hook event",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return notImplemented("hook claude user-prompt-submit")
			},
		},
		&cobra.Command{
			Use:   "stop",
			Short: "Handle a Claude Code Stop hook event",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return notImplemented("hook claude stop")
			},
		},
		&cobra.Command{
			Use:   "stop-failure",
			Short: "Handle a Claude Code Stop hook event that ended in failure",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return notImplemented("hook claude stop-failure")
			},
		},
	)
	return cmd
}

// NewHookClaudeCmd builds the REAL `preflight hook claude ...` subtree,
// wired against deps (internal/orchestrator.HookDeps). This is the
// runtime-b04 constructor internal/app/wiring.App.RootCmd() uses in place
// of the package-private stub tree above. Exported because
// internal/app/wiring is a different package that needs to call it
// (mirrors NewRootCmd's own export rationale — see doc.go).
//
// Every leaf follows agents/runtime.md Part B's "JSON and errors"
// requirements: reads the full raw hook payload from stdin, never logs or
// echoes it, always writes a syntactically valid provider-compatible JSON
// response to stdout (even on Preflight's own internal failure — the
// orchestrator Handle* functions are fail-open by design, see hooks.go),
// and exits 0 in every case except a genuine command-usage error (e.g.
// unreadable stdin) — a hook's own semantic block decision is content in
// the response body, never a non-zero process exit, so the provider's own
// hook runner does not misinterpret an ordinary block as Preflight
// crashing.
func NewHookClaudeCmd(deps orchestrator.HookDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "claude",
		Short: "Claude Code hook entry points",
	}
	cmd.AddCommand(
		newRealStatusLineCmd(deps),
		newRealUserPromptSubmitCmd(deps),
		newRealStopCmd(deps),
		newRealStopFailureCmd(deps),
	)
	return cmd
}

func newRealStatusLineCmd(deps orchestrator.HookDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "statusline",
		Short: "Handle a Claude Code status-line hook event",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			stdin, err := readAllStdin(cmd)
			if err != nil {
				return err
			}
			// HandleStatusLine is fail-open on malformed input (see
			// hooks.go); its own returned error is reserved for a
			// framework-level fault, not a parse failure, so no error
			// path needs a fallback response here.
			if _, err := orchestrator.HandleStatusLine(cmd.Context(), deps, stdin); err != nil {
				return err
			}
			// ADD §22.6: composing with a previously-configured status
			// line command is a separate, not-yet-built installer
			// mechanism (see hooks.go's HandleStatusLine doc). Until
			// that exists, this command's stdout contribution is
			// intentionally empty — Preflight does not yet own
			// rendering the visible status-line text, only observing
			// it — so it never overwrites or duplicates whatever the
			// user's actual status-line command already prints.
			return nil
		},
	}
}

func newRealUserPromptSubmitCmd(deps orchestrator.HookDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "user-prompt-submit",
		Short: "Handle a Claude Code UserPromptSubmit hook event",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			stdin, err := readAllStdin(cmd)
			if err != nil {
				return err
			}
			result, err := orchestrator.HandleUserPromptSubmit(cmd.Context(), deps, stdin)
			if err != nil {
				// Framework-level fault (not a parse failure — those
				// fail open inside HandleUserPromptSubmit already).
				// The hook fallback must still be syntactically valid,
				// so fall back to the safe allow response rather than
				// emitting nothing.
				return writeJSON(cmd, claude.FallbackAllowResponse())
			}
			body, encErr := claude.EncodeUserPromptSubmitResponse(result.Response)
			if encErr != nil {
				return writeJSON(cmd, claude.FallbackAllowResponse())
			}
			return writeJSON(cmd, body)
		},
	}
}

func newRealStopCmd(deps orchestrator.HookDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Handle a Claude Code Stop hook event",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			stdin, err := readAllStdin(cmd)
			if err != nil {
				return err
			}
			if _, err := orchestrator.HandleStop(cmd.Context(), deps, stdin); err != nil {
				return err
			}
			return nil
		},
	}
}

func newRealStopFailureCmd(deps orchestrator.HookDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "stop-failure",
		Short: "Handle a Claude Code Stop hook event that ended in failure",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			stdin, err := readAllStdin(cmd)
			if err != nil {
				return err
			}
			if _, err := orchestrator.HandleStopFailure(cmd.Context(), deps, stdin); err != nil {
				return err
			}
			return nil
		},
	}
}

// readAllStdin reads the full hook payload from cmd's configured input
// (cmd.InOrStdin() — real stdin in production, an injectable io.Reader in
// tests, per Cobra's own testing convention). A read failure is a genuine
// command-usage fault (e.g. a closed pipe), distinct from "the payload we
// read was malformed JSON" (handled inside the orchestrator Handle*
// functions, fail-open) — this one DOES propagate as a real error, since
// there is no payload at all to fail open with.
func readAllStdin(cmd *cobra.Command) ([]byte, error) {
	return io.ReadAll(cmd.InOrStdin())
}

// writeJSON writes body to cmd's stdout followed by a newline. Per
// agents/runtime.md Part B "JSON and errors": "machine mode never emits
// decorative text to stdout" — this is the only thing hook commands ever
// write to stdout, no banners, no progress text.
func writeJSON(cmd *cobra.Command, body []byte) error {
	_, err := cmd.OutOrStdout().Write(append(body, '\n'))
	return err
}
