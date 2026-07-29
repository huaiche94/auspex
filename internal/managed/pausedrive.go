// pausedrive.go: the M10 Graceful Pause auto-trigger for managed runs
// (issue #122) — the end-to-end wiring from runway observation to a
// sleeping pause that internal/pause's fully-tested machinery was missing
// a caller for. While Runner.Run's provider process is alive, a small
// heartbeat loop (ADD §20.3's "heartbeat every 5s while active") produces
// runway forecast samples for the run's session, feeds each one into a
// pause.Observer (the ADD §17.6/§20.2 debounce/hysteresis trigger,
// observe.go), and — when an observation fires — drives the EXISTING
// frozen pause lifecycle end to end via app.GracefulPauseService:
//
//	RequestPause -> ReachSafePoint (safe-point coordination -> Progress
//	Tree snapshot -> State Checkpoint -> Repository Checkpoint -> pause
//	record -> durable wake job -> provider interrupt) -> Sleeping ->
//	EnterSleep (report the wake job)
//
// Nothing of that lifecycle is rebuilt here: RequestPause/ReachSafePoint/
// EnterSleep are pause.Service's own already-tested composition of
// requestpause.go, safepoint.go, persistphase.go, interrupt.go and
// wake.go. This file adds only (1) the observation pump, (2) the
// trigger-to-lifecycle call sequence, and (3) the one capability that can
// only live here: a real provider interrupt, because the managed runner is
// the process that owns the provider subprocess.
//
// # Managed mode ONLY — why this is not on the hook path
//
// internal/orchestrator/runwaydrive.go:25-28 documents the design
// constraint this file is the other half of: native-hook mode cannot act
// on a pause — a hook process cannot interrupt the provider's interactive
// turn (ADD §8.8), and a fail-open hook must never require the pause
// service's heavy managed-mode dependency graph. So the hook path records
// runway forecasts only (observe-only), while THIS file — which runs
// inside `auspex run`, owning the spawned provider process — is where the
// forecast is allowed to become an actual interrupt.
//
// # The calibration gate (ADD §15.6/§17.6/§20.2)
//
// ADD §20.2's primary trigger ("P(hit) >= 0.80, two consecutive samples")
// is CALIBRATED-only, and no calibration exists yet (M13):
// internal/predictor/runway.Scorer never sets Calibrated=true this phase
// (runway.go's own cold-start contract), so every forecast produced in
// production is uncalibrated. The gate is enforced structurally by
// pause.Observer itself — qualifiesCalibrated requires forecast.Calibrated
// AND a fresh QuotaObservedAt, both failing closed — which means:
//
//   - today, only ADD §17.6's EMERGENCY trigger (used >= 98%, or
//     estimated time-to-limit P50 <= 60s; TriggerReasonEmergency,
//     reason code "emergency_uncalibrated") can fire in production;
//   - the calibrated 0.80 path activates automatically, with no change to
//     this file, the moment a calibrated forecaster starts emitting
//     Calibrated=true forecasts (reason code "calibrated_hit_probability").
//
// This file deliberately never fabricates Calibrated or HitProbability —
// doing so would bypass the gate (Constitution §7 rule 7).
//
// # Fail toward continuing work (never toward killing the session)
//
// A trigger must be safe: every failure in the trigger-to-lifecycle
// sequence BEFORE the provider interrupt (pause request, safe-point
// coordination, any checkpoint write) leaves the provider running and the
// run continuing — pause.PersistThenInterrupt's own ordering guarantee is
// what makes this structural (the interrupt is never attempted unless
// every durable write already succeeded), and this file's own posture is
// to record the failure on the run's human log and return, never to
// escalate it into a run failure. One trigger attempt is made per run: a
// failed attempt does not re-fire every heartbeat (the failure is almost
// certainly structural — a missing task row, a checkpoint fault — and
// hammering checkpoint creation against it would risk the very work the
// pause exists to protect).
package managed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/pause"
	"github.com/huaiche94/auspex/internal/predictor/runway"
)

