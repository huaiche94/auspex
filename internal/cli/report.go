package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/report"
)

// ReportGenerator is the narrow seam `auspex report` consumes —
// satisfied by *report.Engine. An interface (rather than the engine
// type) for the same reason every other command family here takes a
// deps seam: the CLI layer never constructs storage.
type ReportGenerator interface {
	GenerateReport(ctx context.Context, window time.Duration) (report.Report, error)
}

// NewReportCmd builds the REAL `auspex report` command (issue #91 items
// 1-3): a read-only personal usage report over the local database —
// totals, model/effort mix, right-sizing observations, cache hygiene,
// quota incidents, and the top costliest turns for a trailing window
// (default 7 days). This is the constructor
// internal/app/wiring.App.RootCmd() swaps in for root.go's `report`
// stub, the same stub-then-swap pattern gc/export follow (gated on the
// same real-database wiring).
//
// Output modes: default is aligned human-readable text
// (report.RenderText); --json emits the schema-versioned
// auspex.report.v1 document instead (FR-160/161 machine-output
// discipline — exactly one JSON document on stdout, nothing decorative).
func NewReportCmd(gen ReportGenerator) *cobra.Command {
	var windowFlag string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Show a personal usage report (cost, tokens, model mix, cache hygiene, quota) for a recent window",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			window, err := parseReportWindow(windowFlag)
			if err != nil {
				return err
			}

			rep, genErr := gen.GenerateReport(cmd.Context(), window)
			if genErr != nil {
				return &domain.Error{
					Code:      domain.ErrCodeInternal,
					Message:   "report: generating report: " + genErr.Error(),
					Retryable: false,
				}
			}

			if jsonOut {
				body, err := marshalOrError("report", rep)
				if err != nil {
					return err
				}
				return writeJSON(cmd, body)
			}
			_, writeErr := fmt.Fprint(cmd.OutOrStdout(), report.RenderText(rep))
			return writeErr
		},
	}
	cmd.Flags().StringVar(&windowFlag, "window", "7d", "Report window: <N>d for days (e.g. 7d) or a Go duration (e.g. 36h)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON output (auspex.report.v1)")
	cmd.AddCommand(newReportDiffCmd(gen))
	return cmd
}

// newReportDiffCmd builds `auspex report diff <baseline.json>` (issue
// #144, Cost Guard): compare the current window's report against a
// SAVED `auspex report --json` document — the baseline is the report
// format itself, versioned and self-dating, never a new artifact
// format. The comparison applies internal/report/diff.go's documented
// noise floors and quality-aware overall verdict (spend up is only a
// regression when cost per completed node did not improve).
func newReportDiffCmd(gen ReportGenerator) *cobra.Command {
	var windowFlag string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "diff <baseline.json>",
		Short: "Compare the current window against a saved `auspex report --json` baseline",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := os.ReadFile(args[0])
			if err != nil {
				return &domain.Error{
					Code: domain.ErrCodeValidation, Message: "report diff: reading baseline: " + err.Error(), Retryable: false,
				}
			}
			var baseline report.Report
			if err := json.Unmarshal(body, &baseline); err != nil {
				return &domain.Error{
					Code: domain.ErrCodeValidation, Message: "report diff: baseline is not a JSON report document: " + err.Error(), Retryable: false,
				}
			}
			if baseline.SchemaVersion != report.ReportSchemaVersion {
				return &domain.Error{
					Code:      domain.ErrCodeValidation,
					Message:   "report diff: baseline schema_version is not " + report.ReportSchemaVersion,
					Retryable: false,
					Details:   map[string]string{"schema_version": baseline.SchemaVersion},
				}
			}

			window, err := parseReportWindow(windowFlag)
			if err != nil {
				return err
			}
			current, genErr := gen.GenerateReport(cmd.Context(), window)
			if genErr != nil {
				return &domain.Error{
					Code: domain.ErrCodeInternal, Message: "report diff: generating current report: " + genErr.Error(), Retryable: false,
				}
			}

			d := report.BuildDiff(baseline, current)
			if jsonOut {
				body, err := marshalOrError("report diff", d)
				if err != nil {
					return err
				}
				return writeJSON(cmd, body)
			}
			_, writeErr := fmt.Fprint(cmd.OutOrStdout(), report.RenderDiffText(d))
			return writeErr
		},
	}
	cmd.Flags().StringVar(&windowFlag, "window", "7d", "Window for the CURRENT side of the comparison")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON output (auspex.report-diff.v1)")
	return cmd
}

// reportWindowDaysRE accepts the day shorthand the flag documents.
var reportWindowDaysRE = regexp.MustCompile(`^(\d+)d$`)

// parseReportWindow parses the --window flag: "<N>d" day shorthand or
// any positive Go duration string.
func parseReportWindow(s string) (time.Duration, error) {
	if m := reportWindowDaysRE.FindStringSubmatch(s); m != nil {
		days, err := strconv.Atoi(m[1])
		if err == nil && days > 0 {
			return time.Duration(days) * 24 * time.Hour, nil
		}
	} else if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d, nil
	}
	return 0, &domain.Error{
		Code:      domain.ErrCodeValidation,
		Message:   "report: invalid --window (want <N>d, e.g. 7d, or a positive Go duration, e.g. 36h)",
		Retryable: false,
		Details:   map[string]string{"window": s},
	}
}
