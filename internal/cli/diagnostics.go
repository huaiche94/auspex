package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/orchestrator"
)

// NewStatusCmd builds the REAL `auspex status` command, wired against
// deps (internal/orchestrator.StatusDeps). This is the runtime-b08
// constructor internal/app/wiring.App.RootCmd() uses in place of the
// package-private `status` stub in root.go. Exported for the same reason
// as NewHookClaudeCmd/NewCheckpointCmd (see hook.go/checkpoint.go).
//
// --session-id is required (Status has no session to report on
// otherwise); --task-id is optional, matching
// orchestrator.StatusRequest's own optionality.
func NewStatusCmd(deps orchestrator.StatusDeps) *cobra.Command {
	var sessionID, taskID string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show a summary of the current Auspex-managed session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if sessionID == "" {
				return &domain.Error{
					Code:      domain.ErrCodeValidation,
					Message:   "status: --session-id is required",
					Retryable: false,
				}
			}
			req := orchestrator.StatusRequest{SessionID: domain.SessionID(sessionID)}
			if taskID != "" {
				tid := domain.TaskID(taskID)
				req.TaskID = &tid
			}

			result, err := orchestrator.Status(cmd.Context(), deps, req)
			if err != nil {
				return err
			}

			out := statusOutput{
				SchemaVersion:   "auspex.status.v1",
				SessionID:       string(result.SessionID),
				HasProgressTree: result.HasProgressTree,
			}
			if result.HasProgressTree {
				out.ProgressTreeTaskID = string(result.ProgressTree.TaskID)
				out.ProgressTreeNodeCount = len(result.ProgressTree.Nodes)
			}
			body, encErr := json.Marshal(out)
			if encErr != nil {
				return &domain.Error{
					Code: domain.ErrCodeInternal, Message: "status: encoding response: " + encErr.Error(), Retryable: false,
				}
			}
			_, writeErr := cmd.OutOrStdout().Write(append(body, '\n'))
			return writeErr
		},
	}
	cmd.Flags().StringVar(&sessionID, "session-id", "", "Session ID to summarize")
	cmd.Flags().StringVar(&taskID, "task-id", "", "Task ID whose Progress Tree to include")
	return cmd
}

type statusOutput struct {
	SchemaVersion         string `json:"schema_version"`
	SessionID             string `json:"session_id"`
	HasProgressTree       bool   `json:"has_progress_tree"`
	ProgressTreeTaskID    string `json:"progress_tree_task_id,omitempty"`
	ProgressTreeNodeCount int    `json:"progress_tree_node_count,omitempty"`
}

// NewDoctorCmd builds the REAL `auspex doctor` command, wired against
// deps (internal/orchestrator.DoctorDeps). This is the runtime-b08
// constructor internal/app/wiring.App.RootCmd() uses in place of the
// package-private `doctor` stub in root.go.
//
// doctor is purely diagnostic (ADD §28.9): it never mutates anything, and
// always exits 0 with a JSON report even when individual checks fail —
// the caller inspects DoctorResult.Healthy / each CheckResult.Status to
// decide what to do, rather than doctor itself deciding a failing check
// is a command-execution error. This mirrors the hook handlers' own
// "always produce valid output" discipline (hook.go) applied to a
// diagnostic command instead of a provider hook.
func NewDoctorCmd(deps orchestrator.DoctorDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose the local Auspex installation and provider setup",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			result := orchestrator.Doctor(cmd.Context(), deps)

			out := doctorOutput{
				SchemaVersion: "auspex.doctor.v1",
				Healthy:       result.Healthy,
			}
			for _, c := range result.Checks {
				out.Checks = append(out.Checks, doctorCheckOutput{
					Name:   c.Name,
					Status: string(c.Status),
					Detail: c.Detail,
				})
			}
			body, encErr := json.Marshal(out)
			if encErr != nil {
				return &domain.Error{
					Code: domain.ErrCodeInternal, Message: "doctor: encoding response: " + encErr.Error(), Retryable: false,
				}
			}
			_, writeErr := cmd.OutOrStdout().Write(append(body, '\n'))
			return writeErr
		},
	}
}

type doctorOutput struct {
	SchemaVersion string              `json:"schema_version"`
	Healthy       bool                `json:"healthy"`
	Checks        []doctorCheckOutput `json:"checks"`
}

type doctorCheckOutput struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}
