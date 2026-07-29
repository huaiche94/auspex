package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
)

// NewStateCmd builds the REAL `auspex state show` subtree (issue #138),
// wired against the frozen app.StateCheckpointService port and called
// directly from the leaf per the `checkpoint restore` precedent
// (checkpoint.go): LoadLatest is a pure read-back with no orchestration
// to add. This is the constructor internal/app/wiring.App.RootCmd() uses
// in place of the package-private `state` stub tree in root.go; exported
// for the same reason as NewProgressCmd.
//
// Every rendered field is an identifier, version, count, timestamp, or
// digest, plus the checkpoint's own next-action description — which is
// Auspex-authored manifest state (ADD Appendix B's `next_action` block),
// not provider/prompt content, and is already durably persisted in the
// user's local DB by the checkpoint itself (Constitution §7 concerns raw
// provider content; none is present here).
func NewStateCmd(state app.StateCheckpointService) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "state",
		Short: "Inspect State Checkpoints",
	}
	cmd.AddCommand(newStateShowCmd(state))
	return cmd
}

func newStateShowCmd(state app.StateCheckpointService) *cobra.Command {
	var taskID string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the latest state checkpoint",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if taskID == "" {
				return &domain.Error{Code: domain.ErrCodeValidation, Message: "state show: --task is required", Retryable: false}
			}
			if state == nil {
				return &domain.Error{Code: domain.ErrCodeUnavailable, Message: "state show: StateCheckpointService is not wired", Retryable: false}
			}
			cp, err := state.LoadLatest(cmd.Context(), domain.TaskID(taskID))
			if err != nil {
				return err
			}

			if jsonOut {
				out := stateShowOutput{
					SchemaVersion:       "auspex.state-show.v1",
					CheckpointID:        string(cp.ID),
					TaskID:              string(cp.TaskID),
					ProgressTreeVersion: cp.ProgressTreeVersion,
					CompletedNodeCount:  len(cp.CompletedNodeIDs),
					NextAction:          cp.NextAction.Description,
					CreatedAt:           cp.CreatedAt.UTC().Format(time.RFC3339),
					IntegritySHA256:     cp.IntegritySHA256,
				}
				if cp.NextAction.NodeID != nil {
					id := string(*cp.NextAction.NodeID)
					out.NextActionNodeID = &id
				}
				if cp.ActiveNodeID != nil {
					id := string(*cp.ActiveNodeID)
					out.ActiveNodeID = &id
				}
				if cp.RepositoryCheckpointID != nil {
					id := string(*cp.RepositoryCheckpointID)
					out.RepositoryCheckpointID = &id
				}
				body, err := marshalOrError("state show", out)
				if err != nil {
					return err
				}
				return writeJSON(cmd, body)
			}

			w := cmd.OutOrStdout()
			active := "-"
			if cp.ActiveNodeID != nil {
				active = string(*cp.ActiveNodeID)
			}
			repoCkpt := "-"
			if cp.RepositoryCheckpointID != nil {
				repoCkpt = string(*cp.RepositoryCheckpointID)
			}
			_, err = fmt.Fprintf(w,
				"checkpoint %s (task %s)\n  tree version %d, %d completed node(s), active node %s\n  next action %q, repository checkpoint %s\n  created %s, integrity sha256 %s\n",
				cp.ID, cp.TaskID, cp.ProgressTreeVersion, len(cp.CompletedNodeIDs), active,
				cp.NextAction.Description, repoCkpt,
				cp.CreatedAt.UTC().Format(time.RFC3339), cp.IntegritySHA256)
			return err
		},
	}
	cmd.Flags().StringVar(&taskID, "task", "", "Task ID whose latest state checkpoint to show")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON output")
	return cmd
}

type stateShowOutput struct {
	SchemaVersion          string  `json:"schema_version"`
	CheckpointID           string  `json:"checkpoint_id"`
	TaskID                 string  `json:"task_id"`
	ProgressTreeVersion    int64   `json:"progress_tree_version"`
	ActiveNodeID           *string `json:"active_node_id,omitempty"`
	CompletedNodeCount     int     `json:"completed_node_count"`
	NextAction             string  `json:"next_action"`
	NextActionNodeID       *string `json:"next_action_node_id,omitempty"`
	RepositoryCheckpointID *string `json:"repository_checkpoint_id,omitempty"`
	CreatedAt              string  `json:"created_at"`
	IntegritySHA256        string  `json:"integrity_sha256"`
}
