// openturn.go: the SQL-backed OpenTurnResolver (issue #11 turn
// correlation). Lives in this package — not cmd/auspex's adapter file —
// for the same reason SessionBootstrapper does (sessionbootstrap.go's
// precedent): it is hook-path infrastructure with its own behavior worth
// testing against a real migrated DB in-package, not a pure DTO-shape
// translation.
package orchestrator

import (
	"context"

	"github.com/huaiche94/auspex/internal/domain"
	"github.com/huaiche94/auspex/internal/storage/sqlite"
)

// OpenTurnStore resolves a session's latest started turn from the events
// table: the most recent provider.turn.started event carrying a turn_id
// — the ID HandleUserPromptSubmit minted and stamped (issue #14's
// event/prediction linkage), which a fresh Stop-hook process otherwise
// has no way to know. Sessions are turn-serial, so latest-started IS the
// turn a terminal hook reports on (see HookDeps.stampOpenTurn's
// edge-case notes for the crash-orphan and re-entrant-Stop semantics).
type OpenTurnStore struct {
	DB *sqlite.DB
}

var _ OpenTurnResolver = (*OpenTurnStore)(nil)

// LatestStartedTurn implements OpenTurnResolver. Fail-open by that
// contract: a nil receiver/DB, query error, or no matching row is
// ok=false — never a hook failure, never a fabricated ID.
func (s *OpenTurnStore) LatestStartedTurn(ctx context.Context, sessionID domain.SessionID) (domain.TurnID, bool) {
	if s == nil || s.DB == nil {
		return "", false
	}
	var turnID string
	err := s.DB.Conn().QueryRowContext(ctx, `
		SELECT turn_id FROM events
		WHERE session_id = ? AND event_type = 'provider.turn.started'
		  AND turn_id IS NOT NULL AND turn_id != ''
		ORDER BY occurred_at DESC, rowid DESC LIMIT 1`,
		string(sessionID),
	).Scan(&turnID)
	if err != nil || turnID == "" {
		return "", false
	}
	return domain.TurnID(turnID), true
}
