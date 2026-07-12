// diagnostics.go implements `preflight status` and `preflight doctor`
// (agents/runtime.md Part B P0 commands; runtime-b08). Both are read-only:
// neither mutates repository/worktree/session/task state, config, or the
// database beyond what schema-check machinery itself requires (e.g.
// *sqlite.DB.CurrentVersion creates the schema_migrations bookkeeping
// table if absent — the same read-safe behavior migrate.go already
// documents, not something this package introduces).
package orchestrator

import (
	"context"
	"database/sql"
	"os"
	"strconv"

	"github.com/huaiche94/preflight/internal/app"
	"github.com/huaiche94/preflight/internal/domain"
)

// --- preflight status --------------------------------------------------

// StatusRequest is `preflight status`'s input — whatever identity is
// already known to the caller (a CLI flag, an already-resolved session).
// All fields are optional: a field left empty simply does not appear in
// StatusResult (unknown is not zero — this command reports what it
// actually knows, never a placeholder).
type StatusRequest struct {
	SessionID domain.SessionID
	TaskID    *domain.TaskID
}

// StatusResult is `preflight status`'s best-effort summary. Each Has*
// flag distinguishes "we don't know" from "we know and it's empty/zero",
// per CONTRACT_FREEZE.md's unknown-is-not-zero discipline.
//
// No pause-state field exists here: the frozen app.GracefulPauseService
// port (internal/app/ports.go) has no passive "read the current pause
// status for a session" query — Observe/RequestPause/ReachSafePoint/
// EnterSleep/Resume/Cancel are all state-transition actions, not a
// read-only accessor, and Status must never call one of those just to
// render a summary (that would make a read command mutate pause state).
// A pause summary belongs here once a real read path exists (e.g.
// pause_records queried directly, once internal/pause's persist-phase
// nodes land) — adding it is additive.
type StatusResult struct {
	SessionID domain.SessionID

	HasProgressTree bool
	ProgressTree    app.ProgressTreeSnapshot
}

// StatusDeps bundles Status's optional collaborators. Every field is
// independently optional: Status degrades (Has* flags false) rather than
// erroring when a dependency is absent, matching ADD §17.5's fail-open
// default for an operational read — "status" is a read command, and a
// missing piece of state (e.g. no Progress Tree yet for a brand-new
// task) is ordinary, not exceptional.
type StatusDeps struct {
	ProgressTree app.ProgressTreeService
}

// Status implements `preflight status`: report current
// repository/worktree/session/task state, best-effort, against whatever
// real stores or fakes are wired. It never mutates anything.
func Status(ctx context.Context, deps StatusDeps, req StatusRequest) (StatusResult, error) {
	if req.SessionID == "" {
		return StatusResult{}, &domain.Error{
			Code: domain.ErrCodeValidation, Message: "orchestrator: Status requires a SessionID", Retryable: false,
		}
	}

	result := StatusResult{SessionID: req.SessionID}

	if deps.ProgressTree != nil && req.TaskID != nil {
		if snap, err := deps.ProgressTree.Snapshot(ctx, *req.TaskID); err == nil {
			result.ProgressTree = snap
			result.HasProgressTree = true
		}
	}

	return result, nil
}

// --- preflight doctor ----------------------------------------------------

// CheckStatus is one doctor check's outcome.
type CheckStatus string

const (
	CheckOK      CheckStatus = "ok"
	CheckWarn    CheckStatus = "warn"
	CheckFail    CheckStatus = "fail"
	CheckSkipped CheckStatus = "skipped"
)

// CheckResult is a single named doctor check's result.
type CheckResult struct {
	Name   string
	Status CheckStatus
	Detail string
}

// DoctorResult is `preflight doctor`'s full report: every check that ran,
// in a fixed order, plus an overall pass/fail summarizing them (fail if
// any check is CheckFail, ok otherwise — CheckWarn/CheckSkipped do not by
// themselves fail the overall report, matching ADD §17.5's fail-open
// posture for operational diagnostics).
type DoctorResult struct {
	Checks  []CheckResult
	Healthy bool
}

// DBPinger is the narrow interface Doctor needs from a database handle —
// just enough to prove it is reachable and to read its schema version.
// Satisfied by *sqlite.DB, whose Conn() method returns the underlying
// *sql.DB (which itself has PingContext) — declared locally rather than
// importing *sqlite.DB's concrete type so Doctor can be tested without a
// real SQLite file, and so this interface depends on exactly the two
// capabilities it uses, not sqlite.DB's whole surface.
type DBPinger interface {
	Conn() *sql.DB
	CurrentVersion(ctx context.Context) (int, error)
}

