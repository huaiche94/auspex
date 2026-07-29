package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/orchestrator"
)

// NewProgressCmd builds the REAL `auspex progress ...` subtree, wired
// against deps (internal/orchestrator.ProgressCompleteDeps). This is the
// issue-#1 constructor internal/app/wiring.App.RootCmd() uses in place of
// the package-private `progress` stub tree in root.go. Exported for the
// same reason as NewHookClaudeCmd/NewCheckpointCmd (see hook.go/
// checkpoint.go): internal/app/wiring is a different package that needs to
// call it.
func NewProgressCmd(deps orchestrator.ProgressCompleteDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "progress",
		Short: "Inspect the Progress Tree",
	}
	cmd.AddCommand(newProgressShowCmd(deps), newProgressCompleteCmd(deps))
	return cmd
}

// newProgressShowCmd builds `auspex progress show` (issue #138) — the
// read-only snapshot of a task's Progress Tree via the frozen
// app.ProgressTreeService.Snapshot port, called directly from the leaf
// per the `checkpoint restore` precedent (checkpoint.go): a pure
// read-back has no orchestration to add. The frozen snapshot shape
// carries identifiers, statuses, and kinds only — no node labels or
// content — so the output is privacy-clean by construction
// (Constitution §7).
func newProgressShowCmd(deps orchestrator.ProgressCompleteDeps) *cobra.Command {
	var taskID string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the current Progress Tree snapshot",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if taskID == "" {
				return &domain.Error{Code: domain.ErrCodeValidation, Message: "progress show: --task is required", Retryable: false}
			}
			if deps.ProgressTree == nil {
				return &domain.Error{Code: domain.ErrCodeUnavailable, Message: "progress show: ProgressTreeService is not wired", Retryable: false}
			}
			snapshot, err := deps.ProgressTree.Snapshot(cmd.Context(), domain.TaskID(taskID))
			if err != nil {
				return err
			}

			if jsonOut {
				nodes := make([]progressShowNode, 0, len(snapshot.Nodes))
				for _, n := range snapshot.Nodes {
					nodes = append(nodes, progressShowNode{
						ID:     string(n.ID),
						Status: string(n.Status),
						Kind:   string(n.Kind),
					})
				}
				body, err := marshalOrError("progress show", progressShowOutput{
					SchemaVersion: "auspex.progress-show.v1",
					TaskID:        string(snapshot.TaskID),
					NodeCount:     len(nodes),
					Nodes:         nodes,
				})
				if err != nil {
					return err
				}
				return writeJSON(cmd, body)
			}
			out := cmd.OutOrStdout()
			if _, err := fmt.Fprintf(out, "task %s: %d node(s)\n", snapshot.TaskID, len(snapshot.Nodes)); err != nil {
				return err
			}
			for _, n := range snapshot.Nodes {
				if _, err := fmt.Fprintf(out, "  %s  %-12s %s\n", n.ID, n.Status, n.Kind); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&taskID, "task", "", "Task ID whose Progress Tree to show")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON output")
	return cmd
}

type progressShowNode struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Kind   string `json:"kind"`
}

type progressShowOutput struct {
	SchemaVersion string             `json:"schema_version"`
	TaskID        string             `json:"task_id"`
	NodeCount     int                `json:"node_count"`
	Nodes         []progressShowNode `json:"nodes"`
}

