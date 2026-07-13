// service.go: the real, production app.ProgressTreeService adapter — a
// corrective addition identified by the Final integration gate review
// (contract-integrator-final), not a numbered DAG node. Across a01-a09 this
// role built and exhaustively tested every individual piece the frozen
// 7-method app.ProgressTreeService contract needs (NodeStore, EdgeStore,
// ArtifactStore, the node state machine, CompleteNode's atomic protocol,
// Reconciler) but never assembled one concrete type implementing that exact
// interface — confirmed by grepping the whole repo for
// `var _ app.ProgressTreeService` before writing this file: only
// internal/testutil/fakes.FakeProgressTreeService (a test double) existed.
// That gap is why cmd/auspex/main.go could never be wired to real
// services: composing the app's root requires a real implementation of
// every frozen port, and this one never existed until now.
//
// Service's job here is composition and DTO-shape translation only — every
// piece of real logic (state transitions, atomicity, idempotency, crash
// recovery, dependency/parent-ordering checks) already exists and is
// already exhaustively tested in this package's own NodeStore/EdgeStore/
// ArtifactStore/CompleteNode/Reconciler (a01-a09) and in
// internal/statecheckpoint (this role's sibling Part A package). Nothing in
// this file reimplements any of that; it only translates between the
// frozen app.* request/response DTOs and this package's own, narrower
// input/output shapes, exactly the same "one-line field copy" adapter
// pattern complete_node.go's own CompleteNodeInput doc comment anticipated
// ("the wiring layer that implements app.ProgressTreeService adapts
// app.CompleteNodeRequest to this shape").
package progress

import (
	"context"
	"fmt"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
)

// Service implements app.ProgressTreeService (internal/app/ports.go, frozen
// by contract-integrator) by composing this package's own already-tested
// stores and protocols. Every field is a pointer to a type this role built
// and tested across a01-a09; Service itself introduces no new persistence
// or business logic.
//
// EdgeStore and ArtifactStore are deliberately NOT fields here: none of the
// 7 frozen app.ProgressTreeService methods need to reach them directly —
// CompleteOp (this package's own *CompleteNode) already owns and uses both
// internally for dependency-policy checks and evidence rows, and Reconciler
// already owns what it needs of NodeStore/statecheckpoint.Store. Adding
// fields Service's own methods never touch would be exactly the kind of
// unused abstraction Constitution §7.10 warns against.
type Service struct {
	Tasks      *TaskStore
	Nodes      *NodeStore
	CompleteOp *CompleteNode
	Reconciler *Reconciler
	Clock      domain.Clock
	IDs        domain.IDGenerator
}

// NewService constructs a Service from this package's own already-wired
// components. Callers (production wiring, integration tests) assemble each
// field exactly as a01-a09's own tests already do (see
// complete_node_integration_test.go's newFullStackHarness for the
// reference wiring shape) and hand them to this constructor rather than
// this package re-deriving how to build a CompleteNode/Reconciler itself.
func NewService(tasks *TaskStore, nodes *NodeStore, completeOp *CompleteNode, reconciler *Reconciler, clock domain.Clock, ids domain.IDGenerator) *Service {
	return &Service{
		Tasks:      tasks,
		Nodes:      nodes,
		CompleteOp: completeOp,
		Reconciler: reconciler,
		Clock:      clock,
		IDs:        ids,
	}
}

var _ app.ProgressTreeService = (*Service)(nil)

// CreateTask creates a new tasks row (migrations/0004_tasks.sql) via
// TaskStore.Insert, per app.ProgressTreeService's frozen CreateTask
// method — the Progress Tree's own root: every node/edge/artifact this
// package manages is scoped to a task_id this method mints.
func (s *Service) CreateTask(ctx context.Context, req app.CreateTaskRequest) (app.Task, error) {
	if req.WorktreeID == "" {
		return app.Task{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "progress: CreateTask requires a non-empty WorktreeID",
			Retryable: false,
		}
	}
	if req.ObjectiveHash == "" {
		return app.Task{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "progress: CreateTask requires a non-empty ObjectiveHash",
			Retryable: false,
		}
	}

	taskID := domain.TaskID(s.IDs.NewID())
	row := TaskRow{
		ID:            taskID,
		SessionID:     req.SessionID,
		WorktreeID:    req.WorktreeID,
		ObjectiveHash: req.ObjectiveHash,
		Status:        TaskOpen,
	}
	if err := s.Tasks.Insert(ctx, row); err != nil {
		return app.Task{}, err
	}

	return app.Task{ID: taskID, Status: string(TaskOpen)}, nil
}

