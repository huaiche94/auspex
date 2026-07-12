// Package buildinfo holds the minimal build/version metadata needed to back
// the `preflight version` command. Values are hardcoded for the Day-1
// vertical slice; wiring these to real ldflags-injected values (release
// tag, commit SHA, build date) is out of scope for foundation-01.
package buildinfo

// Version is the Preflight release version. It is a fixed development
// placeholder until real release packaging exists (out of scope for
// foundation-01 per agents/foundation.md "Out of scope").
const Version = "0.0.0-dev"

// String returns the human-readable version string printed by
// `preflight version`.
func String() string {
	return Version
}
