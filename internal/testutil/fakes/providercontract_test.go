package fakes_test

import (
	"context"
	"errors"
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/testutil/fakes"
)

// errFakeInterruptFailed/errFakeResumeFailed are this test file's own
// induced-failure sentinels — kept local (rather than importing an
// unexported error from the fakes package) since a contract-suite caller
// should be free to arrange failure however makes sense for its own
// implementation; the suite itself only asserts "a non-nil error," never a
// specific value or type (see providercontract.go's doc comment).
var (
	errFakeInterruptFailed = errors.New("fake: induced Interrupt failure")
	errFakeResumeFailed    = errors.New("fake: induced Resume failure")
)

// TestProviderContract_FakeTurnInterrupter proves FakeTurnInterrupter
// (provider.go, this phase) is contract-compliant NOW — the whole point of
// runtime-a10's deliverable: this fake, and any future real
// implementation, run the exact same suite. The InterruptFunc supplied
// here deliberately honors ctx (checks ctx.Err() before reporting success),
// since that is what a genuinely compliant implementation must do —
// FakeTurnInterrupter's Func field is a plain closure with no built-in
// context handling of its own, so honoring ctx is this test's
// configuration choice, exactly as a real caller wiring the fake for
// production-shaped test coverage would do.
func TestProviderContract_FakeTurnInterrupter(t *testing.T) {
	newInterrupter := func() app.TurnInterrupter {
		return &fakes.FakeTurnInterrupter{}
	}
	fakes.ProviderInterrupterContract(t, newInterrupter, fakes.InterrupterContractConfig{
		ArrangeSuccess: func(t *testing.T, interrupter app.TurnInterrupter) {
			t.Helper()
			fake, ok := interrupter.(*fakes.FakeTurnInterrupter)
			if !ok {
				t.Fatalf("interrupter is %T, want *fakes.FakeTurnInterrupter", interrupter)
			}
			fake.InterruptFunc = func(ctx context.Context, _ app.RunLocator) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				return nil
			}
		},
		ArrangeFailure: func(t *testing.T, interrupter app.TurnInterrupter) {
			t.Helper()
			fake, ok := interrupter.(*fakes.FakeTurnInterrupter)
			if !ok {
				t.Fatalf("interrupter is %T, want *fakes.FakeTurnInterrupter", interrupter)
			}
			fake.InterruptFunc = func(_ context.Context, _ app.RunLocator) error {
				return errFakeInterruptFailed
			}
		},
	})
}

// TestProviderContract_FakeTurnInterrupter_NaiveConfigurationOptsOutOfContextCheck
// demonstrates the OTHER legitimate path through the same suite: a caller
// that wires FakeTurnInterrupter with a minimal InterruptFunc which does
// not itself inspect ctx (e.g. a quick unit test that doesn't care about
// cancellation) is still free to do so, by setting
// SkipContextCancellation: true — the suite does not force every caller
// into context-awareness, it only requires that a caller who claims to be
// context-aware (the default, SkipContextCancellation: false) is telling
// the truth. This is what TestProviderContract_FakeTurnInterrupter above
// proves for FakeTurnInterrupter's intended, production-shaped
// configuration.
func TestProviderContract_FakeTurnInterrupter_NaiveConfigurationOptsOutOfContextCheck(t *testing.T) {
	newInterrupter := func() app.TurnInterrupter {
		return &fakes.FakeTurnInterrupter{}
	}
	fakes.ProviderInterrupterContract(t, newInterrupter, fakes.InterrupterContractConfig{
		ArrangeSuccess: func(t *testing.T, interrupter app.TurnInterrupter) {
			t.Helper()
			fake := interrupter.(*fakes.FakeTurnInterrupter)
			fake.InterruptFunc = func(_ context.Context, _ app.RunLocator) error {
				return nil
			}
		},
		ArrangeFailure: func(t *testing.T, interrupter app.TurnInterrupter) {
			t.Helper()
			fake := interrupter.(*fakes.FakeTurnInterrupter)
			fake.InterruptFunc = func(_ context.Context, _ app.RunLocator) error {
				return errFakeInterruptFailed
			}
		},
		SkipContextCancellation: true,
	})
}

// TestProviderContract_FakeSessionResumer proves FakeSessionResumer
// (provider.go) is contract-compliant.
func TestProviderContract_FakeSessionResumer(t *testing.T) {
	newResumer := func() app.SessionResumer {
		return &fakes.FakeSessionResumer{}
	}
	fakes.ProviderSessionResumerContract(t, newResumer, fakes.ResumerContractConfig{
		ArrangeSuccess: func(t *testing.T, resumer app.SessionResumer) {
			t.Helper()
			fake, ok := resumer.(*fakes.FakeSessionResumer)
			if !ok {
				t.Fatalf("resumer is %T, want *fakes.FakeSessionResumer", resumer)
			}
			fake.ResumeFunc = func(ctx context.Context, req app.ResumeProviderRequest) (app.RunHandle, error) {
				if err := ctx.Err(); err != nil {
					return app.RunHandle{}, err
				}
				if req.SessionID == "" {
					return app.RunHandle{}, &domain.Error{
						Code: domain.ErrCodeValidation, Message: "fake: Resume requires a non-empty SessionID", Retryable: false,
					}
				}
				return app.RunHandle{SessionID: req.SessionID, TurnID: domain.TurnID("turn-resumed-1")}, nil
			}
		},
		ArrangeFailure: func(t *testing.T, resumer app.SessionResumer) {
			t.Helper()
			fake, ok := resumer.(*fakes.FakeSessionResumer)
			if !ok {
				t.Fatalf("resumer is %T, want *fakes.FakeSessionResumer", resumer)
			}
			fake.ResumeFunc = func(_ context.Context, _ app.ResumeProviderRequest) (app.RunHandle, error) {
				return app.RunHandle{}, errFakeResumeFailed
			}
		},
	})
}

// TestProviderContract_FakeSessionResumer_UnconfiguredFailsClosed proves
// the fake's OWN default nil-Func behavior (errUnconfigured, per the
// package's general fake convention) is itself a valid "failure" arrangement
// — i.e. an unconfigured fake is contract-compliant too (it fails with a
// plain error, never panics), reusing the suite to prove that default path
// specifically, rather than only ever testing a caller-supplied ResumeFunc.
func TestProviderContract_FakeSessionResumer_UnconfiguredFailsClosed(t *testing.T) {
	newResumer := func() app.SessionResumer {
		return &fakes.FakeSessionResumer{}
	}
	fakes.ProviderSessionResumerContract(t, newResumer, fakes.ResumerContractConfig{
		ArrangeSuccess: func(t *testing.T, resumer app.SessionResumer) {
			t.Helper()
			fake := resumer.(*fakes.FakeSessionResumer)
			fake.ResumeFunc = func(ctx context.Context, req app.ResumeProviderRequest) (app.RunHandle, error) {
				if err := ctx.Err(); err != nil {
					return app.RunHandle{}, err
				}
				if req.SessionID == "" {
					return app.RunHandle{}, &domain.Error{Code: domain.ErrCodeValidation, Message: "empty session", Retryable: false}
				}
				return app.RunHandle{SessionID: req.SessionID, TurnID: domain.TurnID("turn-resumed-1")}, nil
			}
		},
		// Leave ResumeFunc nil for the failure case: FakeSessionResumer's
		// own errUnconfigured default IS the induced failure here.
		ArrangeFailure: func(t *testing.T, resumer app.SessionResumer) {
			t.Helper()
			fake := resumer.(*fakes.FakeSessionResumer)
			fake.ResumeFunc = nil
		},
	})
}
