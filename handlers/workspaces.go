package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"agentic-ai-backend/config"
	"agentic-ai-backend/internal/cloudworkspace"
	"agentic-ai-backend/models"
)

// Cloud Workspace (Phase 6). The API server keeps the files and the metadata and
// queues jobs; it never runs project code (see docs/phase6-cloud-workspace.md).

var (
	cloudMu         sync.Mutex
	cloudStorage    cloudworkspace.WorkspaceStorage
	cloudLimits     = cloudworkspace.DefaultLimits()
	cloudDownloader = cloudworkspace.NewGitHubDownloader()
)

// UseCloudWorkspaceStorage replaces the storage, limits and downloader. Tests
// and main call it; handlers fall back to CLOUD_WORKSPACE_ROOT otherwise.
func UseCloudWorkspaceStorage(storage cloudworkspace.WorkspaceStorage, limits cloudworkspace.Limits, downloader *cloudworkspace.GitHubDownloader) {
	cloudMu.Lock()
	defer cloudMu.Unlock()
	cloudStorage, cloudLimits = storage, limits
	if downloader != nil {
		cloudDownloader = downloader
	}
}

func workspaceStorage() (cloudworkspace.WorkspaceStorage, cloudworkspace.Limits, error) {
	cloudMu.Lock()
	defer cloudMu.Unlock()
	if cloudStorage == nil {
		root := strings.TrimSpace(os.Getenv("CLOUD_WORKSPACE_ROOT"))
		if root == "" {
			root = filepath.Join("data", "workspaces")
		}
		storage, err := cloudworkspace.NewLocalDevelopmentStorage(root)
		if err != nil {
			return nil, cloudLimits, err
		}
		cloudStorage = storage
	}
	return cloudStorage, cloudLimits, nil
}

func workspaceError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"ok": false, "error": message})
}

var runtimeNames = map[string]bool{"": true, "go": true, "node": true, "python": true}

func requireOwnedWorkspace(c *gin.Context, userID uint, workspaceID *uint) bool {
	if workspaceID == nil {
		return true
	}
	var count int64
	if err := config.DB.Model(&models.CloudWorkspace{}).Where("id = ? AND user_id = ?", *workspaceID, userID).Count(&count).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "Gagal memeriksa workspace."})
		return false
	}
	if count == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Workspace tidak ditemukan."})
		return false
	}
	return true
}

// ---------------------------------------------------------------- operations
// The operations below are shared by the HTTP handlers and the agent tools, so a
// tool can do nothing the API would refuse.

type workspaceHandle struct {
	Workspace models.CloudWorkspace
	FilesDir  string
	MetaDir   string
	Storage   cloudworkspace.WorkspaceStorage
	Limits    cloudworkspace.Limits
}

var errWorkspaceNotFound = errors.New("workspace not found")
var errWorkspaceNotReady = errors.New("workspace is not ready")

// openWorkspace finds a workspace of this user. Another user's workspace is
// indistinguishable from one that does not exist.
func openWorkspace(userID, workspaceID uint, touch bool) (*workspaceHandle, error) {
	var workspace models.CloudWorkspace
	if err := config.DB.Where("id = ? AND user_id = ?", workspaceID, userID).First(&workspace).Error; err != nil {
		return nil, errWorkspaceNotFound
	}
	storage, limits, err := workspaceStorage()
	if err != nil {
		return nil, err
	}
	filesDir, err := storage.FilesDir(userID, workspace.StorageRef)
	if err != nil {
		return nil, err
	}
	metaDir, err := storage.MetaDir(userID, workspace.StorageRef)
	if err != nil {
		return nil, err
	}
	if touch && (workspace.LastAccessedAt == nil || time.Since(*workspace.LastAccessedAt) > time.Minute) {
		now := time.Now()
		config.DB.Model(&models.CloudWorkspace{}).Where("id = ?", workspace.ID).UpdateColumn("last_accessed_at", now)
		workspace.LastAccessedAt = &now
	}
	return &workspaceHandle{Workspace: workspace, FilesDir: filesDir, MetaDir: metaDir, Storage: storage, Limits: limits}, nil
}

