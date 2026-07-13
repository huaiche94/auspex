package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/idgen"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/pause"
)

// NewPauseCmd builds the REAL `auspex pause {request,cancel}` command
// tree, wired against deps (internal/orchestrator.PauseLifecycleDeps). This
// is the runtime-b07 constructor internal/app/wiring.App.RootCmd() uses in
// place of the package-private `pause` stub in root.go. Exported for the
// same reason as NewCheckpointCmd/NewStatusCmd (see checkpoint.go/
// diagnostics.go).
//
// --task-id/--session-id (request) and --pause-id (cancel) are read as
// direct flags: no resolver port exists yet, the same documented scope
// boundary runtime-b03's Evaluate pipeline and runtime-b05's
// CheckpointCreate both already established.
func NewPauseCmd(deps orchestrator.PauseLifecycleDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pause",
		Short: "Request or cancel a Graceful Pause",
	}
	cmd.AddCommand(newPauseRequestCmd(deps), newPauseCancelCmd(deps))
	return cmd
}

func newPauseRequestCmd(deps orchestrator.PauseLifecycleDeps) *cobra.Command {
	var taskID, sessionID, reason string
	cmd := &cobra.Command{
		Use:   "request",
		Short: "Request a Graceful Pause for the current session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if taskID == "" {
				return &domain.Error{Code: domain.ErrCodeValidation, Message: "pause request: --task-id is required", Retryable: false}
			}
			if sessionID == "" {
				return &domain.Error{Code: domain.ErrCodeValidation, Message: "pause request: --session-id is required", Retryable: false}
			}
			var triggerReason pause.TriggerReason
			switch reason {
			case "", string(pause.TriggerReasonCalibrated):
				triggerReason = pause.TriggerReasonCalibrated
			case string(pause.TriggerReasonEmergency):
				triggerReason = pause.TriggerReasonEmergency
			default:
				return &domain.Error{
					Code:      domain.ErrCodeValidation,
					Message:   "pause request: --reason must be one of \"calibrated_hit_probability\", \"emergency_uncalibrated\"",
					Retryable: false,
					Details:   map[string]string{"reason": reason},
				}
			}

			result, err := orchestrator.PauseRequestCmd(cmd.Context(), deps, idgen.New(), orchestrator.PauseRequestRequest{
				TaskID:    domain.TaskID(taskID),
				SessionID: domain.SessionID(sessionID),
				Reason:    triggerReason,
			})
			if err != nil {
				return err
			}
			body, err := marshalOrError("pause request", pauseRequestOutput{
				SchemaVersion: "auspex.pause-request.v1",
				PauseID:       string(result.Record.ID),
				Status:        string(result.Record.Status),
				Created:       result.Created,
			})
			if err != nil {
				return err
			}
			return writeJSON(cmd, body)
		},
	}
	cmd.Flags().StringVar(&taskID, "task-id", "", "Task ID to pause")
	cmd.Flags().StringVar(&sessionID, "session-id", "", "Session ID to pause")
	cmd.Flags().StringVar(&reason, "reason", string(pause.TriggerReasonCalibrated), "Trigger reason (calibrated_hit_probability|emergency_uncalibrated)")
	return cmd
}

func newPauseCancelCmd(deps orchestrator.PauseLifecycleDeps) *cobra.Command {
	var pauseID string
	cmd := &cobra.Command{
		Use:   "cancel",
		Short: "Cancel a pending or in-flight pause",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if pauseID == "" {
				return &domain.Error{Code: domain.ErrCodeValidation, Message: "pause cancel: --pause-id is required", Retryable: false}
			}
			result, err := orchestrator.PauseCancelCmd(cmd.Context(), deps, orchestrator.PauseCancelRequest{PauseID: domain.PauseID(pauseID)})
			if err != nil {
				return err
			}
			body, err := marshalOrError("pause cancel", pauseCancelOutput{
				SchemaVersion: "auspex.pause-cancel.v1",
				PauseID:       string(result.Record.ID),
				Status:        string(result.Record.Status),
			})
			if err != nil {
				return err
			}
			return writeJSON(cmd, body)
		},
	}
	cmd.Flags().StringVar(&pauseID, "pause-id", "", "Pause ID to cancel")
	return cmd
}

