package cli

import (
	"encoding/json"
	"time"

	"github.com/spf13/cobra"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/retention"
)

// NewGCCmd builds the REAL `auspex gc` command (ADR-046 tiered telemetry
// retention), wired against deps (internal/orchestrator.GCDeps). This is
// the constructor internal/app/wiring.App.RootCmd() uses in place of the
// package-private `gc` stub in root.go — the same stub-then-swap pattern
// status/doctor follow. Exported for the same reason as
// NewStatusCmd/NewDoctorCmd (see diagnostics.go).
//
// Flags mirror orchestrator.GCRequest: --dry-run (default false),
// --retention-days (default retention.DefaultRetentionDays = 90, per
// ADR-046), --vacuum (full VACUUM after a deleting pass — opt-in, see
// GCRequest.Vacuum's doc for why). Success output is auspex.gc.v1 JSON;
// errors use the frozen typed envelope like every other command
// (errors.go).
func NewGCCmd(deps orchestrator.GCDeps) *cobra.Command {
	var (
		dryRun        bool
		retentionDays int
		vacuum        bool
	)
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Archive and delete telemetry older than the retention window",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := orchestrator.GC(cmd.Context(), deps, orchestrator.GCRequest{
				DryRun:        dryRun,
				RetentionDays: retentionDays,
				Vacuum:        vacuum,
			})
			if err != nil {
				return err
			}

			body, encErr := json.Marshal(gcOutputFromResult(result))
			if encErr != nil {
				return &domain.Error{
					Code: domain.ErrCodeInternal, Message: "gc: encoding response: " + encErr.Error(), Retryable: false,
				}
			}
			_, writeErr := cmd.OutOrStdout().Write(append(body, '\n'))
			return writeErr
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Report what would be archived/deleted without touching anything")
	cmd.Flags().IntVar(&retentionDays, "retention-days", retention.DefaultRetentionDays, "Hot-window length in days; rows older than this are archived then deleted")
	cmd.Flags().BoolVar(&vacuum, "vacuum", false, "Run a full VACUUM after a deleting pass to return freed pages to the OS")
	return cmd
}

// gcOutput is auspex.gc.v1's wire shape: per-table counts, archive file
// details, rollup counts, the honest space-reclamation accounting
// (ADR-046: bytes are only RECLAIMABLE unless vacuum_ran is true), and
// the dry_run flag so a consumer never mistakes a rehearsal for a pass.
type gcOutput struct {
	SchemaVersion                string          `json:"schema_version"`
	RunID                        string          `json:"run_id"`
	RanAt                        string          `json:"ran_at"`
	Status                       string          `json:"status"`
	DryRun                       bool            `json:"dry_run"`
	RetentionDays                int             `json:"retention_days"`
	Cutoff                       string          `json:"cutoff"`
	Tables                       []gcTableOutput `json:"tables"`
	UsageRollupRows              int             `json:"usage_rollup_rows"`
	CalibrationSamples           int             `json:"calibration_samples"`
	CalibrationSamplesWithActual int             `json:"calibration_samples_with_actual"`
	ReclaimableBytesEstimate     int64           `json:"reclaimable_bytes_estimate"`
	AutoVacuumMode               string          `json:"auto_vacuum_mode"`
	VacuumRan                    bool            `json:"vacuum_ran"`
	Notes                        []string        `json:"notes,omitempty"`
}

type gcTableOutput struct {
	Table         string `json:"table"`
	Selected      int    `json:"selected"`
	Deleted       int    `json:"deleted"`
	ArchivePath   string `json:"archive_path,omitempty"`
	ArchiveRows   int    `json:"archive_rows,omitempty"`
	ArchiveSHA256 string `json:"archive_sha256,omitempty"`
	ArchiveBytes  int64  `json:"archive_bytes,omitempty"`
}

func gcOutputFromResult(result retention.RunResult) gcOutput {
	out := gcOutput{
		SchemaVersion:                "auspex.gc.v1",
		RunID:                        result.RunID,
		RanAt:                        result.RanAt.UTC().Format(time.RFC3339Nano),
		Status:                       result.Status,
		DryRun:                       result.DryRun,
		RetentionDays:                result.RetentionDays,
		Cutoff:                       result.Cutoff.UTC().Format(time.RFC3339Nano),
		UsageRollupRows:              result.UsageRollupRows,
		CalibrationSamples:           result.CalibrationSamples,
		CalibrationSamplesWithActual: result.CalibrationSamplesWithActual,
		ReclaimableBytesEstimate:     result.ReclaimableBytesEstimate,
		AutoVacuumMode:               result.AutoVacuumMode,
		VacuumRan:                    result.VacuumRan,
		Notes:                        result.Notes,
	}
	for _, t := range result.Tables {
		row := gcTableOutput{Table: t.Table, Selected: t.Selected, Deleted: t.Deleted}
		if t.Archive != nil {
			row.ArchivePath = t.Archive.Path
			row.ArchiveRows = t.Archive.Rows
			row.ArchiveSHA256 = t.Archive.SHA256
			row.ArchiveBytes = t.Archive.Bytes
		}
		out.Tables = append(out.Tables, row)
	}
	return out
}
