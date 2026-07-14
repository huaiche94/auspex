// contextstore.go: durable pauseContext, closing the cross-PROCESS gap the
// in-memory Service.contexts bookkeeping deliberately left open (service.go's
// own contexts field doc comment: "a real durable equivalent is
// pause_records' own columns") — and that a resident daemon worker (#7, M6)
// makes load-bearing for the first time: the process that requests a pause
// (a short-lived CLI/hook invocation) is never the process that resumes it
// unattended (the daemon), so QuotaBaseline / WorktreeID / PausedWorkPaths /
// GitHeadBaseline must survive in the row itself or ValidateResume can never
// be given its inputs across that boundary (D-16).
//
// Storage is pause_records.metadata_json's existing free-form column — the
// same no-new-migration decision encodePauseMetadata (TriggerReason) and
// GetProgress/SaveProgress (persist-phase markers) already made twice; this
// role's migration budget is unchanged (Constitution §4.7). Unlike those two
// flat-key precedents, the context is a nested object with an array field,
// so this file uses encoding/json — and, critically, reads-modifies-writes
// the whole metadata object as a map[string]json.RawMessage so keys written
// by the OTHER two encoders survive a context write byte-for-byte, and vice
// versa (SaveProgress gained the same merge discipline alongside this file).
package pause

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// pauseContextStore is the optional store capability Service probes for
// (type assertion) to make pauseContext durable: unexported because
// pauseContext itself is, and every production store (SQLiteStore) lives in
// this same package. A store that does not implement it (MemStore, narrow
// test fakes) simply keeps today's in-memory-only semantics.
type pauseContextStore interface {
	SaveContext(ctx context.Context, id domain.PauseID, pctx pauseContext) error
	LoadContext(ctx context.Context, id domain.PauseID) (pauseContext, bool, error)
}

var _ pauseContextStore = (*SQLiteStore)(nil)

// persistedPauseContext is pauseContext's JSON shape under metadata_json's
// "context" key. Field-for-field with pauseContext; a separate struct so the
// wire encoding is explicit and stable rather than borrowing whatever tags a
// future pauseContext refactor might imply.
type persistedPauseContext struct {
	TaskID          string                  `json:"task_id,omitempty"`
	WorktreeID      string                  `json:"worktree_id,omitempty"`
	PausedWorkPaths []string                `json:"paused_work_paths,omitempty"`
	GitHeadBaseline string                  `json:"git_head_baseline,omitempty"`
	QuotaBaseline   *persistedQuotaBaseline `json:"quota_baseline,omitempty"`
}

// persistedQuotaBaseline persists the domain.QuotaObservation fields
// ValidateResume's quotaWorseThan actually reads (UsedPercent, Reached,
// identity/audit fields) — the full observation, so a future check gaining
// a new input does not find a lossy baseline.
type persistedQuotaBaseline struct {
	ID            string     `json:"id,omitempty"`
	SessionID     string     `json:"session_id,omitempty"`
	Provider      string     `json:"provider,omitempty"`
	LimitID       string     `json:"limit_id,omitempty"`
	LimitName     string     `json:"limit_name,omitempty"`
	UsedPercent   *float64   `json:"used_percent,omitempty"`
	WindowSeconds *int64     `json:"window_seconds,omitempty"`
	ResetsAt      *time.Time `json:"resets_at,omitempty"`
	Reached       bool       `json:"reached,omitempty"`
	Source        string     `json:"source,omitempty"`
	Confidence    string     `json:"confidence,omitempty"`
	ObservedAt    time.Time  `json:"observed_at,omitzero"`
}

func toPersistedContext(pctx pauseContext) persistedPauseContext {
	out := persistedPauseContext{
		TaskID:          string(pctx.TaskID),
		WorktreeID:      string(pctx.WorktreeID),
		PausedWorkPaths: pctx.PausedWorkPaths,
		GitHeadBaseline: pctx.GitHeadBaseline,
	}
	q := pctx.QuotaBaseline
	if q != (domain.QuotaObservation{}) {
		out.QuotaBaseline = &persistedQuotaBaseline{
			ID: q.ID, SessionID: string(q.SessionID), Provider: q.Provider,
			LimitID: q.LimitID, LimitName: q.LimitName,
			UsedPercent: q.UsedPercent, WindowSeconds: q.WindowSeconds, ResetsAt: q.ResetsAt,
			Reached: q.Reached, Source: string(q.Source), Confidence: string(q.Confidence),
			ObservedAt: q.ObservedAt,
		}
	}
	return out
}

func fromPersistedContext(p persistedPauseContext) pauseContext {
	out := pauseContext{
		TaskID:          domain.TaskID(p.TaskID),
		WorktreeID:      domain.WorktreeID(p.WorktreeID),
		PausedWorkPaths: p.PausedWorkPaths,
		GitHeadBaseline: p.GitHeadBaseline,
	}
	if q := p.QuotaBaseline; q != nil {
		out.QuotaBaseline = domain.QuotaObservation{
			ID: q.ID, SessionID: domain.SessionID(q.SessionID), Provider: q.Provider,
			LimitID: q.LimitID, LimitName: q.LimitName,
			UsedPercent: q.UsedPercent, WindowSeconds: q.WindowSeconds, ResetsAt: q.ResetsAt,
			Reached: q.Reached, Source: domain.MeasurementSource(q.Source), Confidence: domain.Confidence(q.Confidence),
			ObservedAt: q.ObservedAt,
		}
	}
	return out
}

