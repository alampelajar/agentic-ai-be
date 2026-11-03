package cloudexec

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"agentic-ai-backend/internal/cloudworkspace"
	"agentic-ai-backend/internal/jobspec"
	"agentic-ai-backend/models"
	"agentic-ai-backend/services"
)

// Worker takes queued jobs from the database and runs them through an Executor.
// It is the only place that executes anything, and it runs as its own process.
type Worker struct {
	DB       *gorm.DB
	Storage  cloudworkspace.WorkspaceStorage
	Executor Executor
	Images   jobspec.Images

	// MaxConcurrent is how many jobs run at once on this worker; MaxPerUser is how
	// many of those may belong to one user.
	MaxConcurrent int
	MaxPerUser    int
	PollInterval  time.Duration
	// StaleAfter is how long past its timeout a running job may stay before it is
	// declared failed (the worker that had it died).
	StaleAfter time.Duration
	// OnlyUsers, when set, limits the worker to the jobs of these users. Tests that
	// share one database use it so parallel packages do not take each other's jobs.
	OnlyUsers []uint
	Logf      func(format string, args ...any)
}

func (w *Worker) logf(format string, args ...any) {
	if w.Logf != nil {
		w.Logf(format, args...)
	} else {
		log.Printf(format, args...)
	}
}

func (w *Worker) defaults() {
	if w.MaxConcurrent <= 0 {
		w.MaxConcurrent = 2
	}
	if w.MaxPerUser <= 0 {
		w.MaxPerUser = 1
	}
	if w.PollInterval <= 0 {
		w.PollInterval = 2 * time.Second
	}
	if w.StaleAfter <= 0 {
		w.StaleAfter = 2 * time.Minute
	}
	if w.Images == (jobspec.Images{}) {
		w.Images = jobspec.DefaultImages()
	}
}

// Run works until ctx is cancelled, then waits for running jobs to be stopped.
func (w *Worker) Run(ctx context.Context) {
	w.defaults()
	slots := make(chan struct{}, w.MaxConcurrent)
	var running sync.WaitGroup
	ticker := time.NewTicker(w.PollInterval)
	defer ticker.Stop()
	lastReap := time.Time{}
	for {
		if time.Since(lastReap) > 30*time.Second {
			w.ReapStale(ctx, time.Now())
			lastReap = time.Now()
		}
		for {
			select {
			case slots <- struct{}{}:
			default:
				goto wait // all slots busy
			}
			job, err := w.ClaimNext(ctx)
			if err != nil || job == nil {
				<-slots
				if err != nil && ctx.Err() == nil {
					w.logf("[EXEC] claim failed: %v", err)
				}
				break
			}
			running.Add(1)
			go func() {
				defer running.Done()
				defer func() { <-slots }()
				w.RunJob(ctx, job)
			}()
		}
	wait:
		select {
		case <-ctx.Done():
			running.Wait()
			return
		case <-ticker.C:
		}
	}
}

// ClaimNext moves the oldest eligible queued job to "preparing" and returns it.
// SKIP LOCKED lets several workers share one queue without taking the same job.
// A job is skipped while its user already has MaxPerUser jobs in progress.
func (w *Worker) ClaimNext(ctx context.Context) (*models.ExecutionJob, error) {
	w.defaults()
	var claimed *models.ExecutionJob
	err := w.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var candidates []models.ExecutionJob
		query := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ? AND cancel_requested = ?", models.JobQueued, false)
		if len(w.OnlyUsers) > 0 {
			query = query.Where("user_id IN ?", w.OnlyUsers)
		}
		if err := query.Order("id ASC").Limit(25).Find(&candidates).Error; err != nil {
			return err
		}
		for index := range candidates {
			job := candidates[index]
			var active int64
			if err := tx.Model(&models.ExecutionJob{}).Where("user_id = ? AND status IN ?", job.UserID, []string{models.JobPreparing, models.JobRunning}).Count(&active).Error; err != nil {
				return err
			}
			if int(active) >= w.MaxPerUser {
				continue
			}
			now := time.Now()
			result := tx.Model(&models.ExecutionJob{}).Where("id = ? AND status = ?", job.ID, models.JobQueued).
				Updates(map[string]interface{}{"status": models.JobPreparing, "started_at": now})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 1 {
				job.Status, job.StartedAt = models.JobPreparing, &now
				claimed = &job
				return nil
			}
		}
		return nil
	})
	return claimed, err
}

// finish records the outcome unless the job already reached a final state (a
// cancel request for a queued job, say), so a late result never overwrites it.
func (w *Worker) finish(job *models.ExecutionJob, updates map[string]interface{}) {
	now := time.Now()
	updates["completed_at"] = now
	if job.StartedAt != nil {
		updates["duration_ms"] = now.Sub(*job.StartedAt).Milliseconds()
	}
	if err := w.DB.Model(&models.ExecutionJob{}).Where("id = ? AND status IN ?", job.ID, []string{models.JobPreparing, models.JobRunning}).Updates(updates).Error; err != nil {
		w.logf("[EXEC] could not record the result of job %d: %v", job.ID, err)
	}
}

