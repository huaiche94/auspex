// duplicate_outoforder_test.go implements qa-04 (docs/implementation/day1/
// EXECUTION_DAG.md's qa-04 row; agents/qa.md deliverable #4:
// "Duplicate/out-of-order event test").
//
// This node's job is to prove, at INTEGRATION scope, that two pieces of
// prior work actually compose correctly end to end, not merely that each
// passes its own unit tests in isolation:
//
//   - claude-provider-05's idempotent telemetry persistence
//     (internal/telemetry/claude/store.go's EventStore, keyed by
//     Event.IdempotencyKey) — already unit-tested in
//     internal/telemetry/claude/store_test.go.
//   - checkpoint-a07's duplicate/out-of-order PROVIDER event handling for
//     node completion (internal/progress/idempotency.go's
//     checkDuplicateProviderEvent, and complete_node.go's
//     checkParentOrdering) — already unit-tested in
//     internal/progress/complete_node_provider_events_test.go.
//
// # A load-bearing discovery this node made before writing a single test
//
// Proving these two components work "together, exactly the way a real
// duplicate/out-of-order delivery would actually manifest end-to-end"
// first requires a real, production code path connecting a persisted
// claude-provider pkg/protocol/v1.Event to a progress.CompleteNode.Run
// call. A thorough repo-wide search (documented in full in this node's
// docs/implementation/day1/qa.md entry and in the qa-04 final report)
// established that NO such path exists in production code today:
//
//   - internal/orchestrator/hooks.go's HandleStop/HandleUserPromptSubmit/
//     HandleStopFailure/HandleStatusLine normalize a claude-provider
//     payload and persist the resulting v1.Event(s) via EventPersister —
//     and stop there. HandleStop's own doc comment says so explicitly:
//     "Full Progress Tree/Git/artifact reconciliation... is outcome
//     labeling depth beyond this node's scope."
//   - internal/telemetry/claude/normalizer.go never assigns
//     Event.TaskID or Event.ProgressNodeID on any event it produces
//     (every producer only sets SessionID via the shared envelope()
//     helper) — so even a hypothetical future consumer would have nothing
//     to resolve a stored event to a specific progress node with.
//   - progress.Node carries a ProviderNodeID field (node_store.go) that is
//     stored and read back, but no code anywhere looks a node up BY its
//     ProviderNodeID, and progress.CompleteNodeInput/app.CompleteNodeRequest
//     (the frozen contract, internal/app/ports.go) both take only
//     {NodeID, IdempotencyKey, Artifacts[, RepositoryCheckpointID]} — no
//     v1.Event, EventID, or EventType field exists anywhere on that path.
//   - internal/app/wiring/wiring.go wires no bridge between
//     internal/telemetry/claude and internal/progress either; its own doc
//     comment says real ProgressTreeService implementations are a later
//     node's concern.
//
// This is documented as this node's own P1 integration finding (see this
// file's final test, TestDuplicateOutOfOrder_KnownGap_NoProviderEventToCompleteNodeAdapterExists,
// and the qa-04 progress-artifact entry / final report for full routing).
// Per agents/qa.md ("Do not alter feature production code in the initial
// pass... only the contract-integrator authorizes cross-owner fixes"),
// this file does NOT invent or land that adapter as production code. It
// builds it ONLY as local, test-only glue (a package-private
// `deriveCompleteNodeInput` helper below) — exactly the shape a future
// production adapter would need — strictly so the two real components
// (EventStore and CompleteNode) can be proven to compose correctly along
// the fields the frozen v1.Event contract actually offers
// (IdempotencyKey, ProgressNodeID, TaskID), while making unmistakably
// clear in both code and this node's report that this glue is a TEST
// fixture standing in for a production gap, not evidence that the gap is
// closed.
//
// Every test in this file is named so `go test ... -run 'Duplicate|OutOfOrder'`
// (this node's own frozen validation command, EXECUTION_DAG.md qa-04 row)
// selects it.
package integrationtest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/huaiche94/preflight/internal/artifacts"
	"github.com/huaiche94/preflight/internal/domain"
	claudehooks "github.com/huaiche94/preflight/internal/hooks/claude"
	"github.com/huaiche94/preflight/internal/progress"
	"github.com/huaiche94/preflight/internal/statecheckpoint"
	"github.com/huaiche94/preflight/internal/storage/sqlite"
	claudetelemetry "github.com/huaiche94/preflight/internal/telemetry/claude"
	v1 "github.com/huaiche94/preflight/pkg/protocol/v1"
)