// metadataContextKey is the "context" key inside metadata_json — sibling to
// "reason" (encodePauseMetadata) and the persist-progress keys
// (SaveProgress); mergeMetadataJSON keeps them from clobbering each other.
const metadataContextKey = "context"

// SaveContext durably records pctx under metadata_json's "context" key via
// read-modify-write, preserving every other key in the object.
func (s *SQLiteStore) SaveContext(ctx context.Context, id domain.PauseID, pctx pauseContext) error {
	raw, err := json.Marshal(toPersistedContext(pctx))
	if err != nil {
		return fmt.Errorf("pause: SQLiteStore.SaveContext: marshal: %w", err)
	}
	q := sqlite.QuerierFromContext(ctx, s.db)
	merged, err := readMergedMetadata(ctx, q, id, "SaveContext", map[string]json.RawMessage{
		metadataContextKey: raw,
	})
	if err != nil {
		return err
	}
	res, err := q.ExecContext(ctx, `UPDATE pause_records SET metadata_json = ? WHERE id = ?`, merged, string(id))
	if err != nil {
		return fmt.Errorf("pause: SQLiteStore.SaveContext: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("pause: SQLiteStore.SaveContext: RowsAffected: %w", err)
	}
	if n == 0 {
		return notFoundMetadataError("SaveContext", id)
	}
	return nil
}

// LoadContext reads the "context" key back; found=false (not an error) when
// the row exists but predates SaveContext, mirroring GetProgress's
// zero-value tolerance for pre-existing rows.
func (s *SQLiteStore) LoadContext(ctx context.Context, id domain.PauseID) (pauseContext, bool, error) {
	q := sqlite.QuerierFromContext(ctx, s.db)
	row := q.QueryRowContext(ctx, `SELECT metadata_json FROM pause_records WHERE id = ?`, string(id))
	var metadataJSON string
	err := row.Scan(&metadataJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return pauseContext{}, false, nil
	}
	if err != nil {
		return pauseContext{}, false, fmt.Errorf("pause: SQLiteStore.LoadContext: %w", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(metadataJSON), &obj); err != nil {
		return pauseContext{}, false, fmt.Errorf("pause: SQLiteStore.LoadContext: metadata_json for %q is not a JSON object: %w", id, err)
	}
	raw, ok := obj[metadataContextKey]
	if !ok {
		return pauseContext{}, false, nil
	}
	var p persistedPauseContext
	if err := json.Unmarshal(raw, &p); err != nil {
		return pauseContext{}, false, fmt.Errorf("pause: SQLiteStore.LoadContext: decoding context for %q: %w", id, err)
	}
	return fromPersistedContext(p), true, nil
}

// readMergedMetadata is the shared read-and-merge half of every
// metadata_json read-modify-write in this store: decode the row's current
// metadata_json into a key→raw map, overlay updates, and return the
// re-encoded object (map marshaling sorts keys — deterministic output).
// The caller performs its own UPDATE so multi-column writers (SaveProgress)
// stay a single statement; callers already run inside the ambient
// transaction when one is on ctx (QuerierFromContext).
func readMergedMetadata(ctx context.Context, q sqlite.Querier, id domain.PauseID, verb string, updates map[string]json.RawMessage) (string, error) {
	row := q.QueryRowContext(ctx, `SELECT metadata_json FROM pause_records WHERE id = ?`, string(id))
	var metadataJSON string
	err := row.Scan(&metadataJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return "", notFoundMetadataError(verb, id)
	}
	if err != nil {
		return "", fmt.Errorf("pause: SQLiteStore.%s: read metadata: %w", verb, err)
	}
	obj := map[string]json.RawMessage{}
	if metadataJSON != "" {
		if err := json.Unmarshal([]byte(metadataJSON), &obj); err != nil {
			return "", fmt.Errorf("pause: SQLiteStore.%s: metadata_json for %q is not a JSON object: %w", verb, id, err)
		}
	}
	for k, v := range updates {
		obj[k] = v
	}
	merged, err := json.Marshal(obj)
	if err != nil {
		return "", fmt.Errorf("pause: SQLiteStore.%s: marshal metadata: %w", verb, err)
	}
	return string(merged), nil
}

func notFoundMetadataError(verb string, id domain.PauseID) error {
	return &domain.Error{
		Code:      domain.ErrCodeNotFound,
		Message:   fmt.Sprintf("pause: SQLiteStore.%s: pause record %q not found", verb, id),
		Retryable: false,
		Details:   map[string]string{"pause_id": string(id)},
	}
}

// rawJSONBool/rawJSONString encode a single scalar as a json.RawMessage for
// readMergedMetadata's overlay map.
func rawJSONBool(b bool) json.RawMessage {
	if b {
		return json.RawMessage("true")
	}
	return json.RawMessage("false")
}

func rawJSONString(s string) json.RawMessage {
	raw, _ := json.Marshal(s) // a plain string cannot fail to marshal
	return raw
}
