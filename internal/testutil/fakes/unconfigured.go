package fakes

import "github.com/huaiche94/preflight/internal/domain"

// errUnconfigured is every fake's shared nil-Func behavior: the frozen
// domain.Error shape (CONTRACT_FREEZE.md "Error contract") with
// ErrCodeUnavailable — the method's real implementation is "not available"
// in this test double — and Retryable: false, because retrying an
// unconfigured fake can never succeed; the test itself must set the
// corresponding <Method>Func field.
func errUnconfigured(fake, method string) error {
	return &domain.Error{
		Code:      domain.ErrCodeUnavailable,
		Message:   fake + "." + method + ": fake method not configured (set " + method + "Func)",
		Retryable: false,
		Details: map[string]string{
			"fake":   fake,
			"method": method,
		},
	}
}
