// hookprecompact.go builds the `auspex hook claude pre-compact` /
// `post-compact` and `auspex hook codex pre-compact` / `post-compact`
// leaves (issue #114, M4/M10) — both the standalone stubs for the bare
// NewRootCmd() tree and the real handlers NewHookClaudeCmd/NewHookCodexCmd
// register (hook.go adds one line per leaf there; the constructors live
// here per the repo's new-logic-in-new-files rule).
//
// Every real leaf follows hook.go's "JSON and errors" contract verbatim:
// read the full raw hook payload from stdin, never log or echo it, always
// write a syntactically valid provider-compatible JSON response to stdout,
// and exit 0 in every case except a genuine command-usage error. Neither
// compaction hook ever has an opinion — PreCompact cannot be blocked by
// this branch's fail-open design (a checkpoint failure must never block
// the provider's compaction; see internal/orchestrator/hooksprecompact.go)
// and PostCompact fires after the fact — so the response is always the
// empty-object no-op `{}`, including on internal failure (the Handle*
// functions are fail-open by design).
package cli

import (
	"github.com/spf13/cobra"

	"github.com/huaiche94/auspex/internal/orchestrator"
)

// --- stubs (bare NewRootCmd tree — see hook.go's newHookCmd doc) -----------

func newHookClaudePreCompactStubCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pre-compact",
		Short: "Handle a Claude Code PreCompact hook event (state checkpoint before compaction)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("hook claude pre-compact")
		},
	}
}

func newHookClaudePostCompactStubCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "post-compact",
		Short: "Handle a Claude Code PostCompact hook event",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("hook claude post-compact")
		},
	}
}

func newHookCodexPreCompactStubCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pre-compact",
		Short: "Handle a Codex PreCompact hook event (state checkpoint before compaction)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("hook codex pre-compact")
		},
	}
}

func newHookCodexPostCompactStubCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "post-compact",
		Short: "Handle a Codex PostCompact hook event",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplemented("hook codex post-compact")
		},
	}
}

// --- real handlers ----------------------------------------------------------

// newRealPreCompactCmd builds `auspex hook claude pre-compact` (issue
// #114): the auto-state-checkpoint-before-compaction hook (ADD §22.4).
// HandlePreCompact does the whole job — including the fail-open checkpoint
// capture — before this leaf answers; `{}` is the unconditional
// no-opinion response (a PreCompact hook's stdout carries no decision this
// branch honors, and a checkpoint failure must never block compaction).
func newRealPreCompactCmd(deps orchestrator.HookDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "pre-compact",
		Short: "Handle a Claude Code PreCompact hook event (state checkpoint before compaction)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			stdin, err := readAllStdin(cmd)
			if err != nil {
				return err
			}
			if _, err := orchestrator.HandlePreCompact(cmd.Context(), deps, stdin); err != nil {
				// Framework-level fault: the response must still be
				// syntactically valid JSON, so answer the no-op body.
				return writeJSON(cmd, []byte(`{}`))
			}
			return writeJSON(cmd, []byte(`{}`))
		},
	}
}

// newRealPostCompactCmd builds `auspex hook claude post-compact` —
// observation only (phase "post" telemetry; no checkpoint, no injected
// context). Registered as a command per ADD §22.3's event list; NOT wired
// into integrations/claude/hooks.json because Claude Code ships no
// PostCompact hook event today (see internal/hooks/claude/precompact.go's
// capability note).
func newRealPostCompactCmd(deps orchestrator.HookDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "post-compact",
		Short: "Handle a Claude Code PostCompact hook event",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			stdin, err := readAllStdin(cmd)
			if err != nil {
				return err
			}
			if _, err := orchestrator.HandlePostCompact(cmd.Context(), deps, stdin); err != nil {
				return writeJSON(cmd, []byte(`{}`))
			}
			return writeJSON(cmd, []byte(`{}`))
		},
	}
}

// newRealCodexPreCompactCmd builds `auspex hook codex pre-compact` — the
// codex twin of newRealPreCompactCmd over HandleCodexPreCompact. See
// internal/hooks/codex/precompact.go's capability note for why
// integrations/codex/hooks.json does not register it yet.
func newRealCodexPreCompactCmd(deps orchestrator.HookDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "pre-compact",
		Short: "Handle a Codex PreCompact hook event (state checkpoint before compaction)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			stdin, err := readAllStdin(cmd)
			if err != nil {
				return err
			}
			if _, err := orchestrator.HandleCodexPreCompact(cmd.Context(), deps, stdin); err != nil {
				return writeJSON(cmd, []byte(`{}`))
			}
			return writeJSON(cmd, []byte(`{}`))
		},
	}
}

// newRealCodexPostCompactCmd builds `auspex hook codex post-compact` — the
// codex twin of newRealPostCompactCmd over HandleCodexPostCompact.
func newRealCodexPostCompactCmd(deps orchestrator.HookDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "post-compact",
		Short: "Handle a Codex PostCompact hook event",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			stdin, err := readAllStdin(cmd)
			if err != nil {
				return err
			}
			if _, err := orchestrator.HandleCodexPostCompact(cmd.Context(), deps, stdin); err != nil {
				return writeJSON(cmd, []byte(`{}`))
			}
			return writeJSON(cmd, []byte(`{}`))
		},
	}
}
