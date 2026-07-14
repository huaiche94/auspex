package claude

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "provider-events", "claude", "statusline", name))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return b
}

func ptr[T any](v T) *T { return &v }

func TestParseStatusLine(t *testing.T) {
	tests := []struct {
		name        string
		fixture     string
		wantErr     bool
		wantErrCode domain.ErrorCode
		check       func(t *testing.T, snap StatusLineSnapshot)
	}{
		{
			name:    "normal",
			fixture: "normal.json",
			check: func(t *testing.T, snap StatusLineSnapshot) {
				if snap.SessionID != "sess_01H9X8K7QZ3M4N5P6R7S8T9V0W" {
					t.Errorf("SessionID = %q", snap.SessionID)
				}
				if snap.ModelID == nil || *snap.ModelID != "claude-opus-4-1-20250805" {
					t.Errorf("ModelID = %v", snap.ModelID)
				}
				if snap.ContextInputTokens == nil || *snap.ContextInputTokens != 42000 {
					t.Errorf("ContextInputTokens = %v", snap.ContextInputTokens)
				}
				if snap.ContextUsedPercent == nil || *snap.ContextUsedPercent != 21.9 {
					t.Errorf("ContextUsedPercent = %v", snap.ContextUsedPercent)
				}
				if snap.TotalCostUSD == nil || *snap.TotalCostUSD != 1.2345 {
					t.Errorf("TotalCostUSD = %v", snap.TotalCostUSD)
				}
				if snap.TotalLinesAdded == nil || *snap.TotalLinesAdded != 128 {
					t.Errorf("TotalLinesAdded = %v", snap.TotalLinesAdded)
				}
				if snap.FiveHourUsedPercent == nil || *snap.FiveHourUsedPercent != 42.5 {
					t.Errorf("FiveHourUsedPercent = %v", snap.FiveHourUsedPercent)
				}
				wantReset := time.Date(2026, 7, 12, 18, 0, 0, 0, time.UTC)
				if snap.FiveHourResetsAt == nil || !snap.FiveHourResetsAt.Equal(wantReset) {
					t.Errorf("FiveHourResetsAt = %v, want %v", snap.FiveHourResetsAt, wantReset)
				}
				if snap.SevenDayUsedPercent == nil || *snap.SevenDayUsedPercent != 11.2 {
					t.Errorf("SevenDayUsedPercent = %v", snap.SevenDayUsedPercent)
				}
			},
		},
		{
			name:    "missing_fields",
			fixture: "missing_fields.json",
			check: func(t *testing.T, snap StatusLineSnapshot) {
				if snap.SessionID != "sess_01H9X8K7QZ3M4N5P6R7S8T9V0X" {
					t.Errorf("SessionID = %q", snap.SessionID)
				}
				// All null-valued fields must decode to nil, never 0.
				if snap.ContextInputTokens != nil {
					t.Errorf("ContextInputTokens = %v, want nil", *snap.ContextInputTokens)
				}
				if snap.ContextUsedPercent != nil {
					t.Errorf("ContextUsedPercent = %v, want nil", *snap.ContextUsedPercent)
				}
				if snap.TotalCostUSD != nil {
					t.Errorf("TotalCostUSD = %v, want nil", *snap.TotalCostUSD)
				}
				// rate_limits present but empty object -> both windows nil.
				if snap.FiveHourUsedPercent != nil || snap.FiveHourResetsAt != nil {
					t.Errorf("expected nil five-hour quota fields, got %v / %v", snap.FiveHourUsedPercent, snap.FiveHourResetsAt)
				}
				// context_window_size WAS present (200000) even though other
				// context fields are null - must still be captured.
				if snap.ContextWindowSize == nil || *snap.ContextWindowSize != 200000 {
					t.Errorf("ContextWindowSize = %v, want 200000", snap.ContextWindowSize)
				}
			},
		},
		{
			name:    "unknown_fields",
			fixture: "unknown_fields.json",
			check: func(t *testing.T, snap StatusLineSnapshot) {
				// Unknown top-level and nested fields must not break parsing
				// and must not appear anywhere in the struct (there's no
				// field to hold them - this is asserted implicitly by
				// compiling/parsing without error).
				if snap.SessionID != "sess_01H9X8K7QZ3M4N5P6R7S8T9V0Z" {
					t.Errorf("SessionID = %q", snap.SessionID)
				}
				if snap.ModelID == nil || *snap.ModelID != "claude-opus-4-1-20250805" {
					t.Errorf("ModelID = %v", snap.ModelID)
				}
				if snap.ContextUsedPercent == nil || *snap.ContextUsedPercent != 7.95 {
					t.Errorf("ContextUsedPercent = %v", snap.ContextUsedPercent)
				}
			},
		},
		{
			name:    "high_usage",
			fixture: "high_usage.json",
			check: func(t *testing.T, snap StatusLineSnapshot) {
				if snap.ContextUsedPercent == nil || *snap.ContextUsedPercent != 98.85 {
					t.Errorf("ContextUsedPercent = %v", snap.ContextUsedPercent)
				}
				if snap.FiveHourUsedPercent == nil || *snap.FiveHourUsedPercent != 97.3 {
					t.Errorf("FiveHourUsedPercent = %v", snap.FiveHourUsedPercent)
				}
				if snap.TotalLinesRemoved == nil || *snap.TotalLinesRemoved != 1876 {
					t.Errorf("TotalLinesRemoved = %v", snap.TotalLinesRemoved)
				}
			},
		},
		{
			name:        "malformed",
			fixture:     "malformed.json",
			wantErr:     true,
			wantErrCode: domain.ErrCodeValidation,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap, err := ParseStatusLine(fixture(t, tt.fixture))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				var derr *domain.Error
				if !errors.As(err, &derr) {
					t.Fatalf("expected *domain.Error, got %T: %v", err, err)
				}
				if derr.Code != tt.wantErrCode {
					t.Fatalf("Code = %q, want %q", derr.Code, tt.wantErrCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tt.check(t, snap)
		})
	}
}

// TestParseStatusLine_ResetsAtEncodings pins issue #27's fix: Claude Code
// sends rate_limits.*.resets_at as Unix epoch seconds (statusline.md), the
// parser originally demanded RFC3339, and because encoding/json aborts the
// whole Unmarshal on one recognized-field mismatch — and rate_limits only
// appears after the session's first API response — every later payload of
// every real session failed wholesale (bare statusline, zero
// quota/context/usage ingest). Both real encodings must parse, and an
// unrecognized shape must degrade to nil while the REST of the snapshot
// (model, session) survives.
func TestParseStatusLine_ResetsAtEncodings(t *testing.T) {
	cases := []struct {
		name string
		raw  string // JSON value for resets_at
		want *time.Time
	}{
		{"epoch seconds", "1783879200", ptr(time.Date(2026, 7, 12, 18, 0, 0, 0, time.UTC))},
		{"epoch with fraction", "1783879200.5", ptr(time.Date(2026, 7, 12, 18, 0, 0, 500000000, time.UTC))},
		{"rfc3339 string", `"2026-07-12T18:00:00Z"`, ptr(time.Date(2026, 7, 12, 18, 0, 0, 0, time.UTC))},
		{"unparseable string degrades to nil", `"soon"`, nil},
		{"object degrades to nil", `{"seconds":1}`, nil},
		{"boolean degrades to nil", "true", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := `{"session_id":"sess_1","model":{"display_name":"Fable 5"},` +
				`"rate_limits":{"five_hour":{"used_percentage":5.1,"resets_at":` + tc.raw + `}}}`
			snap, err := ParseStatusLine([]byte(payload))
			if err != nil {
				t.Fatalf("ParseStatusLine must never fail on a resets_at shape, got: %v", err)
			}
			if snap.ModelDisplayName == nil || *snap.ModelDisplayName != "Fable 5" {
				t.Errorf("ModelDisplayName = %v — the rest of the snapshot must survive", snap.ModelDisplayName)
			}
			if snap.FiveHourUsedPercent == nil || *snap.FiveHourUsedPercent != 5.1 {
				t.Errorf("FiveHourUsedPercent = %v, want 5.1", snap.FiveHourUsedPercent)
			}
			switch {
			case tc.want == nil && snap.FiveHourResetsAt != nil:
				t.Errorf("FiveHourResetsAt = %v, want nil (unknown, not an error)", snap.FiveHourResetsAt)
			case tc.want != nil && (snap.FiveHourResetsAt == nil || !snap.FiveHourResetsAt.Equal(*tc.want)):
				t.Errorf("FiveHourResetsAt = %v, want %v", snap.FiveHourResetsAt, tc.want)
			}
		})
	}
}