// ConfigLoader reports whether Preflight's configuration loads cleanly.
// Declared locally (not internal/config's concrete Load signature) so
// Doctor depends on the one capability it actually needs.
type ConfigLoader interface {
	LoadConfig() error
}

// DoctorDeps bundles Doctor's collaborators. Every field is optional:
// omitting one renders that check CheckSkipped rather than failing the
// whole report — Doctor never panics or aborts early just because one
// dependency was not wired (e.g. a CLI invocation with no DB path
// configured yet, before `preflight init`).
type DoctorDeps struct {
	DB           DBPinger
	Config       ConfigLoader
	RequiredDirs []string
}

// Doctor implements `preflight doctor`: DB reachable/migrated, config
// loadable, required directories/permissions present. Purely diagnostic —
// it never creates a directory, writes a config file, or applies a
// migration; every check is read-only (DBPinger.CurrentVersion may create
// schema_migrations if wholly absent, mirroring *sqlite.DB's own
// documented behavior, not a Doctor-specific write).
func Doctor(ctx context.Context, deps DoctorDeps) DoctorResult {
	var checks []CheckResult

	checks = append(checks, checkDB(ctx, deps.DB))
	checks = append(checks, checkConfig(deps.Config))
	checks = append(checks, checkDirs(deps.RequiredDirs)...)

	healthy := true
	for _, c := range checks {
		if c.Status == CheckFail {
			healthy = false
			break
		}
	}

	return DoctorResult{Checks: checks, Healthy: healthy}
}

func checkDB(ctx context.Context, db DBPinger) CheckResult {
	if db == nil {
		return CheckResult{Name: "database", Status: CheckSkipped, Detail: "no database configured"}
	}
	if err := db.Conn().PingContext(ctx); err != nil {
		return CheckResult{Name: "database", Status: CheckFail, Detail: "unreachable: " + err.Error()}
	}
	version, err := db.CurrentVersion(ctx)
	if err != nil {
		return CheckResult{Name: "database", Status: CheckFail, Detail: "schema version unreadable: " + err.Error()}
	}
	if version <= 0 {
		return CheckResult{Name: "database", Status: CheckWarn, Detail: "reachable but no migrations applied yet"}
	}
	return CheckResult{Name: "database", Status: CheckOK, Detail: "reachable, schema version " + strconv.Itoa(version)}
}

func checkConfig(cfg ConfigLoader) CheckResult {
	if cfg == nil {
		return CheckResult{Name: "config", Status: CheckSkipped, Detail: "no config loader configured"}
	}
	if err := cfg.LoadConfig(); err != nil {
		return CheckResult{Name: "config", Status: CheckFail, Detail: "load failed: " + err.Error()}
	}
	return CheckResult{Name: "config", Status: CheckOK, Detail: "loaded"}
}

func checkDirs(dirs []string) []CheckResult {
	results := make([]CheckResult, 0, len(dirs))
	for _, dir := range dirs {
		info, err := os.Stat(dir)
		if err != nil {
			if os.IsNotExist(err) {
				results = append(results, CheckResult{Name: "dir:" + dir, Status: CheckFail, Detail: "does not exist"})
				continue
			}
			results = append(results, CheckResult{Name: "dir:" + dir, Status: CheckFail, Detail: err.Error()})
			continue
		}
		if !info.IsDir() {
			results = append(results, CheckResult{Name: "dir:" + dir, Status: CheckFail, Detail: "exists but is not a directory"})
			continue
		}
		if !isWritable(dir) {
			results = append(results, CheckResult{Name: "dir:" + dir, Status: CheckFail, Detail: "not writable"})
			continue
		}
		results = append(results, CheckResult{Name: "dir:" + dir, Status: CheckOK, Detail: "present and writable"})
	}
	return results
}

// isWritable probes write permission by creating and immediately removing
// a temp file inside dir — a permission bit check alone (info.Mode())
// is not reliable across platforms/filesystems (e.g. ACLs, mounted
// read-only filesystems that still report writable bits), and doctor is
// explicitly a read-diagnostic command, so this probe's own artifact is
// removed in the same call, leaving no observable side effect.
func isWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".preflight-doctor-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}
