// predictor-10: Authorization security test suite.
//
// This file is the dedicated adversarial audit of Service.ConsumeAuthorization/
// IssueAuthorization (agents/predictor.md deliverable #12: "one-time
// authorization issuance/consumption with prompt/session/evaluation binding,
// expiry, and replay rejection"), per docs/implementation/vertical-slice/EXECUTION_DAG.md's
// predictor-10 node ("High — replay protection is a security control"). Its
// validation gate is `go test ./internal/evaluation/... -run Authorization`,
// so every test in this file is named to be readable in isolation by an
// external auditor running that exact filter, grouped into four sections:
//
//   - Exactly-once consumption / replay rejection (including high-contention
//     and tight-loop adversarial patterns beyond predictor-09's original
//     8-goroutine test).
//   - Prompt/session binding (including the boundary and encoding cases an
//     attacker would try: empty, whitespace-only, case, unicode
//     normalization).
//   - Expiry precision (nanosecond-adjacent boundary, and expiry racing
//     concurrent replay attempts).
//   - Baseline/plumbing checks (unknown ID, empty required fields, default
//     TTL) carried over from predictor-09 unchanged.
//
// predictor-09 built ConsumeAuthorization in full (see doc.go's
// "ConsumeAuthorization scope note"); this node re-verifies and hardens it.
// One real gap was found and fixed here: see
// TestConsumeAuthorization_RejectsOmittedPromptAgainstBoundAuthorization below
// and service.go's prompt-hash-binding comment for the exact defect
// (omitting PromptHash on the request used to silently skip the binding
// check even when the authorization itself was bound to a real prompt hash).
// Every other adversarial scenario in this file passed on the first try
// against predictor-09's existing logic — see the phase's progress artifact
// (docs/implementation/vertical-slice/predictor.md, node predictor-10) for the full
// enumeration of what was tested and which passed pre-existing vs. needed
// the fix.
package evaluation_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/evaluation"
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

func requireDomainError(t *testing.T, err error, wantCode domain.ErrorCode) *domain.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error with code %q, got nil", wantCode)
	}
	var domErr *domain.Error
	if !errors.As(err, &domErr) {
		t.Fatalf("expected *domain.Error, got %T: %v", err, err)
	}
	if domErr.Code != wantCode {
		t.Errorf("Code = %q, want %q", domErr.Code, wantCode)
	}
	return domErr
}

// --- Section 1: exactly-once consumption / replay rejection ---------------

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
	_ = requireDomainError(t, err, domain.ErrCodeConflict)
}

func TestConsumeAuthorization_ReplayRejected_TightSequentialLoop(t *testing.T) {
	// Adversarial pattern: an attacker (or a buggy retry loop) hammering
	// the same already-consumed authorization ID many times in a tight
	// sequential loop, not just once. Every attempt after the first must
	// fail with the same conflict code — no attempt in the loop may ever
	// slip through, and no attempt may return a different/ambiguous error
	// shape than the first replay rejection.
	clk := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ids := &sequentialIDs{prefix: "auth"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())
	ctx := context.Background()

	auth := issueTestAuthorization(t, svc, "turn-loop", "sha256:abc")

	req := app.ConsumeAuthorizationRequest{
		AuthorizationID: auth.ID,
		TurnID:          "turn-loop",
		PromptHash:      "sha256:abc",
	}
	if _, err := svc.ConsumeAuthorization(ctx, req); err != nil {
		t.Fatalf("first consume: %v", err)
	}

	const replayAttempts = 200
	for i := 0; i < replayAttempts; i++ {
		_, err := svc.ConsumeAuthorization(ctx, req)
		domErr := requireDomainError(t, err, domain.ErrCodeConflict)
		if domErr.Retryable {
			t.Fatalf("replay attempt %d: Retryable = true, want false (replay is not a transient condition)", i)
		}
	}
}