// --- shared test doubles / fixtures --------------------------------------
//
// These mirror the exact patterns internal/progress's own test suite and
// qa-05's leakage_scanner_test.go already established for this repo
// (fixedClock/seqIDs test doubles, real on-disk temp SQLite DBs, real
// fixture files under testdata/provider-events/claude) — duplicated here
// rather than imported since both are unexported to their respective test
// packages, the same precedent leakage_scanner_test.go's own header comment
// already documents for this package.

type qa04Clock struct{ t time.Time }

func (c qa04Clock) Now() time.Time { return c.t }

// qa04IDs is a deterministic, sequential domain.IDGenerator test double.
type qa04IDs struct{ n int }

func (g *qa04IDs) NewID() string {
	g.n++
	return "qa04-id-" + itoaQA04(g.n)
}

func itoaQA04(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// qa04Fixture reads a real claude-provider fixture file directly off disk,
// mirroring leakage_scanner_test.go's own fixture() helper (unexported to
// internal/telemetry/claude's own test package, so not importable here).
func qa04Fixture(t *testing.T, dir, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", "provider-events", "claude", dir, name))
	if err != nil {
		t.Fatalf("reading fixture %s/%s: %v", dir, name, err)
	}
	return b
}

// openQA04DB opens a REAL on-disk (temp-file, not :memory:) SQLite database
// and runs every migration, matching what a real Preflight process would
// have at both the event-persistence layer AND the progress-tree layer —
// this is the crux of testing these two roles' work "actually wired
// together," which requires both EventStore's `events` table and
// CompleteNode's progress_nodes/node_completions/state_checkpoints tables
// to live in the SAME database, exactly as production wiring would put
// them.
func openQA04DB(t *testing.T) *sqlite.DB {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "preflight.db")
	db, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("sqlite.AllMigrations: %v", err)
	}
	if err := db.Migrate(context.Background(), migrations); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	return db
}

// qa04SeedTask inserts a minimal repositories -> worktrees -> tasks chain,
// mirroring internal/progress's own seedTask helper (unexported to that
// package's test binary), so progress_nodes' FK into tasks(id) is
// satisfiable.
func qa04SeedTask(t *testing.T, db *sqlite.DB) domain.TaskID {
	t.Helper()
	ctx := context.Background()
	repoID := "repo-" + t.Name()
	worktreeID := "worktree-" + t.Name()
	taskID := "task-" + t.Name()
	now := time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)

	err := db.WithTx(ctx, func(ctx context.Context) error {
		q := sqlite.QuerierFromContext(ctx, db)
		if _, err := q.ExecContext(ctx, `
			INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at)
			VALUES (?, ?, ?, ?, ?)`, repoID, "/tmp/"+repoID, "/tmp/"+repoID+"/.git", now, now); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at)
			VALUES (?, ?, ?, ?, ?, ?)`, worktreeID, repoID, "/tmp/"+repoID, "/tmp/"+repoID+"/.git", now, now); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO tasks (id, worktree_id, objective_hash, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`, taskID, worktreeID, "objective-hash", "in_progress", now, now); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("qa04SeedTask: %v", err)
	}
	return domain.TaskID(taskID)
}

// qa04DocumentNode mirrors internal/progress's own newDocumentNode test
// fixture builder.
func qa04DocumentNode(taskID domain.TaskID, id domain.ProgressNodeID, parentID *domain.ProgressNodeID, ordinal int64, status domain.ProgressNodeStatus, heading string) progress.Node {
	return progress.Node{
		ID:       id,
		TaskID:   taskID,
		ParentID: parentID,
		Ordinal:  ordinal,
		Kind:     domain.NodeDocumentSection,
		Title:    "Node " + string(id),
		Status:   status,
		Acceptance: []progress.AcceptanceCriterion{
			{Kind: "heading_exists", Value: heading},
			{Kind: "fence_balance"},
		},
		Version:   1,
		UpdatedAt: time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
	}
}