func (h *workspaceHandle) requireReady() error {
	if h.Workspace.Status != models.WorkspaceStatusReady {
		return errWorkspaceNotReady
	}
	return nil
}

// userUsage is how many bytes the user's other workspaces hold.
func userBytesExcluding(userID, exceptWorkspace uint) int64 {
	var total int64
	config.DB.Model(&models.CloudWorkspace{}).
		Where("user_id = ? AND id <> ?", userID, exceptWorkspace).
		Select("COALESCE(SUM(size_bytes), 0)").Scan(&total)
	return total
}

func (h *workspaceHandle) budget() int64 {
	remaining := h.Limits.MaxUserBytes - userBytesExcluding(h.Workspace.UserID, h.Workspace.ID) - h.Workspace.SizeBytes
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (h *workspaceHandle) refreshUsage() {
	bytes, files, err := h.Storage.Usage(h.Workspace.UserID, h.Workspace.StorageRef)
	if err != nil {
		return
	}
	h.Workspace.SizeBytes, h.Workspace.FileCount = bytes, files
	config.DB.Model(&models.CloudWorkspace{}).Where("id = ?", h.Workspace.ID).
		UpdateColumns(map[string]interface{}{"size_bytes": bytes, "file_count": files})
}

func (h *workspaceHandle) tree(dir string) (cloudworkspace.Tree, error) {
	if err := h.requireReady(); err != nil {
		return cloudworkspace.Tree{}, err
	}
	return cloudworkspace.BuildTree(h.FilesDir, dir, h.Limits)
}

func (h *workspaceHandle) readFile(path string) (string, int64, error) {
	if err := h.requireReady(); err != nil {
		return "", 0, err
	}
	return cloudworkspace.ReadFile(h.FilesDir, path, h.Limits)
}

func (h *workspaceHandle) writeFile(path, content string) (cloudworkspace.WriteResult, error) {
	if err := h.requireReady(); err != nil {
		return cloudworkspace.WriteResult{}, err
	}
	result, err := cloudworkspace.WriteFile(h.FilesDir, path, content, h.Limits, h.budget())
	if err == nil {
		h.refreshUsage()
	}
	return result, err
}

func (h *workspaceHandle) changes() ([]cloudworkspace.Change, error) {
	if err := h.requireReady(); err != nil {
		return nil, err
	}
	return cloudworkspace.Changes(h.FilesDir, h.MetaDir, h.Limits)
}

// fileFailure turns a workspace error into an HTTP status and a message that is
// safe to show. Anything unexpected is logged without file content and answered
// generically.
func fileFailure(err error) (int, string) {
	switch {
	case errors.Is(err, errWorkspaceNotFound):
		return http.StatusNotFound, "Workspace tidak ditemukan."
	case errors.Is(err, errWorkspaceNotReady):
		return http.StatusConflict, "Workspace belum siap."
	case errors.Is(err, cloudworkspace.ErrInvalidPath):
		return http.StatusBadRequest, "Path tidak valid. Gunakan path relatif di dalam workspace."
	case errors.Is(err, cloudworkspace.ErrSecretFile):
		return http.StatusForbidden, "File ini mungkin berisi rahasia dan tidak tersedia lewat workspace."
	case errors.Is(err, cloudworkspace.ErrGeneratedPath):
		return http.StatusForbidden, "Folder ini berisi dependensi hasil instalasi (node_modules atau .pydeps) dan tidak dapat dibuka atau diubah lewat workspace."
	case errors.Is(err, cloudworkspace.ErrNotFound):
		return http.StatusNotFound, "File tidak ditemukan."
	case errors.Is(err, cloudworkspace.ErrIsDirectory):
		return http.StatusBadRequest, "Path adalah direktori."
	case errors.Is(err, cloudworkspace.ErrTooLarge):
		return http.StatusRequestEntityTooLarge, "File terlalu besar."
	case errors.Is(err, cloudworkspace.ErrBinary):
		return http.StatusUnsupportedMediaType, "File biner tidak dapat ditampilkan atau disimpan sebagai teks."
	case errors.Is(err, cloudworkspace.ErrQuota):
		return http.StatusRequestEntityTooLarge, "Kuota penyimpanan terlampaui."
	}
	log.Printf("[WORKSPACE] unexpected error: %v", err)
	return http.StatusInternalServerError, "Operasi workspace gagal."
}

func openOwned(c *gin.Context, touch bool) (*workspaceHandle, bool) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		workspaceError(c, http.StatusUnauthorized, "User tidak ditemukan.")
		return nil, false
	}
	id, err := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id == 0 {
		workspaceError(c, http.StatusNotFound, "Workspace tidak ditemukan.")
		return nil, false
	}
	handle, err := openWorkspace(userID, uint(id), touch)
	if err != nil {
		status, message := fileFailure(err)
		workspaceError(c, status, message)
		return nil, false
	}
	return handle, true
}

