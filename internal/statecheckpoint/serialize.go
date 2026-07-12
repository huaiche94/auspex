// serialize.go: deterministic JSON serialization and integrity checksum
// for a Manifest (agents/checkpoint.md Part A deliverable #5's "manifest
// serialization and checksum"). ADD §18.7 step 7's transaction only
// commits a node as `completed` alongside a checkpoint row that itself
// carries integrity_sha256 (§18.8) — a manifest whose checksum cannot be
// independently recomputed and re-verified later (LoadLatest/Verify,
// checkpoint-a04 deliverable #8) would defeat the point of durable
// evidence, so this file is exercised by every single CompleteNode call,
// not just an optional extra.
package statecheckpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Digest computes the manifest's integrity checksum: SHA-256 over the
// canonical JSON encoding of every field EXCEPT IntegritySHA256 itself
// (which is the checksum's own output — including it would make the
// digest depend on itself). Go's encoding/json marshals struct fields in
// their declared order deterministically (not map iteration order), and
// Manifest declares IntegritySHA256 last, so zeroing it before marshaling
// and marshaling the same struct type on both sides (Digest here, and
// Verify's recomputation) is sufficient for a stable, reproducible digest
// — no separate canonicalization pass is needed for a fixed Go struct
// shape with no maps in it (every nested type above is a fixed-shape
// struct or slice of one, never map[string]any).
func Digest(m Manifest) (string, error) {
	m.IntegritySHA256 = ""
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("statecheckpoint: marshal manifest for digest: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Seal computes m's Digest and returns a copy of m with IntegritySHA256
// populated. It never mutates its argument.
func Seal(m Manifest) (Manifest, error) {
	digest, err := Digest(m)
	if err != nil {
		return Manifest{}, err
	}
	m.IntegritySHA256 = digest
	return m, nil
}

// Marshal serializes a sealed manifest to its canonical wire JSON form
// (the same encoding Digest hashed, but WITH IntegritySHA256 populated —
// this is the document actually written to durable storage / manifest_json,
// per Appendix B's example, which includes integrity_sha256 in the JSON
// itself).
func Marshal(m Manifest) ([]byte, error) {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("statecheckpoint: marshal manifest: %w", err)
	}
	return b, nil
}

// Unmarshal parses a manifest previously produced by Marshal.
func Unmarshal(b []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, fmt.Errorf("statecheckpoint: unmarshal manifest: %w", err)
	}
	return m, nil
}

// Verify recomputes m's digest from its own content and reports whether it
// matches m.IntegritySHA256. This is the same check CompleteNode's
// reconciliation path and StateCheckpointService.Verify both need: never
// trust a stored checksum string alone without recomputing it from the
// actual bytes (same "verify.go never trusts the DB row alone" discipline
// checkpoint-b04 already established for Repository Checkpoints).
func Verify(m Manifest) (bool, error) {
	want := m.IntegritySHA256
	got, err := Digest(m)
	if err != nil {
		return false, err
	}
	return got == want, nil
}
