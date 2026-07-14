// metadata.go: the runtime metadata file (ADD §23.2) — how clients discover
// a running daemon: its PID, its dynamically-chosen loopback address, and
// where the bearer token lives. Lives in dirs.Runtime (paths.go names
// "daemon socket, pidfile, lockfile" as exactly this directory's purpose),
// written 0600 ("runtime metadata owner-only"), removed on clean shutdown
// so a missing file means "not running (or crashed)" and a present file
// means "check the PID/health endpoint" (§23.3's autostart probe order).
package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SchemaVersionMetadata is the frozen schema identifier for the runtime
// metadata file (ADD §23.2's example payload).
const SchemaVersionMetadata = "auspex.daemon.v1"

// MetadataFileName is the runtime metadata file's fixed basename inside
// the runtime dir. Full path: <runtime>/daemon.json.
const MetadataFileName = "daemon.json"

// Metadata is the auspex.daemon.v1 document, field-for-field ADD §23.2.
type Metadata struct {
	SchemaVersion string    `json:"schema_version"`
	PID           int       `json:"pid"`
	Address       string    `json:"address"`
	TokenFile     string    `json:"token_file"`
	StartedAt     time.Time `json:"started_at"`
	Version       string    `json:"version"`
}

// MetadataPath returns the metadata file's path under runtimeDir.
func MetadataPath(runtimeDir string) string {
	return filepath.Join(runtimeDir, MetadataFileName)
}

// WriteMetadata persists m to runtimeDir/daemon.json (0600, owner-only).
// The write is atomic (temp file + rename) so a §23.3 autostart probe
// racing the write never reads a torn document.
func WriteMetadata(runtimeDir string, m Metadata) error {
	m.SchemaVersion = SchemaVersionMetadata
	raw, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("daemon: marshal metadata: %w", err)
	}
	path := MetadataPath(runtimeDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("daemon: writing metadata %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("daemon: publishing metadata %s: %w", path, err)
	}
	return nil
}

// ReadMetadata loads runtimeDir/daemon.json. found=false (not an error)
// when no daemon has published metadata — the ordinary cold state.
func ReadMetadata(runtimeDir string) (Metadata, bool, error) {
	raw, err := os.ReadFile(MetadataPath(runtimeDir))
	if errors.Is(err, os.ErrNotExist) {
		return Metadata{}, false, nil
	}
	if err != nil {
		return Metadata{}, false, fmt.Errorf("daemon: reading metadata: %w", err)
	}
	var m Metadata
	if err := json.Unmarshal(raw, &m); err != nil {
		return Metadata{}, false, fmt.Errorf("daemon: metadata is not valid JSON: %w", err)
	}
	if m.SchemaVersion != SchemaVersionMetadata {
		return Metadata{}, false, fmt.Errorf("daemon: metadata schema %q, want %q", m.SchemaVersion, SchemaVersionMetadata)
	}
	return m, true, nil
}

// RemoveMetadata deletes the metadata file; idempotent (a missing file is
// already the desired state).
func RemoveMetadata(runtimeDir string) error {
	err := os.Remove(MetadataPath(runtimeDir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("daemon: removing metadata: %w", err)
	}
	return nil
}
