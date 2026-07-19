package cli

import (
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/huaiche94/auspex/internal/hooks/claude"
	"github.com/huaiche94/auspex/internal/hooks/codex"
	"github.com/huaiche94/auspex/internal/orchestrator"
)

// newHookCmd builds the standalone-stub `auspex hook claude
// {statusline,user-prompt-submit,post-tool-use,stop,stop-failure}` subtree
// (agents/runtime.md Part B P0 command list, plus issue #67's ADR-052
// post-tool-use leaf). Every leaf here is a stub,
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
// Auspex_ADD.md Appendix E.3 (PascalCase) and three other frozen
// documents (kebab-case), tracked as ADR_Recommendations.md REC-03. This
// package follows agents/runtime.md's own P0 command list verbatim.
func newHookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hook",
		Short: "Provider hook entry points",
	}
	cmd.AddCommand(newHookClaudeStubCmd())
	cmd.AddCommand(newHookCodexStubCmd())
	return cmd
}

func newHookCodexStubCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "codex",
		Short: "Codex CLI hook entry points",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "session-start",
			Short: "Handle a Codex SessionStart hook event",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return notImplemented("hook codex session-start")
			},
		},
		&cobra.Command{
			Use:   "user-prompt-submit",
			Short: "Handle a Codex UserPromptSubmit hook event",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return notImplemented("hook codex user-prompt-submit")
			},
		},
		&cobra.Command{
			Use:   "stop",
			Short: "Handle a Codex Stop hook event",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return notImplemented("hook codex stop")
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Print a one-line status display for the latest Codex session",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return notImplemented("hook codex status")
			},
		},
		// issue #114 (hookprecompact.go): compaction hook stubs.
		newHookCodexPreCompactStubCmd(),
		newHookCodexPostCompactStubCmd(),
	)
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
			Use:   "post-tool-use",
			Short: "Handle a Claude Code PostToolUse hook event",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return notImplemented("hook claude post-tool-use")
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
		// issue #114 (hookprecompact.go): compaction hook stubs.
		newHookClaudePreCompactStubCmd(),
		newHookClaudePostCompactStubCmd(),
	)
	return cmd
}

// NewHookClaudeCmd builds the REAL `auspex hook claude ...` subtree,
// wired against deps (internal/orchestrator.HookDeps). This is the
// runtime-b04 constructor internal/app/wiring.App.RootCmd() uses in place
// of the package-private stub tree above. Exported because
// internal/app/wiring is a different package that needs to call it
// (mirrors NewRootCmd's own export rationale — see doc.go).
//
// Every leaf follows agents/runtime.md Part B's "JSON and errors"
// requirements: reads the full raw hook payload from stdin, never logs or
// echoes it, always writes a syntactically valid provider-compatible JSON
// response to stdout (even on Auspex's own internal failure — the
// orchestrator Handle* functions are fail-open by design, see hooks.go),
// and exits 0 in every case except a genuine command-usage error (e.g.
// unreadable stdin) — a hook's own semantic block decision is content in
// the response body, never a non-zero process exit, so the provider's own
// hook runner does not misinterpret an ordinary block as Auspex
// crashing.
func NewHookClaudeCmd(deps orchestrator.HookDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "claude",
		Short: "Claude Code hook entry points",
	}
	cmd.AddCommand(
		newRealStatusLineCmd(deps),
		newRealUserPromptSubmitCmd(deps),
		newRealPostToolUseCmd(deps),
		newRealStopCmd(deps),
		newRealStopFailureCmd(deps),
		// issue #114 (hookprecompact.go): pre/post-compaction hooks.
		newRealPreCompactCmd(deps),
		newRealPostCompactCmd(deps),
	)
	return cmd
}

// newRealPostToolUseCmd builds `auspex hook claude post-tool-use` (issue
// #67 slice 3a, ADR-052 approval touch 1; kebab-case per ADR-050's
// convention, like every sibling leaf). It follows the same "JSON and
// errors" contract as the rest of this file: read the full raw payload
// from stdin, never log or echo it, always answer a syntactically valid
// response on stdout, exit 0 for everything except a genuine
// command-usage error. This hook never has an opinion — PostToolUse fires
// AFTER the tool already ran, and Auspex only counts — so the response is
// always the empty-object no-op `{}` (the same no-decision body the
// UserPromptSubmit allow golden and the codex leaves use), including on
// internal failure: HandlePostToolUse is fail-open by design (toolops.go).
func newRealPostToolUseCmd(deps orchestrator.HookDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "post-tool-use",
		Short: "Handle a Claude Code PostToolUse hook event",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			stdin, err := readAllStdin(cmd)
			if err != nil {
				return err
			}
			if _, err := orchestrator.HandlePostToolUse(cmd.Context(), deps, stdin); err != nil {
				// Framework-level fault: the response must still be
				// syntactically valid JSON, so answer the no-op body.
				return writeJSON(cmd, []byte(`{}`))
			}
			return writeJSON(cmd, []byte(`{}`))
		},
	}
}

