package evaluation_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/huaiche94/preflight/internal/app"
	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/evaluation"
)

func issueTestAuthorization(t *testing.T, svc interface {
	IssueAuthorization(ctx context.Context, turnID domain.TurnID, promptHash, snapshotFingerprint, decision string, repoCheckpointID *domain.RepositoryCheckpointID) (app.Authorization, error)
}, turnID domain.TurnID, promptHash string) app.Authorization {
	t.Helper()
	auth, err := svc.IssueAuthorization(context.Background(), turnID, promptHash, "fp-1", "CHECKPOINT_AND_RUN", nil)
	if err != nil {
		t.Fatalf("IssueAuthorization: %v", err)
	}
	return auth
}

func TestConsumeAuthorization_ConsumesExactlyOnce(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ids := &sequentialIDs{prefix: "auth"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())

	auth := issueTestAuthorization(t, svc, "turn-1", "sha256:abc")

	ctx := context.Background()
	got, err := svc.ConsumeAuthorization(ctx, app.ConsumeAuthorizationRequest{
		AuthorizationID: auth.ID,
		TurnID:          "turn-1",
		PromptHash:      "sha256:abc",
	})
	if err != nil {
		t.Fatalf("first ConsumeAuthorization: %v", err)
	}
	if got.ConsumedAt == nil {
		t.Fatal("ConsumedAt is nil after a successful consume")
	}
	if !got.ConsumedAt.Equal(clk.Now()) {
		t.Errorf("ConsumedAt = %v, want %v", got.ConsumedAt, clk.Now())
	}

	// Second consume of the SAME authorization ID must be rejected —
	// exactly-once, replay rejected.
	_, err = svc.ConsumeAuthorization(ctx, app.ConsumeAuthorizationRequest{
		AuthorizationID: auth.ID,
		TurnID:          "turn-1",
		PromptHash:      "sha256:abc",
	})
	if err == nil {
		t.Fatal("expected the second ConsumeAuthorization (replay) to be rejected")
	}
	var domErr *domain.Error
	if !errors.As(err, &domErr) {
		t.Fatalf("expected *domain.Error, got %T: %v", err, err)
	}
	if domErr.Code != domain.ErrCodeConflict {
		t.Errorf("replay rejection Code = %q, want conflict", domErr.Code)
	}
}

func TestConsumeAuthorization_ConcurrentReplayOnlyOneWins(t *testing.T) {
	// Exercises the storage-layer race directly (two goroutines racing
	// the same authorization ID through the same Service/DB), not just
	// two sequential calls — the required "authorization consume exactly
	// once" test should hold under concurrency too, and this is also
	// covered by `go test -race`.
	clk := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ids := &sequentialIDs{prefix: "auth"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())

	auth := issueTestAuthorization(t, svc, "turn-concurrent", "sha256:abc")

	const attempts = 8
	var wg sync.WaitGroup
	successes := make([]bool, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := svc.ConsumeAuthorization(context.Background(), app.ConsumeAuthorizationRequest{
				AuthorizationID: auth.ID,
				TurnID:          "turn-concurrent",
				PromptHash:      "sha256:abc",
			})
			successes[idx] = err == nil
		}(i)
	}
	wg.Wait()

	successCount := 0
	for _, ok := range successes {
		if ok {
			successCount++
		}
	}
	if successCount != 1 {
		t.Errorf("successCount = %d, want exactly 1 (exactly-once consumption under concurrency)", successCount)
	}
}

func TestConsumeAuthorization_RejectsWrongSession(t *testing.T) {
	clk := newFakeClock(time.Now())
	ids := &sequentialIDs{prefix: "auth"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())

	auth := issueTestAuthorization(t, svc, "turn-real", "sha256:abc")

	_, err := svc.ConsumeAuthorization(context.Background(), app.ConsumeAuthorizationRequest{
		AuthorizationID: auth.ID,
		TurnID:          "turn-wrong", // wrong turn/session binding
		PromptHash:      "sha256:abc",
	})
	if err == nil {
		t.Fatal("expected ConsumeAuthorization to reject a wrong-turn binding")
	}
	var domErr *domain.Error
	if !errors.As(err, &domErr) {
		t.Fatalf("expected *domain.Error, got %T: %v", err, err)
	}
	if domErr.Code != domain.ErrCodeUnauthorized {
		t.Errorf("Code = %q, want unauthorized", domErr.Code)
	}
}

func TestConsumeAuthorization_RejectsWrongPrompt(t *testing.T) {
	clk := newFakeClock(time.Now())
	ids := &sequentialIDs{prefix: "auth"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())

	auth := issueTestAuthorization(t, svc, "turn-1", "sha256:abc")

	_, err := svc.ConsumeAuthorization(context.Background(), app.ConsumeAuthorizationRequest{
		AuthorizationID: auth.ID,
		TurnID:          "turn-1",
		PromptHash:      "sha256:different", // wrong prompt binding
	})
	if err == nil {
		t.Fatal("expected ConsumeAuthorization to reject a wrong-prompt binding")
	}
	var domErr *domain.Error
	if !errors.As(err, &domErr) {
		t.Fatalf("expected *domain.Error, got %T: %v", err, err)
	}
	if domErr.Code != domain.ErrCodeUnauthorized {
		t.Errorf("Code = %q, want unauthorized", domErr.Code)
	}
}

