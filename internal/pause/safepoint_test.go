package pause

import (
	"context"
	"errors"
	"testing"

	"github.com/huaiche94/preflight/internal/domain"
)

// --- fakes: record call order without any real checkpoint/provider store ---
//
// Per the DAG's own note ("Can start against fakes for checkpoint; no
// concrete store required to begin") and consistent with what
// runtime-b05 already did last wave for the identical reason
// (internal/orchestrator/checkpoint_test.go), these fakes live local to
// this test file rather than in internal/testutil/fakes (a Part B path
// this wave's task brief does not authorize touching) or against the
// frozen app.StateCheckpointService/app.TurnInterrupter directly (this
// node's CheckpointPersister/Interrupter seams are intentionally narrower
// — see safepoint.go's doc comments).

type recordingPersister struct {
	calls   *[]string
	failErr error
}

func (p *recordingPersister) Persist(_ context.Context, pauseID domain.PauseID) error {
	*p.calls = append(*p.calls, "persist:"+string(pauseID))
	if p.failErr != nil {
		return p.failErr
	}
	return nil
}

type recordingInterrupter struct {
	calls   *[]string
	failErr error
}

func (i *recordingInterrupter) Interrupt(_ context.Context, pauseID domain.PauseID) error {
	*i.calls = append(*i.calls, "interrupt:"+string(pauseID))
	if i.failErr != nil {
		return i.failErr
	}
	return nil
}

const testPauseID domain.PauseID = "pause-1"

