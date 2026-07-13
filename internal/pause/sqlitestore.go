// sqlitestore.go: a REAL SQLite-backed PauseStore, against the pause_records
// table this role's own migration range created (0050_pause_records.sql).
//
// Five prior nodes (runtime-a04 through a09, see requestpause.go/
// persistphase_test.go's own doc comments) each explicitly named the same
// gap and deliberately deferred it: PauseStore had no durable, cross-process
// implementation, only MemStore (an in-memory reference/test double). That
// was a reasonable, honestly-documented deferral at each of those stages —
// none of them needed cross-restart durability to prove their own required
// tests. runtime-b10 (this file) is different: its whole job is proving
// Auspex survives a real process restart against the SAME on-disk SQLite
// file, and PauseStore is exactly the seam PauseLifecycleDeps.Store
// (internal/orchestrator/pauselifecycle.go) wires `pause request`/
// `pause cancel`/`resume` through. Proving restart-safety for those three
// commands while they still ran against an in-memory store would be
// dishonest — MemStore is discarded the instant its owning App is, by
// definition. So this file closes that specific, repeatedly-flagged gap: a
// minimal, real PauseStore, satisfying the interface unchanged (no signature
// in requestpause.go moves), so every existing caller
// (RequestPause/Cancel/Resume/Wake, and PauseLifecycleDeps.Store) accepts it
// as a drop-in replacement for MemStore with zero other code changes.
//
// Scope: this file implements PauseStore only (FindActiveByKey/Insert/
// GetByID/UpdateStatus/CompareAndSwapStatus) — exactly the interface
// PauseLifecycleDeps.Store and RequestPause/Cancel/Resume/Wake actually take.
// PersistPauseStore (GetProgress/SaveProgress, persistphase.go's own,
// narrower interface for runtime-a05's five-step persist orchestration) is
// NOT implemented here: reconciling PersistPauseStore onto this same table
// is a separate, already-tracked gap (persistphase_test.go's own
// seedPauseRecordRow doc comment: "a future integration node reconciles
// PersistPauseStore onto a real SQLite-backed PauseStore against this same
// table") that a05's own progress-phase bookkeeping concern, not this
// integration node's. Building it here too would be scope creep beyond
// "prove restart safety end-to-end" into a new persist-phase feature this
// task brief did not ask for.
package pause

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// SQLiteStore is a real, cross-process-durable PauseStore backed by the
// pause_records table (migration 0050). It stores exactly the fields
// PauseRecord/PauseKey need round-tripped; every other pause_records column
// (turn_id, runway_forecast_id, safe_point_at, ...) is either not yet
// populated by any caller this role has built, or belongs to a later node —
// this store does not invent values for them, it only writes what the
// PauseStore interface's own callers pass it (NOT NULL columns this store
// doesn't otherwise populate get a fixed placeholder, documented per-column
// below, exactly as seedPauseRecordRow's own test helper already does).
type SQLiteStore struct {
	db *sqlite.DB
}

