package cli

import (
	"github.com/spf13/cobra"

	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/orchestrator"
)

// NewDecisionCmd builds the REAL `preflight decision {allow,deny}` command
// tree, wired against deps (internal/orchestrator.DecisionDeps). This is
// the runtime-b06 constructor internal/app/wiring.App.RootCmd() uses in
// place of the package-private `decision` stub in root.go.
//
// --evaluation-id/--turn-id/--prompt-hash/--authorization-id/
// --snapshot-fingerprint/--repository-checkpoint-id are read as direct
// flags: no resolver port exists yet, the same documented scope boundary
// runtime-b03's Evaluate pipeline and runtime-b05's CheckpointCreate both
// already established. --authorization-id selects DecisionAllowCmd's
// consume flow (a resubmitted prompt) when supplied; omitting it selects
// the issue flow (see orchestrator/decision.go's package comment for the
// full two-flow rationale).
func NewDecisionCmd(deps orchestrator.DecisionDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "decision",
		Short: "Act on a pending evaluation decision",
	}
	cmd.AddCommand(newDecisionAllowCmd(deps), newDecisionDenyCmd(deps))
	return cmd
}

func newDecisionAllowCmd(deps orchestrator.DecisionDeps) *cobra.Command {
	var evaluationID, turnID, promptHash, authorizationID, snapshotFingerprint, repositoryCheckpointID string
	cmd := &cobra.Command{
		Use:   "allow",
		Short: "Issue a one-time authorization allowing the turn to proceed, or consume one on resubmission",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if turnID == "" {
				return &domain.Error{Code: domain.ErrCodeValidation, Message: "decision allow: --turn-id is required", Retryable: false}
			}
			req := orchestrator.DecisionAllowRequest{
				EvaluationID:        domain.EvaluationID(evaluationID),
				TurnID:              domain.TurnID(turnID),
				PromptHash:          promptHash,
				AuthorizationID:     authorizationID,
				SnapshotFingerprint: snapshotFingerprint,
			}
			if repositoryCheckpointID != "" {
				id := domain.RepositoryCheckpointID(repositoryCheckpointID)
				req.RepositoryCheckpointID = &id
			}

			result, err := orchestrator.DecisionAllowCmd(cmd.Context(), deps, req)
			if err != nil {
				return err
			}

			out := decisionAllowOutput{
				SchemaVersion:   "preflight.decision-allow.v1",
				Issued:          result.Issued,
				Consumed:        result.Consumed,
				AuthorizationID: result.Authorization.ID,
			}
			if result.Issued {
				out.Action = string(result.Decision.Action)
			}
			body, err := marshalOrError("decision allow", out)
			if err != nil {
				return err
			}
			return writeJSON(cmd, body)
		},
	}
	cmd.Flags().StringVar(&evaluationID, "evaluation-id", "", "Evaluation ID to allow (issue flow)")
	cmd.Flags().StringVar(&turnID, "turn-id", "", "Turn ID this authorization is bound to")
	cmd.Flags().StringVar(&promptHash, "prompt-hash", "", "Prompt hash this authorization is bound to")
	cmd.Flags().StringVar(&authorizationID, "authorization-id", "", "Authorization ID to consume (resubmission/consume flow); omit for the issue flow")
	cmd.Flags().StringVar(&snapshotFingerprint, "snapshot-fingerprint", "", "Snapshot fingerprint to bind the new authorization to (issue flow only)")
	cmd.Flags().StringVar(&repositoryCheckpointID, "repository-checkpoint-id", "", "Repository Checkpoint ID to bind the new authorization to (issue flow only)")
	return cmd
}

func newDecisionDenyCmd(deps orchestrator.DecisionDeps) *cobra.Command {
	var evaluationID string
	cmd := &cobra.Command{
		Use:   "deny",
		Short: "Deny the pending turn",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if evaluationID == "" {
				return &domain.Error{Code: domain.ErrCodeValidation, Message: "decision deny: --evaluation-id is required", Retryable: false}
			}
			result, err := orchestrator.DecisionDenyCmd(cmd.Context(), deps, orchestrator.DecisionDenyRequest{
				EvaluationID: domain.EvaluationID(evaluationID),
			})
			if err != nil {
				return err
			}
			out := decisionDenyOutput{
				SchemaVersion: "preflight.decision-deny.v1",
				Action:        string(result.Decision.Action),
			}
			body, err := marshalOrError("decision deny", out)
			if err != nil {
				return err
			}
			return writeJSON(cmd, body)
		},
	}
	cmd.Flags().StringVar(&evaluationID, "evaluation-id", "", "Evaluation ID to deny")
	return cmd
}

type decisionAllowOutput struct {
	SchemaVersion   string `json:"schema_version"`
	Issued          bool   `json:"issued"`
	Consumed        bool   `json:"consumed"`
	AuthorizationID string `json:"authorization_id"`
	Action          string `json:"action,omitempty"`
}

type decisionDenyOutput struct {
	SchemaVersion string `json:"schema_version"`
	Action        string `json:"action"`
}
