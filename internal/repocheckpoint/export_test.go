// export_test.go: standard Go "export for test" seam — this file is only
// compiled during `go test` (any _test.go file is excluded from production
// builds), so it exposes writeArtifactDir under a capitalized name for the
// EXTERNAL test package (repocheckpoint_test) without adding anything to
// this package's real, production-facing exported API. Internal
// (white-box) tests in package repocheckpoint itself (e.g.
// atomicwrite_crash_test.go) call the unexported function directly and
// have no need of this shim.
package repocheckpoint

// WriteArtifactDirForTest exposes writeArtifactDir to repocheckpoint_test
// for orphanscan_test.go's concurrent-capture race test, which needs a
// real, live writer racing against ScanOrphanedTempDirs/
// CleanOrphanedTempDirs from outside this package.
func WriteArtifactDirForTest(finalDir string, files map[string][]byte) error {
	return writeArtifactDir(finalDir, files)
}
