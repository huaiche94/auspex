// daemon_test.go: the Run lifecycle (§23.1-23.3) — singleton lock, token
// rotation, metadata publish/remove, authenticated HTTP round-trip, SSE
// delivery, graceful ctx shutdown. Uses the same e2e DB/service helpers as
// e2e_test.go (same package).
package daemon_test

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/huaiche94/auspex/internal/daemon"
	"github.com/huaiche94/auspex/internal/httpapi"
	"github.com/huaiche94/auspex/internal/pause"
	"github.com/huaiche94/auspex/internal/scheduler"
	protocol "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// startTestDaemon composes a real Daemon over a migrated temp DB and runs
// it, returning its RunInfo, broker, and a stop func that asserts clean
// shutdown.
func startTestDaemon(t *testing.T) (daemon.RunInfo, *daemon.Broker, string, string, func()) {
	t.Helper()
	return startTestDaemonIn(t, t.TempDir(), t.TempDir())
}

// startTestDaemonIn is startTestDaemon against caller-owned dirs, so a
// restart test can reuse the SAME data/runtime dirs across two runs.
func startTestDaemonIn(t *testing.T, dataDir, runtimeDir string) (daemon.RunInfo, *daemon.Broker, string, string, func()) {
	t.Helper()
	db := openMigratedE2EDB(t)
	clk := &e2eClock{t: time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC)}
	store := pause.NewSQLiteStore(db)
	wakes := scheduler.NewStore(db.Conn(), clk, &e2eIDs{prefix: "wj"})
	svc := e2eService(t, db, store, wakes, clk, "pause")
	broker := daemon.NewBroker()

	d := daemon.New(daemon.Config{
		DataDir:    dataDir,
		RuntimeDir: runtimeDir,
		Version:    "test-1",
		Clock:      clk,
		Worker: daemon.NewWorker(daemon.WorkerDeps{
			Jobs: wakes, Pause: svc, PauseStore: store, Clock: clk,
			Events: broker, PollInterval: 50 * time.Millisecond,
		}),
		NewHandler: func(token string) http.Handler {
			return httpapi.NewHandler(httpapi.Deps{
				Version: "test-1", StartedAt: clk.Now(), Clock: clk,
				Jobs: wakes, Events: broker,
			}, token)
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan daemon.RunInfo, 1)
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx, func(info daemon.RunInfo) { ready <- info }) }()

	var info daemon.RunInfo
	select {
	case info = <-ready:
	case err := <-done:
		t.Fatalf("daemon exited before ready: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("daemon never became ready")
	}
	stop := func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run returned %v on ctx cancel, want nil (graceful)", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("daemon never shut down")
		}
	}
	return info, broker, dataDir, runtimeDir, stop
}

