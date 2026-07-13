// correlate.go implements the event-correlation half of the "explicit
// completion + event correlation" design that closes qa's P1 finding
// (docs/implementation/vertical-slice/qa.md §Severity-ranked findings;
// GitHub issue #1): before this file, no producer anywhere assigned
// pkg/protocol/v1.Event.TaskID or Event.ProgressNodeID — every event the
// hook handlers persisted carried only a SessionID, so no consumer could
// ever connect a persisted provider observation back to the Progress Tree
// node it was evidence about.
//
// EventCorrelator fills those two already-frozen fields (they have existed
// on v1.Event since the contract freeze; nothing in pkg/protocol/v1
// changes here) at hook-persist time, and ONLY when the answer is
// unambiguous:
//
//   - TaskID comes from the frozen app.FeatureDataSource.Resolve port
//     (ADR-044 / REC-01, internal/app/ports.go — frozen 2026-07-13), the
//     same session -> repository/task lookup the evaluation pipeline
//     already uses. Resolve returning a nil TaskID is cold-start, not an
//     error (per that port's own doc comment), and leaves the field empty.
//   - ProgressNodeID comes from the task's Progress Tree snapshot
//     (app.ProgressTreeService.Snapshot), and is populated ONLY when
//     exactly one node is currently in_progress. Zero in-progress nodes
//     means there is nothing to attribute the observation to; more than
//     one means attribution would be a guess between candidates — and the
//     Constitution's "unknown is not zero" principle (ADD principle 1,
//     CONTRACT_FREEZE.md) forbids fabricating a correlation that was not
//     actually observed. Both cases leave the field empty.
//
// Correlation is deliberately best-effort and fail-open, matching the hook
// handlers' own discipline (hooks.go: "a hook must never fail the user's
// actual prompt/turn because Auspex's own event log could not be
// written", ADD §17.5): any resolver/snapshot error simply persists the
// event uncorrelated. An uncorrelated event is strictly less useful, never
// wrong; a hook aborted by a correlation lookup would be provider-visible
// breakage.
//
// Note what this file deliberately does NOT do: it never calls
// app.ProgressTreeService.CompleteNode. Correlation annotates persisted
// evidence; actually completing a node remains an explicit, evidence-
// carrying operation (`auspex progress complete`, internal/cli/
// progress.go) per the approved issue #1 design — Constitution §6.2's "a
// node may not become completed without durable, validator-checked
// artifact evidence" rules out treating a bare Stop signal as completion
// evidence by itself.
package orchestrator

import (
	"context"

	"github.com/huaiche94/auspex/internal/app"
	"github.com/huaiche94/auspex/internal/domain"
	v1 "github.com/huaiche94/auspex/pkg/protocol/v1"
)

// SessionResolver is the narrow, package-local view of the frozen
// app.FeatureDataSource port this correlator actually consumes — exactly
// the consumption pattern that port's own doc comment prescribes
// ("Consumers that need only a subset SHOULD keep depending on their own
// narrow package-local view of this port... rather than importing the full
// interface"). The real implementation is internal/evaluation.SQLDataSource
// (already constructed in cmd/auspex/wire.go); tests supply a fake.
type SessionResolver interface {
	// Resolve returns the RepositoryID and (optional) TaskID a session
	// belongs to. See app.FeatureDataSource.Resolve.
	Resolve(ctx context.Context, sessionID domain.SessionID) (app.ResolvedSession, error)
}

// The frozen full port must always satisfy this narrow view — if this
// assertion ever breaks, the narrow interface has drifted from the frozen
// contract rather than the other way around.
var _ SessionResolver = (app.FeatureDataSource)(nil)

// ProgressSnapshotReader is the narrow view of the frozen
// app.ProgressTreeService port the correlator consumes: only Snapshot,
// never the mutating methods — correlation is a read-only annotation step
// and must stay structurally incapable of advancing the tree itself (see
// the package doc comment's "what this file deliberately does NOT do").
type ProgressSnapshotReader interface {
	Snapshot(ctx context.Context, taskID domain.TaskID) (app.ProgressTreeSnapshot, error)
}

