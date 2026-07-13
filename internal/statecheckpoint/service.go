// service.go: wires manifest.go/build.go/serialize.go/store.go together
// behind the frozen app.StateCheckpointService contract
// (internal/app/ports.go), so the runtime role's Part A persist phase
// (agents/checkpoint.md Part A "consumed directly by runtime Part A persist
// phase") can depend on the interface without reaching into this package's
// internals or into internal/progress directly.
//
// checkpoint-a04's CompleteNode already builds and inserts a State
// Checkpoint row as part of its own atomic transaction — that is the
// completion-triggered path (Constitution §6.3, "every node completion
// creates a State Checkpoint in the same atomic operation") and stays
// exactly as it is; this file does NOT change or wrap that path. What
// CompleteNode does not provide is a STANDALONE, ad hoc snapshot entry
// point that a caller (e.g. runtime's pause persist-phase, or a manual
// "checkpoint now" request) can invoke against a Progress Tree's CURRENT
// state without also completing a node — that is Create's job here, using
// the exact same Build/Seal/Marshal/Store primitives CompleteNode already
// proved correct, per this role's own lessons-learned note from a04 ("a05
// should verify what remains, likely just Snapshot/verify-API polish").
package statecheckpoint

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
)

// NodeSnapshot is the narrow view of one progress_nodes row Create needs to
// assemble a ProgressTreeSummary and pick an active node — deliberately NOT
// *progress.Node itself, so this package (internal/statecheckpoint) does not
// import internal/progress (that would invert the existing dependency
// direction: internal/progress already imports internal/statecheckpoint,
// per complete_node.go; a cycle is not an option). Callers (the production
// wiring layer, or a test) adapt their own node rows to this shape.
type NodeSnapshot struct {
	ID     domain.ProgressNodeID
	Status domain.ProgressNodeStatus
}

// ArtifactSnapshot is the narrow view of one artifacts row Create needs.
// Same dependency-direction rationale as NodeSnapshot above. Its field set
// is deliberately identical (name, type, and order) to ArtifactSummary
// (manifest.go) so Create can convert a slice of these with a plain type
// cast rather than a field-by-field copy.
type ArtifactSnapshot struct {
	ID               string
	URI              string
	MediaType        string
	Bytes            int64
	SHA256           string
	ValidationStatus string
}

// TreeReader is the seam Service.Create uses to read a task's current
// Progress Tree state without importing internal/progress. Production
// wiring supplies an adapter over *progress.NodeStore/*progress.ArtifactStore;
// tests supply an in-memory fake.
type TreeReader interface {
	ListNodes(ctx context.Context, taskID domain.TaskID) ([]NodeSnapshot, error)
	ListArtifacts(ctx context.Context, taskID domain.TaskID) ([]ArtifactSnapshot, error)
}

// Service implements app.StateCheckpointService.
type Service struct {
	store *Store
	tree  TreeReader
	clock domain.Clock
	ids   domain.IDGenerator

	// HaltAfter is checkpoint-a06's crash-injection seam (mirrors
	// internal/progress.CompleteNode's identical HaltAfter/Phase/HaltError
	// idiom exactly, so both halves of this role's own packages test crash
	// windows the same way): when non-empty, Create returns a *HaltError
	// immediately after completing the named Phase, simulating a process
	// crash at exactly that point. Zero value (empty string) never
	// matches any Phase, so production callers that never touch this
	// field see no behavior change at all.
	HaltAfter Phase
}

// NewService constructs a Service. tree supplies the Progress Tree state
// Create snapshots; store is this package's own CRUD layer (already proven
// by checkpoint-a04's CompleteNode use of the identical type).
func NewService(store *Store, tree TreeReader, clock domain.Clock, ids domain.IDGenerator) *Service {
	return &Service{store: store, tree: tree, clock: clock, ids: ids}
}

// Phase names the distinct steps of Create's sequence, used by
// crash-injection tests to interrupt execution at an exact point and by
// Reconciler to describe what it found — the same vocabulary role
// internal/progress.Phase plays for CompleteNode, scoped to this package's
// own, narrower sequence (checkpoint-a06's assigned reconciliation gap:
// "a05's Create writes a manifest, serializes it, and stores it — what
// crash windows exist there specifically").
type Phase string

const (
	// PhaseReadTree: after ListNodes/ListArtifacts return, before the
	// manifest is assembled. A crash here leaves NOTHING durable at all —
	// Create has not written anything yet, so this window needs no
	// reconciliation; a retry simply starts over.
	PhaseReadTree Phase = "read_tree"
	// PhaseSeal: after Build/Seal/Marshal produce a fully-formed,
	// self-checksummed manifest, before Store.Insert runs. Still nothing
	// durable — the manifest exists only in process memory at this point.
	PhaseSeal Phase = "seal"
	// PhaseInsert: after Store.Insert's single INSERT statement has
	// committed (SQLite commits a single statement atomically — there is
	// no "partially inserted row" state to crash into), before Create
	// returns to its caller. This is the one phase where durable state
	// exists when the halt fires; Reconcile's job is proving that state is
	// always a fully valid, self-consistent row, never a dangling or
	// half-written one.
	PhaseInsert Phase = "insert"
)

