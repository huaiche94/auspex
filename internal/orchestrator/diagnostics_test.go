package orchestrator_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/huaiche94/preflight/internal/app"
	"github.com/huaiche94/preflight/internal/domain"
	"github.com/huaiche94/preflight/internal/orchestrator"
	"github.com/huaiche94/preflight/internal/storage/sqlite"
	"github.com/huaiche94/preflight/internal/testutil/fakes"
)

// --- Status ---------------------------------------------------------------

func TestStatus_RequiresSessionID(t *testing.T) {
	_, err := orchestrator.Status(context.Background(), orchestrator.StatusDeps{}, orchestrator.StatusRequest{})
	if err == nil {
		t.Fatal("expected a validation error for an empty SessionID")
	}
	var derr *domain.Error
	if !errors.As(err, &derr) {
		t.Fatalf("err = %T, want *domain.Error", err)
	}
	if derr.Code != domain.ErrCodeValidation {
		t.Errorf("Code = %q, want %q", derr.Code, domain.ErrCodeValidation)
	}
}

func TestStatus_NoDepsStillSucceedsDegraded(t *testing.T) {
	result, err := orchestrator.Status(context.Background(), orchestrator.StatusDeps{}, orchestrator.StatusRequest{
		SessionID: "sess-1",
	})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if result.SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want sess-1", result.SessionID)
	}
	if result.HasProgressTree {
		t.Error("HasProgressTree = true, want false (no ProgressTree service configured)")
	}
}

func TestStatus_LoadsProgressTreeWhenTaskIDGiven(t *testing.T) {
	taskID := domain.TaskID("task-1")
	svc := &fakes.FakeProgressTreeService{
		SnapshotFunc: func(_ context.Context, gotID domain.TaskID) (app.ProgressTreeSnapshot, error) {
			if gotID != taskID {
				t.Errorf("Snapshot called with %q, want %q", gotID, taskID)
			}
			return app.ProgressTreeSnapshot{TaskID: taskID, Nodes: []app.ProgressNode{{ID: "n1"}, {ID: "n2"}}}, nil
		},
	}
	result, err := orchestrator.Status(context.Background(), orchestrator.StatusDeps{ProgressTree: svc}, orchestrator.StatusRequest{
		SessionID: "sess-1", TaskID: &taskID,
	})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !result.HasProgressTree {
		t.Fatal("HasProgressTree = false, want true")
	}
	if len(result.ProgressTree.Nodes) != 2 {
		t.Errorf("ProgressTree.Nodes = %v, want 2 entries", result.ProgressTree.Nodes)
	}
}

func TestStatus_ProgressTreeErrorDegradesNotAborts(t *testing.T) {
	taskID := domain.TaskID("task-1")
	svc := &fakes.FakeProgressTreeService{
		SnapshotFunc: func(_ context.Context, _ domain.TaskID) (app.ProgressTreeSnapshot, error) {
			return app.ProgressTreeSnapshot{}, &domain.Error{Code: domain.ErrCodeUnavailable, Message: "db down"}
		},
	}
	result, err := orchestrator.Status(context.Background(), orchestrator.StatusDeps{ProgressTree: svc}, orchestrator.StatusRequest{
		SessionID: "sess-1", TaskID: &taskID,
	})
	if err != nil {
		t.Fatalf("Status should fail open on a Snapshot error, got: %v", err)
	}
	if result.HasProgressTree {
		t.Error("HasProgressTree = true, want false after a Snapshot error")
	}
}

// --- Doctor -----------------------------------------------------------------

type fakeDB struct {
	sqlDB      *sql.DB
	version    int
	versionErr error
}

func (f *fakeDB) Conn() *sql.DB { return f.sqlDB }

func (f *fakeDB) CurrentVersion(ctx context.Context) (int, error) {
	if f.versionErr != nil {
		return 0, f.versionErr
	}
	return f.version, nil
}

func openClosedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}
	return db
}

func TestDoctor_NoDepsConfigured_AllChecksSkipped(t *testing.T) {
	result := orchestrator.Doctor(context.Background(), orchestrator.DoctorDeps{})
	if !result.Healthy {
		t.Error("Healthy = false, want true (skipped checks do not fail the report)")
	}
	for _, c := range result.Checks {
		if c.Status != orchestrator.CheckSkipped {
			t.Errorf("check %q Status = %q, want skipped", c.Name, c.Status)
		}
	}
	if len(result.Checks) < 2 {
		t.Fatalf("expected at least a database and config check, got %v", result.Checks)
	}
}