func fail(job *models.ExecutionJob, w *Worker, message string) {
	w.finish(job, map[string]interface{}{"status": models.JobFailed, "error": message})
}

// RunJob runs one claimed job from preparation to its final state.
func (w *Worker) RunJob(ctx context.Context, job *models.ExecutionJob) {
	w.defaults()

	var workspace models.CloudWorkspace
	if err := w.DB.Where("id = ? AND user_id = ?", job.WorkspaceID, job.UserID).First(&workspace).Error; err != nil {
		fail(job, w, "The workspace no longer exists.")
		return
	}
	if workspace.Status != models.WorkspaceStatusReady {
		fail(job, w, "The workspace is not ready.")
		return
	}
	filesDir, err := w.Storage.FilesDir(job.UserID, workspace.StorageRef)
	if err != nil {
		fail(job, w, "The workspace storage is not available.")
		return
	}
	// The command comes from the allowlist by job type; the stored text is only a label.
	spec, err := jobspec.Resolve(job.JobType, w.Images, filesDir)
	if err != nil {
		fail(job, w, err.Error())
		return
	}
	timeout := jobspec.ClampTimeout(job.TimeoutSeconds, spec)

	started := w.DB.Model(&models.ExecutionJob{}).Where("id = ? AND status = ?", job.ID, models.JobPreparing).UpdateColumn("status", models.JobRunning)
	if started.Error != nil {
		w.logf("[EXEC] job %d could not start: %v", job.ID, started.Error)
		return
	}
	if started.RowsAffected == 0 {
		return // cancelled or reaped between being claimed and starting
	}

	// Watch for a cancel request while the command runs.
	runCtx, stopRun := context.WithCancel(ctx)
	defer stopRun()
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				var requested bool
				w.DB.Model(&models.ExecutionJob{}).Where("id = ?", job.ID).Select("cancel_requested").Scan(&requested)
				if requested {
					stopRun()
					return
				}
			}
		}
	}()

	result, runErr := w.Executor.Run(runCtx, RunRequest{
		Name:     "cloudexec-" + strconv.FormatUint(uint64(job.ID), 10) + "-" + strconv.FormatInt(time.Now().UnixNano()%1_000_000, 36),
		FilesDir: filesDir,
		Spec:     spec,
		Timeout:  timeout,
	})
	stopRun()
	<-watcherDone

	status, message := models.JobCompleted, ""
	hasExit := false
	switch {
	case runErr != nil:
		status, message = models.JobFailed, services.RedactSecrets(runErr.Error())
	case result.TimedOut:
		status, message = models.JobTimedOut, fmt.Sprintf("The command did not finish within %s and was stopped.", timeout)
	case result.Cancelled || ctx.Err() != nil:
		status, message = models.JobCancelled, "The job was cancelled."
	case result.ExitCode == 0:
		hasExit = true
	default:
		status, hasExit = models.JobFailed, true
	}
	if result.Truncated {
		if message != "" {
			message += " "
		}
		message += fmt.Sprintf("Output was cut at %d KiB.", MaxOutputBytes>>10)
	}
	updates := map[string]interface{}{
		"status": status,
		"stdout": truncateStored(services.RedactSecrets(result.Stdout)),
		"stderr": truncateStored(services.RedactSecrets(result.Stderr)),
		"error":  message,
	}
	if hasExit {
		updates["exit_code"] = result.ExitCode
	}
	w.finish(job, updates)
	w.refreshUsage(workspace)
}

func truncateStored(text string) string {
	if len(text) > MaxOutputBytes {
		return text[:MaxOutputBytes]
	}
	return text
}

// refreshUsage updates the cached size after a build wrote files.
func (w *Worker) refreshUsage(workspace models.CloudWorkspace) {
	bytes, files, err := w.Storage.Usage(workspace.UserID, workspace.StorageRef)
	if err != nil {
		return
	}
	w.DB.Model(&models.CloudWorkspace{}).Where("id = ?", workspace.ID).UpdateColumns(map[string]interface{}{"size_bytes": bytes, "file_count": files})
}

// ReapStale fails jobs that have been "running" far longer than any timeout
// allows, which means the worker that had them is gone.
func (w *Worker) ReapStale(ctx context.Context, now time.Time) int64 {
	w.defaults()
	cutoff := now.Add(-(jobspec.MaxTimeout + w.StaleAfter))
	query := w.DB.WithContext(ctx).Model(&models.ExecutionJob{}).
		Where("status IN ? AND started_at < ?", []string{models.JobPreparing, models.JobRunning}, cutoff)
	if len(w.OnlyUsers) > 0 {
		query = query.Where("user_id IN ?", w.OnlyUsers)
	}
	result := query.Updates(map[string]interface{}{"status": models.JobFailed, "error": "The worker stopped before this job finished.", "completed_at": now})
	if result.Error != nil && !errors.Is(result.Error, context.Canceled) {
		w.logf("[EXEC] reaping stale jobs failed: %v", result.Error)
	}
	return result.RowsAffected
}