// UpsertPlan inserts (or extends) a task's initial Progress Tree node/edge
// structure. Per this role's own frozen DTOs, app.UpsertPlanRequest carries
// only a TaskID (the actual node/edge plan shape a caller supplies is not
// yet part of the frozen contract's request body) — so, honoring the
// interface exactly as frozen, this method's job scoped to what the DTO
// actually carries is: confirm the task exists, then report the Progress
// Tree's current version (this package's own NodeStore.ListByTask count,
// the same "version = len(nodes)" convention statecheckpoint.Service.Create
// already established for ProgressTreeSummary.Version). A caller that wants
// to seed actual nodes/edges alongside this call uses NodeStore.Insert/
// EdgeStore.Insert directly (exactly as this package's own tests do,
// e.g. insertNode in complete_node_helpers_test.go) — this method does not
// invent a bulk-node-creation request shape ports.go does not define; doing
// so would be widening the frozen contract, which only contract-integrator
// may do (Constitution §4).
func (s *Service) UpsertPlan(ctx context.Context, req app.UpsertPlanRequest) (app.ProgressTree, error) {
	if req.TaskID == "" {
		return app.ProgressTree{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "progress: UpsertPlan requires a non-empty TaskID",
			Retryable: false,
		}
	}
	if _, err := s.Tasks.Get(ctx, req.TaskID); err != nil {
		return app.ProgressTree{}, err
	}

	nodes, err := s.Nodes.ListByTask(ctx, req.TaskID)
	if err != nil {
		return app.ProgressTree{}, err
	}
	version := int64(len(nodes))

	var activeNodeID *domain.ProgressNodeID
	for _, n := range nodes {
		if n.Status == domain.NodeInProgress || n.Status == domain.NodeCheckpointing {
			id := n.ID
			activeNodeID = &id
			break
		}
	}
	if err := s.Tasks.SetActiveNodeAndVersion(ctx, req.TaskID, activeNodeID, version); err != nil {
		return app.ProgressTree{}, err
	}

	return app.ProgressTree{TaskID: req.TaskID, Version: version}, nil
}

// StartNode transitions a node to in_progress via the state machine
// (ValidateTransition, enforced inside NodeStore.TransitionStatus) and
// records its started_at timestamp, per app.ProgressTreeService's frozen
// StartNode method. This does not reimplement the state machine: it is a
// thin caller of the exact same NodeStore this package's own tests already
// exercise (e.g. moveNodeToInProgress in complete_node_helpers_test.go),
// generalized here to whatever status a node is actually in (pending/ready/
// paused/blocked/failed all have a valid edge to in_progress per
// statemachine.go's transitions table) rather than assuming pending->ready
// happened first.
func (s *Service) StartNode(ctx context.Context, req app.StartNodeRequest) (app.ProgressNode, error) {
	if req.NodeID == "" {
		return app.ProgressNode{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "progress: StartNode requires a non-empty NodeID",
			Retryable: false,
		}
	}

	node, err := s.Nodes.Get(ctx, req.NodeID)
	if err != nil {
		return app.ProgressNode{}, err
	}

	if err := s.Nodes.TransitionStatus(ctx, req.NodeID, node.Status, domain.NodeInProgress, node.Version); err != nil {
		return app.ProgressNode{}, err
	}
	startedAt := s.Clock.Now().UTC().Format(time.RFC3339)
	if err := s.Nodes.SetTimestamps(ctx, req.NodeID, &startedAt, nil); err != nil {
		return app.ProgressNode{}, err
	}

	refreshed, err := s.Nodes.Get(ctx, req.NodeID)
	if err != nil {
		return app.ProgressNode{}, err
	}
	return nodeToDTO(refreshed), nil
}

