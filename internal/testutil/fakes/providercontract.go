// providercontract.go implements agents/runtime.md Part A deliverable 11:
// "Provider interrupter/resumer fake contract tests" (runtime-a10).
//
// # What a "contract test" means for these two interfaces specifically
//
// app.TurnInterrupter and app.SessionResumer (internal/app/ports.go) are
// deliberately narrow, single-method interfaces — Constitution §4's
// "no God interface" rule applied to the provider-interrupt/resume
// boundary ADD §9.10 names. Neither CONTRACT_FREEZE.md nor
// agents/runtime.md freezes additional behavioral invariants for them
// beyond the bare method signature (unlike, say,
// EvaluationService.ConsumeAuthorization, which CONTRACT_FREEZE.md's
// "Transaction boundaries" section pins down precisely). So this suite
// does not invent contract requirements that were never specified — it
// tests exactly the properties internal/pause's own callers (safepoint.go's
// PersistThenInterrupt, resumevalidation.go's session-capability check, and
// lifecycle.go's Resume) actually rely on an implementation upholding,
// which is the honest, non-speculative scope for a "contract" here:
//
//  1. Interrupt/Resume on a well-formed request succeed and return
//     data that is internally consistent with what was asked (Resume's
//     RunHandle.SessionID must reflect the session that was actually
//     resumed, not a different or zero one — a caller threads this ID
//     onward into subsequent evaluation/observation calls, so a silent
//     substitution here would misattribute an entire turn to the wrong
//     session).
//  2. Both methods surface a failure as a plain returned error, never a
//     panic — this is what "provider interrupt failure leaves recoverable
//     state" (agents/runtime.md required test) depends on at the layer
//     below the state machine: EventInterruptFailed (statemachine.go) can
//     only be applied if the interrupter's own failure arrives as an
//     ordinary error value the caller can inspect and react to, not a
//     crashed goroutine. (ADD §20.15 additionally distinguishes a plain
//     failure from a TIMEOUT specifically — "kill managed process, mark
//     uncertain" — but that distinction is the pause package's own
//     interrupt_confidence/reconciliation concern layered above this call,
//     per §28.4's "inspect provider, mark sleeping/failed"; it is not a
//     second return channel this narrow interface itself needs, so this
//     suite does not test for it separately from "a plain error.")
//  3. Both methods respect context cancellation: an already-cancelled
//     context must not be silently ignored in favor of "succeeding
//     anyway" — a caller (e.g. a request-scoped CLI invocation that timed
//     out) needs to be able to trust that a cancelled context actually
//     stops the operation rather than leaving it to complete unobserved.
//
// # Why this is a function taking a constructor, not a fixed instance
//
// ProviderInterrupterContract/ProviderSessionResumerContract each take a
// `newX func() X` constructor rather than a single ready-made instance, so
// a caller that needs fresh per-subtest state (a fake with call-counting,
// or a future real adapter that needs a fresh provider session per subtest)
// can supply one, and the suite still runs every check as an isolated
// t.Run subtest. This mirrors Go's own stdlib convention for reusable
// contract suites (e.g. testing/fstest.TestFS) applied to this project's
// own frozen interfaces. Any implementation — FakeTurnInterrupter/
// FakeSessionResumer (provider.go, this wave) today, or a future real
// claude-provider signal-interruption/session-resume adapter (claude-
// provider's own documented stretch goal, agents/claude-provider.md
// "Stretch") — runs this exact suite to prove itself compliant, without
// runtime needing to write bespoke tests for that future adapter.
package fakes

import (
	"context"
	"testing"

	"github.com/huaiche94/auspex/internal/app"
)

// ProviderInterrupterContract runs the full app.TurnInterrupter contract
// suite against newInterrupter(). Configure controls what a "successful"
// and "failing" Interrupt call look like for this particular
// implementation, since a bare interface constructor has no way to inject
// that behavior itself — see InterrupterContractConfig's own doc comment.
func ProviderInterrupterContract(t *testing.T, newInterrupter func() app.TurnInterrupter, cfg InterrupterContractConfig) {
	t.Helper()
	cfg.setDefaults()

	validLocator := app.RunLocator{SessionID: "sess-contract-1", TurnID: "turn-contract-1"}

	t.Run("SucceedsOnWellFormedLocator", func(t *testing.T) {
		interrupter := newInterrupter()
		cfg.ArrangeSuccess(t, interrupter)
		if err := interrupter.Interrupt(context.Background(), validLocator); err != nil {
			t.Fatalf("Interrupt(valid locator) = %v, want nil error", err)
		}
	})

	t.Run("FailureIsAPlainErrorNotAPanic", func(t *testing.T) {
		interrupter := newInterrupter()
		cfg.ArrangeFailure(t, interrupter)
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Interrupt panicked instead of returning an error: %v", r)
			}
		}()
		err := interrupter.Interrupt(context.Background(), validLocator)
		if err == nil {
			t.Fatal("Interrupt() = nil error, want a non-nil error for the configured failure case")
		}
	})

	t.Run("RespectsAlreadyCancelledContext", func(t *testing.T) {
		if cfg.SkipContextCancellation {
			t.Skip("this implementation documents itself as not context-cancellation-aware (SkipContextCancellation=true)")
		}
		interrupter := newInterrupter()
		cfg.ArrangeSuccess(t, interrupter)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := interrupter.Interrupt(ctx, validLocator)
		if err == nil {
			t.Fatal("Interrupt(already-cancelled ctx) = nil error, want a non-nil error (context must be respected, not silently ignored)")
		}
	})
}