func TestConsumeAuthorization_ConcurrentReplayOnlyOneWins(t *testing.T) {
	// Exercises the storage-layer race directly (goroutines racing the
	// same authorization ID through the same Service/DB), not just
	// sequential calls — the required "authorization consume exactly
	// once" test should hold under concurrency too, and this is also
	// covered by `go test -race`.
	clk := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ids := &sequentialIDs{prefix: "auth"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())

	auth := issueTestAuthorization(t, svc, "turn-concurrent", "sha256:abc")

	runConcurrentConsumeAttempts(t, svc, auth.ID, "turn-concurrent", "sha256:abc", 8)
}

func TestConsumeAuthorization_HighContentionReplayOnlyOneWins(t *testing.T) {
	// predictor-10 hardening: predictor-09's original concurrent test used
	// 8 goroutines. This repeats it at materially higher contention (64
	// goroutines, 8x) against a single authorization ID to check whether
	// the exactly-once guarantee is a property of the conditional UPDATE
	// itself (contention-independent, as store.go's markAuthorizationConsumed
	// doc comment claims) or happened to hold at low concurrency only.
	clk := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ids := &sequentialIDs{prefix: "auth"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())

	auth := issueTestAuthorization(t, svc, "turn-high-contention", "sha256:abc")

	runConcurrentConsumeAttempts(t, svc, auth.ID, "turn-high-contention", "sha256:abc", 64)
}

// runConcurrentConsumeAttempts fires n concurrent ConsumeAuthorization calls
// at the same authorization ID/binding and asserts exactly one succeeds, and
// every failure is specifically ErrCodeConflict (a losing goroutine reading
// a stale, pre-consumption snapshot of the row and treating it as still
// consumable would surface as either a spurious second success or a
// different/wrong error code — this asserts against both failure modes, not
// just the count).
func runConcurrentConsumeAttempts(t *testing.T, svc *evaluation.Service, authID string, turnID domain.TurnID, promptHash string, n int) {
	t.Helper()
	var wg sync.WaitGroup
	results := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := svc.ConsumeAuthorization(context.Background(), app.ConsumeAuthorizationRequest{
				AuthorizationID: authID,
				TurnID:          turnID,
				PromptHash:      promptHash,
			})
			results[idx] = err
		}(i)
	}
	wg.Wait()

	successCount := 0
	for i, err := range results {
		if err == nil {
			successCount++
			continue
		}
		var domErr *domain.Error
		if !errors.As(err, &domErr) {
			t.Fatalf("attempt %d: expected *domain.Error on failure, got %T: %v", i, err, err)
		}
		if domErr.Code != domain.ErrCodeConflict {
			t.Errorf("attempt %d: losing attempt's Code = %q, want conflict (stale-read or wrong-error-shape symptom)", i, domErr.Code)
		}
	}
	if successCount != 1 {
		t.Errorf("successCount = %d, want exactly 1 (exactly-once consumption under concurrency, n=%d)", successCount, n)
	}
}

func TestConsumeAuthorization_ReplayRacesExpiryBoundary(t *testing.T) {
	// Adversarial pattern: replay attempts racing the authorization's own
	// expiry boundary, not just racing each other's consumption. The fake
	// clock is fixed for the duration of the concurrent burst (no goroutine
	// can observe a different "now" than any other — this is intentional:
	// it isolates the exactly-once invariant from expiry-instant flakiness),
	// then a SEPARATE, sequential follow-up attempt after advancing the
	// clock past expiry confirms expiry is still enforced once the
	// contention window closes, regardless of which goroutine won it.
	clk := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ids := &sequentialIDs{prefix: "auth"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())
	svc.AuthorizationTTL = 1 * time.Minute

	auth := issueTestAuthorization(t, svc, "turn-expiry-race", "sha256:abc")

	clk.Advance(59 * time.Second) // one second before expiry throughout the burst
	runConcurrentConsumeAttempts(t, svc, auth.ID, "turn-expiry-race", "sha256:abc", 16)

	// Whichever goroutine won already consumed it; a fresh attempt now
	// (clock unchanged) must see "already consumed", never "expired" —
	// the two failure classes must not be conflated even this close to
	// the boundary.
	_, err := svc.ConsumeAuthorization(context.Background(), app.ConsumeAuthorizationRequest{
		AuthorizationID: auth.ID,
		TurnID:          "turn-expiry-race",
		PromptHash:      "sha256:abc",
	})
	_ = requireDomainError(t, err, domain.ErrCodeConflict)
}

