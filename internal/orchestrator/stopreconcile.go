// stopreconcile.go: the post-turn Progress Tree/Git/artifact
// reconciliation + evidence-gate outcome labeling the Stop hook runs
// (issue #115, M4; ADD §22.4 "`Stop` 時 reconcile Progress Tree、Git、
// artifacts", §13.6 outcome labeling, §18.9 reconciliation). Before this
// file, the evidence gate (internal/progress.CompleteNode's stage→verify
// protocol) fired only via the explicit `auspex progress complete` CLI —
// nothing checked the turn's claimed work automatically after the turn.
//
// # Failure surface: FLAG, never block (owner decision on issue #115)
//
// Stop fires AFTER the turn — there is nothing left to block — and hooks
// fail open (ADD §17.5). So this step never mutates the Progress Tree and
// never fails the Stop hook. The hard enforcement already lives elsewhere
// and is not duplicated here: a node claimed complete without verified
// artifacts simply STAYS incomplete, because CompleteNode is the only
// write path to `completed` and it rejects unverified evidence
// (Constitution §6.2). What this step adds is the record: it observes the
// tree's post-turn state, runs the existing crash-window reconciliation
// (internal/progress.Reconciler — REUSED, not reimplemented), snapshots
// the worktree's Git fingerprint, and persists one
// progress.tree.reconciled event whose payload labels the outcome
// (evidence_gate clear/flagged + machine-readable flags). The flag then
// surfaces wherever events/tree state are read: the open node is visible
// in `auspex status` (Progress Tree snapshot), and the persisted event is
// joinable by turn for reporting/calibration, exactly like the other
// turn-terminal events.
//
// # Reuse map (what runs, where it already lives)
//
//   - Progress Tree state: app.ProgressTreeService.Snapshot via the same
//     narrow ProgressSnapshotReader view the event correlator uses
//     (correlate.go) — read-only by construction.
//   - Artifact/checkpoint crash windows: internal/progress.Reconciler
//     (reconcile.go — the M4 vertical slice's staged-artifact-vs-DB
//     reconciliation), through the narrow ProgressTreeReconciler seam
//     below. The frozen app.ProgressTreeService.Reconcile port is NOT used
//     here because its frozen ReconcileResult shape discards the report
//     detail (orphan/violation lists) this event's payload records;
//     depending on the concrete reconciler for its richer report mirrors
//     how ForecastCardSource depends on the real evaluation.Service for
//     what the frozen port cannot carry (hooks.go).
//   - Git: gitx.Client.Fingerprint (checkpoint's already-real repository
//     fingerprint) against the Stop payload's own reported cwd — the same
//     dir-not-worktree-row entry point SessionBootstrapper uses, so the
//     observation works from the session's very first turn.
package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/gitx"
	"github.com/huaiche94/auspex/internal/progress"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// StopReconciler is HookDeps' narrow view of the post-turn reconciliation
// step. ok=false means "nothing to reconcile" (session not yet resolvable
// to a task — ordinary cold start — or the tree itself unreadable) and the
// caller emits nothing; a fabricated empty report would violate unknown-
// is-not-zero. Implementations must be fail-open: any internal error is an
// ok=false or a degraded report field, never a hook failure.
type StopReconciler interface {
	ReconcileTurn(ctx context.Context, sessionID domain.SessionID, dir *string) (StopReconcileReport, bool)
}

