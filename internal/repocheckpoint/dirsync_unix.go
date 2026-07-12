//go:build !windows

package repocheckpoint

import (
	"errors"
	"syscall"
)

// isDirSyncUnsupported reports whether err is one of the well-known
// "directory fsync not supported on this filesystem/platform" errno
// values, so syncDir can tolerate it without masking a genuine I/O
// failure. This is a narrow, explicit allowlist rather than swallowing
// every error, per the general "degrade explicitly, never silently"
// discipline (Constitution §7 rule 3, applied here to a filesystem
// capability rather than a provider capability).
func isDirSyncUnsupported(err error) bool {
	return errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EINVAL)
}