// CompleteNode is the frozen app.ProgressTreeService method (not to be
// confused with this package's own CompleteNode struct/type, constructed
// separately and injected as Service.CompleteOp) — it delegates the ENTIRE
// atomic protocol to CompleteOp.Run unchanged, translating only the
// request/result DTO shapes at the boundary, exactly as complete_node.go's
// own CompleteNodeInput doc comment describes this exact seam ("a one-line
// field copy, since the fields already match CONTRACT_FREEZE.md's frozen
// contract"). No completion logic, atomicity, idempotency, or crash-
// recovery behavior is reimplemented here.
func (s *Service) CompleteNode(ctx context.Context, req app.CompleteNodeRequest) (app.ProgressNode, domain.StateCheckpoint, error) {
	result, err := s.CompleteOp.Run(ctx, CompleteNodeInput{
		NodeID:         req.NodeID,
		IdempotencyKey: req.IdempotencyKey,
		Artifacts:      req.Artifacts,
	})
	if err != nil {
		return app.ProgressNode{}, domain.StateCheckpoint{}, err
	}

	checkpoint := stateCheckpointFromResult(result)
	return nodeToDTO(result.Node), checkpoint, nil
}

// FailNode transitions a node to failed via the state machine plus
// NodeStore, per app.ProgressTreeService's frozen FailNode method. The
// caller-supplied FailureClass (domain.FailureClass) is not yet a column
// this package's own progress_nodes schema (0020) persists per node — the
// frozen FailNodeRequest DTO accepts it, but no migration in this role's
// owned 0020-0029 range added a failure_class column, and adding one now
// would be a schema change beyond this corrective addition's scope (a
// genuinely new column needs its own migration + ADR discussion, not a
// silent addition inside a composition-only fix). This method still
// performs the real, durable state transition the interface promises;
// FailureClass is accepted and validated as non-empty but not yet persisted
// as its own column — a documented, narrow gap, not a silently dropped
// contract term.
func (s *Service) FailNode(ctx context.Context, req app.FailNodeRequest) (app.ProgressNode, error) {
	if req.NodeID == "" {
		return app.ProgressNode{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "progress: FailNode requires a non-empty NodeID",
			Retryable: false,
		}
	}
	if req.FailureClass == "" {
		return app.ProgressNode{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "progress: FailNode requires a non-empty FailureClass",
			Retryable: false,
		}
	}

	node, err := s.Nodes.Get(ctx, req.NodeID)
	if err != nil {
		return app.ProgressNode{}, err
	}
	if err := s.Nodes.TransitionStatus(ctx, req.NodeID, node.Status, domain.NodeFailed, node.Version); err != nil {
		return app.ProgressNode{}, err
	}

	refreshed, err := s.Nodes.Get(ctx, req.NodeID)
	if err != nil {
		return app.ProgressNode{}, err
	}
	return nodeToDTO(refreshed), nil
}

// Snapshot returns a full read of a task's Progress Tree nodes, per
// app.ProgressTreeService's frozen Snapshot method. app.ProgressTreeSnapshot
// (ports.go) carries only TaskID + Nodes today — edges and artifacts are
// this package's own richer NodeStore/EdgeStore/ArtifactStore reads
// (available to any caller that needs them directly, e.g. via
// EdgeStore.ListByTask/ArtifactStore.ListByNode), not yet part of the
// frozen response shape; this method returns exactly what
// app.ProgressTreeSnapshot defines, not more.
func (s *Service) Snapshot(ctx context.Context, taskID domain.TaskID) (app.ProgressTreeSnapshot, error) {
	if taskID == "" {
		return app.ProgressTreeSnapshot{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "progress: Snapshot requires a non-empty TaskID",
			Retryable: false,
		}
	}
	nodes, err := s.Nodes.ListByTask(ctx, taskID)
	if err != nil {
		return app.ProgressTreeSnapshot{}, err
	}
	out := make([]app.ProgressNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, nodeToDTO(n))
	}
	return app.ProgressTreeSnapshot{TaskID: taskID, Nodes: out}, nil
}

