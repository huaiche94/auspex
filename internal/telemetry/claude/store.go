// store.go: idempotent durable persistence for normalized
// pkg/protocol/v1.Event values (claude-provider-05). This is the second
// half of this package's pipeline — normalizer.go turns raw Claude Code
// payloads into v1.Event values; EventStore here writes them to SQLite
// (internal/storage/sqlite, foundation-06) durably and idempotently, keyed
// by Event.IdempotencyKey (CONTRACT_FREEZE.md: "Owning role... defines the
// exact digest algorithm; the field itself is frozen here" — claude-provider
// owns both the digest algorithm, in normalizer.go, and the persistence
// mechanics, here).
//
// EventStore writes through internal/storage/sqlite's DB.WithTx (the frozen
// app.TxRunner boundary) rather than opening its own connection or issuing
// queries outside a transaction — CONTRACT_FREEZE.md's "Transaction
// boundaries" section and this node's own task brief both require this.
package claude

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// EventStore persists normalized v1.Event values into the `events` table
// (migrations/0010_events.sql, this role's own migration range per
// CONTRACT_FREEZE.md). It depends only on *sqlite.DB (for QuerierFromContext)
// and the frozen app.TxRunner interface for the actual transaction
// boundary — callers are expected to invoke Persist inside a
// runner.WithTx(...) call (directly, or via PersistAll's own convenience
// wrapper) rather than this type inventing its own connection handling.
type EventStore struct {
	db *sqlite.DB
}

// NewEventStore constructs an EventStore bound to db. db is also used as
// the app.TxRunner for PersistAll's convenience transaction wrapper; a
// caller already inside a WithTx callback should call Persist directly
// instead so nested transactions are never attempted.
func NewEventStore(db *sqlite.DB) *EventStore {
	return &EventStore{db: db}
}

// Persist writes a single normalized event idempotently. It MUST be called
// from within an active transaction context, i.e. inside a
// TxRunner.WithTx(...) callback — ctx must be the context WithTx's fn
// received (or a context derived from it), so QuerierFromContext resolves
// to the active *sql.Tx rather than the bare connection pool.
//
// Idempotency: events.idempotency_key carries a UNIQUE index
// (idx_events_idempotency, migrations/0010_events.sql) scoped to non-NULL
// keys. Every event this package's normalizer produces sets
// IdempotencyKey (normalizer.go's digestKey helper), so in practice this
// path is always exercised; an event with an empty IdempotencyKey is still
// accepted (no uniqueness enforced for it) since the frozen v1.Event type
// does not itself require the field to be non-empty, but this package's own
// producers never emit one that way.
//
// A second Persist call carrying the same IdempotencyKey is a deliberate
// no-op (ON CONFLICT(idempotency_key) WHERE idempotency_key IS NOT NULL DO
// NOTHING — the WHERE clause on the conflict target must repeat the
// migration's partial-index predicate verbatim, or SQLite rejects the
// statement with "ON CONFLICT clause does not match any PRIMARY KEY or
// UNIQUE constraint"; this was caught by this node's own test run, not
// found via documentation) rather than an error or a silent
// overwrite: CONTRACT_FREEZE.md's "same completion request replayed with
// the same key MUST return the same result" principle for
// CompleteNodeRequest.IdempotencyKey is mirrored here for event
// idempotency, and re-delivery (e.g. a hook firing twice, or a status-line
// snapshot re-read after a crash) is expected, ordinary behavior for a
// provider integration, not an exceptional one. Per the ADD's
// "unknown is not zero" / conflict-not-overwrite discipline, this
// implementation does NOT attempt to detect or reject a *different*
// payload arriving under the same idempotency key (a true conflict) — the
// digest algorithm (normalizer.go's digestKey) is constructed so that two
// genuinely different observations always produce different keys already
// (it incorporates the observation timestamp), so a same-key-different-
// payload collision would indicate a normalizer bug, not a legitimate
// re-delivery; detecting that class of bug is out of scope for this
// storage-layer node.
//
// Out-of-order delivery: this table has no ordering precondition on
// insert — Persist does not require events to arrive in OccurredAt or
// ObservedAt order, and does not compare a new event's timestamps against
// already-stored rows before inserting. Each event is uniquely identified
// by its own EventID (primary key) and de-duplicated by its own
// IdempotencyKey, independent of any other row, so an event delivered
// before or after another logically-later/earlier event persists
// correctly either way; there is no mutable "current state" row this
// operation updates in place that ordering could corrupt.
func (s *EventStore) Persist(ctx context.Context, ev v1.Event) error {
	payloadJSON, err := json.Marshal(ev.Payload)
	if err != nil {
		return fmt.Errorf("claude: marshaling event payload for %s: %w", ev.EventID, err)
	}

	q := sqlite.QuerierFromContext(ctx, s.db)

	_, err = q.ExecContext(ctx, `
		INSERT INTO events (
			event_id, schema_version, event_type, occurred_at, observed_at,
			sequence, idempotency_key, source, provider, repository_id,
			worktree_id, session_id, turn_id, task_id, progress_node_id,
			payload_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
	`,
		ev.EventID,
		ev.SchemaVersion,
		string(ev.EventType),
		formatTime(ev.OccurredAt),
		formatTime(ev.ObservedAt),
		nullableSequence(ev.Sequence),
		nullableString(ev.IdempotencyKey),
		ev.Source,
		nullableString(ev.Provider),
		nullableString(ev.RepositoryID),
		nullableString(ev.WorktreeID),
		nullableString(ev.SessionID),
		nullableString(ev.TurnID),
		nullableString(ev.TaskID),
		nullableString(ev.ProgressNodeID),
		string(payloadJSON),
	)
	if err != nil {
		return fmt.Errorf("claude: inserting event %s: %w", ev.EventID, err)
	}
	return nil
}

