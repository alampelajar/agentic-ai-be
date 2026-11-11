package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"agentic-ai-backend/config"
	"agentic-ai-backend/internal/cloudworkspace"
	"agentic-ai-backend/models"
	"agentic-ai-backend/services"
)

// Agent tools for Cloud Workspace (Phase 6). They are ordinary entries of the
// Tool Registry. Reading runs at once; writing a file and running a command are
// held as an approval that only the signed-in user can grant, in a separate
// request that a model's text cannot trigger.

const (
	toolApprovalTTL      = 60 * time.Second
	maxPendingApprovals  = 20
	maxToolOutputRunes   = 8000
	maxExecutionWaitSecs = 20
)

type toolArgs map[string]any

func (a toolArgs) str(key string) (string, bool) {
	value, present := a[key]
	if !present {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

// normalizeToolArgs checks the arguments against the tool's schema: only known
// keys, right types, enum values and ranges. It returns the cleaned arguments.
func normalizeToolArgs(tool string, raw map[string]any, workspace models.CloudWorkspace) (toolArgs, error) {
	definition, ok := services.ToolRegistry[tool]
	if !ok || !services.IsCloudTool(tool) {
		return nil, fmt.Errorf("tool %q is not a workspace tool", tool)
	}
	properties, _ := definition.InputSchema["properties"].(map[string]any)
	for key := range raw {
		if _, known := properties[key]; !known {
			return nil, fmt.Errorf("unknown argument %q", key)
		}
	}
	args := toolArgs{}
	for key, value := range raw {
		property, _ := properties[key].(map[string]any)
		switch property["type"] {
		case "string":
			text, isString := value.(string)
			if !isString {
				return nil, fmt.Errorf("argument %q must be a string", key)
			}
			if enum, hasEnum := property["enum"].([]any); hasEnum {
				found := false
				for _, allowed := range enum {
					if allowed == text {
						found = true
					}
				}
				if !found {
					return nil, fmt.Errorf("argument %q has an unsupported value", key)
				}
			}
			args[key] = text
		case "integer":
			number, isNumber := value.(float64)
			if !isNumber || number != float64(int64(number)) || number < 0 || number > 1<<31 {
				return nil, fmt.Errorf("argument %q must be a whole number", key)
			}
			args[key] = int64(number)
		}
	}
	if required, ok := definition.InputSchema["required"].([]string); ok {
		for _, key := range required {
			if _, present := args[key]; !present {
				return nil, fmt.Errorf("tool %q requires %q", tool, key)
			}
		}
	}
	if path, present := args.str("path"); present && len(path) > 512 {
		return nil, errors.New("path is too long")
	}
	if content, present := args.str("content"); present && int64(len(content)) > cloudLimitsSnapshot().MaxFileWriteBytes {
		return nil, errors.New("content is too large")
	}
	if wait, present := args["wait_seconds"].(int64); present && wait > maxExecutionWaitSecs {
		args["wait_seconds"] = int64(maxExecutionWaitSecs)
	}
	if tool == services.ToolWorkspaceRunTest || tool == services.ToolWorkspaceBuild || tool == services.ToolWorkspaceInstall {
		if _, present := args.str("target"); !present {
			if workspace.DefaultRuntime == "" {
				return nil, errors.New("choose a target (go, node or python) or set a default runtime on the workspace")
			}
			args["target"] = workspace.DefaultRuntime
		}
	}
	return args, nil
}

func cloudLimitsSnapshot() cloudworkspace.Limits {
	_, limits, _ := workspaceStorage()
	return limits
}

// jobTypeFor maps a run_test or build target to an allowlisted job type.
func jobTypeFor(tool, target string) (string, error) {
	table := map[string]map[string]string{
		services.ToolWorkspaceRunTest: {"go": "go-test", "node": "npm-test", "python": "python-test"},
		services.ToolWorkspaceBuild:   {"go": "go-build", "node": "npm-build"},
		services.ToolWorkspaceInstall: {"go": "go-deps", "node": "npm-install", "python": "pip-install"},
	}
	if jobType, ok := table[tool][target]; ok {
		return jobType, nil
	}
	return "", fmt.Errorf("%s does not support the target %q", tool, target)
}

func argsHash(workspaceID, agentID uint, tool, encoded string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%d|%s|%s", workspaceID, agentID, tool, encoded)))
	return hex.EncodeToString(sum[:])
}

func canonicalArgs(args toolArgs) string {
	encoded, _ := json.Marshal(map[string]any(args)) // map keys are sorted by encoding/json
	return string(encoded)
}

func approvalSummary(tool string, args toolArgs) string {
	switch tool {
	case services.ToolWorkspaceWriteFile:
		path, _ := args.str("path")
		content, _ := args.str("content")
		return fmt.Sprintf("Write %s (%d characters)", path, len([]rune(content)))
	case services.ToolWorkspaceRunTest, services.ToolWorkspaceBuild:
		target, _ := args.str("target")
		jobType, _ := jobTypeFor(tool, target)
		return fmt.Sprintf("Run %s in the cloud sandbox", jobType)
	case services.ToolWorkspaceInstall:
		target, _ := args.str("target")
		jobType, _ := jobTypeFor(tool, target)
		return fmt.Sprintf("Install dependencies (%s) in the cloud sandbox; only package registries are reachable", jobType)
	}
	return tool
}

func trimOutput(text string) string {
	runes := []rune(text)
	if len(runes) <= maxToolOutputRunes {
		return text
	}
	return string(runes[:maxToolOutputRunes]) + "\n[output shortened]"
}

// executeCloudTool performs one already-validated, already-authorised tool call.
// The result is what the model sees: ok true with data, or ok false with a message.
func executeCloudTool(handle *workspaceHandle, tool string, args toolArgs, agentID uint, taskID *uint) gin.H {
	failure := func(err error) gin.H {
		_, message := fileFailure(err)
		return gin.H{"ok": false, "error": message}
	}
	switch tool {
	case services.ToolWorkspaceListFiles:
		path, _ := args.str("path")
		tree, err := handle.tree(path)
		if err != nil {
			return failure(err)
		}
		entries := make([]gin.H, 0, len(tree.Entries))
		for _, entry := range tree.Entries {
			item := gin.H{"path": entry.Path, "type": entry.Type}
			if entry.Type == "file" {
				item["size"] = entry.Size
			}
			if entry.Sensitive {
				item["sensitive"] = true
			}
			entries = append(entries, item)
		}
		return gin.H{"ok": true, "entries": entries, "truncated": tree.Truncated}
	case services.ToolWorkspaceReadFile:
		path, _ := args.str("path")
		content, size, err := handle.readFile(path)
		if err != nil {
			return failure(err)
		}
		return gin.H{"ok": true, "path": path, "size": size, "content": content}
	case services.ToolWorkspaceWriteFile:
		path, _ := args.str("path")
		content, _ := args.str("content")
		result, err := handle.writeFile(path, content)
		if err != nil {
			return failure(err)
		}
		return gin.H{"ok": true, "path": path, "created": result.Created, "size": result.Size}
	case services.ToolWorkspaceGetChanges:
		changes, err := handle.changes()
		if err != nil {
			return failure(err)
		}
		return gin.H{"ok": true, "changes": changes}
	case services.ToolWorkspaceRunTest, services.ToolWorkspaceBuild, services.ToolWorkspaceInstall:
		target, _ := args.str("target")
		jobType, err := jobTypeFor(tool, target)
		if err != nil {
			return gin.H{"ok": false, "error": err.Error()}
		}
		agent := agentID
		job, queueErr := queueJob(handle, jobType, 0, taskID, &agent)
		if queueErr != nil {
			return gin.H{"ok": false, "error": queueErr.message}
		}
		return gin.H{"ok": true, "job_id": job.ID, "job_type": job.JobType, "status": job.Status, "note": "Use workspace.get_execution with this job_id to read the result."}
	case services.ToolWorkspacePublish:
		return executePublishTool(handle, args)
	case services.ToolWorkspaceGetExecution:
		id, _ := args["job_id"].(int64)
		wait, _ := args["wait_seconds"].(int64)
		deadline := time.Now().Add(time.Duration(wait) * time.Second)
		for {
			var job models.ExecutionJob
			// The job must belong to this user and this workspace.
			if err := config.DB.Where("id = ? AND user_id = ? AND workspace_id = ?", id, handle.Workspace.UserID, handle.Workspace.ID).First(&job).Error; err != nil {
				return gin.H{"ok": false, "error": "Job tidak ditemukan di workspace ini."}
			}
			finished := job.Status != models.JobQueued && job.Status != models.JobPreparing && job.Status != models.JobRunning
			if finished || !time.Now().Before(deadline) {
				result := gin.H{"ok": true, "job_id": job.ID, "job_type": job.JobType, "status": job.Status, "finished": finished,
					"stdout": trimOutput(job.Stdout), "stderr": trimOutput(job.Stderr), "duration_ms": job.DurationMS}
				if job.ExitCode != nil {
					result["exit_code"] = *job.ExitCode
				}
				if job.Error != "" {
					result["error"] = job.Error
				}
				return result
			}
			time.Sleep(time.Second)
		}
	}
	return gin.H{"ok": false, "error": "Tool tidak didukung."}
}

// recordToolActivity writes the Tool Activity row for a cloud tool call. The
// arguments pass through the same redaction as every other activity: file content
// and anything that looks like a secret is replaced.
func recordToolActivity(userID, agentID uint, workspaceID uint, tool string, args toolArgs, approval, execution, message string, started time.Time) {
	encoded, _ := json.Marshal(redactActivity(map[string]any(args)))
	if len(encoded) > maxActivityArgsBytes {
		encoded = []byte(`{"truncated":true}`)
	}
	agent := agentID
	row := models.ToolActivity{
		UserID: userID, AgentID: &agent, ToolName: tool, ArgumentsJSON: string(encoded),
		TargetApplication: fmt.Sprintf("workspace:%d", workspaceID), ApprovalStatus: approval, ExecutionStatus: execution,
		Error: truncateRunes(redactActivityString(message), maxActivityErrorLength), DurationMS: time.Since(started).Milliseconds(),
	}
	config.DB.Create(&row)
}

func resultStatus(result gin.H) (string, string) {
	if ok, _ := result["ok"].(bool); ok {
		return "completed", ""
	}
	message, _ := result["error"].(string)
	return "failed", message
}

// authorizeToolCall resolves the workspace and the agent and checks the agent's
// role may use the tool.
func authorizeToolCall(c *gin.Context, userID, workspaceID, agentID uint, tool string) (*workspaceHandle, bool) {
	if !services.IsCloudTool(tool) {
		workspaceError(c, http.StatusBadRequest, "Tool workspace tidak dikenal.")
		return nil, false
	}
	if agentID == 0 || !ensureAgentAccessible(userID, agentID) {
		workspaceError(c, http.StatusForbidden, "Agent tidak ditemukan.")
		return nil, false
	}
	var agent models.Agent
	config.DB.Select("slug").First(&agent, agentID)
	if !services.CloudToolAllowedForAgent(tool, agent.Slug) {
		workspaceError(c, http.StatusForbidden, "Agent ini tidak diizinkan memakai tool tersebut.")
		return nil, false
	}
	handle, err := openWorkspace(userID, workspaceID, true)
	if err != nil {
		status, message := fileFailure(err)
		workspaceError(c, status, message)
		return nil, false
	}
	return handle, true
}

// RunWorkspaceTool runs a read-only tool at once and turns a tool that changes
// something into a pending approval.
func RunWorkspaceTool(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		workspaceError(c, http.StatusUnauthorized, "User tidak ditemukan.")
		return
	}
	var req struct {
		WorkspaceID uint           `json:"workspace_id"`
		AgentID     uint           `json:"agent_id"`
		TaskID      *uint          `json:"task_id"`
		Tool        string         `json:"tool"`
		Arguments   map[string]any `json:"arguments"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<20)
	if err := c.ShouldBindJSON(&req); err != nil {
		workspaceError(c, http.StatusBadRequest, "Format tool tidak valid.")
		return
	}
	tool := services.CanonicalToolName(req.Tool)
	handle, ok := authorizeToolCall(c, userID, req.WorkspaceID, req.AgentID, tool)
	if !ok {
		return
	}
	args, err := normalizeToolArgs(tool, req.Arguments, handle.Workspace)
	if err != nil {
		workspaceError(c, http.StatusBadRequest, err.Error())
		return
	}
	if req.TaskID != nil {
		var count int64
		config.DB.Model(&models.Task{}).Where("id = ? AND user_id = ?", *req.TaskID, userID).Count(&count)
		if count == 0 {
			workspaceError(c, http.StatusBadRequest, "Task tidak ditemukan.")
			return
		}
	}

	summary, preview := approvalSummary(tool, args), previewArguments(tool, args)
	if tool == services.ToolWorkspacePublish {
		if !workspaceFromGitHub(handle.Workspace) {
			workspaceError(c, http.StatusBadRequest, "Workspace ini tidak berasal dari GitHub, jadi tidak ada tempat mengirim perubahannya.")
			return
		}
		files, failure := collectPublishFiles(handle)
		if failure != nil {
			workspaceError(c, failure.Status, failure.Message)
			return
		}
		// The approval pins exactly these changes; publishing refuses if they differ by then.
		args[publishFingerprintKey] = changesFingerprint(files)
		summary, preview = publishApprovalSummary(handle.Workspace, args, files), publishApprovalPreview(handle.Workspace, args, files)
	}

	started := time.Now()
	if !services.CloudToolNeedsApproval(tool) {
		result := executeCloudTool(handle, tool, args, req.AgentID, req.TaskID)
		execution, message := resultStatus(result)
		recordToolActivity(userID, req.AgentID, handle.Workspace.ID, tool, args, "not_required", execution, message, started)
		c.JSON(http.StatusOK, gin.H{"ok": true, "tool": tool, "result": result})
		return
	}

	var pending int64
	config.DB.Model(&models.WorkspaceToolApproval{}).Where("user_id = ? AND status = ? AND expires_at > ?", userID, models.ApprovalPending, time.Now()).Count(&pending)
	if pending >= maxPendingApprovals {
		workspaceError(c, http.StatusTooManyRequests, "Terlalu banyak persetujuan yang menunggu. Selesaikan atau tolak dahulu.")
		return
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		workspaceError(c, http.StatusInternalServerError, "Gagal membuat persetujuan.")
		return
	}
	encoded := canonicalArgs(args)
	agent := req.AgentID
	approval := models.WorkspaceToolApproval{
		ID: "wsa-" + hex.EncodeToString(random), UserID: userID, WorkspaceID: handle.Workspace.ID, AgentID: &agent, TaskID: req.TaskID,
		Tool: tool, ArgsJSON: encoded, ArgsHash: argsHash(handle.Workspace.ID, req.AgentID, tool, encoded),
		Status: models.ApprovalPending, ExpiresAt: time.Now().Add(toolApprovalTTL),
	}
	if err := config.DB.Create(&approval).Error; err != nil {
		workspaceError(c, http.StatusInternalServerError, "Gagal membuat persetujuan.")
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"ok": true, "approval_required": true, "approval": gin.H{
		"id": approval.ID, "tool": tool, "workspace_id": handle.Workspace.ID, "workspace_name": handle.Workspace.Name,
		"summary": summary, "arguments": preview, "expires_at": approval.ExpiresAt,
	}})
}

// previewArguments is what the approval dialog shows: the path and size of a write
// rather than the whole file, and the plain arguments of everything else.
func previewArguments(tool string, args toolArgs) gin.H {
	preview := gin.H{}
	for key, value := range args {
		if text, ok := value.(string); ok && key == "content" {
			runes := []rune(text)
			preview["content_characters"] = len(runes)
			if len(runes) > 600 {
				text = string(runes[:600]) + "..."
			}
			preview["content_preview"] = redactActivityString(text)
			continue
		}
		preview[key] = value
	}
	return preview
}

func loadApproval(c *gin.Context, userID uint) (*models.WorkspaceToolApproval, bool) {
	var approval models.WorkspaceToolApproval
	if err := config.DB.Where("id = ? AND user_id = ?", strings.TrimSpace(c.Param("id")), userID).First(&approval).Error; err != nil {
		workspaceError(c, http.StatusNotFound, "Persetujuan tidak ditemukan.")
		return nil, false
	}
	return &approval, true
}

// takeApproval moves a pending, unexpired approval to the given status in one
// statement, so it can be used at most once even with concurrent requests.
func takeApproval(approval *models.WorkspaceToolApproval, status string) (int, string) {
	result := config.DB.Model(&models.WorkspaceToolApproval{}).
		Where("id = ? AND user_id = ? AND status = ? AND expires_at > ?", approval.ID, approval.UserID, models.ApprovalPending, time.Now()).
		UpdateColumn("status", status)
	if result.Error != nil {
		return http.StatusInternalServerError, "Gagal memproses persetujuan."
	}
	if result.RowsAffected == 1 {
		return 0, ""
	}
	var current models.WorkspaceToolApproval
	config.DB.Where("id = ?", approval.ID).First(&current)
	if current.Status == models.ApprovalPending {
		config.DB.Model(&models.WorkspaceToolApproval{}).Where("id = ? AND status = ?", current.ID, models.ApprovalPending).UpdateColumn("status", models.ApprovalExpired)
		return http.StatusGone, "Persetujuan sudah kedaluwarsa. Minta agent mengajukan permintaan baru."
	}
	return http.StatusConflict, "Persetujuan ini sudah digunakan atau dibatalkan."
}

// ApproveWorkspaceTool runs exactly what was stored when the approval was made.
// The request carries no arguments, so nothing can be swapped after the user saw it.
func ApproveWorkspaceTool(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		workspaceError(c, http.StatusUnauthorized, "User tidak ditemukan.")
		return
	}
	approval, ok := loadApproval(c, userID)
	if !ok {
		return
	}
	if status, message := takeApproval(approval, models.ApprovalApproved); status != 0 {
		workspaceError(c, status, message)
		return
	}
	started := time.Now()
	agentID := uint(0)
	if approval.AgentID != nil {
		agentID = *approval.AgentID
	}
	var args toolArgs
	if json.Unmarshal([]byte(approval.ArgsJSON), &args) != nil || approval.ArgsHash != argsHash(approval.WorkspaceID, agentID, approval.Tool, canonicalArgsFromStored(approval.ArgsJSON)) {
		workspaceError(c, http.StatusConflict, "Persetujuan tidak valid.")
		return
	}
	args = restoreIntegers(args)
	handle, ok := authorizeToolCall(c, userID, approval.WorkspaceID, agentID, approval.Tool)
	if !ok {
		return
	}
	result := executeCloudTool(handle, approval.Tool, args, agentID, approval.TaskID)
	execution, message := resultStatus(result)
	recordToolActivity(userID, agentID, approval.WorkspaceID, approval.Tool, args, "approved_once", execution, message, started)
	c.JSON(http.StatusOK, gin.H{"ok": true, "tool": approval.Tool, "result": result})
}

// canonicalArgsFromStored re-encodes stored JSON the same way canonicalArgs did.
func canonicalArgsFromStored(stored string) string {
	var args map[string]any
	if json.Unmarshal([]byte(stored), &args) != nil {
		return ""
	}
	encoded, _ := json.Marshal(args)
	return string(encoded)
}

// restoreIntegers turns the float64 numbers JSON decoding produces back into the
// integers the tools expect.
func restoreIntegers(args toolArgs) toolArgs {
	for key, value := range args {
		if number, ok := value.(float64); ok {
			args[key] = int64(number)
		}
	}
	return args
}

func DenyWorkspaceTool(c *gin.Context) {
	userID, ok := getCurrentUserID(c)
	if !ok {
		workspaceError(c, http.StatusUnauthorized, "User tidak ditemukan.")
		return
	}
	approval, ok := loadApproval(c, userID)
	if !ok {
		return
	}
	if status, message := takeApproval(approval, models.ApprovalDenied); status != 0 {
		workspaceError(c, status, message)
		return
	}
	agentID := uint(0)
	if approval.AgentID != nil {
		agentID = *approval.AgentID
	}
	var args toolArgs
	_ = json.Unmarshal([]byte(approval.ArgsJSON), &args)
	recordToolActivity(userID, agentID, approval.WorkspaceID, approval.Tool, args, "denied", "cancelled", "", time.Now())
	c.JSON(http.StatusOK, gin.H{"ok": true, "status": "denied"})
}

// cloudWorkspacePrompt tells a model about the selected workspace and its tools.
// Names and paths are data; the text never grants anything the registry and the
// agent's role do not.
func cloudWorkspacePrompt(workspace models.CloudWorkspace, tools []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n\nCLOUD WORKSPACE: the user selected the workspace %q (%d files). It lives on the server, so the Local Agent Runner is NOT needed for it. Use these registered tools: %s.\n", memoryPromptName(workspace.Name), workspace.FileCount, strings.Join(tools, ", "))
	b.WriteString("Call them natively or emit exactly one JSON object inside <AGENT_TOOL>...</AGENT_TOOL>, for example <AGENT_TOOL>{\"action\":\"workspace.read_file\",\"arguments\":{\"path\":\"README.md\"}}</AGENT_TOOL>. Do not use <LOCAL_AGENT_TOOL> markers for workspace files.\n")
	for _, tool := range tools {
		if tool == services.ToolWorkspacePublish {
			fmt.Fprintf(&b, "This workspace was imported from GitHub (%s/%s, branch %s). Only when the user asks to send the changes to GitHub, call workspace.publish with a clear commit message: it puts the changes on a NEW branch with a pull request, after the user approves the exact files. Never call it on your own initiative.\n", memoryPromptName(workspace.GitHubOwner), memoryPromptName(workspace.GitHubRepo), memoryPromptName(workspace.BaseBranch))
		}
	}
	b.WriteString("Paths are relative to the workspace root. Secret files such as .env are not available. Writing a file, running tests and building each need the user's approval, so wait for the actual tool result before continuing. Tests and builds run in a sandbox without internet access, so a project with dependencies needs workspace.install_dependencies first (the user approves it; only package registries are reachable and no project script runs; folders such as node_modules are not listed or readable). A job may be queued: read its result with workspace.get_execution (job_id, wait_seconds up to 20). Never say work succeeded unless a tool result shows it.")
	return b.String()
}

func memoryPromptName(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(name, "\n", " "), "\"", "'")
}