func TestDoctor_DBReachableAndMigrated_OK(t *testing.T) {
	dir := t.TempDir()
	db, err := sqlite.Open(context.Background(), filepath.Join(dir, "preflight.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	migrations, err := sqlite.AllMigrations()
	if err != nil {
		t.Fatalf("AllMigrations: %v", err)
	}
	if err := db.Migrate(context.Background(), migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	result := orchestrator.Doctor(context.Background(), orchestrator.DoctorDeps{DB: db})
	dbCheck := findCheck(t, result, "database")
	if dbCheck.Status != orchestrator.CheckOK {
		t.Errorf("database check Status = %q, want ok (detail: %s)", dbCheck.Status, dbCheck.Detail)
	}
	if !result.Healthy {
		t.Error("Healthy = false, want true")
	}
}

func TestDoctor_DBUnreachable_Fails(t *testing.T) {
	closed := openClosedDB(t)
	result := orchestrator.Doctor(context.Background(), orchestrator.DoctorDeps{
		DB: &fakeDB{sqlDB: closed},
	})
	dbCheck := findCheck(t, result, "database")
	if dbCheck.Status != orchestrator.CheckFail {
		t.Errorf("database check Status = %q, want fail", dbCheck.Status)
	}
	if result.Healthy {
		t.Error("Healthy = true, want false (a fail check must fail the overall report)")
	}
}

type fakeConfigLoader struct{ err error }

func (f *fakeConfigLoader) LoadConfig() error { return f.err }

func TestDoctor_ConfigLoadable_OK(t *testing.T) {
	result := orchestrator.Doctor(context.Background(), orchestrator.DoctorDeps{Config: &fakeConfigLoader{}})
	cfgCheck := findCheck(t, result, "config")
	if cfgCheck.Status != orchestrator.CheckOK {
		t.Errorf("config check Status = %q, want ok", cfgCheck.Status)
	}
}

func TestDoctor_ConfigLoadFailure_Fails(t *testing.T) {
	result := orchestrator.Doctor(context.Background(), orchestrator.DoctorDeps{
		Config: &fakeConfigLoader{err: errors.New("invalid schema_version")},
	})
	cfgCheck := findCheck(t, result, "config")
	if cfgCheck.Status != orchestrator.CheckFail {
		t.Errorf("config check Status = %q, want fail", cfgCheck.Status)
	}
	if result.Healthy {
		t.Error("Healthy = true, want false")
	}
}

func TestDoctor_RequiredDirs_PresentAndWritable_OK(t *testing.T) {
	dir := t.TempDir()
	result := orchestrator.Doctor(context.Background(), orchestrator.DoctorDeps{RequiredDirs: []string{dir}})
	dirCheck := findCheck(t, result, "dir:"+dir)
	if dirCheck.Status != orchestrator.CheckOK {
		t.Errorf("dir check Status = %q, want ok (detail: %s)", dirCheck.Status, dirCheck.Detail)
	}
}

func TestDoctor_RequiredDirs_Missing_Fails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	result := orchestrator.Doctor(context.Background(), orchestrator.DoctorDeps{RequiredDirs: []string{missing}})
	dirCheck := findCheck(t, result, "dir:"+missing)
	if dirCheck.Status != orchestrator.CheckFail {
		t.Errorf("dir check Status = %q, want fail", dirCheck.Status)
	}
	if result.Healthy {
		t.Error("Healthy = true, want false")
	}
}

func TestDoctor_DoesNotMutateFilesystem(t *testing.T) {
	dir := t.TempDir()
	before, err := filepathGlobCount(dir)
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	orchestrator.Doctor(context.Background(), orchestrator.DoctorDeps{RequiredDirs: []string{dir}})

	after, err := filepathGlobCount(dir)
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if before != after {
		t.Errorf("directory entry count changed from %d to %d — doctor's writability probe must clean up after itself", before, after)
	}
}

func filepathGlobCount(dir string) (int, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil {
		return 0, err
	}
	return len(matches), nil
}

func findCheck(t *testing.T, result orchestrator.DoctorResult, name string) orchestrator.CheckResult {
	t.Helper()
	for _, c := range result.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check named %q found in %v", name, result.Checks)
	return orchestrator.CheckResult{}
}