func qa04WriteMarkdown(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func qa04ArtifactRef(id, path string) domain.ArtifactRef {
	return domain.ArtifactRef{
		ID:        id,
		Kind:      "file",
		URI:       "file:" + path,
		MediaType: "text/markdown",
	}
}

// qa04CompleteNodeHarness builds a fully real CompleteNode service (no
// fakes for the stores/registries — a real FileStager, a real
// artifacts.Registry, a real statecheckpoint.Store) against the given real
// on-disk DB, matching internal/progress's own newCompleteNodeHarness
// pattern.
func qa04CompleteNodeHarness(t *testing.T, db *sqlite.DB, clock domain.Clock, ids domain.IDGenerator) *progress.CompleteNode {
	t.Helper()
	evidenceDir := t.TempDir()
	stager, err := progress.NewFileStager(evidenceDir)
	if err != nil {
		t.Fatalf("NewFileStager: %v", err)
	}
	return &progress.CompleteNode{
		DB:          db,
		Clock:       clock,
		IDs:         ids,
		Nodes:       progress.NewNodeStore(db, clock),
		Edges:       progress.NewEdgeStore(db),
		Artifacts:   progress.NewArtifactStore(db),
		Validators:  artifacts.NewRegistry(),
		Stager:      stager,
		Checkpoints: statecheckpoint.NewStore(db),
		Publisher:   progress.NoopPublisher{},
	}
}

func qa04MoveToInProgress(t *testing.T, db *sqlite.DB, clock domain.Clock, id domain.ProgressNodeID) {
	t.Helper()
	store := progress.NewNodeStore(db, clock)
	ctx := context.Background()
	if err := store.TransitionStatus(ctx, id, domain.NodePending, domain.NodeReady, 1); err != nil {
		t.Fatalf("transition to ready: %v", err)
	}
	if err := store.TransitionStatus(ctx, id, domain.NodeReady, domain.NodeInProgress, 2); err != nil {
		t.Fatalf("transition to in_progress: %v", err)
	}
}

// deriveCompleteNodeInput is TEST-ONLY glue standing in for the production
// adapter this node's package doc comment establishes does not yet exist.
// It maps a real, normalized claude-provider v1.Event onto the frozen
// progress.CompleteNodeInput shape the ONLY way the current v1.Event
// contract actually supports: using the event's own IdempotencyKey
// (claude-provider's deterministic digest, normalizer.go's digestKey) as
// the completion's IdempotencyKey, and the caller-supplied nodeID/artifacts
// (since, per this file's header finding, a real produced event carries no
// TaskID/ProgressNodeID of its own to resolve a node from). This is
// exactly the "would a real adapter's dedup semantics survive contact with
// a real event" question qa-04 exists to answer — it is NOT a claim that
// this resolution logic exists in production.
func deriveCompleteNodeInput(ev v1.Event, nodeID domain.ProgressNodeID, artifactsRef []domain.ArtifactRef) progress.CompleteNodeInput {
	return progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: ev.IdempotencyKey,
		Artifacts:      artifactsRef,
	}
}

// =========================================================================
// Scenario 1: duplicate provider event, end-to-end through the real
// normalizer -> EventStore -> CompleteNode pipeline.
// =========================================================================

