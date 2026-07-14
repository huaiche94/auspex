package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/orchestrator"
)

// NewCheckpointCmd builds the REAL `auspex checkpoint create` command,
// wired against deps (internal/orchestrator.CheckpointCreateDeps). This is
// the runtime-b05 constructor internal/app/wiring.App.RootCmd() uses in
// place of the package-private `checkpoint create` stub in root.go.
// Exported for the same reason as NewHookClaudeCmd (see hook.go): a
// different package (internal/app/wiring) needs to call it.
//
// TaskID/WorktreeID are read from --task-id/--worktree-id flags: no
// repository/worktree/session resolver port exists yet (see
// internal/orchestrator/doc.go's "What resolve means" note), so this
// command takes them as direct input rather than inferring them from the
// current working directory — the same documented scope boundary
// runtime-b03's Evaluate pipeline already established.
func NewCheckpointCmd(deps orchestrator.CheckpointCreateDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "checkpoint",
		Short: "Manage Auspex checkpoints",
	}

	var taskID, worktreeID string
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a state checkpoint followed by a repository checkpoint",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if taskID == "" {
				return &domain.Error{
					Code:      domain.ErrCodeValidation,
					Message:   "checkpoint create: --task-id is required",
					Retryable: false,
				}
			}
			if worktreeID == "" {
				return &domain.Error{
					Code:      domain.ErrCodeValidation,
					Message:   "checkpoint create: --worktree-id is required",
					Retryable: false,
				}
			}

			result, err := orchestrator.CheckpointCreate(cmd.Context(), deps, orchestrator.CheckpointCreateRequest{
				TaskID:     domain.TaskID(taskID),
				WorktreeID: domain.WorktreeID(worktreeID),
			})
			if err != nil {
				return err
			}

			// Stable schema-versioned machine output (agents/runtime.md
			// Part B "JSON and errors": "stable schema-versioned
			// output"); no decorative text, matching the same
			// requirement's "machine mode never emits decorative text
			// to stdout".
			out := checkpointCreateOutput{
				SchemaVersion:               "auspex.checkpoint-create.v1",
				StateCheckpointID:           string(result.State.ID),
				RepositoryCheckpointID:      string(result.Repository.ID),
				RepositoryCheckpointGitHead: result.Repository.GitHead,
			}
			body, encErr := json.Marshal(out)
			if encErr != nil {
				return &domain.Error{
					Code:      domain.ErrCodeInternal,
					Message:   "checkpoint create: encoding response: " + encErr.Error(),
					Retryable: false,
				}
			}
			_, writeErr := cmd.OutOrStdout().Write(append(body, '\n'))
			return writeErr
		},
	}
	create.Flags().StringVar(&taskID, "task-id", "", "Task ID to checkpoint")
	create.Flags().StringVar(&worktreeID, "worktree-id", "", "Worktree ID to checkpoint")

	var restoreID string
	var apply, allowDirty bool
	restore := &cobra.Command{
		Use:   "restore",
		Short: "Restore a repository checkpoint (dry-run by default; --apply mutates)",
		Long: "Runs the full ADD §19.6 restore check sequence (checksum, repository identity,\n" +
			"dirty-target policy, git apply --check on both patches) and reports the verdict.\n" +
			"With --apply, a passing check sequence is followed by the real restore: staged\n" +
			"patch to index+worktree, unstaged patch to worktree, untracked files extracted\n" +
			"(never overwriting existing paths). Restore never moves HEAD, never switches\n" +
			"branches, never creates commits (ADR-048). A dirty target requires --allow-dirty\n" +
			"and, when applying, is captured as a safety checkpoint first.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if restoreID == "" {
				return &domain.Error{
					Code:      domain.ErrCodeValidation,
					Message:   "checkpoint restore: --id is required",
					Retryable: false,
				}
			}
			if deps.RepositoryCheckpoint == nil {
				return &domain.Error{
					Code:      domain.ErrCodeUnavailable,
					Message:   "checkpoint restore: repository checkpoint service is not available",
					Retryable: false,
				}
			}

			result, err := deps.RepositoryCheckpoint.Restore(cmd.Context(), app.RestoreRepositoryCheckpointRequest{
				ID:         domain.RepositoryCheckpointID(restoreID),
				AllowDirty: allowDirty,
				Apply:      apply,
			})
			if err != nil {
				return err
			}

			out := checkpointRestoreOutput{
				SchemaVersion:          "auspex.checkpoint-restore.v1",
				RepositoryCheckpointID: string(result.ID),
				Applied:                result.Applied,
				DryRun:                 !apply,
				UntrackedSkipped:       result.UntrackedSkipped,
			}
			if result.SafetyCheckpointID != nil {
				id := string(*result.SafetyCheckpointID)
				out.SafetyCheckpointID = &id
			}
			body, encErr := json.Marshal(out)
			if encErr != nil {
				return &domain.Error{
					Code:      domain.ErrCodeInternal,
					Message:   "checkpoint restore: encoding response: " + encErr.Error(),
					Retryable: false,
				}
			}
			_, writeErr := cmd.OutOrStdout().Write(append(body, '\n'))
			return writeErr
		},
	}
	restore.Flags().StringVar(&restoreID, "id", "", "Repository checkpoint ID to restore")
	restore.Flags().BoolVar(&apply, "apply", false, "Perform the real restore (default is dry-run)")
	restore.Flags().BoolVar(&allowDirty, "allow-dirty", false, "Proceed even when the target worktree has uncommitted changes (a safety checkpoint is captured first when applying)")

	cmd.AddCommand(create)
	cmd.AddCommand(restore)
	return cmd
}

type checkpointCreateOutput struct {
	SchemaVersion               string `json:"schema_version"`
	StateCheckpointID           string `json:"state_checkpoint_id"`
	RepositoryCheckpointID      string `json:"repository_checkpoint_id"`
	RepositoryCheckpointGitHead string `json:"repository_checkpoint_git_head"`
}

type checkpointRestoreOutput struct {
	SchemaVersion          string   `json:"schema_version"`
	RepositoryCheckpointID string   `json:"repository_checkpoint_id"`
	Applied                bool     `json:"applied"`
	DryRun                 bool     `json:"dry_run"`
	SafetyCheckpointID     *string  `json:"safety_checkpoint_id,omitempty"`
	UntrackedSkipped       []string `json:"untracked_skipped,omitempty"`
}
