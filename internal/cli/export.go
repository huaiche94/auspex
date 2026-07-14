package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/retention"
)

// CalibrationExporter is the narrow seam `auspex export calibration`
// consumes — satisfied by *retention.Engine. An interface (rather than
// the engine type) for the same reason every other command family here
// takes a deps seam: the CLI layer never constructs storage.
type CalibrationExporter interface {
	ExportCalibration(ctx context.Context, w io.Writer) (retention.ExportSummary, error)
}

// ObservationsExporter is the narrow seam `auspex export observations`
// consumes — also satisfied by *retention.Engine (the raw series export
// reads the same events table the retention engine owns).
type ObservationsExporter interface {
	ExportObservations(ctx context.Context, w io.Writer) (retention.ObservationsSummary, error)
}

// Exporter is the union NewExportCmd takes: both dataset families ride
// the same retention engine, and wiring swaps the WHOLE export command
// at once — a runner that could serve only one dataset must keep the
// root.go stubs rather than half-wire the family.
type Exporter interface {
	CalibrationExporter
	ObservationsExporter
}

// NewExportCmd builds the REAL `auspex export` command family (FR-170/171,
// issue #11), wired against exporter. This is the constructor
// internal/app/wiring.App.RootCmd() uses in place of root.go's stub —
// the same stub-then-swap pattern gc follows, gated on the same retention
// engine wiring.
//
// Output modes (FR-160/161 machine-output discipline), identical for
// both leaves:
//   - default: JSONL records stream to stdout — the data IS the machine
//     output, pipeable straight into the research/ tooling.
//   - --out <path>: JSONL is written to path instead, and stdout gets a
//     stable *-export-summary.v1 JSON line (row counts, coverage) — the
//     summary a script wants after redirecting the bulk data to a file.
func NewExportCmd(exporter Exporter) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export de-identified datasets for offline analysis",
	}

	var outPath string
	calibration := &cobra.Command{
		Use:   "calibration",
		Short: "Export prediction-vs-actual calibration pairs as JSONL",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				dataOut = cmd.OutOrStdout()
				file    *os.File
			)
			if outPath != "" {
				f, err := os.Create(outPath)
				if err != nil {
					return &domain.Error{
						Code:      domain.ErrCodeValidation,
						Message:   "export calibration: cannot create --out file: " + err.Error(),
						Retryable: false,
					}
				}
				file = f
				dataOut = f
			}

			summary, err := exporter.ExportCalibration(cmd.Context(), dataOut)
			if file != nil {
				if closeErr := file.Close(); closeErr != nil && err == nil {
					err = closeErr
				}
			}
			if err != nil {
				return err
			}

			if outPath == "" {
				return nil // the JSONL stream itself was the output
			}
			body, encErr := json.Marshal(exportSummaryOutput{
				SchemaVersion:   "auspex.calibration-export-summary.v1",
				OutPath:         outPath,
				LiveRows:        summary.LiveRows,
				ArchivedRows:    summary.ArchivedRows,
				TotalRows:       summary.LiveRows + summary.ArchivedRows,
				ActualKnownRows: summary.ActualKnownRows,
				LabeledRows:     summary.LabeledRows,
			})
			if encErr != nil {
				return &domain.Error{
					Code: domain.ErrCodeInternal, Message: "export calibration: encoding summary: " + encErr.Error(), Retryable: false,
				}
			}
			_, writeErr := cmd.OutOrStdout().Write(append(body, '\n'))
			return writeErr
		},
	}
	calibration.Flags().StringVar(&outPath, "out", "", "Write JSONL to this file (stdout then carries a JSON summary instead of the data)")

	var obsOutPath string
	observations := &cobra.Command{
		Use:   "observations",
		Short: "Export raw usage/context/quota series with turn boundaries as JSONL",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				dataOut = cmd.OutOrStdout()
				file    *os.File
			)
			if obsOutPath != "" {
				f, err := os.Create(obsOutPath)
				if err != nil {
					return &domain.Error{
						Code:      domain.ErrCodeValidation,
						Message:   "export observations: cannot create --out file: " + err.Error(),
						Retryable: false,
					}
				}
				file = f
				dataOut = f
			}

			summary, err := exporter.ExportObservations(cmd.Context(), dataOut)
			if file != nil {
				if closeErr := file.Close(); closeErr != nil && err == nil {
					err = closeErr
				}
			}
			if err != nil {
				return err
			}

			if obsOutPath == "" {
				return nil // the JSONL stream itself was the output
			}
			body, encErr := json.Marshal(observationsSummaryOutput{
				SchemaVersion:    "auspex.observations-export-summary.v1",
				OutPath:          obsOutPath,
				Rows:             summary.Rows,
				Sessions:         summary.Sessions,
				TurnBoundaryRows: summary.TurnBoundaryRows,
			})
			if encErr != nil {
				return &domain.Error{
					Code: domain.ErrCodeInternal, Message: "export observations: encoding summary: " + encErr.Error(), Retryable: false,
				}
			}
			_, writeErr := cmd.OutOrStdout().Write(append(body, '\n'))
			return writeErr
		},
	}
	observations.Flags().StringVar(&obsOutPath, "out", "", "Write JSONL to this file (stdout then carries a JSON summary instead of the data)")

	cmd.AddCommand(calibration)
	cmd.AddCommand(observations)
	return cmd
}

type exportSummaryOutput struct {
	SchemaVersion   string `json:"schema_version"`
	OutPath         string `json:"out_path"`
	LiveRows        int    `json:"live_rows"`
	ArchivedRows    int    `json:"archived_rows"`
	TotalRows       int    `json:"total_rows"`
	ActualKnownRows int    `json:"actual_known_rows"`
	LabeledRows     int    `json:"labeled_rows"`
}

type observationsSummaryOutput struct {
	SchemaVersion    string `json:"schema_version"`
	OutPath          string `json:"out_path"`
	Rows             int    `json:"rows"`
	Sessions         int    `json:"sessions"`
	TurnBoundaryRows int    `json:"turn_boundary_rows"`
}
