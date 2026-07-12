package statecheckpoint_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/huaiche94/preflight/internal/app"
	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/statecheckpoint"
	"github.com/huaiche94/preflight/internal/storage/sqlite"
)

// fixedClock is a deterministic domain.Clock test double.
type fixedClock struct{ t time.Time }

func (f fixedClock) Now() time.Time { return f.t }

// seqIDs is a deterministic domain.IDGenerator test double.
type seqIDs struct{ n int }

func (s *seqIDs) NewID() string {
	s.n++
	return fmt.Sprintf("checkpoint-%d", s.n)
}

// fakeTreeReader is an in-memory statecheckpoint.TreeReader test double, so
// Service tests do not need internal/progress's real stores (which would
// require this package to import internal/progress — the wrong dependency
// direction, since internal/progress already imports
// internal/statecheckpoint per complete_node.go).
type fakeTreeReader struct {
	nodes     map[domain.TaskID][]statecheckpoint.NodeSnapshot
	artifacts map[domain.TaskID][]statecheckpoint.ArtifactSnapshot
}

func (f *fakeTreeReader) ListNodes(_ context.Context, taskID domain.TaskID) ([]statecheckpoint.NodeSnapshot, error) {
	return f.nodes[taskID], nil
}

func (f *fakeTreeReader) ListArtifacts(_ context.Context, taskID domain.TaskID) ([]statecheckpoint.ArtifactSnapshot, error) {
	return f.artifacts[taskID], nil
}

// newTestService builds a Service against a fresh temp SQLite DB with one
// seeded task (so state_checkpoints' FK into tasks(id) is satisfiable) and
// returns the service along with that task's real ID — tests that need a
// TaskID use this returned ID rather than an arbitrary literal string,
// since state_checkpoints.task_id has a real foreign key constraint.
func newTestService(t *testing.T, clock domain.Clock, buildTree func(taskID domain.TaskID) statecheckpoint.TreeReader) (*statecheckpoint.Service, domain.TaskID) {
	t.Helper()
	db := openTestDB(t)
	taskID := seedTask(t, db)
	store := statecheckpoint.NewStore(db)
	tree := buildTree(taskID)
	return statecheckpoint.NewService(store, tree, clock, &seqIDs{}), taskID
}

// var _ app.StateCheckpointService = (*statecheckpoint.Service)(nil) is
// asserted in service.go itself; this test additionally exercises Service
// through the app.StateCheckpointService interface type directly, so a
// signature drift would fail to compile here too, not just in service.go.
func serviceAsPort(s *statecheckpoint.Service) app.StateCheckpointService { return s }

func TestService_Create_SnapshotsCurrentTreeState(t *testing.T) {
	activeNode := domain.ProgressNodeID("node-active")
	completedNode := domain.ProgressNodeID("node-done")
	pausedNode := domain.ProgressNodeID("node-paused")

	clock := fixedClock{time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)}
	svc, taskID := newTestService(t, clock, func(taskID domain.TaskID) statecheckpoint.TreeReader {
		return &fakeTreeReader{
			nodes: map[domain.TaskID][]statecheckpoint.NodeSnapshot{
				taskID: {
					{ID: completedNode, Status: domain.NodeCompleted},
					{ID: activeNode, Status: domain.NodeInProgress},
					{ID: pausedNode, Status: domain.NodePaused},
				},
			},
			artifacts: map[domain.TaskID][]statecheckpoint.ArtifactSnapshot{
				taskID: {
					{ID: "a-1", URI: "file:///a.md", Bytes: 10, SHA256: "deadbeef", ValidationStatus: "passed"},
				},
			},
		}
	})
	ctx := context.Background()

	port := serviceAsPort(svc)
	cp, err := port.Create(ctx, app.CreateStateCheckpointRequest{TaskID: taskID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if cp.TaskID != taskID {
		t.Fatalf("expected TaskID %s, got %s", taskID, cp.TaskID)
	}
	if cp.ActiveNodeID == nil || *cp.ActiveNodeID != activeNode {
		t.Fatalf("expected ActiveNodeID %s, got %v", activeNode, cp.ActiveNodeID)
	}
	if len(cp.CompletedNodeIDs) != 1 || cp.CompletedNodeIDs[0] != completedNode {
		t.Fatalf("expected CompletedNodeIDs=[%s], got %v", completedNode, cp.CompletedNodeIDs)
	}
	if cp.IntegritySHA256 == "" {
		t.Fatalf("expected a non-empty IntegritySHA256")
	}
	if cp.ID == "" {
		t.Fatalf("expected a non-empty checkpoint ID")
	}

	// Loading it back via LoadLatest must return the same checkpoint.
	latest, err := port.LoadLatest(ctx, taskID)
	if err != nil {
		t.Fatalf("LoadLatest: %v", err)
	}
	if latest.ID != cp.ID || latest.IntegritySHA256 != cp.IntegritySHA256 {
		t.Fatalf("LoadLatest did not return the just-created checkpoint: %+v vs %+v", latest, cp)
	}
}

func TestService_Create_RequiresTaskID(t *testing.T) {
	svc, _ := newTestService(t, fixedClock{time.Now()}, func(domain.TaskID) statecheckpoint.TreeReader {
		return &fakeTreeReader{}
	})
	_, err := svc.Create(context.Background(), app.CreateStateCheckpointRequest{})
	if err == nil {
		t.Fatal("expected an error for an empty TaskID")
	}
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("expected ErrCodeValidation, got %#v", err)
	}
}

