// complete_node.go: the CompleteNode atomic protocol (agents/checkpoint.md
// Part A deliverable #4) — THE core integrity boundary of the whole
// product (EXECUTION_DAG.md: "single most consequential task in the whole
// DAG"). This is where checkpoint-a01's migrations, checkpoint-a02's state
// machine/stores, and checkpoint-a03's artifact validators compose into
// the one atomic operation Constitution §6 rests on:
//
//   - a node may not become `completed` without durable, validator-checked
//     artifact evidence (§6.2);
//   - every node completion creates a State Checkpoint in the same atomic
//     operation (§6.3);
//   - duplicate completion with conflicting evidence is rejected, not
//     silently merged (§6.6);
//   - state writes are atomic, idempotent, and crash-recoverable (§6.5).
//
// Protocol shape (ADD §18.4, §18.7, CONTRACT_FREEZE.md "Transaction
// boundaries"):
//
//  1. Idempotency check (before any mutation): has this exact node already
//     completed? Same key + same payload -> return the prior result
//     unchanged. Same node, different payload -> conflict. Neither -> proceed.
//  2. Stage artifact evidence to a durable, content-addressed location
//     (outside the DB transaction — filesystem staging per ADD §18.7 steps
//     1-4) and record it as `prepared`.
//  3. Verify staged evidence with internal/artifacts validators.
//  4. Enter one SQLite transaction (WithTx) and, inside it:
//     a. re-validate the node's current status/version (dependency policy +
//     state machine transition in_progress -> checkpointing -> completed);
//     b. commit artifact rows as their final validation_status;
//     c. build + seal the State Checkpoint manifest and insert its row;
//     d. write the node_completions idempotency ledger row;
//     e. transition the node to `completed`.
//  5. After the transaction commits (never before — publishing a
//     completion event for a transaction that might still roll back would
//     let an observer act on work that never became durable), publish the
//     normalized events for the checkpoint and node completion.
//
// Every phase above is a named, independently-triggerable step
// (phase constants below) specifically so crash-injection tests
// (complete_node_crash_test.go) can stop the protocol after each one and
// assert reconciliation recovers correctly.
package progress

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/huaiche94/preflight/internal/artifacts"
	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/statecheckpoint"
	"github.com/huaiche94/preflight/internal/storage/sqlite"
)

// Phase names the distinct steps of the CompleteNode protocol, used by
// crash-injection tests to interrupt execution at an exact point and by
// Reconciler to describe what it found. Not persisted as a column; it is a
// pure in-process/test vocabulary layered over the durable state that
// actually distinguishes these points (staged-artifact files on disk vs.
// DB rows), matching ADD §18.7's own step list.
type Phase string

const (
	PhaseStageArtifacts   Phase = "stage_artifacts"
	PhaseVerifyArtifacts  Phase = "verify_artifacts"
	PhaseUpdateNode       Phase = "update_node"
	PhaseCreateCheckpoint Phase = "create_checkpoint"
	PhaseCommit           Phase = "commit"
	PhasePublishEvent     Phase = "publish_event"
)

// EventPublisher is the narrow seam CompleteNode uses to publish
// normalized pkg/protocol/v1.Event values after a successful commit. It is
// declared here (not in internal/app/ports.go) because no frozen
// cross-component event-publishing port exists yet in the contract-freeze
// baseline (grepped: no EventPublisher/EventSink/PublishEvent anywhere in
// internal/app or pkg/protocol) — adding one is contract-integrator's call
// per Constitution §4, not this role's to make unilaterally. This interface
// is this package's own injected dependency, satisfied trivially by a
// no-op in tests and by whatever real sink a later wiring node supplies
// (e.g. runtime's event bus), without internal/progress importing that
// concrete implementation. If a future contract freeze adds a matching
// app.EventPublisher port, this interface's method set is designed to be
// satisfied by it unchanged.
type EventPublisher interface {
	Publish(ctx context.Context, events ...Event)
}