// HaltError is returned by Create when HaltAfter caused an intentional
// mid-sequence stop, simulating a process crash at exactly that point.
// Mirrors internal/progress.HaltError's shape so a crash-injection test
// can assert both "Create did not proceed further" (via this type) and
// "reconciliation finds nothing broken" (via a subsequent Reconcile call).
type HaltError struct {
	Phase Phase
}

func (e *HaltError) Error() string {
	return fmt.Sprintf("statecheckpoint: Create halted after phase %q (fault injection)", e.Phase)
}

// haltIfRequested returns a *HaltError if phase matches s.HaltAfter, so
// Create's linear sequence can check after each step with one line.
func (s *Service) haltIfRequested(phase Phase) error {
	if s.HaltAfter != "" && s.HaltAfter == phase {
		return &HaltError{Phase: phase}
	}
	return nil
}

var _ app.StateCheckpointService = (*Service)(nil)

// Create assembles, seals, and persists a new State Checkpoint snapshot of
// req.TaskID's CURRENT Progress Tree state (not tied to any single node
// completion — see the package doc comment above for how this differs from
// CompleteNode's own inline checkpoint-on-completion path). It never
// mutates progress_nodes/artifacts itself; it only reads them via
// TreeReader and writes a new state_checkpoints row.
func (s *Service) Create(ctx context.Context, req app.CreateStateCheckpointRequest) (domain.StateCheckpoint, error) {
	if req.TaskID == "" {
		return domain.StateCheckpoint{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "statecheckpoint: Create requires a non-empty TaskID",
			Retryable: false,
		}
	}

	nodes, err := s.tree.ListNodes(ctx, req.TaskID)
	if err != nil {
		return domain.StateCheckpoint{}, fmt.Errorf("statecheckpoint: Create: list nodes for task %s: %w", req.TaskID, err)
	}
	artifactRows, err := s.tree.ListArtifacts(ctx, req.TaskID)
	if err != nil {
		return domain.StateCheckpoint{}, fmt.Errorf("statecheckpoint: Create: list artifacts for task %s: %w", req.TaskID, err)
	}
	if err := s.haltIfRequested(PhaseReadTree); err != nil {
		return domain.StateCheckpoint{}, err
	}

	summary := summarizeNodes(nodes)
	// ArtifactSnapshot and ArtifactSummary deliberately share an identical
	// field set (name, type, and order) so this conversion is a plain type
	// cast, not a field-by-field copy — see ArtifactSnapshot's doc comment.
	artifactSummaries := make([]ArtifactSummary, 0, len(artifactRows))
	for _, a := range artifactRows {
		artifactSummaries = append(artifactSummaries, ArtifactSummary(a))
	}

	checkpointID := domain.StateCheckpointID(s.ids.NewID())
	now := s.clock.Now()

	manifest := Build(BuildInput{
		CheckpointID: checkpointID,
		TaskID:       req.TaskID,
		CreatedAt:    now,
		ProgressTree: summary,
		Artifacts:    artifactSummaries,
	})
	sealed, err := Seal(manifest)
	if err != nil {
		return domain.StateCheckpoint{}, fmt.Errorf("statecheckpoint: Create: seal manifest for task %s: %w", req.TaskID, err)
	}
	manifestJSON, err := Marshal(sealed)
	if err != nil {
		return domain.StateCheckpoint{}, fmt.Errorf("statecheckpoint: Create: marshal manifest for task %s: %w", req.TaskID, err)
	}
	if err := s.haltIfRequested(PhaseSeal); err != nil {
		return domain.StateCheckpoint{}, err
	}

	row := Row{
		ID:                  checkpointID,
		TaskID:              req.TaskID,
		ProgressTreeVersion: int64(len(nodes)),
		ActiveNodeID:        summary.ActiveNodeID,
		ManifestJSON:        string(manifestJSON),
		IntegritySHA256:     sealed.IntegritySHA256,
		CreatedAt:           now.UTC().Format(time.RFC3339),
	}
	if err := s.store.Insert(ctx, row); err != nil {
		return domain.StateCheckpoint{}, err
	}
	if err := s.haltIfRequested(PhaseInsert); err != nil {
		return domain.StateCheckpoint{}, err
	}

	return rowToDomain(row, sealed), nil
}

// LoadLatest returns the most recently created State Checkpoint for
// taskID, per ADD §18.9 reconciliation step 1 / this role's own
// checkpoint-a04 deliverable #8 ("snapshot/load-latest/verify APIs").
func (s *Service) LoadLatest(ctx context.Context, taskID domain.TaskID) (domain.StateCheckpoint, error) {
	row, err := s.store.LoadLatest(ctx, taskID)
	if err != nil {
		return domain.StateCheckpoint{}, err
	}
	manifest, err := Unmarshal([]byte(row.ManifestJSON))
	if err != nil {
		return domain.StateCheckpoint{}, fmt.Errorf("statecheckpoint: LoadLatest: unmarshal manifest for checkpoint %s: %w", row.ID, err)
	}
	return rowToDomain(row, manifest), nil
}