func TestParseStatusLine_EmptySessionID(t *testing.T) {
	_, err := ParseStatusLine([]byte(`{"session_id": ""}`))
	if err == nil {
		t.Fatal("expected error for empty session_id")
	}
	var derr *domain.Error
	if !errors.As(err, &derr) || derr.Code != domain.ErrCodeValidation {
		t.Fatalf("expected ErrCodeValidation, got %v", err)
	}
}

func TestStatusLineSnapshot_ContextObservation(t *testing.T) {
	now := time.Now().UTC()

	t.Run("exact when both tokens and window present", func(t *testing.T) {
		snap := StatusLineSnapshot{
			SessionID:           "sess_1",
			ContextInputTokens:  ptr(int64(42000)),
			ContextOutputTokens: ptr(int64(1800)),
			ContextWindowSize:   ptr(int64(200000)),
			ContextUsedPercent:  ptr(21.9),
		}
		obs := snap.ContextObservation(now)
		if obs.Confidence != domain.ConfidenceExact {
			t.Errorf("Confidence = %q, want exact", obs.Confidence)
		}
		if obs.UsedTokens == nil || *obs.UsedTokens != 43800 {
			t.Errorf("UsedTokens = %v, want 43800", obs.UsedTokens)
		}
		if obs.Source != domain.SourceStatusLine {
			t.Errorf("Source = %q", obs.Source)
		}
	})

	t.Run("unavailable when tokens missing", func(t *testing.T) {
		snap := StatusLineSnapshot{SessionID: "sess_1"}
		obs := snap.ContextObservation(now)
		if obs.Confidence != domain.ConfidenceUnavailable {
			t.Errorf("Confidence = %q, want unavailable", obs.Confidence)
		}
		if obs.UsedTokens != nil {
			t.Errorf("UsedTokens = %v, want nil", obs.UsedTokens)
		}
	})
}

