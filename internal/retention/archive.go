// archive.go: ADR-046 tier 3 — the gzip JSONL archive writer and its
// independent read-back verifier. One JSON object per raw row, full
// column fidelity, written with the same temp-file → fsync → rename
// discipline internal/repocheckpoint/atomicwrite.go established for
// checkpoint artifact directories (adapted here to a single file): an
// archive either exists complete at its final path or does not exist at
// all; no reader can observe a half-written archive.
package retention

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// archiveTempPrefix is the os.CreateTemp pattern staged archive files are
// created under, in the same directory as their final path (rename across
// filesystems is not atomic, so the temp file must share the target dir —
// same reasoning as atomicwrite.go's tempDirPrefix).
const archiveTempPrefix = ".retention-tmp-*"

// ArchiveFile describes one written-and-verified archive, as reported in
// the run summary and the auspex.gc.v1 output.
type ArchiveFile struct {
	// Path is the final on-disk path of the .jsonl.gz file.
	Path string `json:"path"`
	// Rows is the number of JSONL lines (= raw rows) archived.
	Rows int `json:"rows"`
	// SHA256 is the hex digest of the UNCOMPRESSED JSONL byte stream —
	// the compressed file's bytes depend on gzip implementation details,
	// so the digest is defined over the content, which is what fidelity
	// is about.
	SHA256 string `json:"sha256"`
	// Bytes is the compressed on-disk file size.
	Bytes int64 `json:"bytes"`
}

// encodeArchiveLines renders rows as deterministic JSONL: one
// json.Marshal'd object per row (encoding/json sorts map keys, so the
// encoding is stable for a given row), '\n'-terminated, plus the SHA-256
// digest of the whole uncompressed stream. Encoding happens once, up
// front, so the digest the verifier is checked against is computed from
// exactly the bytes handed to the writer — not recomputed from a second
// serialization that could theoretically diverge. Rows are held in
// memory; retention operates on a local per-user database where the
// expired slice of any table is bounded by the user's own activity, so
// this is a deliberate simplicity/scale trade-off, not an oversight.
func encodeArchiveLines(rows []map[string]any) ([]byte, string, error) {
	var buf bytes.Buffer
	for _, row := range rows {
		line, err := json.Marshal(row)
		if err != nil {
			return nil, "", fmt.Errorf("retention: encoding archive row: %w", err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	digest := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(digest[:]), nil
}

// archivePath builds ADR-046's fixed layout:
// <data-dir>/archive/<table>/<YYYY-MM>/<table>-<UTC timestamp>-<runID>.jsonl.gz.
// The timestamp comes from the injected clock (never time.Now), so tests
// are deterministic; runID disambiguates two runs in the same second.
func archivePath(dataDir, table string, now time.Time, runID string) string {
	utc := now.UTC()
	return filepath.Join(
		dataDir, "archive", table, utc.Format("2006-01"),
		fmt.Sprintf("%s-%s-%s.jsonl.gz", table, utc.Format("20060102T150405Z"), runID),
	)
}

// writeArchiveFile gzip-compresses content to finalPath atomically:
// temp file in the same directory → write → gzip flush → file fsync →
// close → rename → parent-dir fsync (mirroring atomicwrite.go step for
// step). On any failure the temp file is removed and finalPath is left
// nonexistent.
func writeArchiveFile(finalPath string, content []byte) (retErr error) {
	dir := filepath.Dir(finalPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("retention: create archive dir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, archiveTempPrefix)
	if err != nil {
		return fmt.Errorf("retention: create temp archive file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		if retErr != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	gzw := gzip.NewWriter(tmp)
	if _, err := gzw.Write(content); err != nil {
		return fmt.Errorf("retention: write archive content: %w", err)
	}
	if err := gzw.Close(); err != nil {
		return fmt.Errorf("retention: finalize gzip stream: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("retention: fsync archive file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("retention: close archive file: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return fmt.Errorf("retention: rename archive into place: %w", err)
	}
	// Post-rename directory fsync, best-effort exactly as atomicwrite.go
	// documents: the rename itself is already durable per POSIX on the
	// filesystems that matter; the entry fsync is metadata durability.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// verifyArchiveFile re-opens path from disk (not from any in-memory
// copy), decompresses it, and checks both the JSONL line count and the
// SHA-256 of the decompressed stream against what was selected. This is
// ADR-046 step (d): the read-back proof that deleting the raw rows is
// safe. Ordinary I/O errors here abort the run exactly like a write
// failure; a successful read whose count/digest MISMATCHES is a
// state-integrity failure and is reported as such by the caller.
func verifyArchiveFile(path string, wantRows int, wantSHA256 string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("retention: reopen archive for verification: %w", err)
	}
	defer func() { _ = f.Close() }()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("retention: open gzip stream for verification: %w", err)
	}
	defer func() { _ = gzr.Close() }()

	h := sha256.New()
	rows := 0
	// bufio.Reader rather than bufio.Scanner: a single archived row can
	// exceed Scanner's default token limit (payload_json/manifest_json
	// columns are unbounded JSON documents).
	r := bufio.NewReader(io.TeeReader(gzr, h))
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			if line[len(line)-1] != '\n' {
				return fmt.Errorf("retention: archive %s: last line is not newline-terminated", path)
			}
			rows++
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("retention: read archive for verification: %w", err)
		}
	}

	if rows != wantRows {
		return errArchiveMismatch(path, fmt.Sprintf("row count %d, want %d", rows, wantRows))
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSHA256 {
		return errArchiveMismatch(path, fmt.Sprintf("content digest %s, want %s", got, wantSHA256))
	}
	return nil
}