// StopReconcileReport is one turn's reconciliation outcome: the Progress
// Tree's post-turn shape, the crash-window scan's findings, and the
// worktree's Git observation. Pointer-typed counts distinguish "scanned
// and found zero" from "scan did not run" (unknown is not zero).
type StopReconcileReport struct {
	// TaskID is the task the session resolved to — always set on an
	// ok=true report.
	TaskID domain.TaskID

	// NodesTotal / NodesCompleted tally the tree's post-turn snapshot.
	NodesTotal     int
	NodesCompleted int

	// OpenNodeIDs lists nodes still in_progress when the turn ended — the
	// evidence-gate half: work the turn had claimed/started that did NOT
	// clear CompleteNode's validator gate (whether because the agent said
	// "done" without evidence, a completion attempt was rejected, or the
	// work is simply mid-flight). Recorded, never mutated.
	OpenNodeIDs []domain.ProgressNodeID

	// CheckpointingNodeIDs lists nodes stuck in `checkpointing` — a
	// completion attempt that entered the atomic protocol but never
	// committed (ADD §28.3 item 6's startup-reconciliation concern,
	// observed here at turn end).
	CheckpointingNodeIDs []domain.ProgressNodeID

	// OrphanedStagedArtifacts / CheckpointIntegrityViolations carry the
	// existing progress.Reconciler scan's finding counts. nil means the
	// scan did not run (not wired, or errored) — flagged as such, not
	// reported as zero.
	OrphanedStagedArtifacts       *int
	CheckpointIntegrityViolations *int

	// GitHeadOID / GitChangedPaths are the worktree's fingerprint at Stop
	// (§18.9 step 4's repo-fingerprint anchor). Empty/nil when no dir was
	// reported or the fingerprint read failed — omitted, not zeroed.
	GitHeadOID      string
	GitChangedPaths *int
}

// Machine-readable flag vocabulary for the progress.tree.reconciled
// payload's "flags" array. Frozen strings once shipped (they land in
// persisted event payloads); additions are fine, renames are not.
const (
	// StopReconcileFlagOpenNodes: the turn ended with in_progress node(s)
	// that never cleared the evidence gate.
	StopReconcileFlagOpenNodes = "open_nodes_without_verified_evidence"
	// StopReconcileFlagStuckCheckpointing: node(s) stuck mid-completion.
	StopReconcileFlagStuckCheckpointing = "nodes_stuck_checkpointing"
	// StopReconcileFlagOrphanedArtifacts: staged evidence files on disk
	// with no committed artifacts row (the crash window the M4 slice
	// reconciles).
	StopReconcileFlagOrphanedArtifacts = "orphaned_staged_artifacts"
	// StopReconcileFlagIntegrityViolations: checkpoint rows that failed
	// integrity re-verification.
	StopReconcileFlagIntegrityViolations = "checkpoint_integrity_violations"
	// StopReconcileFlagScanUnavailable: the crash-window scan itself did
	// not run — the gate result is honestly unknown, not clear.
	StopReconcileFlagScanUnavailable = "artifact_reconciliation_unavailable"
)

// Flags derives the report's discrepancy flags per the vocabulary above.
func (r StopReconcileReport) Flags() []string {
	var flags []string
	if len(r.OpenNodeIDs) > 0 {
		flags = append(flags, StopReconcileFlagOpenNodes)
	}
	if len(r.CheckpointingNodeIDs) > 0 {
		flags = append(flags, StopReconcileFlagStuckCheckpointing)
	}
	switch {
	case r.OrphanedStagedArtifacts == nil || r.CheckpointIntegrityViolations == nil:
		flags = append(flags, StopReconcileFlagScanUnavailable)
	default:
		if *r.OrphanedStagedArtifacts > 0 {
			flags = append(flags, StopReconcileFlagOrphanedArtifacts)
		}
		if *r.CheckpointIntegrityViolations > 0 {
			flags = append(flags, StopReconcileFlagIntegrityViolations)
		}
	}
	return flags
}

// payload renders the report as the progress.tree.reconciled event
// payload: counts + node IDs + flags only — no free text, no paths, no
// prose (Constitution §7; the reconciler's human-readable violation
// strings stay out of the durable event and are re-derivable by re-running
// the scan).
func (r StopReconcileReport) payload() map[string]any {
	p := map[string]any{
		"nodes_total":     r.NodesTotal,
		"nodes_completed": r.NodesCompleted,
		"open_node_count": len(r.OpenNodeIDs),
	}
	if len(r.OpenNodeIDs) > 0 {
		p["open_node_ids"] = nodeIDStrings(r.OpenNodeIDs)
	}
	if len(r.CheckpointingNodeIDs) > 0 {
		p["stuck_checkpointing_node_ids"] = nodeIDStrings(r.CheckpointingNodeIDs)
	}
	if r.OrphanedStagedArtifacts != nil {
		p["orphaned_staged_artifacts"] = *r.OrphanedStagedArtifacts
	}
	if r.CheckpointIntegrityViolations != nil {
		p["checkpoint_integrity_violations"] = *r.CheckpointIntegrityViolations
	}
	if r.GitHeadOID != "" {
		p["git_head_oid"] = r.GitHeadOID
	}
	if r.GitChangedPaths != nil {
		p["git_changed_paths"] = *r.GitChangedPaths
	}
	flags := r.Flags()
	if len(flags) > 0 {
		p["evidence_gate"] = "flagged"
		p["flags"] = flags
	} else {
		p["evidence_gate"] = "clear"
	}
	return p
}