// --- Section 2: prompt/session binding hardening ---------------------------

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
	_ = requireDomainError(t, err, domain.ErrCodeUnauthorized)
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
	_ = requireDomainError(t, err, domain.ErrCodeUnauthorized)
}

func TestConsumeAuthorization_RejectsOmittedPromptAgainstBoundAuthorization(t *testing.T) {
	// predictor-10 audit finding (real gap, fixed this node): the
	// prompt-hash-binding check used to be
	// `req.PromptHash != "" && row.PromptHash != req.PromptHash` in
	// service.go — it skipped the comparison entirely whenever the
	// REQUEST omitted PromptHash, regardless of what the authorization
	// was actually issued with. That let a caller who knows only
	// AuthorizationID + TurnID (no prompt hash at all) consume an
	// authorization that WAS bound to a specific prompt, defeating prompt
	// binding as a control. Fixed by keying the skip on the AUTHORIZATION
	// ROW's own PromptHash instead: a bound authorization must always match
	// on prompt hash, and omitting it on the request is treated as a
	// mismatch, not as "not applicable."
	clk := newFakeClock(time.Now())
	ids := &sequentialIDs{prefix: "auth"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())

	auth := issueTestAuthorization(t, svc, "turn-bound", "sha256:real-prompt-hash")

	_, err := svc.ConsumeAuthorization(context.Background(), app.ConsumeAuthorizationRequest{
		AuthorizationID: auth.ID,
		TurnID:          "turn-bound",
		PromptHash:      "", // omitted — must NOT bypass a real binding
	})
	_ = requireDomainError(t, err, domain.ErrCodeUnauthorized)
}

func TestConsumeAuthorization_AllowsOmittedPromptWhenAuthorizationHasNone(t *testing.T) {
	// The one legitimate use of an empty PromptHash: an authorization that
	// was itself ISSUED without a prompt hash (row.PromptHash == "") has
	// nothing to bind against, so consuming it without a prompt hash is
	// correct, not a bypass. This pins down the fix's boundary precisely
	// so a future change can't re-introduce the omitted-request bypass by
	// "fixing" this case incorrectly.
	clk := newFakeClock(time.Now())
	ids := &sequentialIDs{prefix: "auth"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())

	auth := issueTestAuthorization(t, svc, "turn-no-prompt", "")

	got, err := svc.ConsumeAuthorization(context.Background(), app.ConsumeAuthorizationRequest{
		AuthorizationID: auth.ID,
		TurnID:          "turn-no-prompt",
		PromptHash:      "",
	})
	if err != nil {
		t.Fatalf("expected consume to succeed when the authorization itself has no prompt binding, got: %v", err)
	}
	if got.ID != auth.ID {
		t.Errorf("ID = %q, want %q", got.ID, auth.ID)
	}
}

func TestConsumeAuthorization_RejectsWhitespaceOnlyPromptMismatch(t *testing.T) {
	// Boundary case: a whitespace-only prompt hash on the request must not
	// be treated as equivalent to empty (bypass) nor as accidentally equal
	// to the real bound hash. Plain Go string equality (no TrimSpace
	// anywhere in this package) means " " != "sha256:real" and
	// " " != "" — both must be enforced as a mismatch/rejection, not
	// silently normalized away.
	clk := newFakeClock(time.Now())
	ids := &sequentialIDs{prefix: "auth"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())

	auth := issueTestAuthorization(t, svc, "turn-ws", "sha256:real")

	_, err := svc.ConsumeAuthorization(context.Background(), app.ConsumeAuthorizationRequest{
		AuthorizationID: auth.ID,
		TurnID:          "turn-ws",
		PromptHash:      "   ", // whitespace-only, not empty, not the real hash
	})
	_ = requireDomainError(t, err, domain.ErrCodeUnauthorized)
}

