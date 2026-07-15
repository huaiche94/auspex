// evaluate_privacy_test.go: issue #14 deliverable 6's privacy gate for
// `auspex evaluate` — the one CLI command that genuinely ingests RAW
// prompt text (--prompt-file/stdin). It drives the REAL command (cli.
// NewEvaluateCmd) over the REAL production stack — evaluation.Service +
// SQLDataSource + the real predictor stages against a real on-disk (not
// :memory:) SQLite file, events persisted through the real
// claudetelemetry.EventStore — then scans the resulting on-disk DB bytes
// (main file + WAL sidecars) for the canary prompt, reusing this
// package's own qa-05 leakage-scanner helpers (scanBytesForNeedles,
// sqliteArtifactPaths, fixedClock, seqIDs — same package, same corpus
// technique).
//
// Falsifiability, per qa-05's own negative-control discipline: the test
// also asserts the canary's SHA-256 HASH *is* present in the DB — proof
// the evaluation really ran end-to-end and persisted the prompt-derived
// event/prediction rows, so the canary's absence is a real privacy
// result, not a vacuous pass over an empty database.
package integrationtest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/huaiche94/auspex/internal/cli"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/evaluation"
	"github.com/huaiche94/auspex/internal/features"
	"github.com/huaiche94/auspex/internal/orchestrator"
	"github.com/huaiche94/auspex/internal/policy"
	"github.com/huaiche94/auspex/internal/predictor/quota"
	"github.com/huaiche94/auspex/internal/predictor/risk"
	"github.com/huaiche94/auspex/internal/predictor/scope"
	"github.com/huaiche94/auspex/internal/predictor/token"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	claudetelemetry "github.com/huaiche94/auspex/internal/telemetry/claude"
)

// evaluateCanaryPrompt is a distinctive raw-prompt stand-in, unmistakable
// in a byte scan (this package's established canary technique).
const evaluateCanaryPrompt = "EVALUATE-CLI-RAW-PROMPT-CANARY: please refactor internal/evaluation/service.go to stream prediction rows."

// tokenSourceBridge adapts *evaluation.SQLDataSource to
// internal/predictor/token.FeatureSource — the same narrow adapter
// cmd/auspex/adapters.go's tokenFeatureSourceAdapter implements
// (unexported to package main, so this test declares its own
// matching-shape bridge, this repo's established cross-package test
// convention).
type tokenSourceBridge struct {
	source *evaluation.SQLDataSource
}

func (a tokenSourceBridge) Classification(ctx context.Context, sessionID domain.SessionID) (features.Classification, features.PromptFeatures, error) {
	resolved, err := a.source.Resolve(ctx, sessionID)
	if err != nil {
		return features.Classification{}, features.PromptFeatures{}, err
	}
	return a.source.Classification(ctx, sessionID, resolved.TaskID)
}

func (a tokenSourceBridge) Session(ctx context.Context, sessionID domain.SessionID) (features.SessionFeatures, bool, error) {
	return a.source.Session(ctx, sessionID)
}

func (a tokenSourceBridge) Progress(ctx context.Context, sessionID domain.SessionID) (features.ProgressFeatures, bool, error) {
	resolved, err := a.source.Resolve(ctx, sessionID)
	if err != nil {
		return features.ProgressFeatures{}, false, err
	}
	return a.source.Progress(ctx, resolved.TaskID)
}

func (a tokenSourceBridge) RecentSimilarTurnTokens(ctx context.Context, sessionID domain.SessionID, class features.TaskClass) (features.SimilarTurnTokens, error) {
	return a.source.RecentSimilarTurnTokens(ctx, sessionID, class)
}