// Default operational parameters. Interval is ADD §20.3's "heartbeat every
// 5s while active"; the interrupt grace period is this composition's
// bounded stand-in for ADD §20.6 Phase 4's "graceful SIGINT ... timeout 後
// terminate process tree" (the ADD names no specific number; 10s is this
// file's documented operational default, mirroring pause.Service's own
// defaultWakeAfter convention of fixed, documented service-level
// defaults). The lifecycle timeout bounds one whole fired trigger
// (request -> checkpoints -> interrupt -> sleeping) so a wedged
// collaborator can never stall the run's teardown forever.
const (
	DefaultObserveInterval  = 5 * time.Second
	DefaultInterruptGrace   = 10 * time.Second
	DefaultLifecycleTimeout = 2 * time.Minute
)

// RunwayObservationSource produces the next runway forecast sample for a
// session — the "runway observations (P_hit / quota state) as they are
// computed from provider events" feed the trigger consumes. ok=false means
// no sample is available yet (cold start: no quota telemetry persisted for
// the session), which is an honest skip, never a zero forecast.
// Implementations must be fail-open: an internal error is an ok=false, and
// must never panic — the pump runs concurrently with the user's provider
// turn.
type RunwayObservationSource interface {
	ObserveRunway(ctx context.Context, sessionID domain.SessionID) (domain.RunwayForecast, bool)
}

// QuotaSource is the narrow quota-telemetry read
// GracefulPauseObservationSource needs: the latest persisted
// provider.quota.observed observation per limit window for a session.
// *internal/evaluation.SQLDataSource.Quota satisfies it directly (the same
// events-table read the resume validation's quota check already uses via
// cmd/auspex's quotaSnapshotReaderAdapter).
type QuotaSource interface {
	Quota(ctx context.Context, sessionID domain.SessionID) ([]domain.QuotaObservation, error)
}

// GracefulPauseObservationSource is the production
// RunwayObservationSource: it reads the session's latest persisted quota
// observations (QuotaSource) and feeds each NEW sample through the frozen
// app.GracefulPauseService.Observe — the ADD §20.3 continuous-observation
// step, which computes the domain.RunwayForecast via the shared
// runway.Scorer AND (as its own documented side effect) records the
// per-session quota baseline pause.Service's RequestPause later uses for
// resume validation. Per-window forecasts are combined via
// runway.CombineWindows (ADD §15.5's conservative max), exactly as the
// hook-side driver (orchestrator.RunwayForecastStore) combines them.
//
// Only a sample whose event ID differs from the last one seen for its
// (session, limit) pair is re-observed: Claude's statusline re-emits an
// identical quota snapshot on every render, and feeding the same sample
// into Observe repeatedly would zero the burn-rate delta the scorer's
// time-to-limit estimate needs (the same identical-percent problem
// runwaydrive.go's previousSample documents, solved here by not
// re-observing rather than by scanning back). The most recent forecast per
// window is cached so ticks with no new telemetry still re-evaluate the
// trigger against the latest known state — a stale 98%-used window is
// still 98% used (the emergency trigger has no freshness requirement, ADD
// §17.6), while the calibrated path's own <=30s freshness check fails
// closed on staleness inside pause.Observer, exactly where the ADD puts
// it.
type GracefulPauseObservationSource struct {
	Service app.GracefulPauseService
	Quota   QuotaSource

	mu            sync.Mutex
	lastEventID   map[sourceKey]string
	forecasts     map[sourceKey]domain.RunwayForecast
	limitOrder    map[domain.SessionID][]string
	limitObserved map[sourceKey]bool
}

type sourceKey struct {
	SessionID domain.SessionID
	LimitID   string
}

var _ RunwayObservationSource = (*GracefulPauseObservationSource)(nil)