// Event is this package's own normalized-event shape: a minimal,
// dependency-free mirror of pkg/protocol/v1.Event's field set (this
// package does not import pkg/protocol/v1 to avoid coupling its internal
// event production to that package's full type before contract-integrator
// wires the two together explicitly). Callers that need the frozen wire
// type convert at the boundary.
type Event struct {
	EventType      string
	OccurredAt     time.Time
	IdempotencyKey string
	TaskID         string
	ProgressNodeID string
	Payload        map[string]any
}

// NoopPublisher discards every event. Used as the default when no
// publisher is supplied, so CompleteNode remains usable (and testable)
// without requiring every caller to wire a real event sink first.
type NoopPublisher struct{}

func (NoopPublisher) Publish(context.Context, ...Event) {}

// CompleteNodeInput is everything CompleteNode needs from a caller. It is
// this package's own request shape (not app.CompleteNodeRequest directly)
// so the service can be constructed and unit-tested without depending on
// internal/app; the wiring layer that implements app.ProgressTreeService
// adapts app.CompleteNodeRequest to this shape (a one-line field copy,
// since the fields already match CONTRACT_FREEZE.md's frozen contract).
type CompleteNodeInput struct {
	NodeID         domain.ProgressNodeID
	IdempotencyKey string
	Artifacts      []domain.ArtifactRef
	// RepositoryCheckpointID optionally links this completion's State
	// Checkpoint to a Part B Repository Checkpoint already captured by the
	// caller (agents/checkpoint.md "Cross-part boundary": Part A stores a
	// reference through this frozen field, it never reaches into Part B's
	// Git plumbing itself).
	RepositoryCheckpointID *domain.RepositoryCheckpointID
}

// CompleteNodeResult is CompleteNode's successful return shape.
type CompleteNodeResult struct {
	Node       Node
	Checkpoint statecheckpoint.Row
	Manifest   statecheckpoint.Manifest
	// Replayed reports whether this result came from the idempotency
	// ledger (a prior identical request) rather than fresh work.
	Replayed bool
}

// ArtifactStager is the seam CompleteNode uses to durably stage artifact
// content before verification (ADD §18.7 steps 1-4: temp write, fsync,
// atomic rename, `prepared` row) — kept as an injected interface so tests
// can use a real temp-directory stager (production shape) or a
// crash-injecting fake (fault-injection tests) without CompleteNode itself
// branching on which.
//
// Stage must be idempotent for the same (nodeID, artifact) pair: calling
// it twice with identical content must not error and must return the same
// staged reference, so a retried CompleteNode after a crash between
// staging and verification does not fail merely because staging already
// happened.
type ArtifactStager interface {
	Stage(ctx context.Context, nodeID domain.ProgressNodeID, ref domain.ArtifactRef) (StagedArtifact, error)
}

// StagedArtifact is what Stage returns: enough to run a validator and to
// record the final artifacts row.
type StagedArtifact struct {
	Ref  domain.ArtifactRef
	Path string // filesystem path to validate against, if applicable
}

// CompleteNode is the service implementing the CompleteNode atomic
// protocol. It is constructed with every store/seam it needs so callers
// (production wiring, tests, crash-injection harnesses) can substitute
// fakes for any one dependency independently.
type CompleteNode struct {
	DB          *sqlite.DB
	Clock       domain.Clock
	IDs         domain.IDGenerator
	Nodes       *NodeStore
	Edges       *EdgeStore
	Artifacts   *ArtifactStore
	Validators  *artifacts.Registry
	Stager      ArtifactStager
	Checkpoints *statecheckpoint.Store
	Publisher   EventPublisher

	// haltAfter, if non-empty, causes Run to return a *HaltError
	// immediately after completing the named phase, WITHOUT executing any
	// later phase — this is the crash-injection hook. Production callers
	// leave it empty. Exported so external test packages (progress_test)
	// can drive it without an internal test-only build tag.
	HaltAfter Phase
}

// HaltError is returned by Run when HaltAfter caused an intentional
// mid-protocol stop, simulating a process crash at exactly that point.
// Wrapped so a crash-injection test can assert both "did not proceed
// further" (via this type) and "reconciliation cleans it up" (via a
// subsequent Reconcile call).
type HaltError struct {
	Phase Phase
}

