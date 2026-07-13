// Package retention implements ADR-046's tiered telemetry retention: a
// hot raw window (default 90 days, policy.go), rollup summary tables
// (rollup.go, migration 0060), a gzip JSONL archive with full column
// fidelity (archive.go), and only-after-archive-verified deletion — the
// engine behind `auspex gc`.
//
// The pass is strictly fail-closed, in this order:
//
//	(a) select expired rows per table class (collect.go, read-only);
//	(b/c) encode + write each class's archive file atomically
//	      (temp+fsync+rename, mirroring repocheckpoint/atomicwrite.go);
//	(d) re-open and re-read every archive from disk, verifying row count
//	    and content digest against what was selected;
//	(e) ONLY THEN delete — all classes' rows AND both rollups in one
//	    app.TxRunner.WithTx transaction, with affected-row counts checked
//	    against the selected sets (a mismatch rolls everything back);
//	(f) record the run in retention_runs.
//
// Any failure before (e) leaves every raw row untouched and records a
// failed run. There is no partial-delete state, and a dry run performs
// (a) only — no archive files, no rollup writes, no retention_runs row
// (ADR-046 "Dry run").
//
// Dependencies are the frozen domain.Clock/domain.IDGenerator ports plus
// *sqlite.DB (which is the app.TxRunner) — never time.Now()/rand
// directly, so every test in this package is deterministic.
package retention

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// Engine runs retention passes. All fields are required (Run validates).
type Engine struct {
	// DB is both the query surface and the app.TxRunner transaction
	// boundary for the delete phase.
	DB *sqlite.DB
	// Clock/IDs are the frozen determinism ports (internal/domain/clock.go).
	Clock domain.Clock
	IDs   domain.IDGenerator
	// DataDir is Auspex's user data directory (paths.ResolveHost().Data);
	// archives land under <DataDir>/archive/... and checkpoint artifact
	// directories are only ever removed when they resolve inside it.
	DataDir string
}

// RunRequest configures one pass.
type RunRequest struct {
	Policy Policy
	// DryRun selects and reports without any side effect at all.
	DryRun bool
	// Vacuum runs a full VACUUM after a deleting pass. Opt-in because
	// today's databases run auto_vacuum=NONE (db.go sets no auto_vacuum
	// pragma), where VACUUM — a whole-file rewrite under an exclusive
	// lock — is the only way to return freelist pages to the OS
	// (ADR-046 "Space reclamation").
	Vacuum bool
}

// TableResult is one table's per-pass accounting, as recorded in
// retention_runs.summary_json and rendered by `auspex gc`.
type TableResult struct {
	Table    string       `json:"table"`
	Selected int          `json:"selected"`
	Deleted  int          `json:"deleted"`
	Archive  *ArchiveFile `json:"archive,omitempty"`
}

// RunResult is a pass's full outcome.
type RunResult struct {
	RunID         string
	RanAt         time.Time
	RetentionDays int
	Cutoff        time.Time
	DryRun        bool
	// Status is "ok" or "failed" — mirroring retention_runs.status.
	Status string
	Tables []TableResult

	UsageRollupRows              int
	CalibrationSamples           int
	CalibrationSamplesWithActual int

	// Notes carries every conservative skip/exclusion, so "we did not GC
	// task X and here is why" is reported rather than silent (ADR-046).
	Notes []string

	// ReclaimableBytesEstimate is freelist_count*page_size measured after
	// the delete transaction (before any vacuum): with auto_vacuum=NONE
	// this is space the file holds but SQLite can reuse — it is NOT bytes
	// returned to the OS unless VacuumRan is true.
	ReclaimableBytesEstimate int64
	// AutoVacuumMode is the database's actual PRAGMA auto_vacuum mode
	// ("none"/"full"/"incremental"), read at runtime rather than assumed.
	AutoVacuumMode string
	VacuumRan      bool
}

const (
	runStatusOK     = "ok"
	runStatusFailed = "failed"
)

// runSummary is retention_runs.summary_json's shape.
type runSummary struct {
	Tables                       []TableResult `json:"tables"`
	UsageRollupRows              int           `json:"usage_rollup_rows"`
	CalibrationSamples           int           `json:"calibration_samples"`
	CalibrationSamplesWithActual int           `json:"calibration_samples_with_actual"`
	Notes                        []string      `json:"notes,omitempty"`
}