// TestDuplicateProviderEvent_EndToEnd_StoredOnceAndCompletionReplayed drives
// the SAME real claude-provider Stop fixture through claude-provider-05's
// real Normalizer and EventStore TWICE (simulating a hook firing twice, or
// a status-line/hook re-delivery — EventStore's own doc comment,
// store.go:74-76, names this as "expected, ordinary behavior... not an
// exceptional one"), against a real on-disk SQLite database that ALSO
// holds checkpoint-a07's progress_nodes/node_completions tables (the same
// database a real Preflight process would use — this is what makes this an
// integration test rather than a re-run of either role's own unit tests in
// isolation).
//
// It asserts, at the EventStore layer:
//   - both persist calls succeed with no error;
//   - CountByIdempotencyKey reports exactly 1 (claude-provider-05's own
//     idempotency contract, verified here against a REAL normalized event
//     rather than a hand-built one).
//
// And, at the CompleteNode layer (using this file's deriveCompleteNodeInput
// test-only glue documented above, since no production adapter exists to
// drive this automatically): completing the SAME node using the real
// event's IdempotencyKey twice results in the second call being reported
// as Replayed (checkpoint-a07's harmless-duplicate semantics), not an
// error and not a second checkpoint — proving the two roles' idempotency
// mechanisms don't just each work alone, but agree on what "the same event"
// means when a real event's digest is the thing flowing between them.
func TestDuplicateProviderEvent_EndToEnd_StoredOnceAndCompletionReplayed(t *testing.T) {
	db := openQA04DB(t)
	taskID := qa04SeedTask(t, db)
	ctx := context.Background()

	clock := qa04Clock{t: time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)}
	normalizer := claudetelemetry.NewNormalizer(clock, &qa04IDs{})
	store := claudetelemetry.NewEventStore(db)

	// Real fixture, real parse, real normalize — not a hand-built v1.Event.
	parsed, err := claudehooks.ParseStop(qa04Fixture(t, "stop", "normal.json"))
	if err != nil {
		t.Fatalf("ParseStop: %v", err)
	}
	observedAt := clock.Now()
	ev := normalizer.NormalizeStop(parsed, observedAt)
	if ev.IdempotencyKey == "" {
		t.Fatalf("expected NormalizeStop to set a non-empty IdempotencyKey")
	}
	if ev.EventType != v1.EventProviderTurnCompleted {
		t.Fatalf("expected EventProviderTurnCompleted, got %s", ev.EventType)
	}

	// --- EventStore layer: persist the same real event twice -------------
	if err := store.PersistAll(ctx, db, []v1.Event{ev}); err != nil {
		t.Fatalf("first PersistAll: %v", err)
	}
	// A second, independent delivery of the exact same underlying hook
	// firing (re-parsed from the same fixture bytes, re-normalized with the
	// same clock reading) is how a real duplicate delivery actually
	// happens — the normalizer is deterministic given the same input and
	// observedAt, so this reproduces the identical IdempotencyKey a real
	// re-delivered hook invocation would produce.
	parsedAgain, err := claudehooks.ParseStop(qa04Fixture(t, "stop", "normal.json"))
	if err != nil {
		t.Fatalf("ParseStop (redelivery): %v", err)
	}
	evAgain := normalizer.NormalizeStop(parsedAgain, observedAt)
	if evAgain.IdempotencyKey != ev.IdempotencyKey {
		t.Fatalf("expected redelivered event to reproduce the same IdempotencyKey (deterministic digest), got %q vs %q", evAgain.IdempotencyKey, ev.IdempotencyKey)
	}
	if evAgain.EventID == ev.EventID {
		t.Fatalf("test setup bug: redelivered event must carry a fresh EventID (a genuinely different delivery), got the same ID %q", ev.EventID)
	}

	if err := store.PersistAll(ctx, db, []v1.Event{evAgain}); err != nil {
		t.Fatalf("second PersistAll (duplicate delivery): unexpected error: %v", err)
	}

	count, err := store.CountByIdempotencyKey(ctx, ev.IdempotencyKey)
	if err != nil {
		t.Fatalf("CountByIdempotencyKey: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 stored row for the duplicate event's idempotency key, got %d", count)
	}

	// The original row's content must be unchanged by the no-op duplicate
	// insert (not partially overwritten).
	stored, err := store.GetByEventID(ctx, ev.EventID)
	if err != nil {
		t.Fatalf("GetByEventID(%s): %v", ev.EventID, err)
	}
	if stored.IdempotencyKey != ev.IdempotencyKey {
		t.Fatalf("stored row's idempotency key changed unexpectedly")
	}
	// The redelivered event's OWN EventID must NOT have been separately
	// persisted — proof this was a true dedup, not merely "the count query
	// happens to say 1 for an unrelated reason."
	if _, err := store.GetByEventID(ctx, evAgain.EventID); !errors.Is(err, claudetelemetry.ErrEventNotFound) {
		t.Fatalf("expected the duplicate delivery's own EventID to NOT be stored (ON CONFLICT DO NOTHING), got err=%v", err)
	}

	// --- CompleteNode layer: same real event's digest drives a node
	// completion, twice --------------------------------------------------
	nodeID := domain.ProgressNodeID("node-duplicate-provider-event")
	nodeStore := progress.NewNodeStore(db, clock)
	if err := nodeStore.Insert(ctx, qa04DocumentNode(taskID, nodeID, nil, 1, domain.NodePending, "# X")); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	qa04MoveToInProgress(t, db, clock, nodeID)

	cn := qa04CompleteNodeHarness(t, db, clock, &qa04IDs{})
	path := qa04WriteMarkdown(t, "section.md", "# X\n\nprose\n")

	firstInput := deriveCompleteNodeInput(ev, nodeID, []domain.ArtifactRef{qa04ArtifactRef("artifact-1", path)})
	first, err := cn.Run(ctx, firstInput)
	if err != nil {
		t.Fatalf("first CompleteNode.Run (derived from real event): %v", err)
	}
	if first.Replayed {
		t.Fatalf("first completion must not be reported as replayed")
	}

	secondInput := deriveCompleteNodeInput(evAgain, nodeID, []domain.ArtifactRef{qa04ArtifactRef("artifact-1", path)})
	second, err := cn.Run(ctx, secondInput)
	if err != nil {
		t.Fatalf("second CompleteNode.Run (duplicate real event's digest): unexpected rejection: %v", err)
	}
	if !second.Replayed {
		t.Fatalf("second completion (same event digest, same evidence) must be reported as replayed, not treated as fresh work")
	}
	if first.Checkpoint.ID != second.Checkpoint.ID {
		t.Fatalf("duplicate provider event must yield the SAME checkpoint ID: first=%s second=%s", first.Checkpoint.ID, second.Checkpoint.ID)
	}

	rows, err := cn.Checkpoints.ListByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTask: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 state checkpoint after a duplicate provider event drove CompleteNode twice, got %d", len(rows))
	}
}