// ------------------------------------------------------------------ handlers

func GetWorkspaces(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		workspaceError(c, http.StatusUnauthorized, "User tidak ditemukan.")
		return
	}
	workspaces := []models.CloudWorkspace{}
	if err := config.DB.Where("user_id = ?", userID).Order("COALESCE(last_accessed_at, created_at) DESC, id DESC").Find(&workspaces).Error; err != nil {
		workspaceError(c, http.StatusInternalServerError, "Gagal mengambil workspace.")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "workspaces": workspaces})
}

func GetWorkspace(c *gin.Context) {
	handle, ok := openOwned(c, true)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "workspace": handle.Workspace})
}

var safeFileName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// CreateWorkspace makes an empty workspace (JSON), imports a public GitHub
// repository (JSON) or unpacks an uploaded ZIP (multipart form).
func CreateWorkspace(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		workspaceError(c, http.StatusUnauthorized, "User tidak ditemukan.")
		return
	}
	storage, limits, err := workspaceStorage()
	if err != nil {
		workspaceError(c, http.StatusServiceUnavailable, "Penyimpanan workspace tidak tersedia.")
		return
	}

	var name, description, runtimeName, sourceType, sourceURL string
	var archive []byte
	if strings.HasPrefix(c.GetHeader("Content-Type"), "multipart/form-data") {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limits.MaxZipBytes+(1<<20))
		name, description, runtimeName = c.PostForm("name"), c.PostForm("description"), c.PostForm("default_runtime")
		header, fileErr := c.FormFile("file")
		if fileErr != nil {
			workspaceError(c, http.StatusBadRequest, "File ZIP wajib diunggah pada field file.")
			return
		}
		if header.Size > limits.MaxZipBytes {
			workspaceError(c, http.StatusRequestEntityTooLarge, "Ukuran ZIP terlalu besar.")
			return
		}
		file, openErr := header.Open()
		if openErr != nil {
			workspaceError(c, http.StatusBadRequest, "ZIP tidak dapat dibaca.")
			return
		}
		archive, err = io.ReadAll(io.LimitReader(file, limits.MaxZipBytes+1))
		file.Close()
		if err != nil || int64(len(archive)) > limits.MaxZipBytes {
			workspaceError(c, http.StatusRequestEntityTooLarge, "Ukuran ZIP terlalu besar.")
			return
		}
		sourceType = models.WorkspaceSourceZip
	} else {
		var req struct {
			Name           string `json:"name"`
			Description    string `json:"description"`
			SourceType     string `json:"source_type"`
			SourceURL      string `json:"source_url"`
			DefaultRuntime string `json:"default_runtime"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			workspaceError(c, http.StatusBadRequest, "Format workspace tidak valid.")
			return
		}
		name, description, runtimeName, sourceType, sourceURL = req.Name, req.Description, req.DefaultRuntime, strings.TrimSpace(req.SourceType), strings.TrimSpace(req.SourceURL)
		if sourceType == "" {
			sourceType = models.WorkspaceSourceEmpty
		}
		if sourceType != models.WorkspaceSourceEmpty && sourceType != models.WorkspaceSourceGitHub {
			workspaceError(c, http.StatusBadRequest, "source_type harus empty atau github (ZIP diunggah sebagai form).")
			return
		}
	}

	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	switch {
	case name == "" || len([]rune(name)) > 100:
		workspaceError(c, http.StatusBadRequest, "Nama workspace wajib diisi (maksimal 100 karakter).")
		return
	case len([]rune(description)) > 500:
		workspaceError(c, http.StatusBadRequest, "Deskripsi maksimal 500 karakter.")
		return
	case !runtimeNames[runtimeName]:
		workspaceError(c, http.StatusBadRequest, "default_runtime harus go, node, python, atau kosong.")
		return
	}

	var owner, repo string
	if sourceType == models.WorkspaceSourceGitHub {
		owner, repo, err = cloudworkspace.ParseGitHubURL(sourceURL)
		if err != nil {
			workspaceError(c, http.StatusBadRequest, err.Error())
			return
		}
		sourceURL = fmt.Sprintf("https://github.com/%s/%s", owner, repo)
	} else {
		sourceURL = ""
	}

	var existing int64
	config.DB.Model(&models.CloudWorkspace{}).Where("user_id = ?", userID).Count(&existing)
	if int(existing) >= limits.MaxWorkspacesPerUser {
		workspaceError(c, http.StatusConflict, "Jumlah workspace sudah mencapai batas. Hapus workspace yang tidak dipakai.")
		return
	}
	var used int64
	config.DB.Model(&models.CloudWorkspace{}).Where("user_id = ?", userID).Select("COALESCE(SUM(size_bytes), 0)").Scan(&used)
	budget := limits.MaxUserBytes - used
	if budget <= 0 {
		workspaceError(c, http.StatusRequestEntityTooLarge, "Kuota penyimpanan terlampaui.")
		return
	}

	// The repository is fetched before anything is created, so a failure leaves nothing behind.
	if sourceType == models.WorkspaceSourceGitHub {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
		defer cancel()
		archive, err = cloudDownloader.Download(ctx, owner, repo, limits)
		if err != nil {
			workspaceError(c, http.StatusBadGateway, err.Error())
			return
		}
	}

	ref, err := storage.Create(userID)
	if err != nil {
		log.Printf("[WORKSPACE] create storage: %v", err)
		workspaceError(c, http.StatusInternalServerError, "Gagal membuat workspace.")
		return
	}
	cleanup := func() { _ = storage.Delete(userID, ref) }

	workspace := models.CloudWorkspace{
		UserID: userID, Name: name, Description: description, SourceType: sourceType, SourceURL: sourceURL,
		Status: models.WorkspaceStatusCreating, StorageRef: ref, DefaultRuntime: runtimeName,
	}
	if err := config.DB.Create(&workspace).Error; err != nil {
		cleanup()
		workspaceError(c, http.StatusInternalServerError, "Gagal membuat workspace.")
		return
	}
	fail := func(status int, message string) {
		cleanup()
		config.DB.Unscoped().Delete(&models.CloudWorkspace{}, workspace.ID)
		workspaceError(c, status, message)
	}

	filesDir, _ := storage.FilesDir(userID, ref)
	metaDir, _ := storage.MetaDir(userID, ref)
	if archive != nil {
		if _, err := cloudworkspace.ExtractZip(archive, filesDir, limits, budget); err != nil {
			if errors.Is(err, cloudworkspace.ErrQuota) {
				fail(http.StatusRequestEntityTooLarge, "Kuota penyimpanan terlampaui.")
			} else {
				fail(http.StatusBadRequest, "Arsip ditolak: "+err.Error())
			}
			return
		}
	}
	if err := cloudworkspace.SaveBaseline(filesDir, metaDir, limits); err != nil {
		log.Printf("[WORKSPACE] baseline: %v", err)
		fail(http.StatusInternalServerError, "Gagal membuat workspace.")
		return
	}
	bytes, files, _ := storage.Usage(userID, ref)
	workspace.Status, workspace.SizeBytes, workspace.FileCount = models.WorkspaceStatusReady, bytes, files
	if err := config.DB.Model(&workspace).UpdateColumns(map[string]interface{}{"status": workspace.Status, "size_bytes": bytes, "file_count": files}).Error; err != nil {
		fail(http.StatusInternalServerError, "Gagal membuat workspace.")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"ok": true, "workspace": workspace})
}

func UpdateWorkspace(c *gin.Context) {
	handle, ok := openOwned(c, false)
	if !ok {
		return
	}
	var req struct {
		Name           *string `json:"name"`
		Description    *string `json:"description"`
		DefaultRuntime *string `json:"default_runtime"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		workspaceError(c, http.StatusBadRequest, "Format update tidak valid.")
		return
	}
	updates := map[string]interface{}{}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len([]rune(name)) > 100 {
			workspaceError(c, http.StatusBadRequest, "Nama workspace wajib diisi (maksimal 100 karakter).")
			return
		}
		updates["name"] = name
	}
	if req.Description != nil {
		description := strings.TrimSpace(*req.Description)
		if len([]rune(description)) > 500 {
			workspaceError(c, http.StatusBadRequest, "Deskripsi maksimal 500 karakter.")
			return
		}
		updates["description"] = description
	}
	if req.DefaultRuntime != nil {
		if !runtimeNames[*req.DefaultRuntime] {
			workspaceError(c, http.StatusBadRequest, "default_runtime harus go, node, python, atau kosong.")
			return
		}
		updates["default_runtime"] = *req.DefaultRuntime
	}
	if len(updates) > 0 {
		if err := config.DB.Model(&models.CloudWorkspace{}).Where("id = ? AND user_id = ?", handle.Workspace.ID, handle.Workspace.UserID).Updates(updates).Error; err != nil {
			workspaceError(c, http.StatusInternalServerError, "Gagal memperbarui workspace.")
			return
		}
	}
	var fresh models.CloudWorkspace
	config.DB.Where("id = ? AND user_id = ?", handle.Workspace.ID, handle.Workspace.UserID).First(&fresh)
	c.JSON(http.StatusOK, gin.H{"ok": true, "workspace": fresh})
}