func (e *HaltError) Error() string {
	return fmt.Sprintf("progress: CompleteNode halted after phase %q (fault injection)", e.Phase)
}

// haltIfRequested returns a *HaltError if phase matches c.HaltAfter, so
// Run's linear phase sequence can check after each step with one line.
func (c *CompleteNode) haltIfRequested(phase Phase) error {
	if c.HaltAfter == phase {
		return &HaltError{Phase: phase}
	}
	return nil
}

// payloadDigest computes the idempotency conflict-detection digest over
// everything about a completion request that determines its result:
// node ID and the sorted set of (uri, sha256) artifact pairs. Two calls
// with the same IdempotencyKey but a different payloadDigest are a
// conflict (Constitution §6.6), never silently merged.
//
// This is deliberately narrower than ADD §18.12's full
// `completion_key = SHA256(task_id + node_id + artifact hashes +
// acceptance evidence hashes)` — that formula IS the IdempotencyKey a
// caller supplies (CompleteNodeRequest.IdempotencyKey, frozen by
// CONTRACT_FREEZE.md); payloadDigest here is this protocol's OWN
// second-factor check that the key and the actual payload still agree,
// so a caller cannot accidentally (or maliciously) replay a key against
// different evidence and have it silently accepted.
func payloadDigest(nodeID domain.ProgressNodeID, refs []domain.ArtifactRef) string {
	type pair struct{ uri, sha string }
	pairs := make([]pair, 0, len(refs))
	for _, r := range refs {
		pairs = append(pairs, pair{uri: r.URI, sha: r.SHA256})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].uri != pairs[j].uri {
			return pairs[i].uri < pairs[j].uri
		}
		return pairs[i].sha < pairs[j].sha
	})
	h := sha256.New()
	h.Write([]byte(nodeID))
	for _, p := range pairs {
		h.Write([]byte{0})
		h.Write([]byte(p.uri))
		h.Write([]byte{0})
		h.Write([]byte(p.sha))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (c *CompleteNode) now() time.Time { return c.Clock.Now() }

func (c *CompleteNode) nowRFC3339() string { return c.now().UTC().Format(time.RFC3339) }

// Run executes the CompleteNode atomic protocol end to end. See the
// package-level doc comment above for the full phase list and the
// invariants this method is responsible for.
func (c *CompleteNode) Run(ctx context.Context, in CompleteNodeInput) (CompleteNodeResult, error) {
	if in.IdempotencyKey == "" {
		return CompleteNodeResult{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   "progress: CompleteNode requires a non-empty IdempotencyKey",
			Retryable: false,
		}
	}

	node, err := c.Nodes.Get(ctx, in.NodeID)
	if err != nil {
		return CompleteNodeResult{}, err
	}

	digest := payloadDigest(in.NodeID, in.Artifacts)

	// --- Idempotency check (before any mutation) ---------------------------
	if replay, ok, err := c.checkIdempotency(ctx, node.TaskID, in.NodeID, in.IdempotencyKey, digest); err != nil {
		return CompleteNodeResult{}, err
	} else if ok {
		return replay, nil
	}

	// A node that is already completed but has NO idempotency ledger row
	// for the key/digest supplied is a duplicate-completion attempt outside
	// the idempotency contract entirely (e.g. two independently-generated
	// keys targeting the same already-completed node) — must reject, never
	// re-complete a terminal node (Constitution §6 "duplicate completion
	// with conflicting evidence is rejected").
	if node.Status == domain.NodeCompleted {
		return CompleteNodeResult{}, &domain.Error{
			Code:      domain.ErrCodeConflict,
			Message:   fmt.Sprintf("progress: node %s is already completed; a new completion attempt without a matching idempotency ledger entry is rejected", in.NodeID),
			Retryable: false,
			Details:   map[string]string{"node_id": string(in.NodeID)},
		}
	}

	// Dependency policy: every depends_on edge must point at a completed
	// or skipped node before this node may complete (Constitution §6
	// "completed child with violated dependency policy" must reject).
	if err := c.checkDependencies(ctx, node); err != nil {
		return CompleteNodeResult{}, err
	}

	// Parent ordering (checkpoint-a07): reject an out-of-order provider
	// signal for a child node whose parent's own in-progress transition was
	// never recorded — see checkParentOrdering's doc comment for the full
	// rationale.
	if err := c.checkParentOrdering(ctx, node); err != nil {
		return CompleteNodeResult{}, err
	}

	// --- Phase: stage + verify artifact evidence (outside the DB tx) ------
	if len(in.Artifacts) == 0 {
		return CompleteNodeResult{}, &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   fmt.Sprintf("progress: node %s completion rejected: no artifact evidence supplied (\"agent says complete\" is not sufficient)", in.NodeID),
			Retryable: false,
		}
	}

	staged := make([]StagedArtifact, 0, len(in.Artifacts))
	for _, ref := range in.Artifacts {
		sa, err := c.Stager.Stage(ctx, in.NodeID, ref)
		if err != nil {
			return CompleteNodeResult{}, err
		}
		staged = append(staged, sa)
	}
	if err := c.haltIfRequested(PhaseStageArtifacts); err != nil {
		return CompleteNodeResult{}, err
	}

	verified, err := c.verifyArtifacts(ctx, node, staged)
	if err != nil {
		return CompleteNodeResult{}, err
	}
	if err := c.haltIfRequested(PhaseVerifyArtifacts); err != nil {
		return CompleteNodeResult{}, err
	}

	// --- Single DB transaction: node update + checkpoint + ledger --------
	checkpointID := domain.StateCheckpointID(c.IDs.NewID())
	now := c.now()

	var (
		manifest   statecheckpoint.Manifest
		checkpoint statecheckpoint.Row
		completed  Node
	)

	txErr := c.DB.WithTx(ctx, func(ctx context.Context) error {
		// Re-fetch inside the transaction is unnecessary: NodeStore's
		// TransitionStatus itself enforces an optimistic-concurrency
		// WHERE-clause guard against `node.Version`/`node.Status`
		// (captured before staging began) having changed underneath us,
		// which is exactly the concurrent-completion race this protocol
		// must resolve safely (DAG's own `-race` validation requirement).
		if err := c.Nodes.TransitionStatus(ctx, in.NodeID, node.Status, domain.NodeCheckpointing, node.Version); err != nil {
			return err
		}

		for i, sa := range verified {
			artifactID := c.IDs.NewID()
			row := ArtifactRow{
				ID:               artifactID,
				TaskID:           node.TaskID,
				NodeID:           &in.NodeID,
				Kind:             "file",
				URI:              sa.Ref.URI,
				SHA256:           sa.Ref.SHA256,
				Bytes:            sa.Ref.Bytes,
				ValidationStatus: ValidationPassed,
			}
			if err := c.Artifacts.Insert(ctx, row); err != nil {
				return err
			}
			verified[i] = sa
		}

		if err := c.Nodes.TransitionStatus(ctx, in.NodeID, domain.NodeCheckpointing, domain.NodeCompleted, node.Version+1); err != nil {
			return err
		}
		completedAt := c.nowRFC3339()
		if err := c.Nodes.SetTimestamps(ctx, in.NodeID, nil, &completedAt); err != nil {
			return err
		}
		if err := c.haltIfRequested(PhaseUpdateNode); err != nil {
			return err
		}

		refreshed, err := c.Nodes.Get(ctx, in.NodeID)
		if err != nil {
			return err
		}
		completed = refreshed

		manifest = c.buildManifest(checkpointID, node.TaskID, in.NodeID, now, verified, in.RepositoryCheckpointID)
		sealed, err := statecheckpoint.Seal(manifest)
		if err != nil {
			return fmt.Errorf("progress: seal state checkpoint manifest: %w", err)
		}
		manifest = sealed

		manifestJSON, err := statecheckpoint.Marshal(manifest)
		if err != nil {
			return fmt.Errorf("progress: marshal state checkpoint manifest: %w", err)
		}

		checkpointRow := statecheckpoint.Row{
			ID:                     checkpointID,
			TaskID:                 node.TaskID,
			ProgressTreeVersion:    node.Version + 2, // +1 for checkpointing, +1 for completed
			ActiveNodeID:           nil,
			CompletionNodeID:       &in.NodeID,
			RepositoryCheckpointID: in.RepositoryCheckpointID,
			ManifestJSON:           string(manifestJSON),
			IntegritySHA256:        manifest.IntegritySHA256,
			CreatedAt:              c.nowRFC3339(),
		}
		// checkpoint manifest referencing uncommitted rows must never
		// happen (Constitution §6 "must reject" list): the checkpoint row
		// below is written in this SAME transaction as the node/artifact
		// rows it references, so it is impossible for this row to commit
		// while those do not — either the whole transaction commits
		// (node, artifacts, and checkpoint all durable together) or none
		// of it does (rollback on any error path above or below).
		if err := c.Checkpoints.Insert(ctx, checkpointRow); err != nil {
			return err
		}
		checkpoint = checkpointRow
		if err := c.haltIfRequested(PhaseCreateCheckpoint); err != nil {
			return err
		}

		if err := c.recordIdempotency(ctx, node.TaskID, in.NodeID, in.IdempotencyKey, digest, checkpointID, completed); err != nil {
			return err
		}

		return nil
	})
	if txErr != nil {
		var halt *HaltError
		if isHaltError(txErr, &halt) {
			// The transaction's callback returned a *HaltError, which
			// causes WithTx to roll back (any non-nil error rolls back,
			// per sqlite.DB.WithTx's contract) — exactly the desired
			// "simulated crash mid-transaction" behavior: nothing from
			// this attempt is durable, so reconciliation on the next
			// startup sees the node still in its pre-completion status.
			return CompleteNodeResult{}, txErr
		}

		// Concurrent-completion race (DAG's own `-race` requirement): this
		// goroutine's TransitionStatus lost the optimistic-concurrency
		// race to a concurrent winner (checkIdempotency, above, ran before
		// either transaction opened, so it could not have seen the
		// winner's result yet — a genuine TOCTOU window between the
		// pre-transaction idempotency check and the transaction itself).
		// If the winner completed with the SAME key+payload this loser
		// was asked to apply, this is a safe, expected replay situation,
		// not a real conflict — re-check the now-updated ledger and
		// return the winner's result instead of surfacing a raw
		// optimistic-concurrency error to a caller who did nothing wrong.
		var derr *domain.Error
		if errors.As(txErr, &derr) && derr.Code == domain.ErrCodeConflict {
			if replay, found, rerr := c.checkIdempotency(ctx, node.TaskID, in.NodeID, in.IdempotencyKey, digest); rerr == nil && found {
				return replay, nil
			}
		}
		return CompleteNodeResult{}, txErr
	}
	if err := c.haltIfRequested(PhaseCommit); err != nil {
		// The transaction itself already committed successfully above (a
		// halt requested for PhaseCommit fires AFTER WithTx returns nil),
		// so this simulates a crash after the durable write succeeded but
		// before the process got to publish events — reconciliation must
		// find a fully-consistent, completed node here, not roll anything
		// back.
		return CompleteNodeResult{Node: completed, Checkpoint: checkpoint, Manifest: manifest}, err
	}

	// --- Publish normalized events AFTER commit ---------------------------
	c.publishCompletionEvents(ctx, node.TaskID, in.NodeID, checkpointID)
	_ = c.haltIfRequested(PhasePublishEvent) // nothing meaningful left to do after this; kept for symmetry/documentation

	return CompleteNodeResult{Node: completed, Checkpoint: checkpoint, Manifest: manifest}, nil
}