func newRealStatusLineCmd(deps orchestrator.HookDeps) *cobra.Command {
	var emitLine bool
	cmd := &cobra.Command{
		Use:   "statusline",
		Short: "Handle a Claude Code status-line hook event",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			stdin, err := readAllStdin(cmd)
			if err != nil {
				return err
			}
			// --emit-line (issue #14 deliverable 4, resolving issue #12's
			// friction #2): same ingest as the default path, PLUS one
			// compact display line on stdout — Claude Code's statusLine
			// command must print the visible line, and the ingest-only
			// default blanks the user's status bar when wired directly.
			if emitLine {
				_, line, err := orchestrator.HandleStatusLineEmitLine(cmd.Context(), deps, stdin)
				if err != nil {
					return err
				}
				_, writeErr := cmd.OutOrStdout().Write([]byte(line + "\n"))
				return writeErr
			}
			// HandleStatusLine is fail-open on malformed input (see
			// hooks.go); its own returned error is reserved for a
			// framework-level fault, not a parse failure, so no error
			// path needs a fallback response here.
			if _, err := orchestrator.HandleStatusLine(cmd.Context(), deps, stdin); err != nil {
				return err
			}
			// Without --emit-line, behavior is byte-identical to the
			// pre-issue-#14 command: ADD §22.6's compose-with-existing-
			// status-line installer mechanism still does not exist, so
			// the default stdout contribution stays intentionally empty —
			// Auspex does not overwrite or duplicate whatever the
			// user's previously-configured status-line command prints.
			// Callers that want Auspex to OWN the line opt in via the
			// flag (integrations/claude/hooks.json now does).
			return nil
		},
	}
	cmd.Flags().BoolVar(&emitLine, "emit-line", false, "Also print a one-line status display (model + latest forecast) to stdout")
	return cmd
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

// NewHookCodexCmd builds the REAL `auspex hook codex ...` subtree (issue
// #9 Phase 1), wired against deps — the codex sibling of NewHookClaudeCmd,
// swapped in by internal/app/wiring.App.RootCmd() exactly the same way.
//
// The same "JSON and errors" leaf contract applies: read the full raw hook
// payload from stdin, never log or echo it, always write a syntactically
// valid provider-compatible JSON response to stdout (Codex validates hook
// stdout when non-empty, and `{}` is the every-field-defaulted valid body
// for all three hook output schemas), and exit 0 in every case except a
// genuine command-usage error. A block decision is content in the response
// body, never a non-zero exit.
func NewHookCodexCmd(deps orchestrator.HookDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "codex",
		Short: "Codex CLI hook entry points",
	}
	cmd.AddCommand(
		newRealCodexSessionStartCmd(deps),
		newRealCodexUserPromptSubmitCmd(deps),
		newRealCodexStopCmd(deps),
		newRealCodexStatusCmd(deps),
		// issue #114 (hookprecompact.go): pre/post-compaction hooks.
		newRealCodexPreCompactCmd(deps),
		newRealCodexPostCompactCmd(deps),
	)
	return cmd
}

func newRealCodexSessionStartCmd(deps orchestrator.HookDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "session-start",
		Short: "Handle a Codex SessionStart hook event",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			stdin, err := readAllStdin(cmd)
			if err != nil {
				return err
			}
			// HandleCodexSessionStart is fail-open on malformed input;
			// its returned error is reserved for framework-level faults.
			if _, err := orchestrator.HandleCodexSessionStart(cmd.Context(), deps, stdin); err != nil {
				return writeJSON(cmd, []byte(`{}`))
			}
			// session-start.command.output has no decision field; `{}` is
			// the valid no-opinion response.
			return writeJSON(cmd, []byte(`{}`))
		},
	}
}

func newRealCodexUserPromptSubmitCmd(deps orchestrator.HookDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "user-prompt-submit",
		Short: "Handle a Codex UserPromptSubmit hook event",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			stdin, err := readAllStdin(cmd)
			if err != nil {
				return err
			}
			result, err := orchestrator.HandleCodexUserPromptSubmit(cmd.Context(), deps, stdin)
			if err != nil {
				// Framework-level fault: the hook fallback must still be
				// syntactically valid, so answer the safe allow.
				return writeJSON(cmd, codex.FallbackAllowResponse())
			}
			body, encErr := codex.EncodeUserPromptSubmitResponse(result.Response)
			if encErr != nil {
				return writeJSON(cmd, codex.FallbackAllowResponse())
			}
			return writeJSON(cmd, body)
		},
	}
}

func newRealCodexStopCmd(deps orchestrator.HookDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Handle a Codex Stop hook event",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			stdin, err := readAllStdin(cmd)
			if err != nil {
				return err
			}
			if _, err := orchestrator.HandleCodexStop(cmd.Context(), deps, stdin); err != nil {
				return writeJSON(cmd, []byte(`{}`))
			}
			// stop.command.output's decision field is block-only and this
			// handler never blocks a stop; `{}` is the valid no-opinion
			// response.
			return writeJSON(cmd, []byte(`{}`))
		},
	}
}

// newRealCodexStatusCmd builds `auspex hook codex status` (issue #9 Phase
// 1b): the one hook leaf that reads NO stdin — it exists precisely for
// callers that cannot provide any (tmux status-right polling every ~15s).
// It prints the one-line display for the most recent Codex session
// observed in --cwd (default: the calling process's working directory) and
// exits 0 on every outcome, degrading to the bare "ax»" line rather than
// erroring — a status bar consumer must never see a stack trace.
func newRealCodexStatusCmd(deps orchestrator.HookDeps) *cobra.Command {
	var cwd string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print a one-line status display for the latest Codex session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := cwd
			if dir == "" {
				// Best-effort: an unresolvable working directory just means
				// the latest codex session anywhere is shown.
				dir, _ = os.Getwd()
			}
			line, err := orchestrator.HandleCodexStatus(cmd.Context(), deps, dir)
			if err != nil {
				return err
			}
			_, writeErr := cmd.OutOrStdout().Write([]byte(line + "\n"))
			return writeErr
		},
	}
	cmd.Flags().StringVar(&cwd, "cwd", "", "Directory to resolve the Codex session for (default: current working directory)")
	return cmd
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
