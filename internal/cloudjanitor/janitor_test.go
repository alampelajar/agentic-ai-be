package cloudjanitor

import (
	"fmt"
	"os"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"agentic-ai-backend/internal/cloudworkspace"
	"agentic-ai-backend/internal/testdb"
	"agentic-ai-backend/models"
)

type fixture struct {
	db      *gorm.DB
	storage *cloudworkspace.LocalDevelopmentStorage
	user    models.User
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dsn := os.Getenv("SKILLS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SKILLS_TEST_DATABASE_URL is not configured")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := testdb.Migrate(db, &models.User{}, &models.Task{}, &models.TaskMessage{}, &models.CloudWorkspace{}, &models.ExecutionJob{}, &models.WorkspaceToolApproval{}); err != nil {
		t.Fatal(err)
	}
	f := &fixture{db: db}
	f.user = models.User{Name: "Janitor", Email: fmt.Sprintf("janitor-%d-%d@example.test", os.Getpid(), time.Now().UnixNano()%1_000_000), Password: "x"}
	if err := db.Create(&f.user).Error; err != nil {
		t.Fatal(err)
	}
	f.storage, _ = cloudworkspace.NewLocalDevelopmentStorage(t.TempDir())
	t.Cleanup(func() {
		db.Where("user_id = ?", f.user.ID).Delete(&models.ExecutionJob{})
		db.Where("user_id = ?", f.user.ID).Delete(&models.Task{})
		db.Unscoped().Where("user_id = ?", f.user.ID).Delete(&models.CloudWorkspace{})
		db.Unscoped().Delete(&f.user)
	})
	return f
}

func (f *fixture) workspace(t *testing.T, name string, lastAccess *time.Time, created time.Time, status string) models.CloudWorkspace {
	t.Helper()
	ref, _ := f.storage.Create(f.user.ID)
	dir, _ := f.storage.FilesDir(f.user.ID, ref)
	_ = os.WriteFile(dir+"/a.txt", []byte("data"), 0o640)
	workspace := models.CloudWorkspace{UserID: f.user.ID, Name: name, SourceType: "empty", Status: status, StorageRef: ref, LastAccessedAt: lastAccess, CreatedAt: created}
	if err := f.db.Create(&workspace).Error; err != nil {
		t.Fatal(err)
	}
	f.db.Model(&workspace).UpdateColumn("created_at", created)
	return workspace
}

func names(workspaces []models.CloudWorkspace) map[string]bool {
	result := map[string]bool{}
	for _, workspace := range workspaces {
		result[workspace.Name] = true
	}
	return result
}

func TestOnlyLongUnusedReadyWorkspacesWithoutActiveJobsAreCandidates(t *testing.T) {
	f := newFixture(t)
	now := time.Now()
	long, recent := now.Add(-200*24*time.Hour), now.Add(-2*24*time.Hour)

	f.workspace(t, "stale-never-opened", nil, long, models.WorkspaceStatusReady)
	accessedLong := long
	f.workspace(t, "stale-opened-long-ago", &accessedLong, long, models.WorkspaceStatusReady)
	f.workspace(t, "old-but-opened-recently", &recent, long, models.WorkspaceStatusReady)
	f.workspace(t, "new", nil, recent, models.WorkspaceStatusReady)
	f.workspace(t, "stale-but-not-ready", nil, long, models.WorkspaceStatusCreating)
	busy := f.workspace(t, "stale-with-a-running-job", nil, long, models.WorkspaceStatusReady)
	f.db.Create(&models.ExecutionJob{UserID: f.user.ID, WorkspaceID: busy.ID, JobType: "go-test", Command: "go test ./...", Runtime: "go", Status: models.JobRunning, TimeoutSeconds: 60})

	candidates, err := Inactive(f.db, 90*24*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	got := names(candidates)
	for _, want := range []string{"stale-never-opened", "stale-opened-long-ago"} {
		if !got[want] {
			t.Errorf("%s should be a candidate", want)
		}
	}
	for _, unwanted := range []string{"old-but-opened-recently", "new", "stale-but-not-ready", "stale-with-a-running-job"} {
		if got[unwanted] {
			t.Errorf("%s must not be a candidate", unwanted)
		}
	}
}

func TestRetentionCannotBeShorterThanAWeek(t *testing.T) {
	f := newFixture(t)
	now := time.Now()
	f.workspace(t, "three-days-old", nil, now.Add(-3*24*time.Hour), models.WorkspaceStatusReady)
	f.workspace(t, "ten-days-old", nil, now.Add(-10*24*time.Hour), models.WorkspaceStatusReady)
	candidates, err := Inactive(f.db, time.Hour, now) // a typo: one hour
	if err != nil {
		t.Fatal(err)
	}
	got := names(candidates)
	if got["three-days-old"] || !got["ten-days-old"] {
		t.Fatalf("candidates with a one hour retention: %v", got)
	}
}

func TestRemoveDeletesFilesAndRowsButKeepsTheTask(t *testing.T) {
	f := newFixture(t)
	workspace := f.workspace(t, "to-remove", nil, time.Now().Add(-200*24*time.Hour), models.WorkspaceStatusReady)
	other := f.workspace(t, "to-keep", nil, time.Now(), models.WorkspaceStatusReady)
	task := models.Task{UserID: f.user.ID, Title: "History", Status: "todo", Label: "feature", Priority: "medium", WorkspaceID: &workspace.ID}
	f.db.Create(&task)
	f.db.Create(&models.ExecutionJob{UserID: f.user.ID, WorkspaceID: workspace.ID, JobType: "go-test", Command: "go test ./...", Runtime: "go", Status: models.JobCompleted, TimeoutSeconds: 60})
	dir, _ := f.storage.FilesDir(f.user.ID, workspace.StorageRef)
	keptDir, _ := f.storage.FilesDir(f.user.ID, other.StorageRef)

	if err := Remove(f.db, f.storage, workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("the files remain")
	}
	if _, err := os.Stat(keptDir); err != nil {
		t.Error("another workspace's files were removed")
	}
	var rows, jobs int64
	f.db.Unscoped().Model(&models.CloudWorkspace{}).Where("id = ?", workspace.ID).Count(&rows)
	f.db.Model(&models.ExecutionJob{}).Where("workspace_id = ?", workspace.ID).Count(&jobs)
	if rows != 0 || jobs != 0 {
		t.Errorf("rows=%d jobs=%d", rows, jobs)
	}
	var after models.Task
	if err := f.db.First(&after, task.ID).Error; err != nil || after.WorkspaceID != nil || after.Title != "History" {
		t.Errorf("task after removal = %+v, %v", after, err)
	}
	f.db.Unscoped().Model(&models.CloudWorkspace{}).Where("id = ?", other.ID).Count(&rows)
	if rows != 1 {
		t.Error("another workspace row was removed")
	}
}