// isHaltError is errors.As with a concrete out-param, spelled as a small
// helper so Run's error-handling branch above stays readable.
func isHaltError(err error, out **HaltError) bool {
	return errors.As(err, out)
}

// checkDependencies enforces ADD/Constitution §6's "completed child with
// violated dependency policy" rejection: every depends_on edge FROM this
// node must point at a node that is already completed or skipped.
func (c *CompleteNode) checkDependencies(ctx context.Context, node Node) error {
	deps, err := c.Edges.DependenciesOf(ctx, node.TaskID, node.ID)
	if err != nil {
		return err
	}
	for _, depID := range deps {
		dep, err := c.Nodes.Get(ctx, depID)
		if err != nil {
			return err
		}
		if dep.Status != domain.NodeCompleted && dep.Status != domain.NodeSkipped {
			return &domain.Error{
				Code:      domain.ErrCodeValidation,
				Message:   fmt.Sprintf("progress: node %s cannot complete: dependency %s is not completed or skipped (status=%s)", node.ID, depID, dep.Status),
				Retryable: false,
				Details: map[string]string{
					"node_id":    string(node.ID),
					"dependency": string(depID),
					"dep_status": string(dep.Status),
				},
			}
		}
	}
	return nil
}

// startedStatuses is the set of domain.ProgressNodeStatus values that mean
// "this node's own in_progress transition (or something further along) has
// already been durably recorded" — i.e. the node has genuinely started
// from the Progress Tree's point of view, as opposed to still sitting in
// pending/ready (never started) or blocked (found unreachable before ever
// starting). checkParentOrdering uses this to decide whether a parent has
// "started" for the purpose of accepting a child's completion.
var startedStatuses = map[domain.ProgressNodeStatus]bool{
	domain.NodeInProgress:    true,
	domain.NodeCheckpointing: true,
	domain.NodeCompleted:     true,
	domain.NodeFailed:        true,
	domain.NodePaused:        true,
}