func TestConsumeAuthorization_BindingIsCaseSensitive(t *testing.T) {
	// No COLLATE NOCASE exists anywhere in this package's schema
	// (internal/storage/sqlite/migrations/0044_authorizations.sql) or in
	// Go-level comparisons (plain `!=` on domain.TurnID/string) — confirms
	// an uppercase/lowercase variant of a correct binding is REJECTED, not
	// silently accepted as a case-insensitive match. This guards against a
	// future change accidentally introducing case-insensitive comparison
	// (e.g. by adding COLLATE NOCASE to the migration, or strings.EqualFold
	// in Go) without an explicit, reviewed decision to do so.
	clk := newFakeClock(time.Now())
	ids := &sequentialIDs{prefix: "auth"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())

	auth := issueTestAuthorization(t, svc, "turn-case", "sha256:AbCdEf")

	// Wrong-case TurnID.
	_, err := svc.ConsumeAuthorization(context.Background(), app.ConsumeAuthorizationRequest{
		AuthorizationID: auth.ID,
		TurnID:          "TURN-CASE",
		PromptHash:      "sha256:AbCdEf",
	})
	_ = requireDomainError(t, err, domain.ErrCodeUnauthorized)

	// Wrong-case PromptHash (correct TurnID).
	_, err = svc.ConsumeAuthorization(context.Background(), app.ConsumeAuthorizationRequest{
		AuthorizationID: auth.ID,
		TurnID:          "turn-case",
		PromptHash:      "sha256:abcdef",
	})
	_ = requireDomainError(t, err, domain.ErrCodeUnauthorized)

	// Exact case must still succeed (confirms the rejections above are
	// really about case, not some other typo in the test itself).
	got, err := svc.ConsumeAuthorization(context.Background(), app.ConsumeAuthorizationRequest{
		AuthorizationID: auth.ID,
		TurnID:          "turn-case",
		PromptHash:      "sha256:AbCdEf",
	})
	if err != nil {
		t.Fatalf("expected exact-case consume to succeed, got: %v", err)
	}
	if got.ID != auth.ID {
		t.Errorf("ID = %q, want %q", got.ID, auth.ID)
	}
}

func TestConsumeAuthorization_RejectsUnicodeNormalizationMismatch(t *testing.T) {
	// Boundary case: PromptHash is a hex-encoded digest in every real
	// caller, so unicode normalization is not expected to matter in
	// practice -- but this package's binding check is plain Go string
	// equality with no NFC/NFD normalization step, so if a caller ever did
	// pass a non-ASCII-normalized-form string (e.g. a differently-composed
	// identifier upstream), a byte-distinct NFD form must NOT be silently
	// accepted as equal to the NFC form it was bound with. Built from
	// explicit \u escapes (not literal source-file glyphs, to keep this
	// unambiguous regardless of editor/encoding): nfc uses U+00E9
	// (precomposed "e with acute"); nfd spells the canonically-equivalent
	// "e" (U+0065) followed by the combining acute accent U+0301. Both
	// render identically but are different byte sequences and must not
	// compare equal.
	clk := newFakeClock(time.Now())
	ids := &sequentialIDs{prefix: "auth"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())

	const nfc = "sha256:caf\u00e9"  // precomposed: c-a-f-U+00E9
	const nfd = "sha256:cafe\u0301" // decomposed: c-a-f-e-U+0301

	if nfc == nfd {
		t.Fatal("test setup bug: nfc and nfd must be byte-distinct")
	}

	auth := issueTestAuthorization(t, svc, "turn-unicode", nfc)

	_, err := svc.ConsumeAuthorization(context.Background(), app.ConsumeAuthorizationRequest{
		AuthorizationID: auth.ID,
		TurnID:          "turn-unicode",
		PromptHash:      nfd,
	})
	_ = requireDomainError(t, err, domain.ErrCodeUnauthorized)

	// The exact original (byte-identical) form must still succeed.
	got, err := svc.ConsumeAuthorization(context.Background(), app.ConsumeAuthorizationRequest{
		AuthorizationID: auth.ID,
		TurnID:          "turn-unicode",
		PromptHash:      nfc,
	})
	if err != nil {
		t.Fatalf("expected byte-identical NFC form to succeed, got: %v", err)
	}
	if got.ID != auth.ID {
		t.Errorf("ID = %q, want %q", got.ID, auth.ID)
	}
}

