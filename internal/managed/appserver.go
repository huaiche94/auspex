// appserver.go is the App Server execution path for managed codex
// one-shots (issue #9 M7 Phase 2 slice B): ADR-013 makes the App Server
// the PRIMARY managed path with `codex exec --json` as the fallback, and
// ADR-0056 authorizes the stream as a parsed source and pins its event
// mapping (implemented by internal/telemetry/codex.NormalizeAppServerRun).
//
// The path is selected by Runner.PreferAppServer (wired true by the
// production CLI, left false by default so every argv-stream test and
// consumer keeps its exact pre-slice-B behavior): dial + initialize under
// a short handshake timeout, fall back LOUDLY to the exec spec path on
// any failure — a degraded transport must never make `auspex run`
// unusable (the same fail-open posture as the gate).
package managed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/huaiche94/auspex/internal/buildinfo"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/providers/codex/appserver"
	codextelemetry "github.com/huaiche94/auspex/internal/telemetry/codex"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// appServerHandshakeTimeout bounds dial+initialize before the exec
// fallback takes over. A binary without the app-server subcommand fails
// much faster (spawn error or immediate EOF); the timeout only bounds a
// pathological hang.
const appServerHandshakeTimeout = 5 * time.Second

// appServerDrainTimeout bounds how long, after a turn/interrupt request,
// the runner keeps draining for the interrupted terminal notification
// before honestly recording a lost connection.
const appServerDrainTimeout = 10 * time.Second

// AppServerSummary is RunOutcome's app-server attribution block
// (populated only when UsedAppServer).
type AppServerSummary struct {
	ThreadID       string
	ModelID        string
	TurnStatus     string
	ItemCount      int
	ConnectionLost bool
}

// dialAppServer spawns `<bin> app-server` and performs the §21.2
// handshake under appServerHandshakeTimeout. The spawned process's
// lifetime is tied to runCtx (the whole run), not the handshake timeout.
func dialAppServer(runCtx context.Context, bin string) (*appserver.Client, error) {
	client, err := appserver.Dial(runCtx, bin)
	if err != nil {
		return nil, err
	}
	hctx, cancel := context.WithTimeout(runCtx, appServerHandshakeTimeout)
	defer cancel()
	if _, err := client.Initialize(hctx, appserver.ClientInfo{
		Name:    "auspex",
		Title:   "Auspex",
		Version: buildinfo.Version,
	}); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("managed: app-server handshake: %w", err)
	}
	return client, nil
}

