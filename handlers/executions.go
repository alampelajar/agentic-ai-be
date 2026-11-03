package handlers

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/config"
	"agentic-ai-backend/internal/jobspec"
	"agentic-ai-backend/models"
)

// Execution jobs (Phase 6). The API server only validates, queues, lists and
// cancels jobs. The separate execution worker (cmd/execution-worker) is the only
// process that runs a command, and only inside the sandbox.

// Limits on queued work. They are variables so tests can lower them.
var (
	maxActiveJobsPerUser = 3
	maxQueuedJobsGlobal  = 100
)

// executionEnabled is false unless the operator turned execution on. Even then
// nothing runs unless a worker with a working sandbox is polling the queue.
func executionEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("CLOUD_EXECUTION_ENABLED")), "true")
}

func executionImages() jobspec.Images {
	images := jobspec.DefaultImages()
	for name, target := range map[string]*string{"CLOUD_EXEC_IMAGE_GO": &images.Go, "CLOUD_EXEC_IMAGE_NODE": &images.Node, "CLOUD_EXEC_IMAGE_PYTHON": &images.Python} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			*target = value
		}
	}
	return images
}

type queueError struct {
	status  int
	message string
}

func (e *queueError) Error() string { return e.message }

// queueJob validates and queues one job. The HTTP handler and the agent tools
// both call it, so an agent can start nothing the API would refuse.
func queueJob(handle *workspaceHandle, jobType string, timeoutSeconds int, taskID, agentID *uint) (*models.ExecutionJob, *queueError) {
	if !executionEnabled() {
		return nil, &queueError{http.StatusServiceUnavailable, "Eksekusi kode di cloud belum diaktifkan di server ini. Workspace tetap dapat dibaca dan diedit."}
	}
	if err := handle.requireReady(); err != nil {
		return nil, &queueError{http.StatusConflict, "Workspace belum siap."}
	}
	spec, err := jobspec.Resolve(strings.TrimSpace(jobType), executionImages(), handle.FilesDir)
	if err != nil {
		return nil, &queueError{http.StatusBadRequest, err.Error()}
	}
	userID := handle.Workspace.UserID
	if taskID != nil {
		var count int64
		config.DB.Model(&models.Task{}).Where("id = ? AND user_id = ?", *taskID, userID).Count(&count)
		if count == 0 {
			return nil, &queueError{http.StatusBadRequest, "Task tidak ditemukan."}
		}
	}
	if agentID != nil && !ensureAgentAccessible(userID, *agentID) {
		return nil, &queueError{http.StatusBadRequest, "Agent tidak ditemukan."}
	}

	var active, queued int64
	config.DB.Model(&models.ExecutionJob{}).Where("user_id = ? AND status IN ?", userID, activeJobStatuses).Count(&active)
	if int(active) >= maxActiveJobsPerUser {
		return nil, &queueError{http.StatusTooManyRequests, "Terlalu banyak job yang sedang berjalan. Tunggu atau batalkan salah satu."}
	}
	config.DB.Model(&models.ExecutionJob{}).Where("status = ?", models.JobQueued).Count(&queued)
	if int(queued) >= maxQueuedJobsGlobal {
		return nil, &queueError{http.StatusServiceUnavailable, "Antrean eksekusi penuh. Coba lagi sebentar lagi."}
	}

	job := models.ExecutionJob{
		UserID: userID, WorkspaceID: handle.Workspace.ID, TaskID: taskID, AgentID: agentID,
		JobType: spec.JobType, Command: spec.Label, Runtime: spec.Runtime, Status: models.JobQueued,
		TimeoutSeconds: int(jobspec.ClampTimeout(timeoutSeconds, spec) / time.Second),
	}
	if err := config.DB.Create(&job).Error; err != nil {
		return nil, &queueError{http.StatusInternalServerError, "Gagal membuat job."}
	}
	return &job, nil
}

func CreateExecution(c *gin.Context) {
	handle, ok := openOwned(c, true)
	if !ok {
		return
	}
	var req struct {
		JobType        string `json:"job_type"`
		TimeoutSeconds int    `json:"timeout_seconds"`
		TaskID         *uint  `json:"task_id"`
		AgentID        *uint  `json:"agent_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		workspaceError(c, http.StatusBadRequest, "Format job tidak valid.")
		return
	}
	job, queueErr := queueJob(handle, req.JobType, req.TimeoutSeconds, req.TaskID, req.AgentID)
	if queueErr != nil {
		workspaceError(c, queueErr.status, queueErr.message)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"ok": true, "job": job})
}

func jobIDParam(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	return uint(id), err == nil && id != 0
}

func loadOwnedJob(c *gin.Context) (*models.ExecutionJob, uint, bool) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		workspaceError(c, http.StatusUnauthorized, "User tidak ditemukan.")
		return nil, 0, false
	}
	id, ok := jobIDParam(c)
	if !ok {
		workspaceError(c, http.StatusNotFound, "Job tidak ditemukan.")
		return nil, 0, false
	}
	var job models.ExecutionJob
	if err := config.DB.Where("id = ? AND user_id = ?", id, userID).First(&job).Error; err != nil {
		workspaceError(c, http.StatusNotFound, "Job tidak ditemukan.")
		return nil, 0, false
	}
	return &job, userID, true
}

func GetExecution(c *gin.Context) {
	job, _, ok := loadOwnedJob(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "job": job})
}

func GetWorkspaceExecutions(c *gin.Context) {
	handle, ok := openOwned(c, false)
	if !ok {
		return
	}
	jobs := []models.ExecutionJob{}
	if err := config.DB.Where("workspace_id = ? AND user_id = ?", handle.Workspace.ID, handle.Workspace.UserID).
		Order("id DESC").Limit(50).Find(&jobs).Error; err != nil {
		workspaceError(c, http.StatusInternalServerError, "Gagal mengambil job.")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "jobs": jobs, "execution_enabled": executionEnabled()})
}

// CancelExecution cancels a queued job at once and asks the worker to stop a
// running one. A job that is already over is left as it is.
func CancelExecution(c *gin.Context) {
	job, userID, ok := loadOwnedJob(c)
	if !ok {
		return
	}
	switch job.Status {
	case models.JobQueued:
		now := time.Now()
		config.DB.Model(&models.ExecutionJob{}).Where("id = ? AND user_id = ? AND status = ?", job.ID, userID, models.JobQueued).
			Updates(map[string]interface{}{"status": models.JobCancelled, "cancel_requested": true, "error": "The job was cancelled.", "completed_at": now})
	case models.JobPreparing, models.JobRunning:
		config.DB.Model(&models.ExecutionJob{}).Where("id = ? AND user_id = ? AND status IN ?", job.ID, userID, []string{models.JobPreparing, models.JobRunning}).
			UpdateColumn("cancel_requested", true)
	}
	config.DB.Where("id = ? AND user_id = ?", job.ID, userID).First(job)
	c.JSON(http.StatusOK, gin.H{"ok": true, "job": job})
}