// NewSQLiteStore constructs a SQLiteStore bound to db. db must already be
// migrated (pause_records must exist) — mirrors every other role's
// NewStore(db) constructor convention (statecheckpoint.NewStore,
// repocheckpoint.NewStore, scheduler.NewStore).
func NewSQLiteStore(db *sqlite.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

var _ PauseStore = (*SQLiteStore)(nil)

// pauseRecordPlaceholderTurnID/RunwayForecastID fill pause_records' two
// NOT NULL columns this store's own callers (RequestPause/Cancel/Resume/
// Wake — none of which take a TurnID or RunwayForecastID today; see
// PauseRequest's own field set) never supply. A future node that threads a
// real TurnID/RunwayForecastID through PauseStore.Insert can widen this
// store's Insert signature then; today's callers have nothing to put here,
// and a fixed, greppable placeholder is preferable to silently inventing a
// per-call random value that would make otherwise-identical rows look
// spuriously different.
const (
	pauseRecordPlaceholderTurnID         = "unknown"
	pauseRecordPlaceholderRunwayForecast = "unknown"
	pauseRecordDefaultAutoResumeEnabled  = 0
)

// FindActiveByKey implements PauseStore.FindActiveByKey: the most recent
// non-terminal row for key's (TaskID, SessionID), if any. "Most recent" is
// well-defined because a caller only ever creates a new row for a given key
// once every prior row for it has gone terminal (RequestPause's own
// idempotency invariant) — but this query does not rely on that invariant
// holding; it simply orders by rowid DESC and takes the first non-terminal
// match, so it is correct even if that invariant were ever violated by a
// bug elsewhere.
func (s *SQLiteStore) FindActiveByKey(ctx context.Context, key PauseKey) (PauseRecord, bool, error) {
	q := sqlite.QuerierFromContext(ctx, s.db)
	rows, err := q.QueryContext(ctx, `
		SELECT id, task_id, session_id, status, metadata_json
		FROM pause_records
		WHERE task_id = ? AND session_id = ?
		ORDER BY rowid DESC
	`, string(key.TaskID), string(key.SessionID))
	if err != nil {
		return PauseRecord{}, false, fmt.Errorf("pause: SQLiteStore.FindActiveByKey: query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		rec, err := scanPauseRecord(rows)
		if err != nil {
			return PauseRecord{}, false, err
		}
		if !IsTerminal(rec.Status) {
			return rec, true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return PauseRecord{}, false, fmt.Errorf("pause: SQLiteStore.FindActiveByKey: rows: %w", err)
	}
	return PauseRecord{}, false, nil
}

// Insert implements PauseStore.Insert: a brand new pause_records row.
// metadata_json carries rec.Reason (this store's own encoding, read back
// by scanPauseRecord) since pause_records has no dedicated reason column
// (Auspex_ADD.md §12.2's canonical schema does not name one either).
func (s *SQLiteStore) Insert(ctx context.Context, rec PauseRecord) error {
	if rec.ID == "" {
		return &domain.Error{Code: domain.ErrCodeValidation, Message: "pause: SQLiteStore.Insert requires a non-empty ID", Retryable: false}
	}
	q := sqlite.QuerierFromContext(ctx, s.db)
	metadata := encodePauseMetadata(rec.Reason)
	_, err := q.ExecContext(ctx, `
		INSERT INTO pause_records
			(id, task_id, session_id, turn_id, runway_forecast_id, status, requested_at, auto_resume_enabled, metadata_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		string(rec.ID), string(rec.Key.TaskID), string(rec.Key.SessionID),
		pauseRecordPlaceholderTurnID, pauseRecordPlaceholderRunwayForecast,
		string(rec.Status), nowRFC3339Placeholder(), pauseRecordDefaultAutoResumeEnabled,
		metadata,
	)
	if err != nil {
		return fmt.Errorf("pause: SQLiteStore.Insert: %w", err)
	}
	return nil
}

// GetByID implements PauseStore.GetByID.
func (s *SQLiteStore) GetByID(ctx context.Context, id domain.PauseID) (PauseRecord, bool, error) {
	q := sqlite.QuerierFromContext(ctx, s.db)
	row := q.QueryRowContext(ctx, `
		SELECT id, task_id, session_id, status, metadata_json
		FROM pause_records
		WHERE id = ?
	`, string(id))
	rec, err := scanPauseRecordRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PauseRecord{}, false, nil
	}
	if err != nil {
		return PauseRecord{}, false, fmt.Errorf("pause: SQLiteStore.GetByID: %w", err)
	}
	return rec, true, nil
}

// UpdateStatus implements PauseStore.UpdateStatus: an unconditional status
// write (no compare — callers are expected to have already validated the
// transition via pause.Apply, per the interface's own doc comment).
func (s *SQLiteStore) UpdateStatus(ctx context.Context, id domain.PauseID, status domain.PauseStatus) error {
	q := sqlite.QuerierFromContext(ctx, s.db)
	res, err := q.ExecContext(ctx, `UPDATE pause_records SET status = ? WHERE id = ?`, string(status), string(id))
	if err != nil {
		return fmt.Errorf("pause: SQLiteStore.UpdateStatus: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("pause: SQLiteStore.UpdateStatus: RowsAffected: %w", err)
	}
	if n == 0 {
		return &domain.Error{
			Code:      domain.ErrCodeNotFound,
			Message:   fmt.Sprintf("pause: SQLiteStore.UpdateStatus: pause record %q not found", id),
			Retryable: false,
			Details:   map[string]string{"pause_id": string(id)},
		}
	}
	return nil
}

// CompareAndSwapStatus implements PauseStore.CompareAndSwapStatus via a
// conditional `UPDATE ... WHERE id = ? AND status = ?` — the real-store
// analogue of MemStore's mutex-guarded compare-and-swap, and the same
// conditional-update idiom internal/scheduler.Store.Complete/Fail/Renew
// already established for wake_jobs (`WHERE status = ? AND lease_owner = ?`)
// applied here to pause_records instead. SQLite's single-writer-per-
// transaction semantics (WAL mode, foundation-07) make this UPDATE's
// affected-row-count an atomic, race-free compare-and-swap: two concurrent
// callers racing the same id can both issue this statement, but SQLite
// serializes their commits, so at most one UPDATE actually matches the
// still-`expected` row — the loser's statement affects zero rows and reports
// ok=false, exactly like MemStore's mutex-losing branch, without this
// package needing its own locking on top of what the storage layer already
// guarantees.
func (s *SQLiteStore) CompareAndSwapStatus(ctx context.Context, id domain.PauseID, expected, next domain.PauseStatus) (ok bool, found bool, err error) {
	q := sqlite.QuerierFromContext(ctx, s.db)
	res, execErr := q.ExecContext(ctx, `
		UPDATE pause_records SET status = ? WHERE id = ? AND status = ?
	`, string(next), string(id), string(expected))
	if execErr != nil {
		return false, false, fmt.Errorf("pause: SQLiteStore.CompareAndSwapStatus: %w", execErr)
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return false, false, fmt.Errorf("pause: SQLiteStore.CompareAndSwapStatus: RowsAffected: %w", raErr)
	}
	if n > 0 {
		return true, true, nil
	}
	// Zero rows affected: either the record doesn't exist at all, or it
	// exists but is no longer at `expected` (someone else already moved
	// it — the race-loser case). Distinguish them with a follow-up read,
	// exactly as MemStore's own CompareAndSwapStatus does, so a caller can
	// tell "nothing to compare-and-swap" apart from "lost the race."
	_, foundNow, getErr := s.GetByID(ctx, id)
	if getErr != nil {
		return false, false, getErr
	}
	return false, foundNow, nil
}

// scanRow is satisfied by both *sql.Row and *sql.Rows, so
// scanPauseRecordCore can back both GetByID/CompareAndSwapStatus's
// single-row reads and FindActiveByKey's multi-row scan without duplicating
// the column list twice.
type scanRow interface {
	Scan(dest ...any) error
}

func scanPauseRecordCore(r scanRow) (PauseRecord, error) {
	var id, taskID, sessionID, status, metadataJSON string
	if err := r.Scan(&id, &taskID, &sessionID, &status, &metadataJSON); err != nil {
		return PauseRecord{}, err
	}
	return PauseRecord{
		ID:     domain.PauseID(id),
		Key:    PauseKey{TaskID: domain.TaskID(taskID), SessionID: domain.SessionID(sessionID)},
		Status: domain.PauseStatus(status),
		Reason: decodePauseMetadata(metadataJSON),
	}, nil
}

func scanPauseRecord(rows *sql.Rows) (PauseRecord, error)  { return scanPauseRecordCore(rows) }
func scanPauseRecordRow(row *sql.Row) (PauseRecord, error) { return scanPauseRecordCore(row) }

// encodePauseMetadata/decodePauseMetadata round-trip TriggerReason through
// pause_records.metadata_json's existing free-form JSON column (present
// since 0050_pause_records.sql, default '{}') rather than adding a new
// migration for a dedicated column — this role's Part B migration range is
// explicitly "none unless contract-integrator assigns one"
// (CONTRACT_FREEZE.md), and Part A's own 0050-0059 range is a shared
// resource this single field does not need a new file for. A minimal
// hand-rolled encode/decode (not encoding/json） keeps this file dependency-
// free for the one string field it needs; a future column added by a real
// migration can replace this without changing PauseStore's interface.
//
// decodePauseMetadata delegates to extractJSONStringField (below, added
// alongside this file's PersistPauseStore reconciliation) rather than the
// original strict-prefix/suffix match, specifically so a row whose
// metadata_json has ALREADY been rewritten by SaveProgress into the wider
// four-key persistProgressMetadata shape still correctly yields back its
// own "reason" value — the original strict-shape match would silently
// return "" the first time SaveProgress ran on a row, which would be a
// real, if narrow, data-loss regression this reconciliation must not
// introduce.
func encodePauseMetadata(reason TriggerReason) string {
	if reason == "" {
		return `{}`
	}
	return `{"reason":"` + string(reason) + `"}`
}

func decodePauseMetadata(metadataJSON string) TriggerReason {
	return TriggerReason(extractJSONStringField(metadataJSON, "reason"))
}

// --- PersistPauseStore reconciliation (Service's own composition gap) -----
//
// GetProgress/SaveProgress close the gap persistphase_test.go's own
// seedPauseRecordRow doc comment named explicitly: "a future integration
// node reconciles PersistPauseStore onto a real SQLite-backed PauseStore
// against this same table." That future node is this one (the Final
// integration gate's GracefulPauseService Service, service.go), and this
// is the reconciliation: SQLiteStore now satisfies BOTH PauseStore and
// PersistPauseStore against the exact same pause_records row, using
// columns that already exist (state_checkpoint_id, repository_checkpoint_id
// — migration 0050) plus metadata_json's existing free-form JSON slot
// (already used by encodePauseMetadata/decodePauseMetadata for
// TriggerReason) for the two boolean phase markers and the WakeJobID
// scalar neither dedicated column covers. No new migration, no schema
// change — every field PersistProgress needs already has somewhere to
// live in the row Insert already creates.
var _ PersistPauseStore = (*SQLiteStore)(nil)

// persistProgressMetadata is metadata_json's full decoded shape once this
// file's GetProgress/SaveProgress are in play — a strict superset of the
// single "reason" key encodePauseMetadata/decodePauseMetadata already
// round-trip, so a record written before this file existed (reason only)
// still decodes correctly (the three new fields simply read as their zero
// values: not-yet-taken/not-yet-saved/no wake job recorded here yet).
type persistProgressMetadata struct {
	Reason                string `json:"reason,omitempty"`
	ProgressSnapshotTaken bool   `json:"progress_snapshot_taken,omitempty"`
	PauseRecordSaved      bool   `json:"pause_record_saved,omitempty"`
	WakeJobID             string `json:"wake_job_id,omitempty"`
}

// GetProgress implements PersistPauseStore.GetProgress: reads the current
// phase-progress markers back from pause_records — state_checkpoint_id/
// repository_checkpoint_id from their own dedicated columns, the two
// booleans and WakeJobID from metadata_json.
func (s *SQLiteStore) GetProgress(ctx context.Context, id domain.PauseID) (PersistProgress, bool, error) {
	q := sqlite.QuerierFromContext(ctx, s.db)
	row := q.QueryRowContext(ctx, `
		SELECT state_checkpoint_id, repository_checkpoint_id, metadata_json
		FROM pause_records
		WHERE id = ?
	`, string(id))

	var stateCkptID, repoCkptID sql.NullString
	var metadataJSON string
	err := row.Scan(&stateCkptID, &repoCkptID, &metadataJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return PersistProgress{}, false, nil
	}
	if err != nil {
		return PersistProgress{}, false, fmt.Errorf("pause: SQLiteStore.GetProgress: %w", err)
	}

	meta := decodePersistProgressMetadata(metadataJSON)
	progress := PersistProgress{
		ProgressSnapshotTaken: meta.ProgressSnapshotTaken,
		PauseRecordSaved:      meta.PauseRecordSaved,
	}
	if stateCkptID.Valid && stateCkptID.String != "" {
		v := domain.StateCheckpointID(stateCkptID.String)
		progress.StateCheckpointID = &v
	}
	if repoCkptID.Valid && repoCkptID.String != "" {
		v := domain.RepositoryCheckpointID(repoCkptID.String)
		progress.RepositoryCheckpointID = &v
	}
	if meta.WakeJobID != "" {
		v := domain.WakeJobID(meta.WakeJobID)
		progress.WakeJobID = &v
	}
	return progress, true, nil
}

// SaveProgress implements PersistPauseStore.SaveProgress: durably records
// progress's fields back onto the same row GetProgress reads from. Called
// once per successful Persist step (persistphase.go's own discipline), so
// this is a single-row UPDATE per call, never a batch — matching
// PersistPauseStore's own "never batched" contract exactly.
func (s *SQLiteStore) SaveProgress(ctx context.Context, id domain.PauseID, progress PersistProgress) error {
	q := sqlite.QuerierFromContext(ctx, s.db)

	// Preserve the existing Reason (encodePauseMetadata's own field) —
	// SaveProgress must not silently drop it when it rewrites
	// metadata_json for the phase-progress fields.
	existing, found, err := s.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("pause: SQLiteStore.SaveProgress: reading existing reason: %w", err)
	}
	if !found {
		return &domain.Error{
			Code:      domain.ErrCodeNotFound,
			Message:   fmt.Sprintf("pause: SQLiteStore.SaveProgress: pause record %q not found", id),
			Retryable: false,
			Details:   map[string]string{"pause_id": string(id)},
		}
	}

	meta := persistProgressMetadata{
		Reason:                string(existing.Reason),
		ProgressSnapshotTaken: progress.ProgressSnapshotTaken,
		PauseRecordSaved:      progress.PauseRecordSaved,
	}
	if progress.WakeJobID != nil {
		meta.WakeJobID = string(*progress.WakeJobID)
	}

	var stateCkptID, repoCkptID sql.NullString
	if progress.StateCheckpointID != nil {
		stateCkptID = sql.NullString{String: string(*progress.StateCheckpointID), Valid: true}
	}
	if progress.RepositoryCheckpointID != nil {
		repoCkptID = sql.NullString{String: string(*progress.RepositoryCheckpointID), Valid: true}
	}

	res, err := q.ExecContext(ctx, `
		UPDATE pause_records
		SET state_checkpoint_id = ?, repository_checkpoint_id = ?, metadata_json = ?
		WHERE id = ?
	`, stateCkptID, repoCkptID, encodePersistProgressMetadata(meta), string(id))
	if err != nil {
		return fmt.Errorf("pause: SQLiteStore.SaveProgress: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("pause: SQLiteStore.SaveProgress: RowsAffected: %w", err)
	}
	if n == 0 {
		return &domain.Error{
			Code:      domain.ErrCodeNotFound,
			Message:   fmt.Sprintf("pause: SQLiteStore.SaveProgress: pause record %q not found", id),
			Retryable: false,
			Details:   map[string]string{"pause_id": string(id)},
		}
	}
	return nil
}

// encodePersistProgressMetadata/decodePersistProgressMetadata hand-roll
// the same minimal, dependency-free JSON-object encoding
// encodePauseMetadata/decodePauseMetadata already use for the single
// "reason" key (this file's own doc comment on those functions explains
// why: no migration-range budget this wave for a dedicated column, and a
// hand-rolled encode/decode keeps this file free of an encoding/json
// dependency for a small, fixed field set). Unlike the single-key
// original, this is a small fixed-order four-key object; each key's
// presence/value is written unconditionally in a stable order so encoding
// is deterministic and decoding is a straightforward per-key scan rather
// than a general JSON parse.
func encodePersistProgressMetadata(m persistProgressMetadata) string {
	b := boolStr(m.ProgressSnapshotTaken)
	p := boolStr(m.PauseRecordSaved)
	return `{"reason":"` + jsonEscape(m.Reason) + `","progress_snapshot_taken":` + b +
		`,"pause_record_saved":` + p + `,"wake_job_id":"` + jsonEscape(m.WakeJobID) + `"}`
}

func decodePersistProgressMetadata(metadataJSON string) persistProgressMetadata {
	return persistProgressMetadata{
		Reason:                extractJSONStringField(metadataJSON, "reason"),
		ProgressSnapshotTaken: extractJSONBoolField(metadataJSON, "progress_snapshot_taken"),
		PauseRecordSaved:      extractJSONBoolField(metadataJSON, "pause_record_saved"),
		WakeJobID:             extractJSONStringField(metadataJSON, "wake_job_id"),
	}
}

// extractJSONStringField/extractJSONBoolField/jsonEscape are the minimal,
// dependency-free scanners this hand-rolled encoding needs — sufficient
// for the small, fixed, always-flat field set this file ever writes
// (never a user-supplied or externally-sourced JSON blob), not a general
// JSON parser. A field absent from metadataJSON (e.g. a row written before
// this file existed, which only ever had "reason") decodes to its zero
// value, never an error — exactly the same forward-compatible behavior
// decodePauseMetadata's own single-field version already had.
func extractJSONStringField(json, key string) string {
	marker := `"` + key + `":"`
	idx := indexOf(json, marker)
	if idx < 0 {
		return ""
	}
	start := idx + len(marker)
	end := indexOf(json[start:], `"`)
	if end < 0 {
		return ""
	}
	return jsonUnescape(json[start : start+end])
}

func extractJSONBoolField(json, key string) bool {
	marker := `"` + key + `":`
	idx := indexOf(json, marker)
	if idx < 0 {
		return false
	}
	rest := json[idx+len(marker):]
	return len(rest) >= 4 && rest[:4] == "true"
}

func indexOf(s, substr string) int {
	n, m := len(s), len(substr)
	if m == 0 {
		return 0
	}
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == substr {
			return i
		}
	}
	return -1
}

// jsonEscape/jsonUnescape handle only the one escape this file's own
// values could ever plausibly contain (a literal double quote or
// backslash inside Reason, which is otherwise a closed TriggerReason
// enum value today, or WakeJobID, an ID-generator-produced string) —
// sufficient for this hand-rolled encoding's own fixed, narrow field set,
// not a general JSON string escaper.
func jsonEscape(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"', '\\':
			out = append(out, '\\', s[i])
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}

func jsonUnescape(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			out = append(out, s[i])
			continue
		}
		out = append(out, s[i])
	}
	return string(out)
}

// nowRFC3339Placeholder stamps requested_at (NOT NULL) with a fixed,
// greppable sentinel rather than reading a wall clock: SQLiteStore
// deliberately takes no domain.Clock dependency (PauseStore's interface
// has none either — MemStore's own Insert doesn't stamp a time at all), so
// this avoids silently pretending a wall-clock read is meaningful data.
// requested_at's actual, meaningful value is produced by
// RequestPause/PersistPhase's own callers elsewhere; this column exists to
// satisfy pause_records' NOT NULL constraint for a store whose interface
// was never given a real timestamp to put there. A future node that widens
// PauseStore.Insert to accept a caller-supplied timestamp can replace this
// placeholder outright without any other change.
func nowRFC3339Placeholder() string {
	return "1970-01-01T00:00:00Z"
}