func nodeIDStrings(ids []domain.ProgressNodeID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, string(id))
	}
	return out
}

// ProgressTreeReconciler is the narrow seam over the existing
// internal/progress.Reconciler (the M4 crash-window reconciliation this
// step REUSES — see the package doc comment for why the frozen
// app.ProgressTreeService.Reconcile port's result shape is insufficient
// here). Declared locally per the same consume-a-narrow-view convention
// SessionResolver/ProgressSnapshotReader follow (correlate.go).
type ProgressTreeReconciler interface {
	Reconcile(ctx context.Context, taskID domain.TaskID) (progress.ReconcileReport, error)
}

var _ ProgressTreeReconciler = (*progress.Reconciler)(nil)

// GitFingerprinter is the narrow seam over gitx.Client's repository
// fingerprint read — the one Git capability this step consumes.
type GitFingerprinter interface {
	Fingerprint(ctx context.Context, path string) (gitx.Fingerprint, error)
}

var _ GitFingerprinter = (*gitx.Client)(nil)

// TurnReconcileService is the production StopReconciler: it composes the
// SAME already-tested pieces the rest of the system uses (SQLDataSource's
// session→task Resolve, the Progress Tree service's Snapshot, the
// progress.Reconciler crash-window scan, gitx's fingerprint) into the one
// read-only post-turn pass. It introduces no new persistence and no new
// completion/validation logic — recording, not enforcing, is the whole
// design (see the package doc comment's flag-not-block rationale).
type TurnReconcileService struct {
	// Sessions resolves the session to its task. Required; without it
	// there is nothing to reconcile against.
	Sessions SessionResolver
	// Progress reads the task's Progress Tree snapshot. Required.
	Progress ProgressSnapshotReader
	// Reconciler runs the existing staged-artifact/checkpoint-integrity
	// crash-window scan. Optional: nil leaves the scan counts unknown
	// (StopReconcileFlagScanUnavailable), never fabricated zeros.
	Reconciler ProgressTreeReconciler
	// Git reads the worktree fingerprint from the hook payload's reported
	// dir. Optional: nil (or a read error, or no reported dir) omits the
	// Git fields.
	Git GitFingerprinter
}

var _ StopReconciler = (*TurnReconcileService)(nil)