// checkParentOrdering enforces checkpoint-a07's out-of-order provider event
// scope: a completion signal for a child node must not be accepted before
// its parent's own in-progress transition has been durably recorded. A
// provider integration that emits lifecycle events over multiple channels
// (or that redelivers events after a transient failure) can genuinely
// deliver a child's "completed" signal ahead of its parent's "started"
// signal even though the real-world execution order was correct — Preflight
// must not let the Progress Tree's canonical state (Constitution §6.1)
// silently become internally incoherent (a completed node whose parent
// never started) just because two normalized events raced on delivery.
//
// A node with no parent (a root node) has nothing to check here. A node
// whose parent is itself still pending/ready (never started) or blocked
// (found unreachable) fails this check; every other parent status
// (in_progress, checkpointing, completed, failed, paused) counts as
// "started" and is accepted, since ADD's state machine already allows a
// parent to reach any of those states before every one of its children
// finishes (e.g. a parent legitimately completing slightly before a
// straggling child's own evidence is staged is a real, allowed race this
// check must not block — see the DAG's own child-before-parent framing,
// which is specifically about the parent never having started at all, not
// about strict start-then-finish ordering between parent and child).
func (c *CompleteNode) checkParentOrdering(ctx context.Context, node Node) error {
	if node.ParentID == nil {
		return nil
	}
	parent, err := c.Nodes.Get(ctx, *node.ParentID)
	if err != nil {
		return err
	}
	if !startedStatuses[parent.Status] {
		return &domain.Error{
			Code:      domain.ErrCodeConflict,
			Message:   fmt.Sprintf("progress: node %s cannot complete out of order: parent %s has not started yet (status=%s)", node.ID, parent.ID, parent.Status),
			Retryable: true,
			Details: map[string]string{
				"node_id":       string(node.ID),
				"parent_id":     string(parent.ID),
				"parent_status": string(parent.Status),
			},
		}
	}
	return nil
}

