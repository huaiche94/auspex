// daemon.go: the M6 daemon lifecycle (issue #7; ADD §23.1-23.3) — compose
// the singleton lock, per-restart bearer token, dynamic loopback listener,
// runtime metadata, HTTP API, and the resident worker loop into one
// ctx-driven Run. Signal handling stays with the CLI caller
// (signal.NotifyContext around `daemon run` — ADD §10.1 keeps process
// concerns out of library code); Run itself only knows contexts.
//
// Startup order is dependency order, and shutdown is its reverse:
//
//	lock → token → listener → metadata → serve+work
//	stop serving/working → remove metadata → release lock
//
// Metadata is written LAST before serving (a §23.3 probe that finds the
// file may immediately health-check the address inside it, so the listener
// must already exist) and removed FIRST on shutdown (a probe must not
// find metadata pointing at a dead address any longer than unavoidable;
// after a crash, isStale PID inspection in the lock plus the health-check
// step cover the leftover file).
package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/lock"
)

// LockFileName is the singleton lock's basename inside the runtime dir —
// the exact "one daemon per runtime directory" use internal/lock
// (foundation-04) was built for and, until now, had zero callers.
const LockFileName = "daemon.lock"

// shutdownTimeout bounds http.Server.Shutdown — in-flight requests get
// this long to drain; SSE streams are cut (they reconnect, that is their
// protocol).
const shutdownTimeout = 5 * time.Second

// Config is Daemon's composition surface.
type Config struct {
	// DataDir hosts the bearer token file (D-16). Required.
	DataDir string
	// RuntimeDir hosts the metadata and lock files (paths.Dirs.Runtime's
	// documented purpose). Required.
	RuntimeDir string
	// Version is stamped into the runtime metadata. Required.
	Version string
	// Clock supplies StartedAt. Required.
	Clock domain.Clock
	// Worker is the resident §23.6 loop. Required.
	Worker *Worker
	// NewHandler builds the HTTP API around the freshly-minted bearer
	// token — a constructor rather than a handler value because the token
	// does not exist until Run mints it (rotate-per-restart, §27.5).
	// Required.
	NewHandler func(bearerToken string) http.Handler
}

// RunInfo reports a started daemon's identity to the caller (the CLI
// prints it; tests dial it).
type RunInfo struct {
	Address   string
	TokenFile string
	PID       int
}

// Daemon owns one Run lifecycle.
type Daemon struct {
	cfg Config
}

// New constructs a Daemon.
func New(cfg Config) *Daemon {
	return &Daemon{cfg: cfg}
}

// ErrAlreadyRunning reports a live daemon already holds the runtime
// directory's singleton lock.
var ErrAlreadyRunning = errors.New("daemon: already running (lock held by a live process)")

// Run starts the daemon and blocks until ctx is cancelled or a fatal
// component error occurs, then shuts down gracefully and returns nil on a
// clean ctx-driven exit (cancellation is the REQUESTED outcome of `daemon
// stop`, not an error). onReady, if non-nil, is called exactly once after
// the daemon is fully up (metadata published, API serving, worker running).
func (d *Daemon) Run(ctx context.Context, onReady func(RunInfo)) error {
	cfg := d.cfg
	if cfg.DataDir == "" || cfg.RuntimeDir == "" || cfg.Clock == nil || cfg.Worker == nil || cfg.NewHandler == nil {
		return &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "daemon: Config requires DataDir, RuntimeDir, Clock, Worker, and NewHandler", Retryable: false,
		}
	}
	if err := os.MkdirAll(cfg.RuntimeDir, 0o700); err != nil {
		return fmt.Errorf("daemon: creating runtime dir: %w", err)
	}

	fileLock, err := lock.Acquire(filepath.Join(cfg.RuntimeDir, LockFileName))
	if err != nil {
		if errors.Is(err, lock.ErrLocked) {
			return ErrAlreadyRunning
		}
		return fmt.Errorf("daemon: acquiring singleton lock: %w", err)
	}
	defer func() { _ = fileLock.Release() }()

	token, err := GenerateToken(cfg.DataDir)
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0") // §23.2: loopback only, dynamic port
	if err != nil {
		return fmt.Errorf("daemon: listen: %w", err)
	}

	tokenFile := filepath.Join(cfg.DataDir, TokenFileName)
	if err := WriteMetadata(cfg.RuntimeDir, Metadata{
		PID:       os.Getpid(),
		Address:   listener.Addr().String(),
		TokenFile: tokenFile,
		StartedAt: cfg.Clock.Now(),
		Version:   cfg.Version,
	}); err != nil {
		_ = listener.Close()
		return err
	}
	defer func() { _ = RemoveMetadata(cfg.RuntimeDir) }()

	server := &http.Server{
		Handler:           cfg.NewHandler(token),
		ReadHeaderTimeout: 10 * time.Second, // §23.2: request timeouts
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 2)
	go func() {
		// Any worker error surfacing AFTER cancellation is shutdown noise
		// (a store call interrupted mid-flight), not a component failure —
		// checking runCtx rather than errors.Is(err, context.Canceled)
		// also covers store errors that WRAP the cancellation in a type
		// without Unwrap.
		if err := cfg.Worker.Run(runCtx); err != nil && runCtx.Err() == nil {
			errCh <- fmt.Errorf("daemon: worker: %w", err)
			return
		}
		errCh <- nil
	}()
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("daemon: serve: %w", err)
			return
		}
		errCh <- nil
	}()

	if onReady != nil {
		onReady(RunInfo{Address: listener.Addr().String(), TokenFile: tokenFile, PID: os.Getpid()})
	}

	// Block until the caller cancels or a component dies; either way, tear
	// everything down and drain both goroutines before releasing the lock.
	remaining := 2
	var runErr error
	select {
	case <-ctx.Done():
	case err := <-errCh:
		runErr = err
		remaining--
	}
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
	for ; remaining > 0; remaining-- {
		if err := <-errCh; err != nil && runErr == nil {
			runErr = err
		}
	}
	return runErr
}
