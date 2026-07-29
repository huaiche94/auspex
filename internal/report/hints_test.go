// hints_test.go: #146's cache-shape hints — read-share baseline
// comparison, the drop takeaway, and the per-cohort split.
package report

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// seedShareTurn seeds one closed turn whose completed event carries the
// token accounting and identity labels the share/cohort math reads.
func seedShareTurn(t *testing.T, db *sqlite.DB, ts time.Time, sessionID, turnID string, fresh, cacheRead int64, model, effort string) {
	t.Helper()
	insertEvent(t, db, "provider.turn.completed", ts, "claude", sessionID, turnID,
		fmt.Sprintf(`{"input_tokens":%d,"cache_read_input_tokens":%d,"model_id":%q,"effort":%q}`, fresh, cacheRead, model, effort))
}

func TestGenerateReport_CacheReadShareDropFires(t *testing.T) {
	engine, db := newTestEngine(t)

	// Baseline (10 days ago, pre-window): 8 turns at ~90% read share.
	old := testNow.AddDate(0, 0, -10)
	for i := 0; i < 8; i++ {
		seedShareTurn(t, db, old.Add(time.Duration(i)*time.Minute), "sess-base",
			fmt.Sprintf("turn-b%d", i), 100, 900, "claude-opus-4-8[1m]", "xhigh")
	}
	// Window: 8 turns at ~50% read share (40pp drop).
	for i := 0; i < 8; i++ {
		seedShareTurn(t, db, day(9, i), "sess-win",
			fmt.Sprintf("turn-w%d", i), 500, 500, "claude-opus-4-8[1m]", "xhigh")
	}

	rep, err := engine.GenerateReport(context.Background(), 0)
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}
	h := rep.CacheHygiene
	if h.ReadSharePercent == nil || *h.ReadSharePercent != 50.0 {
		t.Errorf("ReadSharePercent = %v, want 50.0", h.ReadSharePercent)
	}
	if h.BaselineReadSharePercent == nil || *h.BaselineReadSharePercent != 90.0 {
		t.Errorf("BaselineReadSharePercent = %v, want 90.0", h.BaselineReadSharePercent)
	}
	if len(h.Cohorts) != 1 || h.Cohorts[0].Model != "opus" || h.Cohorts[0].Effort != "xhigh" ||
		h.Cohorts[0].ReportingTurns != 8 || h.Cohorts[0].ReadSharePercent != 50.0 {
		t.Errorf("Cohorts = %+v, want one opus/xhigh cohort at 50.0%% over 8 turns", h.Cohorts)
	}

	var drop *Takeaway
	for i := range rep.Takeaways {
		if rep.Takeaways[i].Case == CaseCacheReadShareDrop {
			drop = &rep.Takeaways[i]
		}
	}
	if drop == nil {
		t.Fatal("no cache_read_share_drop takeaway")
	}
	if !drop.Fired {
		t.Fatalf("drop takeaway not fired: %+v", drop)
	}
	for _, want := range []string{"50.0%", "90.0%"} {
		if !strings.Contains(drop.Analysis, want) {
			t.Errorf("drop analysis %q missing %q (the WHY must name both numbers)", drop.Analysis, want)
		}
	}

	text := RenderText(rep)
	for _, want := range []string{"cache-read share: 50.0% (pre-window baseline 90.0%)", "opus/xhigh"} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered report missing %q", want)
		}
	}
}

func TestGenerateReport_CacheReadShare_BelowGateIsAbsent(t *testing.T) {
	engine, db := newTestEngine(t)
	// Only 3 reporting turns — below CacheShareMinReportingTurns.
	for i := 0; i < 3; i++ {
		seedShareTurn(t, db, day(9, i), "sess-few", fmt.Sprintf("turn-f%d", i), 100, 100, "m", "")
	}
	rep, err := engine.GenerateReport(context.Background(), 0)
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}
	if rep.CacheHygiene.ReadSharePercent != nil {
		t.Errorf("ReadSharePercent below the gate = %v, want nil", rep.CacheHygiene.ReadSharePercent)
	}
	for _, tw := range rep.Takeaways {
		if tw.Case == CaseCacheReadShareDrop && tw.Fired {
			t.Error("drop takeaway fired below the reporting gate")
		}
	}
}
