// appserver_run_test.go — the M7-acceptance "fake App Server E2E" for
// the managed runner's ADR-013 primary path (issue #9 M7 Phase 2 slice
// B): gate → handshake → thread/turn lifecycle → scripted notification
// stream → normalized event batch, all against a compiled fake App
// Server (Constitution §5 rule 4: fixtures, never a live account).
package managed

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/huaiche94/auspex/internal/app"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"

	"bytes"
)

func buildFakeAppServer(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fake-codex")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "./testdata/fakeappserver")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build testdata/fakeappserver: %v\n%s", err, out)
	}
	return bin
}

func newAppServerRunner(persister *runTestPersister, bin string) *Runner {
	r := newTestRunner(persister, allowingEvaluation(app.PolicyRun), bin)
	r.PreferAppServer = true
	return r
}

func TestRunner_Run_Codex_AppServer_E2E(t *testing.T) {
	bin := buildFakeAppServer(t)
	t.Setenv("AUSPEX_FAKE_AS_EVENTS", fixtureAbs(t, "appserver_events.jsonl"))

	persister := &runTestPersister{}
	runner := newAppServerRunner(persister, bin)

	var human bytes.Buffer
	req := baseCodexRunRequest()
	req.HumanLog = &human

	outcome, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !outcome.UsedAppServer {
		t.Fatal("UsedAppServer = false, want the ADR-013 primary path")
	}
	if outcome.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0 (terminal notification observed)", outcome.ExitCode)
	}
	as := outcome.AppServer
	if as.ThreadID != "th_fake" || as.ModelID != "gpt-5.3-codex" || as.TurnStatus != "completed" || as.ItemCount != 2 || as.ConnectionLost {
		t.Errorf("AppServer summary = %+v", as)
	}

	if len(persister.calls) != 2 {
		t.Fatalf("persister.calls = %d batches, want 2 (gate, terminal)", len(persister.calls))
	}
	terminal := persister.calls[1]

	started := eventOfType(t, terminal, v1.EventProviderSessionStarted)
	if started.Payload["thread_id"] != "th_fake" || started.Payload["model_id"] != "gpt-5.3-codex" {
		t.Errorf("session.started payload = %+v", started.Payload)
	}

	completed := eventOfType(t, terminal, v1.EventProviderTurnCompleted)
	if completed.Payload["turn_status"] != "completed" || completed.Payload["item_count"] != 2 ||
		completed.Payload["duration_ms"] != int64(4321) {
		t.Errorf("turn.completed payload = %+v", completed.Payload)
	}
	if completed.TurnID != string(outcome.TurnID) {
		t.Errorf("turn.completed TurnID = %q, want %q (one run, one TurnID)", completed.TurnID, outcome.TurnID)
	}

	// Normalized token vocabulary (ADR-0056: identical to every other
	// codex producer): fresh input = 5200-4000, total = fresh + output.
	usage := eventOfType(t, terminal, v1.EventProviderUsageObserved)
	if usage.Payload["input_tokens"] != int64(1200) ||
		usage.Payload["cache_read_input_tokens"] != int64(4000) ||
		usage.Payload["output_tokens"] != int64(450) ||
		usage.Payload["reasoning_output_tokens"] != int64(120) ||
		usage.Payload["total_tokens"] != int64(1650) ||
		usage.Payload["model_id"] != "gpt-5.3-codex" {
		t.Errorf("usage payload = %+v", usage.Payload)
	}

	ctxEv := eventOfType(t, terminal, v1.EventProviderContextObserved)
	if ctxEv.Payload["window_tokens"] != int64(272000) || ctxEv.Payload["used_tokens"] != int64(5650) {
		t.Errorf("context payload = %+v", ctxEv.Payload)
	}

	var quotaIDs []string
	for _, ev := range terminal {
		if ev.EventType == v1.EventProviderQuotaObserved {
			quotaIDs = append(quotaIDs, ev.Payload["limit_id"].(string))
			if ev.Payload["plan_type"] != "plus" {
				t.Errorf("quota payload missing plan_type: %+v", ev.Payload)
			}
		}
	}
	if len(quotaIDs) != 2 || quotaIDs[0] != "primary" || quotaIDs[1] != "secondary" {
		t.Errorf("quota windows = %v, want [primary secondary]", quotaIDs)
	}
}

func TestRunner_Run_Codex_AppServer_FallsBackToExec(t *testing.T) {
	bin := buildFakeAppServer(t)
	t.Setenv("AUSPEX_FAKE_AS_FAIL", "1")
	t.Setenv("AUSPEX_FAKE_STREAM_FILE", codexExecFixtureAbs(t, "normal.jsonl"))

	persister := &runTestPersister{}
	runner := newAppServerRunner(persister, bin)

	var human bytes.Buffer
	req := baseCodexRunRequest()
	req.HumanLog = &human

	outcome, err := runner.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if outcome.UsedAppServer {
		t.Fatal("UsedAppServer = true, want the exec fallback after a failed handshake")
	}
	if outcome.Codex.Completed == nil {
		t.Error("exec fallback did not parse the stream (Codex.Completed nil)")
	}
	if !strings.Contains(human.String(), "falling back to exec --json") {
		t.Errorf("HumanLog = %q, want the loud ADR-013 fallback line", human.String())
	}
}

func TestRunner_Run_Codex_AppServer_BlockNeverStartsThread(t *testing.T) {
	bin := buildFakeAppServer(t)
	t.Setenv("AUSPEX_FAKE_AS_EVENTS", fixtureAbs(t, "appserver_events.jsonl"))

	persister := &runTestPersister{}
	runner := newTestRunner(persister, allowingEvaluation(app.PolicyBlock), bin)
	runner.PreferAppServer = true

	_, err := runner.Run(context.Background(), baseCodexRunRequest())
	if err == nil {
		t.Fatal("Run: want the typed unauthorized error on BLOCK")
	}
	// Only the gate batch: no thread was started, no terminal events —
	// the handshake's initialize is the only frame that crossed the wire
	// before the gate refused (no prompt ever reached the provider).
	if len(persister.calls) != 1 {
		t.Errorf("persister.calls = %d batches, want 1 (gate only)", len(persister.calls))
	}
}