// Snapshot returns the full reconstructed State Checkpoint for one
// specific, historical checkpoint ID — "give me the full state as of
// checkpoint X," a point-in-time query distinct from both of this
// package's other read APIs: LoadLatest is task-scoped (always the most
// recent row, never a specific historical one) and Verify returns only a
// pass/fail verdict (StateCheckpointVerification{ID, Valid}, no
// reconstructed state at all). Snapshot is the genuine incremental gap
// checkpoint-a08 closes on top of a05's LoadLatest/Verify: an
// inspection/debugging/audit entry point for "what did the Progress Tree
// look like at THIS specific semantic boundary," e.g. for a human or a
// future CLI comparing two checkpoints, or for ADD §18.9 reconciliation
// step 5 ("compare provider plan/task events") which needs to reason about
// a SPECIFIC prior checkpoint, not merely the latest one.
//
// Like LoadLatest, Snapshot does not itself recompute or verify the
// integrity digest (that is Verify's job, which a caller can call
// separately against the same ID if it wants the fail-closed guarantee
// before trusting the returned state for something consequential) — it is
// a plain read, mirroring internal/repocheckpoint's own read/verify split
// (Verify is a separate call from whatever reads a checkpoint's manifest
// for inspection).
func (s *Service) Snapshot(ctx context.Context, id domain.StateCheckpointID) (domain.StateCheckpoint, error) {
	row, err := s.store.Get(ctx, id)
	if err != nil {
		return domain.StateCheckpoint{}, err
	}
	manifest, err := Unmarshal([]byte(row.ManifestJSON))
	if err != nil {
		return domain.StateCheckpoint{}, fmt.Errorf("statecheckpoint: Snapshot: unmarshal manifest for checkpoint %s: %w", row.ID, err)
	}
	return rowToDomain(row, manifest), nil
}

// Verify loads the checkpoint's stored manifest and recomputes its digest
// from scratch, reporting whether it still matches the stored
// integrity_sha256 — mirroring internal/repocheckpoint's Verify "never
// trust a stored checksum alone" discipline (checkpoint-b04's own doc
// comment), applied here to Part A's manifest instead of Part B's
// artifacts. A mismatch is reported, not silently corrected: an integrity
// failure is a state-integrity bug (CONTRACT_FREEZE.md's fail-closed rule),
// never papered over.
func (s *Service) Verify(ctx context.Context, id domain.StateCheckpointID) (app.StateCheckpointVerification, error) {
	row, err := s.store.Get(ctx, id)
	if err != nil {
		return app.StateCheckpointVerification{}, err
	}
	manifest, err := Unmarshal([]byte(row.ManifestJSON))
	if err != nil {
		// An unparseable manifest is itself a failed verification, not a
		// plumbing error to propagate raw: the caller asked "is this
		// checkpoint valid" and the honest, fail-closed answer is no.
		return app.StateCheckpointVerification{ID: id, Valid: false}, nil //nolint:nilerr // deliberate: an unparseable manifest IS the fail-closed Valid:false answer, not an error to propagate.
	}
	ok, err := Verify(manifest)
	if err != nil {
		return app.StateCheckpointVerification{}, fmt.Errorf("statecheckpoint: Verify: recompute digest for checkpoint %s: %w", id, err)
	}
	return app.StateCheckpointVerification{ID: id, Valid: ok}, nil
}

// summarizeNodes derives a ProgressTreeSummary from a task's full node
// list: the active (in_progress or checkpointing) node if exactly one
// exists, every completed node ID, and every paused node ID — sorted so
// the resulting manifest is deterministic regardless of the reader's own
// row order.
func summarizeNodes(nodes []NodeSnapshot) ProgressTreeSummary {
	var (
		activeID  *domain.ProgressNodeID
		completed []domain.ProgressNodeID
		paused    []domain.ProgressNodeID
	)
	for _, n := range nodes {
		switch n.Status {
		case domain.NodeInProgress, domain.NodeCheckpointing:
			id := n.ID
			activeID = &id
		case domain.NodeCompleted:
			completed = append(completed, n.ID)
		case domain.NodePaused:
			paused = append(paused, n.ID)
		}
	}
	sort.Slice(completed, func(i, j int) bool { return completed[i] < completed[j] })
	sort.Slice(paused, func(i, j int) bool { return paused[i] < paused[j] })

	return ProgressTreeSummary{
		Version:          int64(len(nodes)),
		ActiveNodeID:     activeID,
		CompletedNodeIDs: completed,
		PausedNodeIDs:    paused,
	}
}

// rowToDomain converts this package's own Row + Manifest into the frozen
// domain.StateCheckpoint shape app.StateCheckpointService's methods return.
func rowToDomain(row Row, manifest Manifest) domain.StateCheckpoint {
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