// TestDuplicateProviderEvent_DifferentChannel_DifferentKey_SameEvidence_Replayed
// exercises checkpoint-a07's OTHER duplicate scenario end to end: a second
// delivery channel that does not share dedup state with the first (so it
// computes its own, unrelated caller-derived IdempotencyKey) but delivers
// IDENTICAL evidence for the same node. Constitution §6.6 says duplicate
// completion with CONFLICTING evidence is rejected — by construction,
// identical evidence under a different key is not a conflict at all, and
// checkDuplicateProviderEvent (idempotency.go) must replay it. This test
// proves that holds when the "evidence" is the artifact produced by a real
// claude-provider StopFailure fixture flow, not a synthetic one.
func TestDuplicateProviderEvent_DifferentChannel_DifferentKey_SameEvidence_Replayed(t *testing.T) {
	db := openQA04DB(t)
	taskID := qa04SeedTask(t, db)
	ctx := context.Background()

	clock := qa04Clock{t: time.Date(2026, 7, 12, 9, 30, 0, 0, time.UTC)}
	normalizer := claudetelemetry.NewNormalizer(clock, &qa04IDs{})
	store := claudetelemetry.NewEventStore(db)

	parsed, err := claudehooks.ParseStopFailure(qa04Fixture(t, "stopfailure", "network.json"))
	if err != nil {
		t.Fatalf("ParseStopFailure: %v", err)
	}
	events := normalizer.NormalizeStopFailure(parsed, clock.Now())
	if len(events) == 0 {
		t.Fatalf("expected NormalizeStopFailure to produce at least one event")
	}
	ev := events[0]

	if err := store.PersistAll(ctx, db, events); err != nil {
		t.Fatalf("PersistAll: %v", err)
	}

	nodeID := domain.ProgressNodeID("node-dup-different-channel")
	nodeStore := progress.NewNodeStore(db, clock)
	if err := nodeStore.Insert(ctx, qa04DocumentNode(taskID, nodeID, nil, 1, domain.NodePending, "# X")); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	qa04MoveToInProgress(t, db, clock, nodeID)

	cn := qa04CompleteNodeHarness(t, db, clock, &qa04IDs{})
	path := qa04WriteMarkdown(t, "section.md", "# X\n\nprose\n")
	artifactRefs := []domain.ArtifactRef{qa04ArtifactRef("artifact-1", path)}

	// Channel A: completes using the real event's own digest as the key.
	first, err := cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: ev.IdempotencyKey,
		Artifacts:      artifactRefs,
	})
	if err != nil {
		t.Fatalf("channel A completion: %v", err)
	}

	// Channel B: redelivers the SAME underlying provider signal (same node,
	// same evidence/artifact set -> same payload digest inside CompleteNode)
	// but through a different integration path that computed its own
	// independent key, exactly as checkpoint-a07's own doc comment
	// (idempotency.go's checkIdempotency) describes: "a 'TaskCompleted'
	// webhook retried over a different channel... did not share dedup state
	// across channels."
	second, err := cn.Run(ctx, progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "channel-b-independently-derived-key",
		Artifacts:      []domain.ArtifactRef{qa04ArtifactRef("artifact-1-redelivered", path)},
	})
	if err != nil {
		t.Fatalf("channel B (duplicate, different key): unexpected rejection: %v", err)
	}
	if !second.Replayed {
		t.Fatalf("channel B delivery with identical evidence under a different key must replay, not error")
	}
	if first.Checkpoint.ID != second.Checkpoint.ID {
		t.Fatalf("expected same checkpoint ID across channels, first=%s second=%s", first.Checkpoint.ID, second.Checkpoint.ID)
	}
}