func TestConsumeAuthorization_RejectsStaleExpired(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ids := &sequentialIDs{prefix: "auth"}
	svc, db := newTestService(t, clk, ids, newFakeDataSource())
	_ = db
	svc.AuthorizationTTL = 1 * time.Minute

	auth := issueTestAuthorization(t, svc, "turn-1", "sha256:abc")

	// Advance the fake clock (never time.Now() directly — this is the
	// clock-bound expiry test) past the authorization's TTL.
	clk.Advance(2 * time.Minute)

	_, err := svc.ConsumeAuthorization(context.Background(), app.ConsumeAuthorizationRequest{
		AuthorizationID: auth.ID,
		TurnID:          "turn-1",
		PromptHash:      "sha256:abc",
	})
	if err == nil {
		t.Fatal("expected ConsumeAuthorization to reject an expired authorization")
	}
	var domErr *domain.Error
	if !errors.As(err, &domErr) {
		t.Fatalf("expected *domain.Error, got %T: %v", err, err)
	}
	if domErr.Code != domain.ErrCodeUnauthorized {
		t.Errorf("Code = %q, want unauthorized", domErr.Code)
	}
}

func TestConsumeAuthorization_SucceedsExactlyAtBoundary(t *testing.T) {
	// Clock-bound expiry boundary test: one tick before ExpiresAt must
	// still succeed; the exact ExpiresAt instant (and after) must not.
	clk := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ids := &sequentialIDs{prefix: "auth"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())
	svc.AuthorizationTTL = 1 * time.Minute

	auth := issueTestAuthorization(t, svc, "turn-1", "sha256:abc")

	clk.Advance(59 * time.Second)
	got, err := svc.ConsumeAuthorization(context.Background(), app.ConsumeAuthorizationRequest{
		AuthorizationID: auth.ID,
		TurnID:          "turn-1",
		PromptHash:      "sha256:abc",
	})
	if err != nil {
		t.Fatalf("expected consume one second before expiry to succeed, got: %v", err)
	}
	if got.ID != auth.ID {
		t.Errorf("ID = %q, want %q", got.ID, auth.ID)
	}
}

func TestConsumeAuthorization_ExactlyAtExpiryIsRejected(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ids := &sequentialIDs{prefix: "auth"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())
	svc.AuthorizationTTL = 1 * time.Minute

	auth := issueTestAuthorization(t, svc, "turn-1", "sha256:abc")

	clk.Advance(1 * time.Minute) // exactly at ExpiresAt
	_, err := svc.ConsumeAuthorization(context.Background(), app.ConsumeAuthorizationRequest{
		AuthorizationID: auth.ID,
		TurnID:          "turn-1",
		PromptHash:      "sha256:abc",
	})
	if err == nil {
		t.Fatal("expected consume exactly at ExpiresAt to be rejected (not before)")
	}
}

func TestConsumeAuthorization_UnknownIDReturnsNotFound(t *testing.T) {
	clk := newFakeClock(time.Now())
	ids := &sequentialIDs{prefix: "auth"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())

	_, err := svc.ConsumeAuthorization(context.Background(), app.ConsumeAuthorizationRequest{
		AuthorizationID: "does-not-exist",
		TurnID:          "turn-1",
	})
	if err == nil {
		t.Fatal("expected an error for an unknown AuthorizationID")
	}
	var domErr *domain.Error
	if !errors.As(err, &domErr) {
		t.Fatalf("expected *domain.Error, got %T: %v", err, err)
	}
	if domErr.Code != domain.ErrCodeNotFound {
		t.Errorf("Code = %q, want not_found", domErr.Code)
	}
}

func TestConsumeAuthorization_RejectsEmptyIDs(t *testing.T) {
	clk := newFakeClock(time.Now())
	ids := &sequentialIDs{prefix: "auth"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())
	ctx := context.Background()

	if _, err := svc.ConsumeAuthorization(ctx, app.ConsumeAuthorizationRequest{TurnID: "turn-1"}); err == nil {
		t.Error("expected an error for an empty AuthorizationID")
	}
	if _, err := svc.ConsumeAuthorization(ctx, app.ConsumeAuthorizationRequest{AuthorizationID: "auth-1"}); err == nil {
		t.Error("expected an error for an empty TurnID")
	}
}

func TestIssueAuthorization_DefaultTTLWhenUnset(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ids := &sequentialIDs{prefix: "auth"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())
	// svc.AuthorizationTTL left at its constructed default.

	auth := issueTestAuthorization(t, svc, "turn-1", "sha256:abc")
	wantExpiry := clk.Now().Add(evaluation.DefaultAuthorizationTTL)
	if !auth.ExpiresAt.Equal(wantExpiry) {
		t.Errorf("ExpiresAt = %v, want %v", auth.ExpiresAt, wantExpiry)
	}
}