// ObserveRunway implements RunwayObservationSource. Fail-open throughout:
// a quota read error, an Observe error, or no telemetry at all yields
// ok=false (or the last cached forecasts), never an error surface.
func (s *GracefulPauseObservationSource) ObserveRunway(ctx context.Context, sessionID domain.SessionID) (domain.RunwayForecast, bool) {
	if s == nil || s.Service == nil || s.Quota == nil || sessionID == "" {
		return domain.RunwayForecast{}, false
	}

	observations, err := s.Quota.Quota(ctx, sessionID)
	if err != nil {
		observations = nil // fail open: fall through to whatever is cached.
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastEventID == nil {
		s.lastEventID = make(map[sourceKey]string)
		s.forecasts = make(map[sourceKey]domain.RunwayForecast)
		s.limitOrder = make(map[domain.SessionID][]string)
		s.limitObserved = make(map[sourceKey]bool)
	}

	for _, obs := range observations {
		if obs.LimitID == "" {
			continue
		}
		key := sourceKey{SessionID: sessionID, LimitID: obs.LimitID}
		if s.lastEventID[key] == obs.ID && obs.ID != "" {
			continue // same sample as last tick — do not zero the burn delta.
		}
		forecast, err := s.Service.Observe(ctx, app.RuntimeObservation{SessionID: sessionID, Quota: obs})
		if err != nil {
			continue // fail open: this window keeps its previous forecast.
		}
		s.lastEventID[key] = obs.ID
		s.forecasts[key] = forecast
		if !s.limitObserved[key] {
			s.limitObserved[key] = true
			s.limitOrder[sessionID] = append(s.limitOrder[sessionID], obs.LimitID)
		}
	}

	order := s.limitOrder[sessionID]
	if len(order) == 0 {
		return domain.RunwayForecast{}, false // honest cold start.
	}
	combined := make([]domain.RunwayForecast, 0, len(order))
	for _, limitID := range order {
		combined = append(combined, s.forecasts[sourceKey{SessionID: sessionID, LimitID: limitID}])
	}
	return runway.CombineWindows(combined), true
}

// LiveRunInterrupter is the managed runner's real app.TurnInterrupter: a
// registry of live managed runs keyed by SessionID, each carrying the
// signal-based interrupt capability M9's managed runner owns (the spawned
// provider process handle). The composition root wires ONE instance both
// as pause.ServiceDeps.Interrupter and as PauseTrigger.Runs, so the pause
// service's ReachSafePoint interrupt step reaches the very provider
// process the triggering run spawned.
//
// With no live run registered for the locator's session it fails closed
// with a typed capability-unavailable error — the same honest posture the
// composition's former stub interrupter had (an interrupt against nothing
// must never silently "succeed"), which is what keeps runtime-a11's
// "interrupt failed, leave the pause record recoverable" behavior intact
// for every non-managed caller.
type LiveRunInterrupter struct {
	mu   sync.Mutex
	runs map[domain.SessionID]*liveRun
}

// NewLiveRunInterrupter constructs an empty registry.
func NewLiveRunInterrupter() *LiveRunInterrupter {
	return &LiveRunInterrupter{runs: make(map[domain.SessionID]*liveRun)}
}

var _ app.TurnInterrupter = (*LiveRunInterrupter)(nil)

// Interrupt implements app.TurnInterrupter by delivering the interrupt to
// the registered live run for locator.SessionID.
func (l *LiveRunInterrupter) Interrupt(ctx context.Context, locator app.RunLocator) error {
	if l == nil {
		return &domain.Error{
			Code: domain.ErrCodeUnavailable, Message: "managed: nil LiveRunInterrupter", Retryable: false,
		}
	}
	l.mu.Lock()
	run := l.runs[locator.SessionID]
	l.mu.Unlock()
	if run == nil {
		return &domain.Error{
			Code:      domain.ErrCodeUnavailable,
			Message:   "managed: no live managed run registered for session; provider interrupt is only available while `auspex run` owns the provider process",
			Retryable: false,
			Details:   map[string]string{"session_id": string(locator.SessionID), "turn_id": string(locator.TurnID)},
		}
	}
	return run.interrupt(ctx)
}

// register adds run under sessionID and returns its release func. Last
// registration wins for a duplicate session (managed runs are one-shot,
// one per session in practice); release only removes the exact run it
// registered, so an overlapping registration is never clobbered.
func (l *LiveRunInterrupter) register(sessionID domain.SessionID, run *liveRun) func() {
	l.mu.Lock()
	if l.runs == nil {
		l.runs = make(map[domain.SessionID]*liveRun)
	}
	l.runs[sessionID] = run
	l.mu.Unlock()
	return func() {
		l.mu.Lock()
		if l.runs[sessionID] == run {
			delete(l.runs, sessionID)
		}
		l.mu.Unlock()
	}
}

// providerStopper is the mechanism seam liveRun.interrupt drives: how a
// graceful stop is delivered to a live managed provider, and how it is
// escalated when not honored. Two implementations exist — processStopper
// (M9's signal capability: SIGINT then kill, the spec/exec paths) and
// appServerTurnStopper (ADD §21.6's protocol-level turn/interrupt then
// connection teardown, appserver.go). The seam exists because the App
// Server path has no per-turn process to signal: the server outlives the
// turn, so its graceful stop is a JSON-RPC call, not a signal — while
// everything AROUND the mechanism (grace periods, confirm-stopped
// discipline, escalation ordering) is identical and stays in interrupt.
type providerStopper interface {
	// SignalStop delivers the graceful stop request. An error means the
	// request could not be delivered (not that the provider is still
	// running) — interrupt escalates straight to ForceStop.
	SignalStop(ctx context.Context) error
	// ForceStop is the escalation: terminate the provider outright.
	// ErrProcessDone-style "already stopped" conditions are the
	// implementation's job to swallow — a stop that finds nothing left
	// to stop has succeeded.
	ForceStop() error
}

// processStopper is the M9 signal-based providerStopper for spawned
// provider processes (claude managed runs, the codex exec fallback).
type processStopper struct {
	proc *os.Process
}

// SignalStop delivers graceful SIGINT (M9's signal-interruption
// capability). On platforms where os.Interrupt delivery is not
// implemented (Windows — os.Process.Signal returns an error there) the
// error surfaces so interrupt degrades explicitly to the hard kill: a
// real Windows control-signal delivery is a documented follow-up, and
// fabricating a graceful stop would violate "wait provider confirms
// stopped".
func (s processStopper) SignalStop(context.Context) error {
	if err := s.proc.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

// ForceStop terminates the process (Phase 4's "timeout 後 terminate
// process tree" — os/exec kills the direct child; descendants of a
// shell-less argv-only spawn are the provider's own).
func (s processStopper) ForceStop() error {
	if err := s.proc.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

// liveRun is one managed run's interrupt capability: the stop mechanism
// plus the channel Run closes once the provider has been observed to
// finish — the "wait provider confirms stopped" half of ADD §20.6
// Phase 4 (a delivered stop request is not a stopped provider; only the
// observed exit/terminal is).
type liveRun struct {
	stop        providerStopper
	exited      <-chan struct{}
	grace       time.Duration
	interrupted atomic.Bool
}

// interrupt delivers ADD §20.6 Phase 4 for a managed run: graceful stop
// first (SIGINT, or §21.6's turn/interrupt), escalate to a forced stop
// after the grace period, and only report success once the provider's
// stop has actually been observed via exited.
func (lr *liveRun) interrupt(ctx context.Context) error {
	lr.interrupted.Store(true)

	if err := lr.stop.SignalStop(ctx); err != nil {
		// Graceful delivery unavailable (Windows signals) or failed:
		// escalate straight to the forced stop by treating the grace
		// period as elapsed.
		if forceErr := lr.stop.ForceStop(); forceErr != nil {
			return forceErr
		}
	}

	select {
	case <-lr.exited:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(lr.grace):
	}

	// Grace elapsed without an observed stop: escalate.
	if err := lr.stop.ForceStop(); err != nil {
		return err
	}
	select {
	case <-lr.exited:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(lr.grace):
		return &domain.Error{
			Code:      domain.ErrCodeUnavailable,
			Message:   "managed: provider did not confirm stop after interrupt and forced stop",
			Retryable: false,
		}
	}
}

// PauseTrigger arms Runner.Run with the M10 auto-pause. All four service
// fields are required for the trigger to arm; a nil PauseTrigger (or one
// with a missing collaborator) disables auto-pause entirely and Run
// behaves exactly as before — the same nil-is-a-documented-degrade
// convention HookDeps' optional fields follow.
type PauseTrigger struct {
	// Service drives the frozen pause lifecycle (RequestPause ->
	// ReachSafePoint -> EnterSleep). Production wires the real
	// *pause.Service.
	Service app.GracefulPauseService
	// Runs is the live-run registry. The SAME instance must be wired as
	// the pause service's TurnInterrupter (pause.ServiceDeps.Interrupter),
	// so ReachSafePoint's interrupt step resolves back to the run this
	// trigger registered.
	Runs *LiveRunInterrupter
	// Source produces the runway forecast samples the trigger observes.
	Source RunwayObservationSource
	// Clock stamps observation times (pause.Observer's debounce/freshness
	// clock). nil falls back to time.Now, mirroring
	// orchestrator.RunwayForecastStore's own fallback.
	Clock domain.Clock

	// Observe overrides the ADD §17.6/§20.2 trigger thresholds; nil uses
	// pause.NewObserveConfig()'s defaults (0.80 threshold, 5s debounce,
	// 30s freshness, 0.70 hysteresis reset, 98%/60s emergency).
	Observe *pause.ObserveConfig
	// Interval overrides DefaultObserveInterval (ADD §20.3's 5s heartbeat)
	// when > 0.
	Interval time.Duration
	// InterruptGrace overrides DefaultInterruptGrace when > 0.
	InterruptGrace time.Duration
	// LifecycleTimeout overrides DefaultLifecycleTimeout when > 0.
	LifecycleTimeout time.Duration
}

func (p *PauseTrigger) observeConfig() pause.ObserveConfig {
	if p.Observe != nil {
		return *p.Observe
	}
	return pause.NewObserveConfig()
}

func (p *PauseTrigger) interval() time.Duration {
	if p.Interval > 0 {
		return p.Interval
	}
	return DefaultObserveInterval
}

func (p *PauseTrigger) interruptGrace() time.Duration {
	if p.InterruptGrace > 0 {
		return p.InterruptGrace
	}
	return DefaultInterruptGrace
}

func (p *PauseTrigger) lifecycleTimeout() time.Duration {
	if p.LifecycleTimeout > 0 {
		return p.LifecycleTimeout
	}
	return DefaultLifecycleTimeout
}

func (p *PauseTrigger) now() time.Time {
	if p.Clock != nil {
		return p.Clock.Now()
	}
	return time.Now()
}

// autoPauseRun is the handle Runner.Run holds for one run's armed (or
// disarmed — the zero value) trigger. Interrupted reports whether the
// trigger delivered a provider interrupt (Run's exit-code normalization
// reads it); Stop cancels the observation pump and joins the driver
// goroutine (bounded: a fired lifecycle is capped by LifecycleTimeout).
type autoPauseRun struct {
	run  *liveRun
	stop func()
}

func (a autoPauseRun) Interrupted() bool {
	return a.run != nil && a.run.interrupted.Load()
}

func (a autoPauseRun) Stop() {
	if a.stop != nil {
		a.stop()
	}
}

// beginRun arms the trigger for one managed run that owns a provider
// PROCESS: registers the M9 signal-based stop capability in the
// interrupter registry and starts the heartbeat pump. nil-receiver-safe:
// an unarmed Runner gets the zero handle and zero behavior change. exited
// must be closed by the caller once the provider process has been waited
// to completion (Run does this immediately after cmd.Wait returns).
func (p *PauseTrigger) beginRun(ctx context.Context, sessionID domain.SessionID, proc *os.Process, exited <-chan struct{}, humanLog io.Writer) autoPauseRun {
	if proc == nil {
		return autoPauseRun{}
	}
	return p.beginStopperRun(ctx, sessionID, processStopper{proc: proc}, exited, humanLog)
}

// beginStopperRun is beginRun's mechanism-agnostic core, and the entry
// the App Server path calls directly with its protocol-level stopper
// (ADD §21.6; appserver.go): same registry, same pump, same lifecycle —
// only the stop mechanism differs. exited must be closed by the caller
// once the provider's stop/finish has been observed (for a protocol run:
// the turn's terminal notification, or the connection ending).
func (p *PauseTrigger) beginStopperRun(ctx context.Context, sessionID domain.SessionID, stopper providerStopper, exited <-chan struct{}, humanLog io.Writer) autoPauseRun {
	if p == nil || p.Service == nil || p.Runs == nil || p.Source == nil || stopper == nil || sessionID == "" {
		return autoPauseRun{}
	}
	if humanLog == nil {
		humanLog = io.Discard
	}

	run := &liveRun{stop: stopper, exited: exited, grace: p.interruptGrace()}
	release := p.Runs.register(sessionID, run)

	pumpCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.drive(pumpCtx, sessionID, run, humanLog)
	}()

	return autoPauseRun{
		run: run,
		stop: func() {
			cancel()
			<-done
			release()
		},
	}
}

// drive is the per-run observation pump: one pause.Observer (fresh per
// run, so no stale debounce arm leaks across runs), one heartbeat tick per
// Interval, one trigger attempt maximum. It returns when the pump context
// is cancelled (run teardown), the provider exits on its own, or a trigger
// attempt — successful or not — completes (see the file doc comment's
// fail-toward-continuing posture for why a failed attempt is not
// retried).
func (p *PauseTrigger) drive(ctx context.Context, sessionID domain.SessionID, run *liveRun, humanLog io.Writer) {
	observer := pause.NewObserver(p.observeConfig())
	ticker := time.NewTicker(p.interval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-run.exited:
			return // provider finished on its own — nothing left to pause.
		case <-ticker.C:
		}

		forecast, ok := p.Source.ObserveRunway(ctx, sessionID)
		if !ok {
			continue // cold start / no telemetry yet — honest skip.
		}
		decision := observer.Observe(sessionID, forecast, p.now())
		if !decision.Fire {
			continue
		}
		p.firePause(ctx, sessionID, decision, humanLog)
		return
	}
}

// firePause drives one fired trigger through the existing frozen pause
// lifecycle. It runs on a context detached from the pump's cancellation
// (context.WithoutCancel) and bounded by LifecycleTimeout: once the
// trigger has fired, the checkpoint/interrupt sequence must be allowed to
// finish even as Run's teardown cancels the pump — aborting a checkpoint
// write halfway because the provider happened to exit would be exactly the
// partial-sequence corruption persistphase.go exists to prevent.
//
// Failure posture, step by step (fail toward continuing work):
//   - RequestPause fails -> logged, run continues, no further attempt.
//   - ReachSafePoint fails BEFORE its interrupt step (safe-point
//     coordination or any checkpoint write) -> the provider was never
//     signalled (PersistThenInterrupt's ordering guarantee), the run
//     continues; logged.
//   - ReachSafePoint fails AT the interrupt step -> the pause record is
//     durably at Failed (InterruptAndSleep's recoverable-state contract);
//     the run continues if the provider is in fact still alive; logged.
//   - EnterSleep fails (a read-back, after the provider already stopped)
//     -> logged; the wake job Persist scheduled is durable regardless.
func (p *PauseTrigger) firePause(ctx context.Context, sessionID domain.SessionID, decision pause.ObserveDecision, humanLog io.Writer) {
	lifecycleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), p.lifecycleTimeout())
	defer cancel()

	record, err := p.Service.RequestPause(lifecycleCtx, app.PauseRequest{
		SessionID: sessionID,
		Reason:    string(decision.Reason),
	})
	if err != nil {
		_, _ = fmt.Fprintf(humanLog, "auspex run: auto-pause trigger fired (%s) but the pause request failed; the run continues: %v\n", decision.Reason, err)
		return
	}
	_, _ = fmt.Fprintf(humanLog, "auspex run: auto-pause %s requested (%s)\n", record.ID, decision.Reason)

	record, err = p.Service.ReachSafePoint(lifecycleCtx, app.SafePoint{PauseID: record.ID, At: p.now()})
	if err != nil {
		_, _ = fmt.Fprintf(humanLog, "auspex run: auto-pause %s could not reach safe point (checkpoints/interrupt); the run continues: %v\n", record.ID, err)
		return
	}

	job, err := p.Service.EnterSleep(lifecycleCtx, record.ID)
	if err != nil {
		_, _ = fmt.Fprintf(humanLog, "auspex run: auto-pause %s interrupted the provider (status %s) but the wake job could not be read back: %v\n", record.ID, record.Status, err)
		return
	}
	_, _ = fmt.Fprintf(humanLog, "auspex run: auto-pause %s sleeping; wake job %s runs after %s\n", record.ID, job.ID, job.RunAfter.UTC().Format(time.RFC3339))
}