// runAppServerTurn drives one thread/start → turn/start → notification
// stream to the turn's terminal notification, returning the privacy-safe
// outcome NormalizeAppServerRun consumes. It never returns an error: a
// broken connection is recorded as ConnectionLost and normalized to an
// honest provider.turn.failed, mirroring the exec path's "a provider
// that dies is attribution data, not a Run error" contract.
func (r *Runner) runAppServerTurn(ctx context.Context, req RunRequest, turnID domain.TurnID, client *appserver.Client, humanLog io.Writer) codextelemetry.AppServerRunOutcome {
	o := codextelemetry.AppServerRunOutcome{
		SessionID:  req.SessionID,
		TurnID:     turnID,
		WorktreeID: req.WorktreeID,
		TaskID:     req.TaskID,
	}

	thread, err := client.StartThread(ctx, appserver.ThreadStartParams{Cwd: req.Dir})
	if err != nil {
		_, _ = fmt.Fprintf(humanLog, "auspex run: app-server thread/start failed: %v\n", err)
		o.ConnectionLost = true
		return o
	}
	o.ThreadSeen, o.ThreadID, o.ModelID = true, thread.Thread.ID, thread.Model

	turnRes, err := client.StartTurn(ctx, appserver.TurnStartParams{
		ThreadID: thread.Thread.ID,
		Input:    appserver.TextInput(req.Prompt),
	})
	if err != nil {
		_, _ = fmt.Fprintf(humanLog, "auspex run: app-server turn/start failed: %v\n", err)
		o.ConnectionLost = true
		return o
	}
	providerTurnID := turnRes.Turn.ID

	notifications := client.Notifications()
	serverReqs := client.ServerRequests()
	ctxDone := ctx.Done()
	var drainDeadline <-chan time.Time

	for {
		select {
		case <-ctxDone:
			// One protocol-level interrupt attempt (§21.6 step 5's call,
			// without the full checkpoint sequence — that is slice C),
			// then a bounded drain for the interrupted terminal.
			ctxDone = nil
			ictx, icancel := context.WithTimeout(context.Background(), 3*time.Second)
			if err := client.InterruptTurn(ictx, thread.Thread.ID, providerTurnID); err != nil {
				_, _ = fmt.Fprintf(humanLog, "auspex run: turn/interrupt failed (%v); closing the connection\n", err)
			}
			icancel()
			timer := time.NewTimer(appServerDrainTimeout)
			defer timer.Stop()
			drainDeadline = timer.C

		case <-drainDeadline:
			o.ConnectionLost = !o.TerminalSeen
			return o

		case sr, ok := <-serverReqs:
			if !ok {
				serverReqs = nil
				continue
			}
			// One-shot runs are non-interactive: decline, never
			// auto-approve (ADR-0056 §4) — an error response is the
			// protocol-safe deny regardless of per-method result shapes.
			_ = client.RespondTo(sr.ID, nil, &appserver.RPCError{
				Code:    -32000,
				Message: "auspex one-shot run declines interactive approval requests",
			})
			_, _ = fmt.Fprintf(humanLog, "auspex run: declined provider approval request %s (one-shot is non-interactive)\n", sr.Method)

		case n, ok := <-notifications:
			if !ok {
				o.ConnectionLost = !o.TerminalSeen
				return o
			}
			switch n.Method {
			case appserver.MethodThreadTokenUsage:
				var u appserver.ThreadTokenUsageNotification
				if json.Unmarshal(n.Params, &u) == nil && u.ThreadID == thread.Thread.ID {
					total := u.TokenUsage.Total
					o.Usage = &total
					o.ModelContextWindow = u.TokenUsage.ModelContextWindow
				}
			case appserver.MethodAccountRateLimits:
				var rl appserver.RateLimitsNotification
				if json.Unmarshal(n.Params, &rl) == nil {
					snapshot := rl.RateLimits
					o.RateLimits = &snapshot
				}
			case appserver.MethodTurnCompleted:
				var tn appserver.TurnNotification
				if json.Unmarshal(n.Params, &tn) != nil || tn.Turn.ID != providerTurnID {
					continue
				}
				o.TerminalSeen = true
				o.Status = tn.Turn.Status
				o.ItemCount = tn.Turn.ItemCount()
				o.DurationMs = tn.Turn.DurationMs
				if tn.Turn.Error != nil {
					l := tn.Turn.Error.MessageLen
					o.FailureMessageLen = &l
				}
				return o
			default:
				// Unknown/unconsumed notifications tolerated (§21.7);
				// the client already counts what it could not route.
			}
		}
	}
}

// persistAppServerTerminal normalizes and persists the app-server run's
// terminal batch, mirroring persistTerminal's degrade discipline.
func (r *Runner) persistAppServerTerminal(ctx context.Context, o codextelemetry.AppServerRunOutcome) int {
	events := codextelemetry.NewNormalizer(r.Hooks.Clock, r.Hooks.IDs).NormalizeAppServerRun(o, r.Hooks.Clock.Now())
	return r.persistEvents(ctx, events)
}

// persistEvents correlates and persists a prebuilt event batch (the
// spec-path persistTerminal's tail, shared).
func (r *Runner) persistEvents(ctx context.Context, events []v1.Event) int {
	r.Hooks.Correlator.Correlate(ctx, events)
	if r.Hooks.Persister == nil || r.Hooks.TxRunner == nil {
		return 0
	}
	if err := r.Hooks.Persister.PersistAll(ctx, r.Hooks.TxRunner, events); err != nil {
		return 0
	}
	return len(events)
}