// =========================================================================
// Scenario 2: out-of-order delivery, end-to-end.
// =========================================================================

// TestOutOfOrderDelivery_EndToEnd_ChildCompletionBeforeParentStarted_Rejected
// constructs the realistic scenario named in this node's brief: a child
// node's completion-triggering signal (derived from a REAL, normalized
// claude-provider event, run through the REAL EventStore first — matching
// how a real delivery would actually reach CompleteNode with the parent's
// own in-progress transition event still unprocessed/delayed/lost) arrives
// before the parent has ever been transitioned to in_progress. It confirms
// the real end-to-end behavior matches checkpoint-a07's documented
// parent-ordering check: a retryable ErrCodeConflict rejection, not silent
// acceptance and not a crash.
func TestOutOfOrderDelivery_EndToEnd_ChildCompletionBeforeParentStarted_Rejected(t *testing.T) {
	db := openQA04DB(t)
	taskID := qa04SeedTask(t, db)
	ctx := context.Background()

	clock := qa04Clock{t: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)}
	normalizer := claudetelemetry.NewNormalizer(clock, &qa04IDs{})
	store := claudetelemetry.NewEventStore(db)

	// The real provider signal that would, in a fully-wired production
	// system, be the trigger for the CHILD node's completion: a real Stop
	// event, parsed and normalized through claude-provider's real pipeline
	// and durably persisted through the real EventStore exactly as
	// production's HandleStop does (internal/orchestrator/hooks.go).
	parsed, err := claudehooks.ParseStop(qa04Fixture(t, "stop", "unknown_fields.json"))
	if err != nil {
		t.Fatalf("ParseStop: %v", err)
	}
	childEvent := normalizer.NormalizeStop(parsed, clock.Now())
	if err := store.PersistAll(ctx, db, []v1.Event{childEvent}); err != nil {
		t.Fatalf("PersistAll (child's triggering event): %v", err)
	}

	// Parent/child progress nodes: the parent is inserted but deliberately
	// never transitioned past `pending` — modeling its own in-progress
	// provider event having been delayed, lost, or simply not yet
	// processed by the time the child's completion signal (childEvent,
	// above) arrives. This is the exact "child before parent" race
	// checkpoint-a07's checkParentOrdering exists to catch, but reached
	// here via a real persisted provider event rather than a hand-built
	// CompleteNodeInput.
	parentID := domain.ProgressNodeID("parent-never-started-e2e")
	childID := domain.ProgressNodeID("child-out-of-order-e2e")

	nodeStore := progress.NewNodeStore(db, clock)
	if err := nodeStore.Insert(ctx, qa04DocumentNode(taskID, parentID, nil, 1, domain.NodePending, "# Parent")); err != nil {
		t.Fatalf("insert parent: %v", err)
	}
	child := qa04DocumentNode(taskID, childID, &parentID, 2, domain.NodePending, "# Child")
	if err := nodeStore.Insert(ctx, child); err != nil {
		t.Fatalf("insert child: %v", err)
	}
	qa04MoveToInProgress(t, db, clock, childID)

	cn := qa04CompleteNodeHarness(t, db, clock, &qa04IDs{})
	path := qa04WriteMarkdown(t, "child-section.md", "# Child\n\nprose\n")

	input := deriveCompleteNodeInput(childEvent, childID, []domain.ArtifactRef{qa04ArtifactRef("artifact-1", path)})
	_, err = cn.Run(ctx, input)
	if err == nil {
		t.Fatalf("expected rejection: child's real provider-event-triggered completion arrived before parent ever started")
	}

	var derr *domain.Error
	if !errors.As(err, &derr) {
		t.Fatalf("expected a *domain.Error, got %T: %v", err, err)
	}
	if derr.Code != domain.ErrCodeConflict {
		t.Fatalf("expected ErrCodeConflict for out-of-order completion, got %q", derr.Code)
	}
	if !derr.Retryable {
		t.Fatalf("expected the out-of-order rejection to be Retryable=true (the parent may still start later, resolving the ordering) — got Retryable=false")
	}

	// The event itself must still be durably stored — an out-of-order
	// COMPLETION rejection must not be confused with, or cause, the
	// underlying provider event's own persistence being rolled back or
	// lost. EventStore's persistence and CompleteNode's completion are
	// independent integrity boundaries; this proves rejecting the latter
	// does not corrupt the former.
	if _, err := store.GetByEventID(ctx, childEvent.EventID); err != nil {
		t.Fatalf("expected the child's provider event to remain durably stored despite the rejected completion: %v", err)
	}

	// And the child node itself must remain exactly where it was
	// (in_progress) — not silently completed, not left in some
	// intermediate/corrupted status.
	refreshed, err := nodeStore.Get(ctx, childID)
	if err != nil {
		t.Fatalf("Get(childID): %v", err)
	}
	if refreshed.Status != domain.NodeInProgress {
		t.Fatalf("expected child node to remain in_progress after the rejected out-of-order completion, got status=%s", refreshed.Status)
	}

	// Once the parent's own in-progress transition IS recorded (the
	// "retryable" half of this contract), a retried delivery of the exact
	// same real event must now succeed — proving the rejection above was
	// genuinely about ordering, not some other defect in the derived input.
	qa04MoveToInProgress(t, db, clock, parentID)
	result, err := cn.Run(ctx, input)
	if err != nil {
		t.Fatalf("expected retried completion to succeed once the parent has started: %v", err)
	}
	if result.Node.Status != domain.NodeCompleted {
		t.Fatalf("expected child node to be completed after retry, got status=%s", result.Node.Status)
	}
}