func TestService_LoadLatest_NoCheckpoints_NotFound(t *testing.T) {
	svc, taskID := newTestService(t, fixedClock{time.Now()}, func(domain.TaskID) statecheckpoint.TreeReader {
		return &fakeTreeReader{}
	})
	_, err := svc.LoadLatest(context.Background(), taskID)
	var derr *domain.Error
	if !errors.As(err, &derr) {
		t.Fatalf("expected a domain.Error, got %v", err)
	}
}

func TestService_LoadLatest_ReturnsMostRecent(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	tree := &fakeTreeReader{
		nodes: map[domain.TaskID][]statecheckpoint.NodeSnapshot{
			taskID: {{ID: "n-1", Status: domain.NodeInProgress}},
		},
	}
	store := statecheckpoint.NewStore(db)
	ids := &seqIDs{}
	base := time.Date(2026, 7, 12, 8, 0, 0, 0, time.UTC)

	// First checkpoint at t=base.
	svc1 := statecheckpoint.NewService(store, tree, fixedClock{base}, ids)
	first, err := svc1.Create(context.Background(), app.CreateStateCheckpointRequest{TaskID: taskID})
	if err != nil {
		t.Fatalf("Create 1: %v", err)
	}

	// Second checkpoint at t=base+1h, same store (simulates a later
	// snapshot request against the same task).
	svc2 := statecheckpoint.NewService(store, tree, fixedClock{base.Add(time.Hour)}, ids)
	second, err := svc2.Create(context.Background(), app.CreateStateCheckpointRequest{TaskID: taskID})
	if err != nil {
		t.Fatalf("Create 2: %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("expected two distinct checkpoint IDs, got the same: %s", first.ID)
	}

	latest, err := svc2.LoadLatest(context.Background(), taskID)
	if err != nil {
		t.Fatalf("LoadLatest: %v", err)
	}
	if latest.ID != second.ID {
		t.Fatalf("expected LoadLatest to return the second (more recent) checkpoint %s, got %s", second.ID, latest.ID)
	}
}

func TestService_Verify_ValidCheckpoint(t *testing.T) {
	svc, taskID := newTestService(t, fixedClock{time.Now()}, func(taskID domain.TaskID) statecheckpoint.TreeReader {
		return &fakeTreeReader{
			nodes: map[domain.TaskID][]statecheckpoint.NodeSnapshot{
				taskID: {{ID: "n-1", Status: domain.NodeCompleted}},
			},
		}
	})
	ctx := context.Background()

	cp, err := svc.Create(ctx, app.CreateStateCheckpointRequest{TaskID: taskID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	result, err := svc.Verify(ctx, cp.ID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !result.Valid {
		t.Fatalf("expected a freshly created checkpoint to verify as valid")
	}
	if result.ID != cp.ID {
		t.Fatalf("expected verification ID %s, got %s", cp.ID, result.ID)
	}
}

func TestService_Verify_TamperedManifest_Invalid(t *testing.T) {
	db := openTestDB(t)
	taskID := seedTask(t, db)
	tree := &fakeTreeReader{
		nodes: map[domain.TaskID][]statecheckpoint.NodeSnapshot{
			taskID: {{ID: "n-1", Status: domain.NodeCompleted}},
		},
	}
	store := statecheckpoint.NewStore(db)
	svc := statecheckpoint.NewService(store, tree, fixedClock{time.Now()}, &seqIDs{})
	ctx := context.Background()

	cp, err := svc.Create(ctx, app.CreateStateCheckpointRequest{TaskID: taskID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Directly corrupt the stored manifest_json (simulating on-disk
	// tampering or bit rot) without going through Service at all, then
	// confirm Verify recomputes the digest rather than trusting the
	// stored integrity_sha256 column.
	row, err := store.Get(ctx, cp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	tamperedManifestJSON := `{"schema_version":"preflight.state-checkpoint.v1","task_id":"tampered","integrity_sha256":"` + row.IntegritySHA256 + `"}`
	q := sqlite.QuerierFromContext(ctx, db)
	if _, err := q.ExecContext(ctx, `UPDATE state_checkpoints SET manifest_json = ? WHERE id = ?`, tamperedManifestJSON, string(cp.ID)); err != nil {
		t.Fatalf("corrupt manifest_json: %v", err)
	}

	result, err := svc.Verify(ctx, cp.ID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.Valid {
		t.Fatalf("expected a tampered manifest to fail verification, not report valid")
	}
}

func TestService_Verify_NotFound(t *testing.T) {
	svc, _ := newTestService(t, fixedClock{time.Now()}, func(domain.TaskID) statecheckpoint.TreeReader {
		return &fakeTreeReader{}
	})
	_, err := svc.Verify(context.Background(), domain.StateCheckpointID("does-not-exist"))
	var derr *domain.Error
	if !errors.As(err, &derr) {
		t.Fatalf("expected a domain.Error for an unknown checkpoint ID, got %v", err)
	}
}
