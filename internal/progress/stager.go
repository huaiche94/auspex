// stager.go: the default ArtifactStager. ADD §18.7 describes two artifact
// storage modes: Preflight writes the artifact itself (temp+fsync+rename),
// or — the day-one common case per §18.6/§18.7's "若 artifact 是 agent 已
// 直接寫入 repo" branch — the agent already wrote the file directly into
// the repository, and Preflight's job is to read its checksum and treat it
// as evidence without rewriting it.
//
// FileStager implements the second mode: it never mutates the artifact's
// own content (an agent's file is not Preflight's to overwrite), and its
// "staging" is instead a durable, content-addressed COPY into this
// package's own evidence directory (fsync + atomic rename, matching
// internal/repocheckpoint's atomic-write discipline) so later verification
// and reconciliation have a stable, Preflight-owned copy to check even if
// the agent's own working copy is later changed or deleted. This is what
// makes "missing or changed artifact" (Constitution §6 "must reject" list)
// detectable at CompleteNode/reconciliation time rather than only at the
// instant of staging.
package progress

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/huaiche94/preflight/internal/domain"
)

// FileStager stages file-kind artifacts into EvidenceDir, a directory this
// package owns exclusively (never the agent's working tree).
type FileStager struct {
	EvidenceDir string
}

// NewFileStager constructs a FileStager rooted at dir, creating dir if it
// does not already exist.
func NewFileStager(dir string) (*FileStager, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("progress: create evidence dir %s: %w", dir, err)
	}
	return &FileStager{EvidenceDir: dir}, nil
}

// sourcePath extracts the filesystem path from an artifact URI. This
// package's own convention (matching ADD Appendix A/B's `file:<path>`
// examples) is a bare "file:" prefix with no "//" authority component —
// the remainder is a path, absolute or relative to the caller's working
// directory.
func sourcePath(uri string) (string, error) {
	const prefix = "file:"
	if !strings.HasPrefix(uri, prefix) {
		return "", &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   fmt.Sprintf("progress: unsupported artifact URI scheme in %q (only file: is supported)", uri),
			Retryable: false,
		}
	}
	path := strings.TrimPrefix(uri, prefix)
	if path == "" {
		return "", &domain.Error{
			Code:      domain.ErrCodeValidation,
			Message:   fmt.Sprintf("progress: artifact URI %q has an empty path", uri),
			Retryable: false,
		}
	}
	return path, nil
}

// Stage reads the artifact's source file, computes its actual SHA-256 (the
// caller-supplied ref.SHA256, if any, is untrusted input — "agent says
// complete" is exactly the claim this protocol must not take on faith),
// and copies it into EvidenceDir under a content-addressed name
// (sha256/<hex>) via temp-write + fsync + atomic rename, so concurrent or
// repeated staging of identical content is naturally idempotent: the
// destination path is a pure function of the content, so re-staging the
// same bytes is a harmless no-op rename-over-self-shaped path, not a
// duplicate-write race.
func (s *FileStager) Stage(ctx context.Context, nodeID domain.ProgressNodeID, ref domain.ArtifactRef) (StagedArtifact, error) {
	path, err := sourcePath(ref.URI)
	if err != nil {
		return StagedArtifact{}, err
	}

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return StagedArtifact{}, &domain.Error{
				Code:      domain.ErrCodeValidation,
				Message:   fmt.Sprintf("progress: artifact file %s does not exist", path),
				Retryable: false,
				Details:   map[string]string{"node_id": string(nodeID), "uri": ref.URI},
			}
		}
		return StagedArtifact{}, fmt.Errorf("progress: open artifact %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return StagedArtifact{}, fmt.Errorf("progress: read artifact %s: %w", path, err)
	}
	actualSHA := hex.EncodeToString(h.Sum(nil))

	if ref.SHA256 != "" && !strings.EqualFold(ref.SHA256, actualSHA) {
		return StagedArtifact{}, &domain.Error{
			Code:      domain.ErrCodeIntegrity,
			Message:   fmt.Sprintf("progress: artifact %s checksum mismatch: claimed %s, actual %s", path, ref.SHA256, actualSHA),
			Retryable: false,
			Details:   map[string]string{"node_id": string(nodeID), "uri": ref.URI},
		}
	}

	dest := filepath.Join(s.EvidenceDir, "sha256", actualSHA[:2], actualSHA)
	if _, statErr := os.Stat(dest); statErr != nil {
		if !os.IsNotExist(statErr) {
			return StagedArtifact{}, fmt.Errorf("progress: stat evidence copy %s: %w", dest, statErr)
		}
		if err := copyFileAtomic(path, dest); err != nil {
			return StagedArtifact{}, fmt.Errorf("progress: stage evidence copy of %s: %w", path, err)
		}
	}
	// If dest already exists, its name IS its content digest, so no
	// re-verification of dest's own bytes is needed here — content
	// addressing makes a collision under a different actual content
	// astronomically unlikely and, in any case, out of scope for this
	// protocol to detect (that would be a SHA-256 break, not a Preflight
	// bug).

	out := ref
	out.SHA256 = actualSHA
	out.Bytes = size
	return StagedArtifact{Ref: out, Path: path}, nil
}

// copyFileAtomic copies src to dest via a temp file in dest's directory,
// fsync, then atomic rename — the same discipline
// internal/repocheckpoint/atomicwrite.go uses, reimplemented narrowly here
// for a single file rather than a whole directory tree (this package does
// not import internal/repocheckpoint: Part A does not reach into Part B's
// implementation, per agents/checkpoint.md's cross-part boundary note,
// even for a shared-looking utility).
func copyFileAtomic(src, dest string) (err error) {
	if mkErr := os.MkdirAll(filepath.Dir(dest), 0o755); mkErr != nil {
		return mkErr
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".stage-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpPath)
		}
	}()

	in, err := os.Open(src)
	if err != nil {
		_ = tmp.Close()
		return err
	}
	defer func() { _ = in.Close() }()

	if _, err = io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmpPath, dest); err != nil {
		return err
	}
	return nil
}
