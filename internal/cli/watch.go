// watch.go: the REAL `auspex watch codex` command (issue #92, the
// rollout-tailing watcher), wired by internal/app/wiring in place of
// root.go's stub — the same stub-then-swap pattern gc/report follow.
// Signal handling lives HERE (signal.NotifyContext), exactly like
// daemon.go's `daemon run`: the watcher layer only ever sees a context.
package cli

import (
	"context"
	"errors"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/rolloutwatch"
	codextelemetry "github.com/huaiche94/auspex/internal/telemetry/codex"
)

// NewWatchCmd builds the real `auspex watch` tree against deps. Exported
// for internal/app/wiring.App.RootCmd, like NewGCCmd/NewReportCmd.
func NewWatchCmd(deps rolloutwatch.Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Watch provider session logs and capture usage telemetry",
	}
	cmd.AddCommand(newWatchCodexCmd(deps))
	return cmd
}

// watchCodexOutput is auspex.watch-codex.v1's wire shape: one JSON line
// per scan pass (a single line in --once mode; the startup line carries
// zero stats in foreground mode, followed by one line per interval tick).
// events_emitted counts events handed to the store; because the store
// deduplicates by idempotency key, re-runs re-emit without creating rows
// (see rolloutwatch.ScanStats).
type watchCodexOutput struct {
	SchemaVersion   string `json:"schema_version"`
	SessionsDir     string `json:"sessions_dir"`
	IntervalSeconds int64  `json:"interval_seconds"`
	Once            bool   `json:"once"`
	FilesSeen       int    `json:"files_seen"`
	FilesRead       int    `json:"files_read"`
	BytesRead       int64  `json:"bytes_read"`
	TurnsEmitted    int    `json:"turns_emitted"`
	EventsEmitted   int    `json:"events_emitted"`
	Errors          int    `json:"errors"`
	Deferred        int    `json:"deferred"`
}

// newWatchCodexCmd builds the `watch codex` leaf: a foreground poll loop
// over the Codex sessions tree (Ctrl-C to exit), or a single scan pass
// with --once (for cron jobs and tests). Fail-open like the watcher
// itself: per-file problems surface as the errors counter in the output,
// never as a nonzero exit.
func newWatchCodexCmd(deps rolloutwatch.Deps) *cobra.Command {
	var (
		codexHome string
		interval  time.Duration
		once      bool
	)
	cmd := &cobra.Command{
		Use:   "codex",
		Short: "Tail Codex session rollouts and capture usage from any surface (CLI, IDE, subagents)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			sessionsDir := ""
			if codexHome != "" {
				sessionsDir = filepath.Join(codexHome, "sessions")
			} else {
				dir, ok := codextelemetry.DefaultSessionsDir()
				if !ok {
					return &domain.Error{
						Code:      domain.ErrCodeValidation,
						Message:   "watch codex: no Codex home found (set --codex-home or CODEX_HOME, or ensure a home directory exists)",
						Retryable: false,
					}
				}
				sessionsDir = dir
			}

			watcher, err := rolloutwatch.New(rolloutwatch.Config{
				SessionsDir: sessionsDir,
				Interval:    interval,
			}, deps)
			if err != nil {
				return err
			}

			emit := func(stats rolloutwatch.ScanStats) error {
				body, err := marshalOrError("watch codex", watchCodexOutput{
					SchemaVersion:   "auspex.watch-codex.v1",
					SessionsDir:     sessionsDir,
					IntervalSeconds: int64(interval / time.Second),
					Once:            once,
					FilesSeen:       stats.FilesSeen,
					FilesRead:       stats.FilesRead,
					BytesRead:       stats.BytesRead,
					TurnsEmitted:    stats.TurnsEmitted,
					EventsEmitted:   stats.EventsEmitted,
					Errors:          stats.Errors,
					Deferred:        stats.Deferred,
				})
				if err != nil {
					return err
				}
				return writeJSON(cmd, body)
			}

			if once {
				return emit(watcher.ScanOnce(cmd.Context()))
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			runErr := watcher.Run(ctx, func(stats rolloutwatch.ScanStats) {
				// One JSON line per pass; an encode/write failure must not
				// stop capture (the write side is presentation only).
				_ = emit(stats)
			})
			if errors.Is(runErr, context.Canceled) {
				return nil // Ctrl-C / SIGTERM is a clean shutdown, not an error
			}
			return runErr
		},
	}
	cmd.Flags().StringVar(&codexHome, "codex-home", "", "Codex home directory to watch (default: $CODEX_HOME or ~/.codex)")
	cmd.Flags().DurationVar(&interval, "interval", rolloutwatch.DefaultInterval, "Poll interval between scan passes")
	cmd.Flags().BoolVar(&once, "once", false, "Run a single scan pass and exit (for cron/tests)")
	return cmd
}
