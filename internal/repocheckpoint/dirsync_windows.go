//go:build windows

package repocheckpoint

// isDirSyncUnsupported reports whether err is a directory-fsync-unsupported
// error. Windows' os.(*File).Sync on a directory handle reliably returns an
// error (directories cannot be flushed the POSIX way on NTFS/ReFS), so this
// platform variant treats every error as "unsupported, tolerate it" — the
// per-file fsyncs in atomicwrite.go already provide the durability
// guarantee that matters; the directory-entry fsync is best-effort
// everywhere, and known-unavailable here.
func isDirSyncUnsupported(error) bool {
	return true
}
