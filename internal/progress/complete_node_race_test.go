// complete_node_race_test.go: the concurrent-completion-race required test
// (agents/checkpoint.md Part A; EXECUTION_DAG.md's own validation command
// for checkpoint-a04 explicitly includes `-race`). Two concurrent
// CompleteNode.Run calls targeting the SAME node must resolve safely: one
// wins and the other fails closed with a clear conflict, and under no
// interleaving may the node end up in a corrupted state (e.g. two
// checkpoints for one completion, or a node stuck in `checkpointing`
// forever, or two different "completed" node rows).
//
// The race is exercised two ways:
//  1. same idempotency key + same payload from N goroutines (expected:
//     exactly one does the real work, the rest replay the same result);
//  2. different idempotency keys (simulating two independent callers who
//     both, incorrectly, believe they should complete the same node) from
//     N goroutines (expected: exactly one succeeds, the rest fail closed
//     with a conflict - never two winners, never a corrupted node).
//
// Safety here rests on NodeStore.TransitionStatus's optimistic-concurrency
// UPDATE ... WHERE status = ? AND version = ? guard (checkpoint-a02): only
// one concurrent transaction's UPDATE can match a given (status, version)
// pair, so only one goroutine's transaction can ever proceed past the
// in_progress -> checkpointing transition for a given attempt.
package progress_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/progress"
)

func TestCompleteNode_ConcurrentCompletion_SameKey_ExactlyOneRealCompletionAllReplaySafely(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 13, 0, 0, 0, time.UTC))
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	path := writeMarkdownFile(t, "section.md", "# X\n\nprose\n")
	nodeID := domain.ProgressNodeID("node-race-same-key")
	insertNode(t, db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# X"))
	moveNodeToInProgress(t, db, clock, nodeID)

	const workers = 20
	req := progress.CompleteNodeInput{
		NodeID:         nodeID,
		IdempotencyKey: "race-same-key",
		Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-race", path)},
	}

	var (
		wg            sync.WaitGroup
		mu            sync.Mutex
		successes     int
		errs          []error
		checkpointIDs = map[domain.StateCheckpointID]bool{}
	)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := cn.Run(ctx, req)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			successes++
			checkpointIDs[result.Checkpoint.ID] = true
		}()
	}
	close(start)
	wg.Wait()

	// Every one of the 20 concurrent identical requests must succeed
	// (same key + same payload is always a safe replay, by definition),
	// but all must agree on exactly one checkpoint.
	if len(errs) != 0 {
		t.Fatalf("expected all same-key/same-payload concurrent calls to succeed (replay-safe), got %d errors: %v", len(errs), errs)
	}
	if successes != workers {
		t.Fatalf("expected %d successes, got %d", workers, successes)
	}
	if len(checkpointIDs) != 1 {
		t.Fatalf("expected exactly 1 distinct checkpoint ID across all concurrent replays, got %d: %v", len(checkpointIDs), checkpointIDs)
	}

	rows, err := cn.Checkpoints.ListByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTask: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 checkpoint row durably stored, got %d", len(rows))
	}

	node, err := cn.Nodes.Get(ctx, nodeID)
	if err != nil {
		t.Fatalf("Get node: %v", err)
	}
	if node.Status != domain.NodeCompleted {
		t.Fatalf("expected node completed, got %s", node.Status)
	}
}

func TestCompleteNode_ConcurrentCompletion_DifferentKeys_ExactlyOneWinnerRestFailClosed(t *testing.T) {
	clock := fixedClockAt(time.Date(2026, 7, 12, 13, 0, 0, 0, time.UTC))
	cn, db, taskID := newCompleteNodeHarness(t, clock)
	ctx := context.Background()

	nodeID := domain.ProgressNodeID("node-race-diff-keys")
	insertNode(t, db, clock, newDocumentNode(taskID, nodeID, 1, domain.NodePending, "# X"))
	moveNodeToInProgress(t, db, clock, nodeID)

	const workers = 20
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		successes int
		failures  int
	)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := writeMarkdownFile(t, "section.md", "# X\n\nprose\n")
			req := progress.CompleteNodeInput{
				NodeID:         nodeID,
				IdempotencyKey: "race-key-" + itoaTest(i),
				Artifacts:      []domain.ArtifactRef{fileArtifactRef("artifact-race-"+itoaTest(i), path)},
			}
			<-start
			_, err := cn.Run(ctx, req)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successes++
				return
			}
			failures++
			var derr *domain.Error
			ok := errors.As(err, &derr)
			if !ok {
				t.Errorf("worker %d: expected a *domain.Error on failure, got %#v", i, err)
				return
			}
			// A loser must fail CLOSED with either a conflict (lost the
			// optimistic-concurrency race, or hit the terminal-node
			// rejection) or a validation error (lost the state-machine
			// transition race) - never anything that suggests partial or
			// corrupted application of its own attempt.
			if derr.Code != domain.ErrCodeConflict && derr.Code != domain.ErrCodeValidation {
				t.Errorf("worker %d: expected fail-closed Conflict/Validation, got code=%s msg=%s", i, derr.Code, derr.Message)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if successes != 1 {
		t.Fatalf("expected EXACTLY 1 winner among %d concurrent distinct-key completions of the same node, got %d successes and %d failures", workers, successes, failures)
	}
	if failures != workers-1 {
		t.Fatalf("expected %d fail-closed losers, got %d", workers-1, failures)
	}

	// Exactly one checkpoint must have been durably created - never zero,
	// never more than one, regardless of goroutine interleaving.
	rows, err := cn.Checkpoints.ListByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("ListByTask: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 checkpoint row after the race resolved, got %d", len(rows))
	}

	node, err := cn.Nodes.Get(ctx, nodeID)
	if err != nil {
		t.Fatalf("Get node: %v", err)
	}
	if node.Status != domain.NodeCompleted {
		t.Fatalf("expected node completed exactly once, got %s", node.Status)
	}
}