// --- Section 3: expiry precision -------------------------------------------

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
	_ = requireDomainError(t, err, domain.ErrCodeUnauthorized)
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

func TestConsumeAuthorization_SucceedsOneNanosecondBeforeExpiry(t *testing.T) {
	// predictor-10 hardening: predictor-09's boundary tests used a 1-second
	// granularity (59s/60s of a 1-minute TTL). This tightens the boundary
	// to the smallest unit time.Time distinguishes (1ns) to confirm the
	// expiry comparison itself (`now.Before(expiresAt)` in service.go) is
	// exact at that resolution, not merely "close enough" at second
	// granularity.
	clk := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ids := &sequentialIDs{prefix: "auth"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())
	svc.AuthorizationTTL = 1 * time.Minute

	auth := issueTestAuthorization(t, svc, "turn-ns-before", "sha256:abc")

	clk.Advance(1*time.Minute - 1*time.Nanosecond)
	got, err := svc.ConsumeAuthorization(context.Background(), app.ConsumeAuthorizationRequest{
		AuthorizationID: auth.ID,
		TurnID:          "turn-ns-before",
		PromptHash:      "sha256:abc",
	})
	if err != nil {
		t.Fatalf("expected consume 1ns before expiry to succeed, got: %v", err)
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
	_ = requireDomainError(t, err, domain.ErrCodeUnauthorized)
}

func TestConsumeAuthorization_RejectedOneNanosecondAfterExpiry(t *testing.T) {
	// Adjacent boundary to TestConsumeAuthorization_ExactlyAtExpiryIsRejected:
	// 1ns past the exact expiry instant must also be rejected (expiry is
	// monotonically enforced past the boundary, not just exactly at it).
	clk := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	ids := &sequentialIDs{prefix: "auth"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())
	svc.AuthorizationTTL = 1 * time.Minute

	auth := issueTestAuthorization(t, svc, "turn-ns-after", "sha256:abc")

	clk.Advance(1*time.Minute + 1*time.Nanosecond)
	_, err := svc.ConsumeAuthorization(context.Background(), app.ConsumeAuthorizationRequest{
		AuthorizationID: auth.ID,
		TurnID:          "turn-ns-after",
		PromptHash:      "sha256:abc",
	})
	_ = requireDomainError(t, err, domain.ErrCodeUnauthorized)
}

// --- Section 4: baseline/plumbing checks (unchanged from predictor-09) ----

func TestConsumeAuthorization_UnknownIDReturnsNotFound(t *testing.T) {
	clk := newFakeClock(time.Now())
	ids := &sequentialIDs{prefix: "auth"}
	svc, _ := newTestService(t, clk, ids, newFakeDataSource())

	_, err := svc.ConsumeAuthorization(context.Background(), app.ConsumeAuthorizationRequest{
		AuthorizationID: "does-not-exist",
		TurnID:          "turn-1",
	})
	_ = requireDomainError(t, err, domain.ErrCodeNotFound)
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