// InterrupterContractConfig lets each implementation tell the contract
// suite how to arrange its own success/failure cases — the suite itself
// has no way to know, e.g., a fake's Func-field wiring or a real adapter's
// own session-setup requirements. ArrangeSuccess/ArrangeFailure receive the
// SAME interrupter instance newInterrupter() just constructed, so a fake
// can mutate its own Func fields (they're exported) and a real adapter can
// perform whatever out-of-band setup (e.g. starting then killing a managed
// process) makes its next call fail deterministically.
type InterrupterContractConfig struct {
	// ArrangeSuccess prepares interrupter so its next Interrupt call
	// succeeds. Defaults to a no-op if interrupter is already
	// success-configured (e.g. FakeTurnInterrupter's zero-value
	// InterruptFunc default, set via WithDefaultSuccess below).
	ArrangeSuccess func(t *testing.T, interrupter app.TurnInterrupter)
	// ArrangeFailure prepares interrupter so its next Interrupt call
	// fails. Required — there is no safe generic default for "make this
	// fail" across arbitrary implementations.
	ArrangeFailure func(t *testing.T, interrupter app.TurnInterrupter)
	// SkipContextCancellation opts out of the context-cancellation check
	// for an implementation that documents itself as not (yet)
	// context-aware — e.g. a minimal fake that intentionally ignores ctx.
	// Left false (the check runs) is the default and the expectation for
	// any implementation intended for production use.
	SkipContextCancellation bool
}

func (c *InterrupterContractConfig) setDefaults() {
	if c.ArrangeSuccess == nil {
		c.ArrangeSuccess = func(t *testing.T, _ app.TurnInterrupter) { t.Helper() }
	}
	if c.ArrangeFailure == nil {
		panic("fakes: InterrupterContractConfig.ArrangeFailure must be set — there is no safe generic default for inducing a failure")
	}
}

// ProviderSessionResumerContract runs the full app.SessionResumer contract
// suite against newResumer(). See ResumerContractConfig's doc comment for
// how a caller configures success/failure arrangement.
func ProviderSessionResumerContract(t *testing.T, newResumer func() app.SessionResumer, cfg ResumerContractConfig) {
	t.Helper()
	cfg.setDefaults()

	req := app.ResumeProviderRequest{SessionID: "sess-contract-1"}

	t.Run("SucceedsAndPreservesSessionIdentity", func(t *testing.T) {
		resumer := newResumer()
		cfg.ArrangeSuccess(t, resumer)
		handle, err := resumer.Resume(context.Background(), req)
		if err != nil {
			t.Fatalf("Resume(valid request) = %v, want nil error", err)
		}
		if handle.SessionID != req.SessionID {
			t.Fatalf("Resume returned SessionID %q, want the requested %q (a resumer must never silently substitute a different session)", handle.SessionID, req.SessionID)
		}
		if handle.TurnID == "" {
			t.Fatal("Resume returned an empty TurnID — a resumed run needs a fresh turn identity for the caller to hang subsequent evaluation off")
		}
	})

	t.Run("FailureIsAPlainErrorNotAPanic", func(t *testing.T) {
		resumer := newResumer()
		cfg.ArrangeFailure(t, resumer)
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Resume panicked instead of returning an error: %v", r)
			}
		}()
		_, err := resumer.Resume(context.Background(), req)
		if err == nil {
			t.Fatal("Resume() = nil error, want a non-nil error for the configured failure case")
		}
	})

	t.Run("RespectsAlreadyCancelledContext", func(t *testing.T) {
		if cfg.SkipContextCancellation {
			t.Skip("this implementation documents itself as not context-cancellation-aware (SkipContextCancellation=true)")
		}
		resumer := newResumer()
		cfg.ArrangeSuccess(t, resumer)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := resumer.Resume(ctx, req)
		if err == nil {
			t.Fatal("Resume(already-cancelled ctx) = nil error, want a non-nil error (context must be respected, not silently ignored)")
		}
	})

	t.Run("RejectsEmptySessionIDRatherThanFabricatingOne", func(t *testing.T) {
		resumer := newResumer()
		cfg.ArrangeSuccess(t, resumer)
		handle, err := resumer.Resume(context.Background(), app.ResumeProviderRequest{})
		if err == nil && handle.SessionID == "" {
			// Some implementations may legitimately reject this with an
			// error (preferred, fail-closed) OR — if they don't validate
			// at all — must at least not return a handle. What must NEVER
			// happen is returning success with a fabricated, non-empty
			// SessionID that does not correspond to the (empty, invalid)
			// request; that check is below.
			return
		}
		if err == nil && handle.SessionID != "" {
			t.Fatalf("Resume(empty SessionID) succeeded with a fabricated SessionID %q — an empty request must not be silently upgraded to a real session", handle.SessionID)
		}
	})
}

// ResumerContractConfig mirrors InterrupterContractConfig for
// app.SessionResumer — see that type's doc comment for the full rationale.
type ResumerContractConfig struct {
	ArrangeSuccess          func(t *testing.T, resumer app.SessionResumer)
	ArrangeFailure          func(t *testing.T, resumer app.SessionResumer)
	SkipContextCancellation bool
}

func (c *ResumerContractConfig) setDefaults() {
	if c.ArrangeSuccess == nil {
		c.ArrangeSuccess = func(t *testing.T, _ app.SessionResumer) { t.Helper() }
	}
	if c.ArrangeFailure == nil {
		panic("fakes: ResumerContractConfig.ArrangeFailure must be set — there is no safe generic default for inducing a failure")
	}
}