var _ ProgressSnapshotReader = (app.ProgressTreeService)(nil)

// EventCorrelator populates v1.Event.TaskID/ProgressNodeID best-effort per
// the package doc comment's rules. A nil *EventCorrelator, or one with a
// nil Sessions resolver, is a valid, documented no-op — mirroring
// HookDeps' own "nil EventPersister is valid" convention (hooks.go), so
// callers that have no data source wired (bare test trees, degraded
// composition) need no branching at the call site.
type EventCorrelator struct {
	// Sessions resolves a SessionID to its task. Required for any
	// correlation to happen at all.
	Sessions SessionResolver
	// Progress reads the resolved task's Progress Tree snapshot to find
	// the single in-progress node, if there is exactly one. Optional: nil
	// limits correlation to TaskID only.
	Progress ProgressSnapshotReader
}

// sessionCorrelation is one session's resolved correlation values within a
// single Correlate call. Empty fields mean "could not be resolved
// unambiguously" and are never written over an event's existing values.
type sessionCorrelation struct {
	taskID         string
	progressNodeID string
}

// Correlate fills TaskID (and, when exactly one node is in_progress,
// ProgressNodeID) in place on every event in evs that carries a SessionID
// and does not already carry a TaskID. Already-populated fields are never
// overwritten: a producer that someday stamps its own correlation (e.g. a
// future managed-runner path that knows its node directly) is a strictly
// better source than this after-the-fact lookup.
//
// Lookups are memoized per call: a NormalizeStatusLine batch carries up to
// four events for the same session (normalizer.go), and one batch must not
// cost four identical Resolve+Snapshot round trips.
//
// Correlate never returns an error by design — see the package doc comment
// on fail-open. A failed lookup yields an uncorrelated event, nothing more.
func (c *EventCorrelator) Correlate(ctx context.Context, evs []v1.Event) {
	if c == nil || c.Sessions == nil {
		return
	}
	cache := make(map[string]sessionCorrelation, 1)
	for i := range evs {
		ev := &evs[i]
		if ev.SessionID == "" || ev.TaskID != "" {
			continue
		}
		corr, ok := cache[ev.SessionID]
		if !ok {
			corr = c.lookup(ctx, domain.SessionID(ev.SessionID))
			cache[ev.SessionID] = corr
		}
		ev.TaskID = corr.taskID
		if ev.ProgressNodeID == "" {
			ev.ProgressNodeID = corr.progressNodeID
		}
	}
}

// lookup resolves one session's correlation values, applying every
// unknown-is-not-zero rule from the package doc comment: any error, a nil
// TaskID, and anything other than exactly one in-progress node all yield
// empty fields rather than a guess.
func (c *EventCorrelator) lookup(ctx context.Context, sessionID domain.SessionID) sessionCorrelation {
	resolved, err := c.Sessions.Resolve(ctx, sessionID)
	if err != nil || resolved.TaskID == nil || *resolved.TaskID == "" {
		// Resolve errors include "session not yet registered"
		// (ErrCodeNotFound from evaluation.SQLDataSource) — for a hook
		// firing before `auspex init`-style registration, uncorrelated
		// is the honest answer, not a failure to escalate.
		return sessionCorrelation{}
	}
	out := sessionCorrelation{taskID: string(*resolved.TaskID)}
	if c.Progress == nil {
		return out
	}
	snap, err := c.Progress.Snapshot(ctx, *resolved.TaskID)
	if err != nil {
		// The task resolved unambiguously; only the node lookup failed.
		// Keep the TaskID (it was genuinely resolved, not guessed) and
		// leave the node empty.
		return out
	}
	var single domain.ProgressNodeID
	inProgress := 0
	for i := range snap.Nodes {
		if snap.Nodes[i].Status == domain.NodeInProgress {
			inProgress++
			single = snap.Nodes[i].ID
		}
	}
	if inProgress == 1 {
		out.progressNodeID = string(single)
	}
	return out
}
