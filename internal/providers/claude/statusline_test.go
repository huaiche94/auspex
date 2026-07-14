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

// findWindow returns the named rate-limit window, or nil when the snapshot
// did not capture it.
func findWindow(snap StatusLineSnapshot, limitID string) *RateLimitWindow {
	for i := range snap.RateLimitWindows {
		if snap.RateLimitWindows[i].LimitID == limitID {
			return &snap.RateLimitWindows[i]
		}
	}
	return nil
}

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
				// #20 Phase 0: effort.level rides the statusline payload —
				// the continuously-observed effort source.
				if snap.EffortLevel == nil || *snap.EffortLevel != "high" {
					t.Errorf("EffortLevel = %v, want high", snap.EffortLevel)
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
				fiveHour := findWindow(snap, "five_hour")
				if fiveHour == nil || fiveHour.UsedPercent == nil || *fiveHour.UsedPercent != 42.5 {
					t.Errorf("five_hour window = %+v, want used 42.5", fiveHour)
				}
				wantReset := time.Date(2026, 7, 12, 18, 0, 0, 0, time.UTC)
				if fiveHour == nil || fiveHour.ResetsAt == nil || !fiveHour.ResetsAt.Equal(wantReset) {
					t.Errorf("five_hour ResetsAt = %+v, want %v", fiveHour, wantReset)
				}
				sevenDay := findWindow(snap, "seven_day")
				if sevenDay == nil || sevenDay.UsedPercent == nil || *sevenDay.UsedPercent != 11.2 {
					t.Errorf("seven_day window = %+v, want used 11.2", sevenDay)
				}
				// Windows sort by LimitID for deterministic downstream
				// iteration (JSON object order is not stable).
				if len(snap.RateLimitWindows) != 2 || snap.RateLimitWindows[0].LimitID != "five_hour" {
					t.Errorf("RateLimitWindows = %+v, want [five_hour seven_day]", snap.RateLimitWindows)
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
				// rate_limits present but empty object -> no windows.
				if len(snap.RateLimitWindows) != 0 {
					t.Errorf("RateLimitWindows = %+v, want none", snap.RateLimitWindows)
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
				// Issue #21's parser-level pin: the fixture's hypothetical
				// weekly_fable window (an id this build has never heard
				// of, with an unknown field inside it) is captured like
				// any other window.
				unknown := findWindow(snap, "weekly_fable")
				if unknown == nil || unknown.UsedPercent == nil || *unknown.UsedPercent != 61.0 {
					t.Errorf("weekly_fable window = %+v, want used 61.0", unknown)
				}
				if len(snap.RateLimitWindows) != 3 {
					t.Errorf("RateLimitWindows = %d, want 3 (five_hour, seven_day, weekly_fable)", len(snap.RateLimitWindows))
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
				fiveHour := findWindow(snap, "five_hour")
				if fiveHour == nil || fiveHour.UsedPercent == nil || *fiveHour.UsedPercent != 97.3 {
					t.Errorf("five_hour window = %+v, want used 97.3", fiveHour)
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
			fiveHour := findWindow(snap, "five_hour")
			if fiveHour == nil || fiveHour.UsedPercent == nil || *fiveHour.UsedPercent != 5.1 {
				t.Fatalf("five_hour window = %+v, want used 5.1", fiveHour)
			}
			switch {
			case tc.want == nil && fiveHour.ResetsAt != nil:
				t.Errorf("ResetsAt = %v, want nil (unknown, not an error)", fiveHour.ResetsAt)
			case tc.want != nil && (fiveHour.ResetsAt == nil || !fiveHour.ResetsAt.Equal(*tc.want)):
				t.Errorf("ResetsAt = %v, want %v", fiveHour.ResetsAt, tc.want)
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
	if obs := snap.QuotaObservations(time.Now()); obs != nil {
		t.Errorf("QuotaObservations = %+v, want nil", obs)
	}
}

func TestStatusLineSnapshot_QuotaObservations_OnePerWindow(t *testing.T) {
	now := time.Now().UTC()
	reset := now.Add(2 * time.Hour)
	snap := StatusLineSnapshot{
		SessionID: "sess_1",
		RateLimitWindows: []RateLimitWindow{
			{LimitID: "five_hour", UsedPercent: ptr(42.5), ResetsAt: &reset},
			// A window this build has never heard of (issue #21): it
			// must project like any other, named by its own id, at
			// medium confidence (one measurement).
			{LimitID: "weekly_fable", UsedPercent: ptr(61.0)},
		},
	}
	obs := snap.QuotaObservations(now)
	if len(obs) != 2 {
		t.Fatalf("QuotaObservations = %d, want 2", len(obs))
	}
	if obs[0].LimitID != "five_hour" || obs[0].LimitName != "5h rolling usage" {
		t.Errorf("obs[0] = %q/%q, want five_hour with its friendly name", obs[0].LimitID, obs[0].LimitName)
	}
	if obs[0].Confidence != domain.ConfidenceHigh {
		t.Errorf("obs[0].Confidence = %q, want high (both measurements)", obs[0].Confidence)
	}
	if obs[0].Provider != "claude" {
		t.Errorf("obs[0].Provider = %q", obs[0].Provider)
	}
	if obs[1].LimitID != "weekly_fable" || obs[1].LimitName != "weekly_fable" {
		t.Errorf("obs[1] = %q/%q, want the unknown id named by itself", obs[1].LimitID, obs[1].LimitName)
	}
	if obs[1].Confidence != domain.ConfidenceMedium {
		t.Errorf("obs[1].Confidence = %q, want medium (one measurement)", obs[1].Confidence)
	}
}

func TestStatusLineSnapshot_WeeklyLimitUsedPercent(t *testing.T) {
	snap := StatusLineSnapshot{
		SessionID: "sess_1",
		RateLimitWindows: []RateLimitWindow{
			{LimitID: "five_hour", UsedPercent: ptr(42.5)},
			{LimitID: "seven_day", UsedPercent: ptr(11.2)},
		},
	}
	if got := snap.WeeklyLimitUsedPercent(); got == nil || *got != 11.2 {
		t.Errorf("WeeklyLimitUsedPercent = %v, want 11.2", got)
	}
	if got := (StatusLineSnapshot{}).WeeklyLimitUsedPercent(); got != nil {
		t.Errorf("WeeklyLimitUsedPercent on empty snapshot = %v, want nil", got)
	}
}
