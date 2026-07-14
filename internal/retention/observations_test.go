// observations_test.go: FR-170/171 observations export (issue #11) — the
// raw usage/context/quota series plus turn boundaries stream out ordered
// per session, carrying ONLY the whitelisted payload projection: the
// prompt/path-shaped payload fields the events table legitimately holds
// (prompt_sha256, cwd) must be unexportable by construction.
package retention

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func decodeObservationLines(t *testing.T, out []byte) []ObservationRecord {
	t.Helper()
	var records []ObservationRecord
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var rec ObservationRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode observation line %q: %v", line, err)
		}
		records = append(records, rec)
	}
	return records
}

func TestExportObservations_EmptyDatabase_IsValidEmptyDataset(t *testing.T) {
	e, _, _ := newTestEngine(t)
	var buf bytes.Buffer

	summary, err := e.ExportObservations(context.Background(), &buf)
	if err != nil {
		t.Fatalf("ExportObservations: %v", err)
	}
	if summary.Rows != 0 || summary.Sessions != 0 || summary.TurnBoundaryRows != 0 {
		t.Errorf("summary = %+v, want all zeros", summary)
	}
	if buf.Len() != 0 {
		t.Errorf("expected zero output bytes for an empty dataset, got %q", buf.String())
	}
}

func TestExportObservations_SeededSeries_WhitelistedLinesInOrder(t *testing.T) {
	e, db, _ := newTestEngine(t)
	ctx := context.Background()

	// sessB's rows are INSERTED first: the export must still order by
	// session_id before occurred_at, so sessA's whole series precedes
	// sessB's despite later rowids.
	seedEvent(t, db, "b-usage", "provider.usage.observed", newTime.Add(1*time.Minute), "sessB", "",
		`{"total_cost_usd":9.75,"total_duration_ms":1000}`)
	seedEvent(t, db, "b-failed", "provider.turn.failed", newTime.Add(2*time.Minute), "sessB", "",
		`{"failure_class":"provider_rate_limit","error_message_len":42,"raw_status_code":429}`)

	seedEvent(t, db, "a-start", "provider.turn.started", newTime, "sessA", "turn-1",
		`{"prompt_sha256":"deadbeef","prompt_byte_length":42,"prompt_approx_tokens":10,"cwd":"/tmp/repo1"}`)
	seedEvent(t, db, "a-usage", "provider.usage.observed", newTime.Add(1*time.Minute), "sessA", "",
		`{"total_cost_usd":0.5,"total_duration_ms":60000,"total_api_duration_ms":30000,"total_lines_added":10,"total_lines_removed":2,"model_id":"claude-fable-5","effort":"high"}`)
	// Same occurred_at as a-usage: rowid (insertion order) breaks the tie.
	seedEvent(t, db, "a-context", "provider.context.observed", newTime.Add(1*time.Minute), "sessA", "",
		`{"used_tokens":12000,"window_tokens":200000,"used_percent":6}`)
	seedEvent(t, db, "a-quota", "provider.quota.observed", newTime.Add(2*time.Minute), "sessA", "",
		`{"limit_id":"five_hour","used_percent":12.5,"resets_at":"2026-06-14T15:00:00Z"}`)
	seedEvent(t, db, "a-done", "provider.turn.completed", newTime.Add(3*time.Minute), "sessA", "",
		`{"stop_hook_active":false,"effort":"high"}`)

	// Event types OUTSIDE the covered set must not appear at all.
	seedEvent(t, db, "a-ratelimit", "provider.rate_limit.hit", newTime.Add(2*time.Minute), "sessA", "",
		`{"failure_class":"provider_rate_limit","raw_status_code":429}`)
	seedEvent(t, db, "a-session", "provider.session.started", newTime, "sessA", "", `{}`)

	var buf bytes.Buffer
	summary, err := e.ExportObservations(ctx, &buf)
	if err != nil {
		t.Fatalf("ExportObservations: %v", err)
	}
	if summary.Rows != 7 || summary.Sessions != 2 || summary.TurnBoundaryRows != 3 {
		t.Fatalf("summary = %+v, want Rows=7 Sessions=2 TurnBoundaryRows=3", summary)
	}

	records := decodeObservationLines(t, buf.Bytes())
	if len(records) != 7 {
		t.Fatalf("got %d records, want 7", len(records))
	}
	wantOrder := []string{
		"provider.turn.started",     // sessA newTime
		"provider.usage.observed",   // sessA +1m (rowid before a-context)
		"provider.context.observed", // sessA +1m
		"provider.quota.observed",   // sessA +2m
		"provider.turn.completed",   // sessA +3m
		"provider.usage.observed",   // sessB +1m
		"provider.turn.failed",      // sessB +2m
	}
	for i, want := range wantOrder {
		if records[i].EventType != want {
			t.Fatalf("record[%d].EventType = %q, want %q (full order: %+v)", i, records[i].EventType, want, records)
		}
	}
	for i, rec := range records {
		if rec.SchemaVersion != ObservationsSchemaVersion {
			t.Errorf("record[%d].SchemaVersion = %q", i, rec.SchemaVersion)
		}
	}

	start, usage, contextRec, quota, done, bUsage, bFailed :=
		records[0], records[1], records[2], records[3], records[4], records[5], records[6]

	if start.SessionID == nil || *start.SessionID != "sessA" || start.TurnID == nil || *start.TurnID != "turn-1" {
		t.Errorf("turn.started identity: %+v", start)
	}
	// turn.started's payload carries NOTHING whitelisted — every
	// projection field must be honestly absent, not zero-filled.
	if start.TotalCostUSD != nil || start.UsedTokens != nil || start.Effort != nil || start.FailureClass != nil {
		t.Errorf("turn.started leaked projection fields: %+v", start)
	}

	if usage.TotalCostUSD == nil || *usage.TotalCostUSD != 0.5 {
		t.Errorf("usage total_cost_usd = %v", usage.TotalCostUSD)
	}
	if usage.TotalDurationMs == nil || *usage.TotalDurationMs != 60000 ||
		usage.TotalAPIDurationMs == nil || *usage.TotalAPIDurationMs != 30000 {
		t.Errorf("usage durations: %+v", usage)
	}
	if usage.TotalLinesAdded == nil || *usage.TotalLinesAdded != 10 ||
		usage.TotalLinesRemoved == nil || *usage.TotalLinesRemoved != 2 {
		t.Errorf("usage line counts: %+v", usage)
	}
	if usage.ModelID == nil || *usage.ModelID != "claude-fable-5" || usage.Effort == nil || *usage.Effort != "high" {
		t.Errorf("usage #20 labels: model=%v effort=%v", usage.ModelID, usage.Effort)
	}

	if contextRec.UsedTokens == nil || *contextRec.UsedTokens != 12000 ||
		contextRec.WindowTokens == nil || *contextRec.WindowTokens != 200000 ||
		contextRec.UsedPercent == nil || *contextRec.UsedPercent != 6 {
		t.Errorf("context fields: %+v", contextRec)
	}

	if quota.LimitID == nil || *quota.LimitID != "five_hour" ||
		quota.UsedPercent == nil || *quota.UsedPercent != 12.5 ||
		quota.ResetsAt == nil || *quota.ResetsAt != "2026-06-14T15:00:00Z" {
		t.Errorf("quota fields: %+v", quota)
	}

	if done.Effort == nil || *done.Effort != "high" {
		t.Errorf("turn.completed effort label: %+v", done)
	}
	if bUsage.SessionID == nil || *bUsage.SessionID != "sessB" || bUsage.TotalCostUSD == nil || *bUsage.TotalCostUSD != 9.75 {
		t.Errorf("sessB usage: %+v", bUsage)
	}
	if bFailed.FailureClass == nil || *bFailed.FailureClass != "provider_rate_limit" {
		t.Errorf("turn.failed failure_class: %+v", bFailed)
	}
}