// newProgressCompleteCmd builds `auspex progress complete` — the
// explicit-completion half of issue #1's "explicit completion + event
// correlation" design. A completion is an EXPLICIT, evidence-carrying
// request (Constitution §6.2: "a node may not become completed without
// durable, validator-checked artifact evidence... never an agent's own
// claim of done"), so every input is a required, caller-supplied flag:
//
//   - --node: the ProgressNodeID to complete.
//   - --idempotency-key: caller-supplied so retries are safe by
//     construction (the frozen app.CompleteNodeRequest.IdempotencyKey
//     contract: "same completion request replayed with the same key MUST
//     return the same result") — this command never mints one itself,
//     because a fresh key per invocation would turn every retry into a
//     new completion attempt, defeating the ledger.
//   - --artifact kind=path (repeatable): each maps to a
//     domain.ArtifactRef with a `file:<path>` URI, the artifact-URI
//     convention internal/progress.FileStager parses (stager.go's
//     sourcePath). The stager reads the file, computes its real SHA-256,
//     and the validators check it — nothing about the artifact is taken
//     on the caller's word.
//
// Validator rejections, conflicting-evidence conflicts, and every other
// service error surface verbatim as the typed *domain.Error the service
// constructs, rendered through the same uniform JSON error envelope as
// every other command (errors.go; errorcontract_test.go's audit covers
// this command like all the rest).
func newProgressCompleteCmd(deps orchestrator.ProgressCompleteDeps) *cobra.Command {
	var nodeID, idempotencyKey string
	var artifactSpecs []string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "complete",
		Short: "Complete a Progress Tree node with artifact evidence",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if nodeID == "" {
				return &domain.Error{Code: domain.ErrCodeValidation, Message: "progress complete: --node is required", Retryable: false}
			}
			if idempotencyKey == "" {
				return &domain.Error{Code: domain.ErrCodeValidation, Message: "progress complete: --idempotency-key is required", Retryable: false}
			}
			refs, err := parseArtifactSpecs(artifactSpecs)
			if err != nil {
				return err
			}

			result, err := orchestrator.ProgressComplete(cmd.Context(), deps, orchestrator.ProgressCompleteRequest{
				NodeID:         domain.ProgressNodeID(nodeID),
				IdempotencyKey: idempotencyKey,
				Artifacts:      refs,
			})
			if err != nil {
				return err
			}

			if jsonOut {
				body, err := marshalOrError("progress complete", progressCompleteOutput{
					SchemaVersion:     "auspex.progress-complete.v1",
					NodeID:            string(result.Node.ID),
					NodeStatus:        string(result.Node.Status),
					StateCheckpointID: string(result.Checkpoint.ID),
				})
				if err != nil {
					return err
				}
				return writeJSON(cmd, body)
			}
			// Human mode: one plain summary line. --json is the machine
			// mode this command's own contract tests parse; agents/
			// runtime.md's "machine mode never emits decorative text to
			// stdout" constrains the JSON path, which stays pure above.
			_, writeErr := fmt.Fprintf(cmd.OutOrStdout(), "node %s %s (state checkpoint %s)\n",
				result.Node.ID, result.Node.Status, result.Checkpoint.ID)
			return writeErr
		},
	}
	cmd.Flags().StringVar(&nodeID, "node", "", "Progress node ID to complete")
	cmd.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "Caller-supplied idempotency key (a retried command with the same key is a safe replay)")
	cmd.Flags().StringArrayVar(&artifactSpecs, "artifact", nil, "Artifact evidence as kind=path (repeatable)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit machine-readable JSON output")
	return cmd
}

// parseArtifactSpecs maps repeated `--artifact kind=path` values onto
// domain.ArtifactRef. Only Kind and a `file:<path>` URI are populated:
// Bytes/SHA256 are measured by the stager from the actual file content
// (never trusted from the caller — stager.go's Stage doc comment), and ID
// is a stable per-invocation ordinal so the same command retried with the
// same flags produces the same request shape (idempotent replay,
// complete_node.go's payloadDigest depends only on URI+SHA256 anyway).
func parseArtifactSpecs(specs []string) ([]domain.ArtifactRef, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	refs := make([]domain.ArtifactRef, 0, len(specs))
	for i, spec := range specs {
		kind, path, found := strings.Cut(spec, "=")
		if !found || kind == "" || path == "" {
			return nil, &domain.Error{
				Code:      domain.ErrCodeValidation,
				Message:   fmt.Sprintf("progress complete: --artifact %q is not of the form kind=path", spec),
				Retryable: false,
				Details:   map[string]string{"artifact": spec},
			}
		}
		refs = append(refs, domain.ArtifactRef{
			ID:   fmt.Sprintf("artifact-%d", i+1),
			Kind: kind,
			URI:  "file:" + path,
		})
	}
	return refs, nil
}

type progressCompleteOutput struct {
	SchemaVersion     string `json:"schema_version"`
	NodeID            string `json:"node_id"`
	NodeStatus        string `json:"node_status"`
	StateCheckpointID string `json:"state_checkpoint_id"`
}