// verifyArtifacts runs internal/artifacts validators against every staged
// artifact according to the node's acceptance criteria. A node with no
// acceptance criteria at all still requires every staged artifact to pass
// FileExists as a floor (an empty acceptance list is not a license to skip
// verification entirely).
func (c *CompleteNode) verifyArtifacts(ctx context.Context, node Node, staged []StagedArtifact) ([]StagedArtifact, error) {
	if len(node.Acceptance) == 0 {
		for _, sa := range staged {
			res, err := c.Validators.Validate(ctx, "file_exists", artifacts.Candidate{Path: sa.Path})
			if err != nil {
				return nil, err
			}
			if !res.Passed {
				return nil, rejectionError(node.ID, "file_exists", res.Reasons)
			}
		}
		return staged, nil
	}

	for _, crit := range node.Acceptance {
		for _, sa := range staged {
			candidate := artifacts.Candidate{
				Path:           sa.Path,
				ExpectedSHA256: sa.Ref.SHA256,
				Heading:        crit.Value,
			}
			res, err := c.Validators.Validate(ctx, crit.Kind, candidate)
			if err != nil {
				return nil, err
			}
			if !res.Passed {
				return nil, rejectionError(node.ID, crit.Kind, res.Reasons)
			}
		}
	}
	return staged, nil
}

