// hookcodexinstall.go — `auspex hook codex install|uninstall` (issue #9
// M8 slice, ADD §21.12): the ownership-record hooks.json installer.
// These leaves are deliberately dependency-free (no DB, no HookDeps):
// they edit the Codex CLI's hooks.json and Auspex's own receipt file,
// and are therefore available on the bare command tree too — a user can
// install hooks before the first session ever runs.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/huaiche94/auspex/internal/buildinfo"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/hookinstall"
	"github.com/huaiche94/auspex/internal/paths"

	"github.com/spf13/cobra"
)

// codexHooksConfigPath resolves the Codex CLI's hooks.json location
// (CODEX_HOME override, else ~/.codex — the same resolution the rollout
// reader uses).
func codexHooksConfigPath() (string, error) {
	if home := os.Getenv("CODEX_HOME"); home != "" {
		return filepath.Join(home, "hooks.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "cli: cannot resolve a home directory for the codex hooks.json (set CODEX_HOME)", Retryable: false,
		}
	}
	return filepath.Join(home, ".codex", "hooks.json"), nil
}

// codexHooksReceiptPath resolves the §21.12 ownership receipt's home:
// Auspex's own data directory, NOT the provider's (a codex reinstall or
// config wipe must never destroy the record of what Auspex added).
func codexHooksReceiptPath() (string, error) {
	dirs, err := paths.ResolveHost(paths.NewOSEnv())
	if err != nil {
		return "", fmt.Errorf("cli: resolve data directory: %w", err)
	}
	return filepath.Join(dirs.Data, "codex-hooks-receipt.json"), nil
}

type codexInstallOutput struct {
	SchemaVersion string   `json:"schema_version"`
	ConfigPath    string   `json:"config_path"`
	ReceiptPath   string   `json:"receipt_path"`
	Added         []string `json:"added"`
	AlreadyWired  []string `json:"already_wired"`
	ConfigCreated bool     `json:"config_created"`
}

func newCodexHooksInstallCmd() *cobra.Command {
	var configOverride, receiptOverride string
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Wire the Auspex command hooks into the Codex CLI's hooks.json (ownership-recorded, user entries preserved)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			configPath, receiptPath, err := resolveCodexInstallPaths(configOverride, receiptOverride)
			if err != nil {
				return err
			}
			exe, _ := os.Executable() // best-effort receipt metadata
			res, err := hookinstall.Install(configPath, receiptPath, "codex", exe, buildinfo.Version, hookinstall.CodexEntries(), time.Now())
			if err != nil {
				return err
			}
			out := codexInstallOutput{
				SchemaVersion: "auspex.codex-hook-install.v1",
				ConfigPath:    res.ConfigPath,
				ReceiptPath:   res.ReceiptPath,
				Added:         entryCommands(res.Added),
				AlreadyWired:  entryCommands(res.AlreadyWired),
				ConfigCreated: res.ConfigCreated,
			}
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "auspex: codex hooks installed — restart any running codex session, then approve the hooks via /hooks (codex pins them by hash)")
			return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
		},
	}
	cmd.Flags().StringVar(&configOverride, "codex-hooks-file", "", "Override the codex hooks.json path (default: $CODEX_HOME/hooks.json or ~/.codex/hooks.json)")
	cmd.Flags().StringVar(&receiptOverride, "receipt-file", "", "Override the ownership receipt path (default: Auspex data dir)")
	_ = cmd.Flags().MarkHidden("receipt-file")
	return cmd
}

type codexUninstallOutput struct {
	SchemaVersion string   `json:"schema_version"`
	ConfigPath    string   `json:"config_path"`
	Removed       []string `json:"removed"`
	Missing       []string `json:"missing"`
	NoReceipt     bool     `json:"no_receipt"`
}

func newCodexHooksUninstallCmd() *cobra.Command {
	var configOverride, receiptOverride string
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove exactly the Auspex-owned hook entries from the Codex CLI's hooks.json (per the install receipt)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			configPath, receiptPath, err := resolveCodexInstallPaths(configOverride, receiptOverride)
			if err != nil {
				return err
			}
			res, err := hookinstall.Uninstall(configPath, receiptPath)
			if err != nil {
				return err
			}
			if res.NoReceipt {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "auspex: no install receipt found — nothing is owned, nothing was touched (hand-wired hooks stay as they are)")
			}
			out := codexUninstallOutput{
				SchemaVersion: "auspex.codex-hook-uninstall.v1",
				ConfigPath:    res.ConfigPath,
				Removed:       entryCommands(res.Removed),
				Missing:       entryCommands(res.Missing),
				NoReceipt:     res.NoReceipt,
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
		},
	}
	cmd.Flags().StringVar(&configOverride, "codex-hooks-file", "", "Override the codex hooks.json path")
	cmd.Flags().StringVar(&receiptOverride, "receipt-file", "", "Override the ownership receipt path")
	_ = cmd.Flags().MarkHidden("receipt-file")
	return cmd
}

func resolveCodexInstallPaths(configOverride, receiptOverride string) (string, string, error) {
	configPath := configOverride
	if configPath == "" {
		p, err := codexHooksConfigPath()
		if err != nil {
			return "", "", err
		}
		configPath = p
	}
	receiptPath := receiptOverride
	if receiptPath == "" {
		p, err := codexHooksReceiptPath()
		if err != nil {
			return "", "", err
		}
		receiptPath = p
	}
	return configPath, receiptPath, nil
}

func entryCommands(entries []hookinstall.Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Command)
	}
	return out
}
