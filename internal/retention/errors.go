// errors.go: this package's constructors for the frozen domain.Error
// shape (CONTRACT_FREEZE.md "Error contract"). Retention is fail-closed
// by design (ADR-046), so the code choices matter:
//
//   - archive write/verify I/O failures are OPERATIONAL — nothing was
//     deleted, nothing is corrupted, retrying after fixing the disk is
//     correct → ErrCodeUnavailable, Retryable: true;
//   - a successfully-read archive whose count/digest mismatches what was
//     selected, or a DELETE affecting a different number of rows than
//     were selected, is a STATE-INTEGRITY failure → ErrCodeIntegrity /
//     ErrCodeConflict per the contract's fail-closed rule.
package retention

import (
	"strconv"

	"github.com/huaiche94/auspex/internal/domain"
)

// errValidation reports a caller/composition mistake (bad flag value,
// missing dependency).
func errValidation(msg string) *domain.Error {
	return &domain.Error{Code: domain.ErrCodeValidation, Message: "retention: " + msg, Retryable: false}
}

// errUnavailable wraps an operational failure (I/O, DB read) that aborted
// the pass before any delete ran. Retryable: the fail-closed ordering
// guarantees no raw data was touched, so re-running after the operational
// condition clears is always safe.
func errUnavailable(msg string, err error) *domain.Error {
	return &domain.Error{
		Code:      domain.ErrCodeUnavailable,
		Message:   "retention: " + msg + ": " + err.Error(),
		Retryable: true,
	}
}

// errArchiveMismatch reports a verified-read archive whose contents do
// not match what was selected — a state-integrity failure per
// CONTRACT_FREEZE.md (the bytes on disk cannot be trusted to reconstruct
// the rows, so deleting them is forbidden).
func errArchiveMismatch(path, detail string) *domain.Error {
	return &domain.Error{
		Code:      domain.ErrCodeIntegrity,
		Message:   "retention: archive verification failed: " + detail,
		Retryable: false,
		Details:   map[string]string{"archive_path": path},
	}
}

// errDeleteMismatch reports a DELETE that affected a different number of
// rows than were selected and archived — the database changed between
// selection and deletion (or a key was deleted twice). The transaction is
// rolled back; re-running against the now-current state is safe, hence
// conflict/retryable rather than integrity/final.
func errDeleteMismatch(table string, got, want int64) *domain.Error {
	return &domain.Error{
		Code:      domain.ErrCodeConflict,
		Message:   "retention: delete affected an unexpected number of rows; rolled back",
		Retryable: true,
		Details: map[string]string{
			"table": table,
			"got":   strconv.FormatInt(got, 10),
			"want":  strconv.FormatInt(want, 10),
		},
	}
}