// buildEvaluateCLIRoot assembles the production-shaped stack: migrated
// on-disk DB, seeded foundation rows (repositories/worktrees/
// provider_sessions — SQLDataSource.Resolve requires a registered
// session, exactly as in the real binary), the real evaluation.Service
// wired precisely the way cmd/auspex/wire.go wires it, and the real
// `evaluate` command over orchestrator.HookDeps with the real
// EventStore/Forecast source.
func buildEvaluateCLIRoot(t *testing.T) (root *cobra.Command, dbPath string, db *sqlite.DB) {
	t.Helper()

	dir := t.TempDir()
	dbPath = filepath.Join(dir, "evaluate-privacy.db")
	ctx := context.Background()

	db, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}
	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	seed := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO repositories (id, canonical_root, git_common_dir, created_at, last_seen_at) VALUES (?, ?, ?, ?, ?)`,
			[]any{"repo-1", "/tmp/repo", "/tmp/repo/.git", now, now}},
		{`INSERT INTO worktrees (id, repository_id, root_path, git_dir, created_at, last_seen_at) VALUES (?, ?, ?, ?, ?, ?)`,
			[]any{"wt-1", "repo-1", "/tmp/repo", "/tmp/repo/.git", now, now}},
		{`INSERT INTO provider_sessions (id, worktree_id, provider, invocation_mode, started_at) VALUES (?, ?, ?, ?, ?)`,
			[]any{"sess-privacy", "wt-1", "claude", "hook", now}},
	}
	for _, s := range seed {
		if _, err := db.Conn().ExecContext(ctx, s.q, s.args...); err != nil {
			t.Fatalf("seed %q: %v", s.q, err)
		}
	}

	clock := fixedClock{t: time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)}
	ids := &seqIDs{}
	dataSource := evaluation.NewSQLDataSource(db)
	svc := evaluation.New(
		db,
		dataSource,
		scope.NewRuleScopeEstimator(dataSource),
		token.NewRuleTokenForecaster(tokenSourceBridge{source: dataSource}),
		quota.NewRuleQuotaForecaster(),
		risk.NewRuleRiskCombiner(),
		policy.NewDecider(),
		clock,
		ids,
	)

	deps := orchestrator.HookDeps{
		Clock:      clock,
		IDs:        ids,
		Persister:  claudetelemetry.NewEventStore(db),
		TxRunner:   db,
		Evaluation: svc,
		Forecast:   svc,
	}

	root = &cobra.Command{Use: "auspex", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(cli.NewEvaluateCmd(deps))
	return cli.WithJSONErrorRendering(root), dbPath, db
}

// TestLeakageScanner_EvaluateCLI_NoRawPromptInDBExport is the end-to-end
// privacy proof: a real `auspex evaluate --prompt-file -` run over the
// full production stack leaves ZERO bytes of the raw prompt in the
// on-disk database (or its WAL sidecars) and in the command's own
// output, while the prompt's SHA-256 hash IS durably present (positive
// control: the evaluation genuinely ran and persisted).
func TestLeakageScanner_EvaluateCLI_NoRawPromptInDBExport(t *testing.T) {
	root, dbPath, db := buildEvaluateCLIRoot(t)
	ctx := context.Background()

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(evaluateCanaryPrompt))
	root.SetArgs([]string{"evaluate", "--session-id", "sess-privacy", "--prompt-file", "-", "--json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("auspex evaluate over the real stack: %v (output: %s)", err, out.String())
	}
	if !bytes.Contains(out.Bytes(), []byte(`"schema_version":"auspex.evaluate.v1"`)) {
		t.Fatalf("expected schema-versioned JSON output, got: %s", out.Bytes())
	}
	if bytes.Contains(out.Bytes(), []byte(evaluateCanaryPrompt)) {
		t.Fatalf("raw prompt canary leaked into evaluate's own output:\n%s", out.Bytes())
	}

	// Positive control BEFORE scanning: the evaluation persisted a
	// prediction row and the prompt-hash-bearing turn.started event.
	sum := sha256.Sum256([]byte(evaluateCanaryPrompt))
	wantHash := hex.EncodeToString(sum[:])
	var predictionCount, eventCount int
	if err := db.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM predictions`).Scan(&predictionCount); err != nil {
		t.Fatalf("count predictions: %v", err)
	}
	if err := db.Conn().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM events WHERE event_type = 'provider.turn.started' AND payload_json LIKE ?`,
		"%"+wantHash+"%",
	).Scan(&eventCount); err != nil {
		t.Fatalf("count hash-bearing events: %v", err)
	}
	if predictionCount == 0 || eventCount == 0 {
		t.Fatalf("positive control failed: predictions=%d hash-bearing events=%d — the evaluation did not persist, so a canary-absence scan would be vacuous", predictionCount, eventCount)
	}

	// Issue #42 payload pin: the persisted turn.started payload now also
	// carries the derived prompt-feature fields — assert, on the REAL
	// stored row, that (a) they are present (the canary contains
	// "refactor", so has_refactor_verb must be a measured true), and (b)
	// every payload value is a bool or a number except the two known
	// strings (prompt_sha256's fixed-alphabet digest; cwd is absent on
	// the evaluate path). Strings are the only JSON type that can carry
	// raw prompt text, so this structural check is the payload-level
	// mirror of the byte scan below (Constitution §7 rule 2: booleans,
	// counts, and hashes only).
	var payloadJSON string
	if err := db.Conn().QueryRowContext(ctx,
		`SELECT payload_json FROM events WHERE event_type = 'provider.turn.started' ORDER BY rowid DESC LIMIT 1`,
	).Scan(&payloadJSON); err != nil {
		t.Fatalf("read persisted turn.started payload: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatalf("decode persisted turn.started payload: %v", err)
	}
	if payload["has_refactor_verb"] != true {
		t.Errorf("has_refactor_verb = %v, want true — the canary prompt says 'refactor', so the derived feature must have been measured and persisted", payload["has_refactor_verb"])
	}
	// #50 item 2: the extraction-era tag is stamped on a genuinely-extracted
	// event, with the exact features-owned constant value.
	if v, ok := features.PromptFeatureVersionFromPayload(payload); !ok || v != features.PromptFeatureVersion {
		t.Errorf("prompt_feature_version = %q present=%v, want %q — extracted events must carry the era tag", v, ok, features.PromptFeatureVersion)
	}
	for k, v := range payload {
		s, ok := v.(string)
		if !ok {
			continue
		}
		switch k {
		case "prompt_sha256", "cwd":
			// The fixed-alphabet SHA-256 digest and the CWD path (a path, not
			// prompt content) are the only free-form strings allowed here.
		case features.PromptFeatureVersionKey:
			// #50: the extraction-era tag is a fixed compile-time constant,
			// provably independent of any prompt's content — exactly as safe
			// as prompt_sha256. Pinning it to the constant keeps this a
			// STRENGTHENING of the string allow-list, not a widening: a
			// version field that ever held prompt-derived text would fail.
			if s != features.PromptFeatureVersion {
				t.Errorf("prompt_feature_version = %q, want the fixed constant %q — a version string must never carry prompt-derived data", s, features.PromptFeatureVersion)
			}
		default:
			t.Errorf("persisted payload field %q is a string (%q) — only prompt_sha256, cwd, and the fixed prompt_feature_version constant may be strings on turn.started (Constitution §7 rule 2)", k, s)
		}
	}

	// Flush the WAL into the main file, close, and scan raw bytes — the
	// same operator's-export posture buildLeakageScannerDB documents.
	if _, err := db.Conn().ExecContext(ctx, "PRAGMA wal_checkpoint(FULL)"); err != nil {
		t.Fatalf("wal_checkpoint: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	needles := []promptNeedle{{needle: evaluateCanaryPrompt, label: "evaluate CLI prompt"}}
	report := &scanReport{}
	sawHash := false
	for _, path := range sqliteArtifactPaths(t, dbPath) {
		raw, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) && path != dbPath {
				continue
			}
			t.Fatalf("reading %s: %v", path, err)
		}
		scanBytesForNeedles(report, path, raw, needles)
		if bytes.Contains(raw, []byte(wantHash)) {
			sawHash = true
		}
	}
	if len(report.hits) > 0 {
		t.Fatalf("raw prompt text from `auspex evaluate` leaked into the SQLite export:\n%s", strings.Join(report.hits, "\n"))
	}
	if !sawHash {
		t.Fatal("falsifiability check failed: the prompt's SHA-256 hash is not in the DB export bytes — the scan may not be reading the data the pipeline wrote")
	}
}