// NewResumeCmd builds the REAL `auspex resume` command, wired against
// deps. This is the runtime-b07 constructor
// internal/app/wiring.App.RootCmd() uses in place of the package-private
// `resume` stub in root.go.
//
// --quota-unsafe/--conflict are the caller-supplied resume-validation
// verdict flags orchestrator.ResumeCmdRequest documents (real validation
// is runtime-a08's not-yet-built scope) — mutually exclusive, and both
// default to false (meaning "valid", the common case).
func NewResumeCmd(deps orchestrator.PauseLifecycleDeps) *cobra.Command {
	var pauseID string
	var quotaUnsafe, conflict bool
	cmd := &cobra.Command{
		Use:   "resume",
		Short: "Resume a paused session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if pauseID == "" {
				return &domain.Error{Code: domain.ErrCodeValidation, Message: "resume: --pause-id is required", Retryable: false}
			}
			if quotaUnsafe && conflict {
				return &domain.Error{Code: domain.ErrCodeValidation, Message: "resume: --quota-unsafe and --conflict are mutually exclusive", Retryable: false}
			}
			result, err := orchestrator.ResumeCmd(cmd.Context(), deps, orchestrator.ResumeCmdRequest{
				PauseID:     domain.PauseID(pauseID),
				QuotaUnsafe: quotaUnsafe,
				Conflict:    conflict,
			})
			if err != nil {
				return err
			}
			body, err := marshalOrError("resume", resumeOutput{
				SchemaVersion: "auspex.resume.v1",
				PauseID:       string(result.Record.ID),
				Status:        string(result.Record.Status),
			})
			if err != nil {
				return err
			}
			return writeJSON(cmd, body)
		},
	}
	cmd.Flags().StringVar(&pauseID, "pause-id", "", "Pause ID to resume")
	cmd.Flags().BoolVar(&quotaUnsafe, "quota-unsafe", false, "Report resume validation found quota still unsafe (reschedules)")
	cmd.Flags().BoolVar(&conflict, "conflict", false, "Report resume validation found a repository/session conflict (blocks)")
	return cmd
}

// NewSchedulerCmd builds the REAL `auspex scheduler run-once` command,
// wired against deps. This is the runtime-b07 constructor
// internal/app/wiring.App.RootCmd() uses in place of the package-private
// `scheduler` stub in root.go.
func NewSchedulerCmd(deps orchestrator.PauseLifecycleDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scheduler",
		Short: "Operate the durable wake scheduler",
	}
	var owner string
	runOnce := &cobra.Command{
		Use:   "run-once",
		Short: "Run a single scheduler sweep and exit",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := orchestrator.SchedulerRunOnceCmd(cmd.Context(), deps, orchestrator.SchedulerRunOnceRequest{Owner: owner})
			if err != nil {
				return err
			}
			out := schedulerRunOnceOutput{
				SchemaVersion: "auspex.scheduler-run-once.v1",
				Claimed:       result.Claimed,
			}
			if result.Claimed {
				out.WakeJobID = string(result.Job.ID)
				out.PauseID = string(result.Job.PauseID)
				out.Status = result.Job.Status
			}
			body, err := marshalOrError("scheduler run-once", out)
			if err != nil {
				return err
			}
			return writeJSON(cmd, body)
		},
	}
	runOnce.Flags().StringVar(&owner, "owner", "", "Lease-owner identity for this sweep (defaults to a fixed CLI identity)")
	cmd.AddCommand(runOnce)
	return cmd
}

type pauseRequestOutput struct {
	SchemaVersion string `json:"schema_version"`
	PauseID       string `json:"pause_id"`
	Status        string `json:"status"`
	Created       bool   `json:"created"`
}

type pauseCancelOutput struct {
	SchemaVersion string `json:"schema_version"`
	PauseID       string `json:"pause_id"`
	Status        string `json:"status"`
}

type resumeOutput struct {
	SchemaVersion string `json:"schema_version"`
	PauseID       string `json:"pause_id"`
	Status        string `json:"status"`
}

type schedulerRunOnceOutput struct {
	SchemaVersion string `json:"schema_version"`
	Claimed       bool   `json:"claimed"`
	WakeJobID     string `json:"wake_job_id,omitempty"`
	PauseID       string `json:"pause_id,omitempty"`
	Status        string `json:"status,omitempty"`
}

// marshalOrError encodes v to JSON, converting an encoding failure into the
// frozen domain.Error shape (CONTRACT_FREEZE.md "Error contract") rather
// than panicking — mirrors NewCheckpointCmd/NewStatusCmd's own inline
// encode-or-error pattern exactly, factored out here since this file has
// four such call sites instead of one.
func marshalOrError(commandPath string, v any) ([]byte, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return nil, &domain.Error{
			Code:      domain.ErrCodeInternal,
			Message:   commandPath + ": encoding response: " + err.Error(),
			Retryable: false,
		}
	}
	return body, nil
}