// ReconcileTurn implements StopReconciler. Every early return is a
// documented fail-open degrade, mirroring the lookup discipline
// EventCorrelator.lookup established for the identical resolution chain:
// an unresolvable session (cold start, not-yet-registered, resolver
// error) and an unreadable tree both yield ok=false; a failed crash-window
// scan or fingerprint read degrades to unknown fields on an ok=true
// report.
func (s *TurnReconcileService) ReconcileTurn(ctx context.Context, sessionID domain.SessionID, dir *string) (StopReconcileReport, bool) {
	if s == nil || s.Sessions == nil || s.Progress == nil || sessionID == "" {
		return StopReconcileReport{}, false
	}
	resolved, err := s.Sessions.Resolve(ctx, sessionID)
	if err != nil || resolved.TaskID == nil || *resolved.TaskID == "" {
		return StopReconcileReport{}, false
	}
	taskID := *resolved.TaskID

	snap, err := s.Progress.Snapshot(ctx, taskID)
	if err != nil {
		return StopReconcileReport{}, false
	}

	report := StopReconcileReport{TaskID: taskID, NodesTotal: len(snap.Nodes)}
	for i := range snap.Nodes {
		switch snap.Nodes[i].Status {
		case domain.NodeCompleted:
			report.NodesCompleted++
		case domain.NodeInProgress:
			report.OpenNodeIDs = append(report.OpenNodeIDs, snap.Nodes[i].ID)
		case domain.NodeCheckpointing:
			report.CheckpointingNodeIDs = append(report.CheckpointingNodeIDs, snap.Nodes[i].ID)
		}
	}

	if s.Reconciler != nil {
		if rep, rerr := s.Reconciler.Reconcile(ctx, taskID); rerr == nil {
			orphans := len(rep.OrphanedStagedArtifacts)
			violations := len(rep.IntegrityViolations)
			report.OrphanedStagedArtifacts = &orphans
			report.CheckpointIntegrityViolations = &violations
		}
		// rerr != nil: counts stay nil — the scan-unavailable flag reports
		// the gap; a reconciliation error must never fail the Stop hook.
	}

	if s.Git != nil && dir != nil && *dir != "" {
		if fp, gerr := s.Git.Fingerprint(ctx, *dir); gerr == nil {
			report.GitHeadOID = fp.HeadOID
			changed := len(fp.Entries)
			report.GitChangedPaths = &changed
		}
		// gerr != nil: Git fields stay empty — omitted, not zeroed.
	}

	return report, true
}

// reconcileAtStop runs the post-turn reconciliation and persists its
// progress.tree.reconciled event, called by the Stop hook AFTER the turn's
// own events are committed. nil-receiver-safe and fail-open end to end
// like driveRunway/foldTurnToolOps: a nil StopReconcile is a documented
// no-op, ReconcileTurn swallows its own errors into ok=false/degraded
// fields, and persist already swallows write errors — no path here can
// fail the Stop hook or the provider session.
//
// Event envelope: built directly (not via the claude Normalizer, which
// owns provider-payload projection, not Auspex's own progress events) but
// under the same idiom — v1.SchemaVersionEvent, injected Clock/IDs,
// Source=hook, and a deterministic digest IdempotencyKey. The key prefers
// the resolved TurnID (one reconciled outcome per turn: a re-entrant Stop
// — stop_hook_active — re-reconciles the SAME turn and dedupes on the
// UNIQUE idempotency index), falling back to the observation timestamp
// exactly like NormalizeStop when no started turn is known. TaskID is
// stamped from the report (already resolved — no correlator round trip),
// which is also what makes the event land pre-correlated for reporting.
func (d HookDeps) reconcileAtStop(ctx context.Context, provider string, sessionID domain.SessionID, dir *string) {
	if d.StopReconcile == nil || sessionID == "" {
		return
	}
	report, ok := d.StopReconcile.ReconcileTurn(ctx, sessionID, dir)
	if !ok {
		return
	}

	now := d.Clock.Now()
	events := []v1.Event{{
		SchemaVersion: v1.SchemaVersionEvent,
		EventID:       d.IDs.NewID(),
		EventType:     v1.EventProgressTreeReconciled,
		OccurredAt:    now,
		ObservedAt:    now,
		Source:        string(domain.SourceHook),
		Provider:      provider,
		SessionID:     string(sessionID),
		TaskID:        string(report.TaskID),
		Payload:       report.payload(),
	}}
	d.stampOpenTurn(ctx, sessionID, events)
	if events[0].TurnID != "" {
		events[0].IdempotencyKey = stopReconcileKey(string(v1.EventProgressTreeReconciled), string(sessionID), events[0].TurnID)
	} else {
		events[0].IdempotencyKey = stopReconcileKey(string(v1.EventProgressTreeReconciled), string(sessionID), now.UTC().Format(time.RFC3339Nano))
	}
	d.persist(ctx, events)
}

// stopReconcileKey derives the event's deterministic idempotency key,
// joining parts with a unit-separator byte so distinct part boundaries
// never collide — the same digest discipline the claude normalizer's
// digestKey and runwaydrive's runwayForecastID follow.
func stopReconcileKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte{0x1f})
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}
