// Command preflight is the Preflight CLI entrypoint. Per Preflight_ADD.md
// §10.1, this package only does wiring and process exit — no business
// logic lives here or in Cobra command handlers. For foundation-01, the
// only wired command is `preflight version`; the full command surface is
// built by the runtime role in a later wave.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/huaiche94/preflight/internal/buildinfo"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

// newRootCmd builds the root Cobra command. Kept separate from main so
// tests can exercise command wiring without invoking os.Exit.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "preflight",
		Short:         "Preflight is a local-first predictive runtime guard for AI coding agents.",
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	root.AddCommand(newVersionCmd())

	return root
}

// newVersionCmd builds `preflight version`, which prints the build's
// version string to stdout.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the Preflight version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), buildinfo.String())
			return err
		},
	}
}