func authedGet(t *testing.T, address, path, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+address+path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func TestDaemon_RunLifecycle(t *testing.T) {
	info, broker, dataDir, runtimeDir, stop := startTestDaemon(t)

	// Token file: exists, 0600, matches RunInfo's advertised path.
	tokenBytes, err := os.ReadFile(info.TokenFile)
	if err != nil {
		t.Fatalf("token file: %v", err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if runtime.GOOS != "windows" {
		// POSIX permission bits don't map onto Windows ACLs (0600 reads
		// back as 0666 there) — same GOOS guard as config/sqlite's
		// permission tests (issue #24 precedent).
		if fi, err := os.Stat(info.TokenFile); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("token file mode = %v (err %v), want 0600", fi.Mode().Perm(), err)
		}
	}
	if !strings.HasPrefix(info.TokenFile, dataDir) {
		t.Errorf("token file %q not under data dir %q (D-16)", info.TokenFile, dataDir)
	}

	// Metadata: published, schema-versioned, pointing at the live address.
	meta, found, err := daemon.ReadMetadata(runtimeDir)
	if err != nil || !found {
		t.Fatalf("ReadMetadata = found %v, err %v", found, err)
	}
	if meta.Address != info.Address || meta.PID != os.Getpid() || meta.TokenFile != info.TokenFile {
		t.Errorf("metadata %+v does not match RunInfo %+v", meta, info)
	}

	// Authenticated round-trip: health is ok with the token…
	resp := authedGet(t, info.Address, "/v1/health", token)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/v1/health with token = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()
	// …and 401 without or with a wrong one (never a silent 200).
	for name, wrong := range map[string]string{"no token": "", "wrong token": "deadbeef"} {
		resp := authedGet(t, info.Address, "/v1/health", wrong)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: /v1/health = %d, want 401", name, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
	// Non-loopback Host header: rejected regardless of a valid token.
	req, _ := http.NewRequest(http.MethodGet, "http://"+info.Address+"/v1/health", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Host = "evil.example.com"
	if resp, err := http.DefaultClient.Do(req); err == nil {
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("forged Host = %d, want 403", resp.StatusCode)
		}
		_ = resp.Body.Close()
	} else {
		t.Errorf("forged-Host request: %v", err)
	}

	// Second daemon on the same runtime dir: refused by the singleton lock.
	second := daemon.New(daemon.Config{
		DataDir: dataDir, RuntimeDir: runtimeDir, Version: "test-1",
		Clock:      &e2eClock{t: time.Now()},
		Worker:     daemon.NewWorker(daemon.WorkerDeps{}),
		NewHandler: func(string) http.Handler { return http.NewServeMux() },
	})
	if err := second.Run(context.Background(), nil); !errors.Is(err, daemon.ErrAlreadyRunning) {
		t.Errorf("second Run = %v, want ErrAlreadyRunning", err)
	}

	// SSE: an event published to the broker reaches a streaming client.
	sseReq, _ := http.NewRequest(http.MethodGet, "http://"+info.Address+"/v1/events/stream", nil)
	sseReq.Header.Set("Authorization", "Bearer "+token)
	sseResp, err := http.DefaultClient.Do(sseReq)
	if err != nil {
		t.Fatalf("SSE connect: %v", err)
	}
	defer func() { _ = sseResp.Body.Close() }()
	broker.Publish(protocol.Event{
		SchemaVersion: protocol.SchemaVersionEvent,
		EventType:     protocol.EventPauseWakeTriggered,
	})
	lines := make(chan string, 8)
	go func() {
		scanner := bufio.NewScanner(sseResp.Body)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	sawEvent := false
	timeout := time.After(5 * time.Second)
	for !sawEvent {
		select {
		case line := <-lines:
			if strings.HasPrefix(line, "event: "+string(protocol.EventPauseWakeTriggered)) {
				sawEvent = true
			}
		case <-timeout:
			t.Fatal("SSE stream never delivered the published event")
		}
	}

	// Graceful shutdown: Run returns nil, metadata is gone.
	stop()
	if _, found, _ := daemon.ReadMetadata(runtimeDir); found {
		t.Error("metadata still present after graceful shutdown")
	}
}

// TestDaemon_TokenRotatesPerRestart: §27.5 — a restart over the SAME data
// dir mints a NEW token at the SAME path, and the old value stops
// authenticating.
func TestDaemon_TokenRotatesPerRestart(t *testing.T) {
	dataDir, runtimeDir := t.TempDir(), t.TempDir()
	info1, _, _, _, stop1 := startTestDaemonIn(t, dataDir, runtimeDir)
	token1Bytes, err := os.ReadFile(info1.TokenFile)
	if err != nil {
		t.Fatalf("first token: %v", err)
	}
	stop1()

	info2, _, _, _, stop2 := startTestDaemonIn(t, dataDir, runtimeDir)
	defer stop2()
	if info2.TokenFile != info1.TokenFile {
		t.Errorf("token path moved across restarts: %q -> %q (clients discover it at ONE stable path, D-16)", info1.TokenFile, info2.TokenFile)
	}
	token2Bytes, err := os.ReadFile(info2.TokenFile)
	if err != nil {
		t.Fatalf("second token: %v", err)
	}
	if strings.TrimSpace(string(token1Bytes)) == strings.TrimSpace(string(token2Bytes)) {
		t.Error("token did not rotate across restarts (§27.5)")
	}
	resp := authedGet(t, info2.Address, "/v1/health", strings.TrimSpace(string(token1Bytes)))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("old token on new daemon = %d, want 401", resp.StatusCode)
	}
	_ = resp.Body.Close()
}