// TestOutOfOrderDelivery_EndToEnd_EventStoreAcceptsEitherArrivalOrder proves
// the OTHER half of "out-of-order, end-to-end": at the EventStore layer
// specifically (as opposed to CompleteNode's node-completion layer above),
// claude-provider-05's own store.go doc comment claims events "persist
// correctly either way" regardless of arrival order, because there is "no
// mutable 'current state' row this operation updates in place that
// ordering could corrupt." This test independently verifies that claim
// against two REAL normalized events (a parent-like turn-started event and
// a child-like turn-completed event) delivered to the real EventStore in
// the semantically "wrong" order (completed-signal persisted before
// started-signal), confirming both still land as independent, correctly
// stored rows — i.e., the storage layer's permissiveness about ordering
// and CompleteNode's strictness about ordering are two DELIBERATELY
// different, independently-correct behaviors at two different layers, not
// a contradiction between claude-provider-05 and checkpoint-a07.
func TestOutOfOrderDelivery_EndToEnd_EventStoreAcceptsEitherArrivalOrder(t *testing.T) {
	db := openQA04DB(t)
	_ = qa04SeedTask(t, db)
	ctx := context.Background()

	clock := qa04Clock{t: time.Date(2026, 7, 12, 10, 30, 0, 0, time.UTC)}
	normalizer := claudetelemetry.NewNormalizer(clock, &qa04IDs{})
	store := claudetelemetry.NewEventStore(db)

	stopParsed, err := claudehooks.ParseStop(qa04Fixture(t, "stop", "missing_fields.json"))
	if err != nil {
		t.Fatalf("ParseStop: %v", err)
	}
	completedEvent := normalizer.NormalizeStop(stopParsed, clock.Now())

	promptParsed, err := claudehooks.ParseUserPromptSubmit(qa04Fixture(t, "userpromptsubmit", "normal.json"))
	if err != nil {
		t.Fatalf("ParseUserPromptSubmit: %v", err)
	}
	startedEvent := normalizer.NormalizeUserPromptSubmit(promptParsed, clock.Now())

	// Deliberately persist the "completed" signal BEFORE the "started"
	// signal — the out-of-order arrival this scenario is about, at the
	// storage layer.
	if err := store.PersistAll(ctx, db, []v1.Event{completedEvent}); err != nil {
		t.Fatalf("PersistAll(completed, first): %v", err)
	}
	if err := store.PersistAll(ctx, db, []v1.Event{startedEvent}); err != nil {
		t.Fatalf("PersistAll(started, second): %v", err)
	}

	stored, err := store.GetByEventID(ctx, completedEvent.EventID)
	if err != nil {
		t.Fatalf("GetByEventID(completed): %v", err)
	}
	if stored.EventType != string(v1.EventProviderTurnCompleted) {
		t.Fatalf("expected stored completed event to retain its EventType, got %s", stored.EventType)
	}

	storedStarted, err := store.GetByEventID(ctx, startedEvent.EventID)
	if err != nil {
		t.Fatalf("GetByEventID(started): %v", err)
	}
	if storedStarted.EventType != string(v1.EventProviderTurnStarted) {
		t.Fatalf("expected stored started event to retain its EventType, got %s", storedStarted.EventType)
	}
}