// TestSafePoint_PersistsCheckpointsBeforeInterrupt is the required test
// (verbatim): "safe point persists checkpoints before interrupt." Proves
// the CALL ORDER — persist must be recorded before interrupt — using
// fakes, exactly as instructed.
func TestSafePoint_PersistsCheckpointsBeforeInterrupt(t *testing.T) {
	var calls []string
	persister := &recordingPersister{calls: &calls}
	interrupter := &recordingInterrupter{calls: &calls}
	coordinator := NewTurnBoundaryCoordinator()

	err := PersistThenInterrupt(context.Background(), coordinator, persister, interrupter, SafePointObservation{
		PauseID:  testPauseID,
		Boundary: BoundaryPostToolUse,
	})
	if err != nil {
		t.Fatalf("PersistThenInterrupt: %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("calls = %v, want exactly 2 (persist then interrupt)", calls)
	}
	if calls[0] != "persist:"+string(testPauseID) {
		t.Fatalf("calls[0] = %q, want the persist call first", calls[0])
	}
	if calls[1] != "interrupt:"+string(testPauseID) {
		t.Fatalf("calls[1] = %q, want the interrupt call second", calls[1])
	}
}

// TestSafePoint_PersistFailureNeverReachesInterrupt proves the other half
// of the ordering guarantee: if Persist fails, Interrupt must never be
// called at all — not "called and its result ignored," genuinely never
// invoked. This is the same fail-closed shape as ADD §20.15's "state
// checkpoint fails -> do not interrupt unless emergency."
func TestSafePoint_PersistFailureNeverReachesInterrupt(t *testing.T) {
	var calls []string
	persistErr := &domain.Error{Code: domain.ErrCodeIntegrity, Message: "checkpoint failed", Retryable: false}
	persister := &recordingPersister{calls: &calls, failErr: persistErr}
	interrupter := &recordingInterrupter{calls: &calls}
	coordinator := NewTurnBoundaryCoordinator()

	err := PersistThenInterrupt(context.Background(), coordinator, persister, interrupter, SafePointObservation{
		PauseID:  testPauseID,
		Boundary: BoundaryItemCompleted,
	})
	if !errors.Is(err, error(persistErr)) {
		t.Fatalf("got err %v, want the persister's error propagated unchanged", err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %v, want exactly 1 (persist only — interrupt must never be reached)", calls)
	}
	if calls[0] != "persist:"+string(testPauseID) {
		t.Fatalf("calls[0] = %q, want persist", calls[0])
	}
}

// TestSafePoint_UnsafeBoundaryRejectedBeforeEitherCall proves that an
// explicitly unsafe boundary (ADD §20.4's "Unsafe" list) is rejected
// before EITHER collaborator is called — the ordering guarantee is
// meaningless if the coordinator can be bypassed by simply not checking
// safety first.
func TestSafePoint_UnsafeBoundaryRejectedBeforeEitherCall(t *testing.T) {
	var calls []string
	persister := &recordingPersister{calls: &calls}
	interrupter := &recordingInterrupter{calls: &calls}
	coordinator := NewTurnBoundaryCoordinator()

	unsafeBoundaries := []Boundary{
		BoundaryFilesystemWriteActive,
		BoundaryGitApplyOrCheckout,
		BoundaryDBMigrationTransaction,
		BoundaryUncommittedCheckpointTempWrite,
		BoundaryPackageManagerLockOperation,
		BoundaryDestructiveCommandUnknownCompletion,
		Boundary("some_unrecognized_boundary"),
	}
	for _, b := range unsafeBoundaries {
		t.Run(string(b), func(t *testing.T) {
			err := PersistThenInterrupt(context.Background(), coordinator, persister, interrupter, SafePointObservation{
				PauseID:  testPauseID,
				Boundary: b,
			})
			var derr *domain.Error
			if !errors.As(err, &derr) {
				t.Fatalf("got err %v, want *domain.Error", err)
			}
			if derr.Code != domain.ErrCodeConflict {
				t.Fatalf("Code = %q, want %q", derr.Code, domain.ErrCodeConflict)
			}
			if len(calls) != 0 {
				t.Fatalf("calls = %v, want none — unsafe boundary must reject before either collaborator runs", calls)
			}
		})
	}
}

// TestSafePoint_AllADDSafeBoundariesAccepted proves every boundary ADD
// §20.4 names as safe is actually recognized by IsSafeBoundary /
// TurnBoundaryCoordinator — a forgotten entry would silently make a real
// safe point rejected as unsafe.
func TestSafePoint_AllADDSafeBoundariesAccepted(t *testing.T) {
	safe := []Boundary{
		BoundaryItemCompleted,
		BoundaryPostToolUse,
		BoundaryFilePatchFlushed,
		BoundaryTestCommandExit,
		BoundaryNodeArtifactPersisted,
		BoundaryBeforeNextToolDispatch,
		BoundaryProviderWaitingForInput,
	}
	coordinator := NewTurnBoundaryCoordinator()
	for _, b := range safe {
		t.Run(string(b), func(t *testing.T) {
			if !IsSafeBoundary(b) {
				t.Fatalf("IsSafeBoundary(%q) = false, want true", b)
			}
			if !coordinator.IsSafe(context.Background(), SafePointObservation{PauseID: testPauseID, Boundary: b}) {
				t.Fatalf("TurnBoundaryCoordinator.IsSafe(%q) = false, want true", b)
			}
		})
	}
}

// TestSafePoint_MissingPauseIDRejected proves PersistThenInterrupt
// validates its input rather than calling collaborators with an empty
// PauseID.
func TestSafePoint_MissingPauseIDRejected(t *testing.T) {
	var calls []string
	persister := &recordingPersister{calls: &calls}
	interrupter := &recordingInterrupter{calls: &calls}
	coordinator := NewTurnBoundaryCoordinator()

	err := PersistThenInterrupt(context.Background(), coordinator, persister, interrupter, SafePointObservation{
		Boundary: BoundaryPostToolUse,
	})
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("got err %v, want *domain.Error{Code: validation}", err)
	}
	if len(calls) != 0 {
		t.Fatalf("calls = %v, want none", calls)
	}
}

// TestSafePoint_NilCollaboratorsRejected proves fail-closed construction:
// a nil coordinator/persister/interrupter must not panic and must not
// silently proceed.
func TestSafePoint_NilCollaboratorsRejected(t *testing.T) {
	var calls []string
	persister := &recordingPersister{calls: &calls}
	interrupter := &recordingInterrupter{calls: &calls}
	coordinator := NewTurnBoundaryCoordinator()
	obs := SafePointObservation{PauseID: testPauseID, Boundary: BoundaryPostToolUse}

	cases := []struct {
		name        string
		coordinator SafePointCoordinator
		persister   CheckpointPersister
		interrupter Interrupter
	}{
		{"nil coordinator", nil, persister, interrupter},
		{"nil persister", coordinator, nil, interrupter},
		{"nil interrupter", coordinator, persister, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := PersistThenInterrupt(context.Background(), c.coordinator, c.persister, c.interrupter, obs)
			var derr *domain.Error
			if !errors.As(err, &derr) || derr.Code != domain.ErrCodeUnavailable {
				t.Fatalf("got err %v, want *domain.Error{Code: unavailable}", err)
			}
		})
	}
}