// PersistAll persists every event in evs, in order, inside a single
// transaction obtained from runner — either all events are durably
// recorded (each idempotently per Persist's own rules) or, on any error,
// none are (the transaction rolls back). This is the convenience path a
// caller with a batch of events from a single normalizer call (e.g.
// NormalizeStatusLine's up-to-four-event slice, or NormalizeStopFailure's
// up-to-two-event slice) should use rather than calling Persist once per
// event outside a shared transaction, which would allow a partial batch to
// land durably if a later event in the same batch failed.
func (s *EventStore) PersistAll(ctx context.Context, runner app.TxRunner, evs []v1.Event) error {
	return runner.WithTx(ctx, func(txCtx context.Context) error {
		for _, ev := range evs {
			if err := s.Persist(txCtx, ev); err != nil {
				return err
			}
		}
		return nil
	})
}

// CountByIdempotencyKey returns how many rows in `events` carry the given
// idempotency key (0 or 1, given the table's unique index) — used by this
// package's own idempotency tests to assert that persisting the same
// event twice does not create a duplicate row, without depending on any
// other role's read-path abstractions.
func (s *EventStore) CountByIdempotencyKey(ctx context.Context, key string) (int, error) {
	q := sqlite.QuerierFromContext(ctx, s.db)
	var count int
	row := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE idempotency_key = ?`, key)
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("claude: counting events for idempotency key: %w", err)
	}
	return count, nil
}

// GetByEventID loads a single persisted event row back out by its
// EventID (primary key), for test assertions that the stored row's fields
// match what was written (e.g. after a duplicate-write no-op, the
// original row's payload must be unchanged, not partially overwritten).
// ErrEventNotFound is returned if no row exists for id.
func (s *EventStore) GetByEventID(ctx context.Context, id string) (StoredEvent, error) {
	q := sqlite.QuerierFromContext(ctx, s.db)
	row := q.QueryRowContext(ctx, `
		SELECT event_id, schema_version, event_type, occurred_at, observed_at,
		       sequence, idempotency_key, source, provider, repository_id,
		       worktree_id, session_id, turn_id, task_id, progress_node_id,
		       payload_json
		FROM events WHERE event_id = ?
	`, id)

	var (
		out                                                                                     StoredEvent
		sequence                                                                                sql.NullInt64
		idempotencyKey, provider, repositoryID, worktreeID, sessionID, turnID, taskID, progNode sql.NullString
		payloadJSON                                                                             string
	)
	err := row.Scan(
		&out.EventID, &out.SchemaVersion, &out.EventType, &out.OccurredAt, &out.ObservedAt,
		&sequence, &idempotencyKey, &out.Source, &provider, &repositoryID,
		&worktreeID, &sessionID, &turnID, &taskID, &progNode,
		&payloadJSON,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredEvent{}, ErrEventNotFound
	}
	if err != nil {
		return StoredEvent{}, fmt.Errorf("claude: loading event %s: %w", id, err)
	}

	if sequence.Valid {
		out.Sequence = &sequence.Int64
	}
	out.IdempotencyKey = idempotencyKey.String
	out.Provider = provider.String
	out.RepositoryID = repositoryID.String
	out.WorktreeID = worktreeID.String
	out.SessionID = sessionID.String
	out.TurnID = turnID.String
	out.TaskID = taskID.String
	out.ProgressNodeID = progNode.String

	if err := json.Unmarshal([]byte(payloadJSON), &out.Payload); err != nil {
		return StoredEvent{}, fmt.Errorf("claude: decoding payload for event %s: %w", id, err)
	}

	return out, nil
}

// StoredEvent is the row shape GetByEventID returns: the same logical
// fields as v1.Event, but with OccurredAt/ObservedAt left as the raw
// RFC3339Nano strings stored on disk (test assertions compare these
// against formatTime(ev.OccurredAt) rather than re-parsing), since this
// type exists only for this package's own test verification, not as a
// public read API for other roles.
type StoredEvent struct {
	EventID        string
	SchemaVersion  string
	EventType      string
	OccurredAt     string
	ObservedAt     string
	Sequence       *int64
	IdempotencyKey string
	Source         string
	Provider       string
	RepositoryID   string
	WorktreeID     string
	SessionID      string
	TurnID         string
	TaskID         string
	ProgressNodeID string
	Payload        map[string]any
}

// ErrEventNotFound is returned by GetByEventID when no row matches.
var ErrEventNotFound = errors.New("claude: event not found")

// rfc3339Nano is the fixed TEXT-column timestamp format this package uses,
// matching foundation's own schema_migrations.applied_at convention
// (internal/storage/sqlite/migrate.go) and ADD §12.2's TEXT timestamp
// columns generally.
const rfc3339Nano = "2006-01-02T15:04:05.999999999Z07:00"

// formatTime renders t in this package's fixed RFC3339Nano/UTC timestamp
// form.
func formatTime(t time.Time) string {
	return t.UTC().Format(rfc3339Nano)
}

// nullableString maps a Go zero-value empty string to SQL NULL. Several
// v1.Event fields (Provider, RepositoryID, WorktreeID, SessionID, TurnID,
// TaskID, ProgressNodeID, IdempotencyKey) are plain strings on the frozen
// wire type but are optional in practice (not every event is scoped to
// every dimension) — storing "" and NULL as the same thing on read would
// silently conflate "not applicable" with "empty string was observed", so
// this only ever produces NULL for a genuinely empty Go string, never for
// a non-empty one.
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullableSequence maps v1.Event's zero-value Sequence (int64) to SQL
// NULL, mirroring nullableString's reasoning: Sequence is not set by this
// package's own normalizer (no producer here assigns one yet), and 0 is
// this package's own zero value, not a real sequence number 0 must never
// be mistaken for having been observed.
func nullableSequence(seq int64) any {
	if seq == 0 {
		return nil
	}
	return seq
}