func rejectionError(nodeID domain.ProgressNodeID, validatorKind string, reasons []string) error {
	return &domain.Error{
		Code:      domain.ErrCodeValidation,
		Message:   fmt.Sprintf("progress: node %s completion rejected by validator %q: %v", nodeID, validatorKind, reasons),
		Retryable: false,
		Details: map[string]string{
			"node_id":   string(nodeID),
			"validator": validatorKind,
		},
	}
}

// buildManifest assembles (unsealed) the State Checkpoint manifest for a
// successful completion.
func (c *CompleteNode) buildManifest(checkpointID domain.StateCheckpointID, taskID domain.TaskID, completedNodeID domain.ProgressNodeID, now time.Time, staged []StagedArtifact, repoCheckpointID *domain.RepositoryCheckpointID) statecheckpoint.Manifest {
	artifactSummaries := make([]statecheckpoint.ArtifactSummary, 0, len(staged))
	for _, sa := range staged {
		artifactSummaries = append(artifactSummaries, statecheckpoint.ArtifactSummary{
			ID:               sa.Ref.ID,
			URI:              sa.Ref.URI,
			MediaType:        sa.Ref.MediaType,
			Bytes:            sa.Ref.Bytes,
			SHA256:           sa.Ref.SHA256,
			ValidationStatus: string(ValidationPassed),
		})
	}

	repo := statecheckpoint.RepositoryInfo{}
	in := statecheckpoint.BuildInput{
		CheckpointID: checkpointID,
		TaskID:       taskID,
		CreatedAt:    now,
		ProgressTree: statecheckpoint.ProgressTreeSummary{
			CompletedNodeIDs: []domain.ProgressNodeID{completedNodeID},
		},
		Artifacts:  artifactSummaries,
		Repository: repo,
		NextAction: statecheckpoint.NextActionInfo{},
	}
	return statecheckpoint.Build(in)
}

// publishCompletionEvents publishes the normalized events a node
// completion produces, strictly after the transaction that made them true
// has committed. Best-effort: publishing is not itself part of the
// integrity boundary (an event bus outage must not un-complete a node that
// is already durably completed — the DB transaction already succeeded),
// so this method has no error return; a real EventPublisher is expected to
// handle its own retries/logging.
func (c *CompleteNode) publishCompletionEvents(ctx context.Context, taskID domain.TaskID, nodeID domain.ProgressNodeID, checkpointID domain.StateCheckpointID) {
	now := c.now()
	c.Publisher.Publish(ctx,
		Event{
			EventType:      "state.checkpoint.created",
			OccurredAt:     now,
			IdempotencyKey: string(checkpointID),
			TaskID:         string(taskID),
			ProgressNodeID: string(nodeID),
			Payload:        map[string]any{"checkpoint_id": string(checkpointID)},
		},
		Event{
			EventType:      "progress.node.completed",
			OccurredAt:     now,
			IdempotencyKey: string(nodeID) + ":completed",
			TaskID:         string(taskID),
			ProgressNodeID: string(nodeID),
			Payload:        map[string]any{"checkpoint_id": string(checkpointID)},
		},
	)
}