var activeJobStatuses = []string{models.JobQueued, models.JobPreparing, models.JobRunning}

// DeleteWorkspace removes the files and the metadata. Tasks that used it keep
// their history and simply lose the link; jobs and approvals are deleted.
func DeleteWorkspace(c *gin.Context) {
	handle, ok := openOwned(c, false)
	if !ok {
		return
	}
	var active int64
	config.DB.Model(&models.ExecutionJob{}).Where("workspace_id = ? AND status IN ?", handle.Workspace.ID, activeJobStatuses).Count(&active)
	if active > 0 {
		workspaceError(c, http.StatusConflict, "Masih ada job yang berjalan. Batalkan job terlebih dahulu.")
		return
	}
	config.DB.Model(&models.CloudWorkspace{}).Where("id = ?", handle.Workspace.ID).UpdateColumn("status", models.WorkspaceStatusDeleting)
	if err := handle.Storage.Delete(handle.Workspace.UserID, handle.Workspace.StorageRef); err != nil {
		log.Printf("[WORKSPACE] delete storage: %v", err)
		workspaceError(c, http.StatusInternalServerError, "Gagal menghapus file workspace. Coba lagi.")
		return
	}
	err := config.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Task{}).Where("workspace_id = ? AND user_id = ?", handle.Workspace.ID, handle.Workspace.UserID).UpdateColumn("workspace_id", nil).Error; err != nil {
			return err
		}
		if err := tx.Where("workspace_id = ? AND user_id = ?", handle.Workspace.ID, handle.Workspace.UserID).Delete(&models.ExecutionJob{}).Error; err != nil {
			return err
		}
		if err := tx.Where("workspace_id = ? AND user_id = ?", handle.Workspace.ID, handle.Workspace.UserID).Delete(&models.WorkspaceToolApproval{}).Error; err != nil {
			return err
		}
		return tx.Unscoped().Where("id = ? AND user_id = ?", handle.Workspace.ID, handle.Workspace.UserID).Delete(&models.CloudWorkspace{}).Error
	})
	if err != nil {
		log.Printf("[WORKSPACE] delete metadata: %v", err)
		workspaceError(c, http.StatusInternalServerError, "Gagal menghapus workspace.")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func GetWorkspaceTree(c *gin.Context) {
	handle, ok := openOwned(c, true)
	if !ok {
		return
	}
	tree, err := handle.tree(c.Query("path"))
	if err != nil {
		status, message := fileFailure(err)
		workspaceError(c, status, message)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "entries": tree.Entries, "truncated": tree.Truncated})
}