// Reconcile delegates to this package's own already-tested Reconciler
// (reconcile.go, checkpoint-a04/a06/a09's startup-reconciliation proof),
// translating its ReconcileReport into the frozen app.ReconcileResult
// shape. app.ReconcileResult.ReconciledNodes is every node ID Reconciler's
// task-scoped pass considered while checking checkpoint-integrity/staged-
// evidence consistency for this task (ADD §18.9's reconciliation steps 1-3,
// scoped to what Part A owns) — the full node list for the task, since
// Reconcile's job is confirming the WHOLE tree's durable state is
// internally consistent, not a subset. No reconciliation logic is
// reimplemented here; Reconciler.Reconcile alone decides what is
// orphaned/violated.
func (s *Service) Reconcile(ctx context.Context, req app.ReconcileProgressRequest) (app.ReconcileResult, error) {
	if req.TaskID == "" {
		return app.ReconcileResult{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "progress: Reconcile requires a non-empty TaskID",
			Retryable: false,
		}
	}

	if _, err := s.Reconciler.Reconcile(ctx, req.TaskID); err != nil {
		return app.ReconcileResult{}, fmt.Errorf("progress: Reconcile: %w", err)
	}

	nodes, err := s.Nodes.ListByTask(ctx, req.TaskID)
	if err != nil {
		return app.ReconcileResult{}, err
	}
	ids := make([]domain.ProgressNodeID, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}

	return app.ReconcileResult{TaskID: req.TaskID, ReconciledNodes: ids}, nil
}

// nodeToDTO converts this package's own Node (node_store.go) into the
// frozen app.ProgressNode shape every StartNode/CompleteNode/FailNode/
// Snapshot response above returns.
func nodeToDTO(n Node) app.ProgressNode {
	return app.ProgressNode{
		ID:     n.ID,
		TaskID: n.TaskID,
		Status: n.Status,
		Kind:   n.Kind,
	}
}

// stateCheckpointFromResult converts CompleteNode's own CompleteNodeResult
// (statecheckpoint.Row + statecheckpoint.Manifest) into the frozen
// domain.StateCheckpoint shape the interface's CompleteNode method returns
// — the identical row-to-domain conversion statecheckpoint.Service's own
// rowToDomain (service.go) already performs for its Create/LoadLatest/
// Snapshot methods, applied here to CompleteNode's result instead of a
// freshly-loaded store row, so both entry points into a State Checkpoint
// (the completion-triggered path and the standalone Create path) produce
// the exact same domain shape for a caller that doesn't care which path
// produced it.
func stateCheckpointFromResult(result CompleteNodeResult) domain.StateCheckpoint {
	row := result.Checkpoint
	manifest := result.Manifest

	createdAt, err := time.Parse(time.RFC3339, row.CreatedAt)
	if err != nil {
		createdAt = manifest.CreatedAt
	}

	quotaIDs := make([]string, 0, len(manifest.Quota))
	for _, q := range manifest.Quota {
		quotaIDs = append(quotaIDs, q.LimitID)
	}

	return domain.StateCheckpoint{
		ID:                     row.ID,
		TaskID:                 row.TaskID,
		ProgressTreeVersion:    row.ProgressTreeVersion,
		ActiveNodeID:           row.ActiveNodeID,
		CompletedNodeIDs:       manifest.ProgressTree.CompletedNodeIDs,
		NextAction:             domain.NextAction{Description: manifest.NextAction.Description, NodeID: manifest.NextAction.NodeID},
		RepositorySnapshotID:   manifest.Repository.WorktreeID,
		ProviderSessionID:      manifest.Provider.SessionID,
		ProviderTurnID:         manifest.Provider.TurnID,
		LatestQuotaIDs:         quotaIDs,
		LatestContextID:        "",
		RepositoryCheckpointID: row.RepositoryCheckpointID,
		CreatedAt:              createdAt,
		IntegritySHA256:        row.IntegritySHA256,
	}
}
