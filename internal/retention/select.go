// select.go: generic full-fidelity row selection. Archives must carry
// EVERY column of every deleted row (ADR-046 / issue #19's acceptance
// criterion), so rows are read via SELECT * into ordered
// map[string]any values rather than per-table structs a future column
// addition could silently fall out of.
package retention

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// tableBatch is one table's selected-for-deletion set: full-fidelity rows
// (archive input) plus their primary-key values (delete input), in the
// same deterministic order.
type tableBatch struct {
	table     string
	keyColumn string
	rows      []map[string]any
	keys      []any
}

// selectChunkSize bounds IN(...) parameter lists well under SQLite's
// default host-parameter limit.
const selectChunkSize = 400

// scanRowMaps reads every row from rows into map[string]any, converting
// []byte to string (modernc.org/sqlite returns TEXT as string already;
// the []byte branch is defensive against driver differences) so values
// JSON-encode as text rather than base64.
func scanRowMaps(rows *sql.Rows) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("retention: reading result columns: %w", err)
	}
	var out []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("retention: scanning row: %w", err)
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				m[c] = string(b)
			} else {
				m[c] = vals[i]
			}
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("retention: iterating rows: %w", err)
	}
	return out, nil
}

// queryRowMaps runs query against db's plain pool (selection happens
// before, and outside, the delete transaction — ADR-046 step (a)).
func queryRowMaps(ctx context.Context, db *sqlite.DB, query string, args ...any) ([]map[string]any, error) {
	rows, err := db.Conn().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("retention: query %q: %w", query, err)
	}
	defer func() { _ = rows.Close() }()
	return scanRowMaps(rows)
}

// coarseCutoffString renders cutoff as a whole-second UTC boundary two
// seconds ABOVE the real cutoff, for use in SQL string comparison.
//
// Stored timestamps are RFC3339Nano with trailing zeros trimmed
// (internal/telemetry/claude/store.go's formatTime and every sibling
// store), which is NOT totally ordered under string comparison at
// sub-second granularity ("...00Z" sorts after "...00.5Z"). Against a
// whole-second boundary string, however, comparison IS exact for every
// value in an earlier second (the fixed-width date-time prefix decides)
// and merely over-includes fractional values inside the boundary second —
// so `ts_column < ?` with this string is a strict superset of the truly
// expired rows, and filterExpired below applies the exact parsed-time
// rule. Two seconds of slack keep the boundary second itself, and any
// same-second fractional values, on the candidate side.
func coarseCutoffString(cutoff time.Time) string {
	return cutoff.UTC().Truncate(time.Second).Add(2 * time.Second).Format("2006-01-02T15:04:05Z")
}

// filterExpired applies the EXACT expiry rule (parsed timestamp strictly
// before cutoff — a row exactly at the cutoff is retained, policy.go) to
// coarsely-selected candidate rows. A row whose timestamp fails to parse
// is conservatively KEPT (never delete what cannot be dated) and reported
// via a note.
func filterExpired(rows []map[string]any, table, tsColumn string, cutoff time.Time) (expired []map[string]any, notes []string) {
	unparseable := 0
	for _, row := range rows {
		ts, ok := row[tsColumn].(string)
		if !ok {
			unparseable++
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil {
			unparseable++
			continue
		}
		if t.Before(cutoff) {
			expired = append(expired, row)
		}
	}
	if unparseable > 0 {
		notes = append(notes, fmt.Sprintf("%s: kept %d row(s) with unparseable %s (conservative: cannot date, will not delete)", table, unparseable, tsColumn))
	}
	return expired, notes
}

// selectExpired implements ADR-046 step (a) for the simple table classes:
// coarse SQL prefilter (see coarseCutoffString) + exact Go-side filter,
// ordered by primary key for deterministic archives.
func selectExpired(ctx context.Context, db *sqlite.DB, table, keyColumn, tsColumn string, cutoff time.Time, extraWhere string) (*tableBatch, []string, error) {
	where := tsColumn + " < ?"
	if extraWhere != "" {
		where += " AND " + extraWhere
	}
	// Table/column names are package-internal constants (never caller
	// input), so string assembly here is not an injection surface.
	query := "SELECT * FROM " + table + " WHERE " + where + " ORDER BY " + keyColumn
	rows, err := queryRowMaps(ctx, db, query, coarseCutoffString(cutoff))
	if err != nil {
		return nil, nil, err
	}
	expired, notes := filterExpired(rows, table, tsColumn, cutoff)
	return newBatch(table, keyColumn, expired), notes, nil
}

// selectByKeyIn selects every row of table whose keyColumn matches one of
// keys, chunked, re-sorted by orderColumn's string value so the combined
// result is deterministic regardless of chunking.
func selectByKeyIn(ctx context.Context, db *sqlite.DB, table, keyColumn string, keys []any, orderColumn string) ([]map[string]any, error) {
	var out []map[string]any
	for _, chunk := range chunkKeys(keys) {
		query := "SELECT * FROM " + table + " WHERE " + keyColumn + " IN (" + placeholders(len(chunk)) + ")"
		rows, err := queryRowMaps(ctx, db, query, chunk...)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, _ := out[i][orderColumn].(string)
		b, _ := out[j][orderColumn].(string)
		return a < b
	})
	return out, nil
}

// newBatch builds a tableBatch from already-ordered rows, extracting each
// row's key value for the delete phase.
func newBatch(table, keyColumn string, rows []map[string]any) *tableBatch {
	b := &tableBatch{table: table, keyColumn: keyColumn, rows: rows}
	for _, row := range rows {
		b.keys = append(b.keys, row[keyColumn])
	}
	return b
}

// chunkKeys splits keys into selectChunkSize-bounded slices.
func chunkKeys(keys []any) [][]any {
	var chunks [][]any
	for len(keys) > 0 {
		n := min(len(keys), selectChunkSize)
		chunks = append(chunks, keys[:n])
		keys = keys[n:]
	}
	return chunks
}

// placeholders renders "?, ?, ..." for n parameters.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}