// Run executes one retention pass per the package doc's (a)-(f) protocol.
// On failure it returns the frozen domain.Error shape and — for non-dry
// runs — best-effort records the failed pass in retention_runs.
func (e *Engine) Run(ctx context.Context, req RunRequest) (RunResult, error) {
	if err := e.validate(req); err != nil {
		return RunResult{}, err
	}

	now := e.Clock.Now().UTC()
	res := RunResult{
		RunID:         e.IDs.NewID(),
		RanAt:         now,
		RetentionDays: req.Policy.Days(),
		Cutoff:        req.Policy.Cutoff(now),
		DryRun:        req.DryRun,
		Status:        runStatusOK,
	}

	// (a) Selection — read-only.
	p, err := e.collect(ctx, res.Cutoff)
	if err != nil {
		return e.fail(ctx, res, err)
	}
	res.Notes = p.notes
	res.UsageRollupRows = len(p.usage)
	res.CalibrationSamples = len(p.samples)
	for _, s := range p.samples {
		if s.actualKnown {
			res.CalibrationSamplesWithActual++
		}
	}
	byTable := make(map[string]*tableBatch, len(p.batches))
	for _, b := range p.batches {
		byTable[b.table] = b
	}
	for _, table := range deletionTables {
		res.Tables = append(res.Tables, TableResult{Table: table, Selected: len(byTable[table].rows)})
	}

	if req.DryRun {
		// Truly side-effect-free (ADR-046): report what WOULD happen —
		// selected counts, would-be rollups — and stop. Space stats are
		// still read (reads only) so the output shape matches a real run.
		e.readSpaceStats(ctx, &res)
		return res, nil
	}

	// (b/c/d) Archive + independent read-back verification, per table.
	for i, table := range deletionTables {
		b := byTable[table]
		if len(b.rows) == 0 {
			continue
		}
		content, digest, err := encodeArchiveLines(b.rows)
		if err != nil {
			return e.fail(ctx, res, err)
		}
		path := archivePath(e.DataDir, table, now, res.RunID)
		if err := writeArchiveFile(path, content); err != nil {
			return e.fail(ctx, res, errUnavailable("writing archive for "+table, err))
		}
		if err := verifyArchiveFile(path, len(b.rows), digest); err != nil {
			return e.fail(ctx, res, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return e.fail(ctx, res, errUnavailable("stat verified archive for "+table, err))
		}
		res.Tables[i].Archive = &ArchiveFile{Path: path, Rows: len(b.rows), SHA256: digest, Bytes: info.Size()}
	}

	// (e) Rollups + deletes, one transaction, count-checked.
	err = e.DB.WithTx(ctx, func(txCtx context.Context) error {
		if err := upsertUsageRollups(txCtx, e.DB, p.usage); err != nil {
			return err
		}
		if err := insertCalibrationSamples(txCtx, e.DB, p.samples, res.RunID, formatTime(now)); err != nil {
			return err
		}
		for _, b := range p.batches {
			if err := e.deleteBatch(txCtx, b); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return e.fail(ctx, res, err)
	}
	for i := range res.Tables {
		res.Tables[i].Deleted = res.Tables[i].Selected
	}

	// (f) Durable audit row. A failure HERE is still a failed run even
	// though the deletes committed: the archives exist and nothing is
	// lost, but the pass lacks its evidence row (Constitution §6), so it
	// must not report success.
	if err := e.recordRun(ctx, res, runStatusOK, nil); err != nil {
		res.Status = runStatusFailed
		return res, errUnavailable("recording retention run (deletes committed, archives on disk)", err)
	}

	// Post-commit, best-effort: artifact directories of deleted
	// repository checkpoints. An orphaned directory is safe (noted); a
	// removed directory behind a surviving row would not be — hence
	// strictly after commit, and only inside the data dir (ADR-046).
	for _, dir := range p.artifactDirs {
		if !artifactDirInsideDataDir(dir, e.DataDir) {
			res.Notes = append(res.Notes, "checkpoints: left artifact dir outside data dir in place: "+dir)
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			res.Notes = append(res.Notes, "checkpoints: failed to remove artifact dir "+dir+": "+err.Error())
		}
	}

	e.reclaimSpace(ctx, &res, req.Vacuum, anyDeleted(res.Tables))
	return res, nil
}

// validate fails closed on a broken composition or request before
// touching anything.
func (e *Engine) validate(req RunRequest) error {
	switch {
	case e.DB == nil:
		return errValidation("Engine.DB is required")
	case e.Clock == nil:
		return errValidation("Engine.Clock is required")
	case e.IDs == nil:
		return errValidation("Engine.IDs is required")
	case e.DataDir == "":
		return errValidation("Engine.DataDir is required")
	case req.Policy.RetentionDays < 0:
		return errValidation("RetentionDays must be positive")
	}
	return nil
}

// fail marks the run failed, best-effort records it (non-dry-run only —
// a dry run writes nothing even on failure), and returns the original
// error, joined with the recording error if that also failed.
func (e *Engine) fail(ctx context.Context, res RunResult, err error) (RunResult, error) {
	res.Status = runStatusFailed
	if !res.DryRun {
		if recErr := e.recordRun(ctx, res, runStatusFailed, err); recErr != nil {
			err = errors.Join(err, recErr)
		}
	}
	return res, err
}

// deleteBatch deletes b's keys in chunks inside the caller's transaction
// and verifies the total affected-row count equals the selected set — a
// mismatch means the table changed between selection and deletion, and
// the whole transaction rolls back (errDeleteMismatch).
func (e *Engine) deleteBatch(txCtx context.Context, b *tableBatch) error {
	if len(b.keys) == 0 {
		return nil
	}
	q := sqlite.QuerierFromContext(txCtx, e.DB)
	var affected int64
	for _, chunk := range chunkKeys(b.keys) {
		query := "DELETE FROM " + b.table + " WHERE " + b.keyColumn + " IN (" + placeholders(len(chunk)) + ")"
		result, err := q.ExecContext(txCtx, query, chunk...)
		if err != nil {
			return fmt.Errorf("retention: deleting from %s: %w", b.table, err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("retention: reading rows affected for %s: %w", b.table, err)
		}
		affected += n
	}
	if affected != int64(len(b.keys)) {
		return errDeleteMismatch(b.table, affected, int64(len(b.keys)))
	}
	return nil
}

// recordRun writes the retention_runs audit row (ADR-046 step (f)).
func (e *Engine) recordRun(ctx context.Context, res RunResult, status string, runErr error) error {
	summary, err := json.Marshal(runSummary{
		Tables:                       res.Tables,
		UsageRollupRows:              res.UsageRollupRows,
		CalibrationSamples:           res.CalibrationSamples,
		CalibrationSamplesWithActual: res.CalibrationSamplesWithActual,
		Notes:                        res.Notes,
	})
	if err != nil {
		return fmt.Errorf("retention: encoding run summary: %w", err)
	}
	var errText any
	if runErr != nil {
		errText = runErr.Error()
	}
	return e.DB.WithTx(ctx, func(txCtx context.Context) error {
		q := sqlite.QuerierFromContext(txCtx, e.DB)
		_, err := q.ExecContext(txCtx, `
			INSERT INTO retention_runs (id, ran_at, retention_days, dry_run, status, summary_json, error)
			VALUES (?, ?, ?, 0, ?, ?, ?)
		`, res.RunID, formatTime(res.RanAt), res.RetentionDays, status, string(summary), errText)
		if err != nil {
			return fmt.Errorf("retention: inserting retention_runs row: %w", err)
		}
		return nil
	})
}

// reclaimSpace implements ADR-046 "Space reclamation": read the ACTUAL
// auto_vacuum mode, run incremental_vacuum where the database supports it,
// run a full VACUUM only when explicitly requested, and report the
// freelist estimate honestly. Failures here degrade to notes rather than
// failing an otherwise-complete pass — the retention work is already
// durably done and audited; space accounting is diagnostics.
func (e *Engine) reclaimSpace(ctx context.Context, res *RunResult, vacuum, deletedAny bool) {
	e.readSpaceStats(ctx, res)

	if deletedAny && res.AutoVacuumMode == "incremental" {
		if _, err := e.DB.Conn().ExecContext(ctx, "PRAGMA incremental_vacuum"); err != nil {
			res.Notes = append(res.Notes, "space: incremental_vacuum failed: "+err.Error())
		} else {
			res.Notes = append(res.Notes, "space: incremental_vacuum ran (auto_vacuum=incremental)")
		}
	}
	if vacuum {
		if _, err := e.DB.Conn().ExecContext(ctx, "VACUUM"); err != nil {
			res.Notes = append(res.Notes, "space: VACUUM failed: "+err.Error())
		} else {
			res.VacuumRan = true
		}
	}
}

// readSpaceStats fills AutoVacuumMode and ReclaimableBytesEstimate from
// the live database (reads only). Best-effort by the same reasoning as
// reclaimSpace.
func (e *Engine) readSpaceStats(ctx context.Context, res *RunResult) {
	res.AutoVacuumMode = e.autoVacuumMode(ctx)

	var freelist, pageSize int64
	if err := e.DB.Conn().QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&freelist); err != nil {
		res.Notes = append(res.Notes, "space: reading freelist_count failed: "+err.Error())
		return
	}
	if err := e.DB.Conn().QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		res.Notes = append(res.Notes, "space: reading page_size failed: "+err.Error())
		return
	}
	res.ReclaimableBytesEstimate = freelist * pageSize
}

// autoVacuumMode maps PRAGMA auto_vacuum's 0/1/2 to its documented names.
func (e *Engine) autoVacuumMode(ctx context.Context) string {
	var mode sql.NullInt64
	if err := e.DB.Conn().QueryRowContext(ctx, "PRAGMA auto_vacuum").Scan(&mode); err != nil || !mode.Valid {
		return "unknown"
	}
	switch mode.Int64 {
	case 1:
		return "full"
	case 2:
		return "incremental"
	default:
		return "none"
	}
}

// anyDeleted reports whether the pass deleted at least one row.
func anyDeleted(tables []TableResult) bool {
	for _, t := range tables {
		if t.Deleted > 0 {
			return true
		}
	}
	return false
}

// formatTime is this package's fixed TEXT timestamp form — RFC3339Nano
// UTC, matching internal/telemetry/claude/store.go and every sibling
// store, so retention's own rows sort and parse identically to the rows
// it manages.
func formatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
}