func GetWorkspaceFile(c *gin.Context) {
	handle, ok := openOwned(c, true)
	if !ok {
		return
	}
	content, size, err := handle.readFile(c.Query("path"))
	if err != nil {
		status, message := fileFailure(err)
		workspaceError(c, status, message)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "path": strings.TrimPrefix(strings.ReplaceAll(c.Query("path"), `\`, "/"), "./"), "content": content, "size": size})
}

func PutWorkspaceFile(c *gin.Context) {
	handle, ok := openOwned(c, true)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, handle.Limits.MaxFileWriteBytes+(64<<10))
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		workspaceError(c, http.StatusBadRequest, "Format file tidak valid.")
		return
	}
	result, err := handle.writeFile(req.Path, req.Content)
	if err != nil {
		status, message := fileFailure(err)
		workspaceError(c, status, message)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "created": result.Created, "size": result.Size, "workspace": handle.Workspace})
}

func GetWorkspaceChanges(c *gin.Context) {
	handle, ok := openOwned(c, true)
	if !ok {
		return
	}
	changes, err := handle.changes()
	if err != nil {
		status, message := fileFailure(err)
		workspaceError(c, status, message)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "changes": changes})
}

func ExportWorkspace(c *gin.Context) {
	handle, ok := openOwned(c, true)
	if !ok {
		return
	}
	if err := handle.requireReady(); err != nil {
		status, message := fileFailure(err)
		workspaceError(c, status, message)
		return
	}
	// Build the archive first so a failure can still be reported as JSON.
	temp, err := os.CreateTemp("", "workspace-export-*.zip")
	if err != nil {
		workspaceError(c, http.StatusInternalServerError, "Gagal membuat ekspor.")
		return
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if err := cloudworkspace.ExportZip(handle.FilesDir, temp, handle.Limits); err != nil {
		workspaceError(c, http.StatusRequestEntityTooLarge, "Workspace terlalu besar untuk diekspor.")
		return
	}
	if _, err := temp.Seek(0, io.SeekStart); err != nil {
		workspaceError(c, http.StatusInternalServerError, "Gagal membuat ekspor.")
		return
	}
	name := strings.Trim(safeFileName.ReplaceAllString(handle.Workspace.Name, "-"), "-.")
	if name == "" {
		name = "workspace"
	}
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, name))
	c.Header("Content-Type", "application/zip")
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, temp)
}
