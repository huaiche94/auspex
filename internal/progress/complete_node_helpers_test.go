package progress_test

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huaiche94/preflight/internal/artifacts"
	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/progress"
	"github.com/huaiche94/preflight/internal/statecheckpoint"
	"github.com/huaiche94/preflight/internal/storage/sqlite"
)

// seqIDGenerator is a deterministic, sequential domain.IDGenerator test
// double so assertions can be made about exact generated IDs without
// depending on UUIDv7 randomness. It MUST be concurrency-safe: CompleteNode
// invokes IDGenerator.NewID from whatever goroutine calls Run, and the
// concurrent-completion-race tests (complete_node_race_test.go) call Run
// from many goroutines against the same *CompleteNode/*seqIDGenerator
// instance deliberately, exactly the way the real production
// idgen.UUIDv7 generator (which carries no mutable state at all) is safe
// under concurrent use. An earlier version of this test double used a
// bare `g.n++`, which `go test -race` correctly flagged as a data race —
// not a bug in CompleteNode itself, but in this fixture failing to meet
// the same concurrency contract the real IDGenerator satisfies.
type seqIDGenerator struct {
	prefix string
	n      atomic.Int64
}

func (g *seqIDGenerator) NewID() string {
	return g.prefix + "-" + itoa(int(g.n.Add(1)))
}

func itoa(n int) string {
	// Avoid importing strconv just for this in a test helper file that
	// already imports plenty; simple manual conversion for small n.
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// newDocumentNode creates a Node fixture requiring the given Markdown
// heading + balanced fences as its acceptance criteria, mirroring
// checkpoint-a03's real ADD-derived fixtures' validator kinds.
func newDocumentNode(taskID domain.TaskID, id domain.ProgressNodeID, ordinal int64, status domain.ProgressNodeStatus, heading string) progress.Node {
	return progress.Node{
		ID:      id,
		TaskID:  taskID,
		Ordinal: ordinal,
		Kind:    domain.NodeDocumentSection,
		Title:   "Node " + string(id),
		Status:  status,
		Acceptance: []progress.AcceptanceCriterion{
			{Kind: "heading_exists", Value: heading},
			{Kind: "fence_balance"},
		},
		Version:   1,
		UpdatedAt: time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
	}
}

// writeMarkdownFile writes content to a fresh file under t.TempDir and
// returns its path.
func writeMarkdownFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// validMarkdown is a minimal, real (not placeholder) Markdown document
// with a heading and one balanced fenced code block.
const validMarkdown = `# 20. Graceful Pause and Auto Resume

## Trigger model

Some real prose describing the trigger model.

` + "```yaml" + `
key: value
` + "```" + `

## Safe point

More real prose.
`

// unbalancedFenceMarkdown is validMarkdown with its closing fence removed.
const unbalancedFenceMarkdown = `# 20. Graceful Pause and Auto Resume

## Trigger model

Some real prose describing the trigger model.

` + "```yaml" + `
key: value

## Safe point

More real prose.
`

// missingHeadingMarkdown is validMarkdown with its H1 heading removed.
const missingHeadingMarkdown = `## Trigger model

Some real prose describing the trigger model.

` + "```yaml" + `
key: value
` + "```" + `

## Safe point

More real prose.
`

// newCompleteNodeHarness builds a fully-wired CompleteNode service against
// a fresh temp SQLite DB and a fresh temp evidence directory, plus the
// stores/registries it needs. clock is injected so tests get deterministic
// timestamps; idGen defaults to a real UUIDv7 generator unless a test
// supplies its own for determinism.
func newCompleteNodeHarness(t *testing.T, clock domain.Clock) (*progress.CompleteNode, *sqlite.DB, domain.TaskID) {
	t.Helper()
	db := openTestDB(t)
	taskID := seedTask(t, db)

	evidenceDir := t.TempDir()
	stager, err := progress.NewFileStager(evidenceDir)
	if err != nil {
		t.Fatalf("NewFileStager: %v", err)
	}

	cn := &progress.CompleteNode{
		DB:          db,
		Clock:       clock,
		IDs:         &seqIDGenerator{prefix: "id"},
		Nodes:       progress.NewNodeStore(db, clock),
		Edges:       progress.NewEdgeStore(db),
		Artifacts:   progress.NewArtifactStore(db),
		Validators:  artifacts.NewRegistry(),
		Stager:      stager,
		Checkpoints: statecheckpoint.NewStore(db),
		Publisher:   progress.NoopPublisher{},
	}
	return cn, db, taskID
}

// insertNode is a small helper wrapping NodeStore.Insert with t.Fatalf on
// error, to keep test bodies focused on the scenario under test.
func insertNode(t *testing.T, db *sqlite.DB, clock domain.Clock, n progress.Node) {
	t.Helper()
	store := progress.NewNodeStore(db, clock)
	ctx := context.Background()
	if err := store.Insert(ctx, n); err != nil {
		t.Fatalf("insert node %s: %v", n.ID, err)
	}
}

// moveNodeToInProgress drives a freshly-inserted (pending) node through
// ready -> in_progress, the state the real caller (StartNode) would have
// already produced before ever calling CompleteNode.
func moveNodeToInProgress(t *testing.T, db *sqlite.DB, clock domain.Clock, id domain.ProgressNodeID) {
	t.Helper()
	store := progress.NewNodeStore(db, clock)
	ctx := context.Background()
	if err := store.TransitionStatus(ctx, id, domain.NodePending, domain.NodeReady, 1); err != nil {
		t.Fatalf("transition to ready: %v", err)
	}
	if err := store.TransitionStatus(ctx, id, domain.NodeReady, domain.NodeInProgress, 2); err != nil {
		t.Fatalf("transition to in_progress: %v", err)
	}
}

// fileArtifactRef builds a domain.ArtifactRef pointing at path with no
// claimed checksum (FileStager computes and fills in the real one) — this
// is the common case tests use since the whole point of staging is that
// Preflight computes the checksum itself, not trust the caller's claim.
func fileArtifactRef(id string, path string) domain.ArtifactRef {
	return domain.ArtifactRef{
		ID:        id,
		Kind:      "file",
		URI:       "file:" + path,
		MediaType: "text/markdown",
	}
}