func TestExportObservations_NoPromptsPathsOrUnlistedFieldsInOutput(t *testing.T) {
	// FR-171 pin: whitelist projection, not blacklist scrubbing. Seed
	// covered event types whose payloads carry every prompt/path-shaped
	// field the pipeline actually writes (normalizer.go's turn.started
	// payload) PLUS a hostile future-producer field (canonical_root) —
	// none may appear in the output, as key or value.
	e, db, _ := newTestEngine(t)
	ctx := context.Background()

	seedCore(t, db)
	seedEvent(t, db, "ev-start", "provider.turn.started", newTime, "sess1", "turn-1",
		`{"prompt_sha256":"deadbeef","prompt_byte_length":42,"prompt_approx_tokens":10,"cwd":"/tmp/repo1"}`)
	seedEvent(t, db, "ev-usage", "provider.usage.observed", newTime.Add(time.Minute), "sess1", "",
		`{"total_cost_usd":1.5,"canonical_root":"/tmp/repo1"}`)
	seedEvent(t, db, "ev-done", "provider.turn.completed", newTime.Add(2*time.Minute), "sess1", "",
		`{"stop_hook_active":true,"effort":"low"}`)
	seedEvent(t, db, "ev-fail", "provider.turn.failed", newTime.Add(3*time.Minute), "sess1", "",
		`{"failure_class":"provider_error","error_message_len":99,"raw_error_type":"boom","raw_status_code":500}`)

	var buf bytes.Buffer
	summary, err := e.ExportObservations(ctx, &buf)
	if err != nil {
		t.Fatalf("ExportObservations: %v", err)
	}
	if summary.Rows != 4 {
		t.Fatalf("summary = %+v, want Rows=4", summary)
	}

	out := buf.String()
	for _, forbidden := range []string{
		"prompt_sha256", "deadbeef", "prompt_byte_length", "prompt_approx_tokens",
		"cwd", "/tmp/repo1", "canonical_root",
		"stop_hook_active", "error_message_len", "raw_error_type", "raw_status_code", "boom",
	} {
		if strings.Contains(out, forbidden) {
			t.Errorf("export leaked %q:\n%s", forbidden, out)
		}
	}
	// And the honest fields still made it through the projection.
	if !strings.Contains(out, `"total_cost_usd":1.5`) || !strings.Contains(out, `"failure_class":"provider_error"`) {
		t.Errorf("whitelisted fields missing:\n%s", out)
	}
}