func TestStatusLineSnapshot_QuotaObservations_NilWhenAbsent(t *testing.T) {
	snap := StatusLineSnapshot{SessionID: "sess_1"}
	if obs := snap.FiveHourQuotaObservation(time.Now()); obs != nil {
		t.Errorf("FiveHourQuotaObservation = %+v, want nil", obs)
	}
	if obs := snap.SevenDayQuotaObservation(time.Now()); obs != nil {
		t.Errorf("SevenDayQuotaObservation = %+v, want nil", obs)
	}
}

func TestStatusLineSnapshot_QuotaObservations_Present(t *testing.T) {
	now := time.Now().UTC()
	reset := now.Add(2 * time.Hour)
	snap := StatusLineSnapshot{
		SessionID:           "sess_1",
		FiveHourUsedPercent: ptr(42.5),
		FiveHourResetsAt:    &reset,
	}
	obs := snap.FiveHourQuotaObservation(now)
	if obs == nil {
		t.Fatal("expected non-nil observation")
	}
	if obs.LimitID != "five_hour" {
		t.Errorf("LimitID = %q", obs.LimitID)
	}
	if obs.Confidence != domain.ConfidenceHigh {
		t.Errorf("Confidence = %q, want high", obs.Confidence)
	}
	if obs.Provider != "claude" {
		t.Errorf("Provider = %q", obs.Provider)
	}
}
