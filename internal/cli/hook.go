package cli

import "github.com/spf13/cobra"

// newHookCmd builds `preflight hook claude {statusline,user-prompt-submit,
// stop,stop-failure}` (agents/runtime.md Part B P0 command list). Every
// leaf is a stub: normalizing a provider hook payload depends on
// HookNormalizer (internal/app/ports.go), implemented by the
// claude-provider role and wired together by a later runtime node, not
// this one.
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
	cmd.AddCommand(newHookClaudeCmd())
	return cmd
}

// newHookClaudeCmd builds the `preflight hook claude ...` subtree — the
// P0 command surface is Claude-Code-specific; other providers get their
// own subtree under `hook` when they exist (ADD §6.7/§8, Constitution §5),
// not this wave.
func newHookClaudeCmd() *cobra.Command {
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