// =========================================================================
// Integration-only finding: documented, not fixed (agents/qa.md mandate).
// =========================================================================

// TestDuplicateOutOfOrder_KnownGap_NoProviderEventToCompleteNodeAdapterExists
// is this node's required "document the discrepancy, don't fix it" test
// (task brief item 3; agents/qa.md: "Do not alter feature production
// code... File defects against the owner"). It independently re-verifies,
// at this integration layer, the finding this file's own package doc
// comment describes in prose: a real claude-provider-normalized v1.Event
// carries no TaskID/ProgressNodeID (they are always the Go zero value,
// "" — confirmed here directly against a REAL normalized event, not by
// inspecting source), so no code path in this repository can derive
// which progress node an arbitrary persisted event's completion applies
// to. This is not a bug this test introduces; it is proof the gap is real
// (not just claimed) at the same integration layer this node's other
// tests exercise, so qa's severity report can cite a concrete assertion
// rather than only a prose claim. See this node's progress-artifact entry
// and the final qa-04 report for the P1 classification and routing to
// contract-integrator/claude-provider/checkpoint.
func TestDuplicateOutOfOrder_KnownGap_NoProviderEventToCompleteNodeAdapterExists(t *testing.T) {
	clock := qa04Clock{t: time.Date(2026, 7, 12, 11, 0, 0, 0, time.UTC)}
	normalizer := claudetelemetry.NewNormalizer(clock, &qa04IDs{})

	parsed, err := claudehooks.ParseStop(qa04Fixture(t, "stop", "normal.json"))
	if err != nil {
		t.Fatalf("ParseStop: %v", err)
	}
	ev := normalizer.NormalizeStop(parsed, clock.Now())

	if ev.TaskID != "" {
		t.Fatalf("expected a real normalized Stop event's TaskID to be empty (no production code assigns it); got %q — if this now fails, claude-provider has started populating TaskID and this node's finding should be re-verified and, if resolved, this test updated/removed rather than silently loosened", ev.TaskID)
	}
	if ev.ProgressNodeID != "" {
		t.Fatalf("expected a real normalized Stop event's ProgressNodeID to be empty (no production code assigns it); got %q — same re-verification note as TaskID above", ev.ProgressNodeID)
	}

	// The EventType this event carries (provider.turn.completed) IS, per
	// this node's own task brief, "a signal [that] corresponds to a
	// node-completion-triggering signal" in the product's intended design
	// (ADD's Progress Tree is meant to advance from real provider
	// observations) — yet nothing in pkg/protocol/v1.Event, app.ports.go's
	// CompleteNodeRequest, or progress.CompleteNodeInput carries a field
	// that would let a consumer resolve THIS SPECIFIC event to a
	// ProgressNodeID without an out-of-band lookup this repository does
	// not yet implement anywhere. This assertion is intentionally about
	// the type/contract shape, not a runtime behavior, because the gap
	// IS the absence of a contract field/consumer, not a bug in existing
	// logic.
	t.Logf("confirmed integration gap: EventType=%s carries SessionID=%q but TaskID/ProgressNodeID are unset on every real produced event, and no CompleteNodeInput/CompleteNodeRequest field accepts a v1.Event or resolves one to a node — see this node's progress artifact / final report for P1 routing", ev.EventType, ev.SessionID)
}
