package localrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const maxFileBytes = 1 << 20
const maxCommandOutput = 64 << 10

type executeRequest struct {
	Action string `json:"action" binding:"required"`
	Runtime string `json:"runtime"` // native or docker
}

func (s *Server) workspace(id string) (Workspace, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, w := range s.cfg.Workspaces { if w.ID == id { return w, true } }
	return Workspace{}, false
}

func (s *Server) resolve(w Workspace, relative string, allowMissing bool) (string, error) {
	if strings.TrimSpace(relative) == "" { relative = "." }
	if filepath.IsAbs(relative) { return "", errors.New("absolute paths are not allowed") }
	root, err := filepath.Abs(w.Path)
	if err != nil { return "", err }
	root, err = filepath.EvalSymlinks(root)
	if err != nil { return "", fmt.Errorf("workspace root must exist: %w", err) }
	target, err := filepath.Abs(filepath.Join(root, filepath.Clean(relative)))
	if err != nil { return "", err }
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes workspace")
	}
	// Resolve existing paths to block symlink escapes. For a new file, validate its parent.
	check := target
	if allowMissing {
		for {
			if _, statErr := os.Lstat(check); statErr == nil { break }
			parent := filepath.Dir(check)
			if parent == check { return "", errors.New("cannot resolve target parent") }
			check = parent
		}
	}
	real, err := filepath.EvalSymlinks(check)
	if err != nil { return "", err }
	realRel, err := filepath.Rel(root, real)
	if err != nil || realRel == ".." || strings.HasPrefix(realRel, ".."+string(filepath.Separator)) {
		return "", errors.New("symlink escapes workspace")
	}
	return target, nil
}

func (s *Server) listWorkspaces(c *gin.Context) {
	type item struct { ID string `json:"id"`; Name string `json:"name"` }
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]item, 0, len(s.cfg.Workspaces))
	for _, w := range s.cfg.Workspaces { items = append(items, item{w.ID, w.Name}) }
	c.JSON(http.StatusOK, gin.H{"workspaces": items})
}

func (s *Server) listTree(c *gin.Context) {
	w, ok := s.workspace(c.Param("id")); if !ok { c.JSON(http.StatusNotFound, gin.H{"error":"workspace not found"}); return }
	path, err := s.resolve(w, c.Query("path"), false); if err != nil { c.JSON(http.StatusBadRequest, gin.H{"error":err.Error()}); return }
	entries, err := os.ReadDir(path); if err != nil { c.JSON(http.StatusBadRequest, gin.H{"error":"cannot read directory"}); return }
	type item struct { Name string `json:"name"`; Path string `json:"path"`; IsDir bool `json:"is_dir"`; Size int64 `json:"size"` }
	out := make([]item, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info(); if err != nil { continue }
		if e.Name() == ".git" || e.Name() == "node_modules" || e.Name() == "vendor" { continue }
		rel := filepath.Join(c.DefaultQuery("path", "."), e.Name())
		out = append(out, item{Name:e.Name(), Path:filepath.ToSlash(rel), IsDir:e.IsDir(), Size:info.Size()})
	}
	c.JSON(http.StatusOK, gin.H{"entries":out})
}

func (s *Server) readFile(c *gin.Context) {
	w, ok := s.workspace(c.Param("id")); if !ok { c.JSON(http.StatusNotFound, gin.H{"error":"workspace not found"}); return }
	path, err := s.resolve(w, c.Query("path"), false); if err != nil { c.JSON(http.StatusBadRequest, gin.H{"error":err.Error()}); return }
	f, err := os.Open(path); if err != nil { c.JSON(http.StatusNotFound, gin.H{"error":"file not found"}); return }; defer f.Close()
	info, err := f.Stat(); if err != nil || !info.Mode().IsRegular() { c.JSON(http.StatusBadRequest, gin.H{"error":"regular file required"}); return }
	if info.Size() > maxFileBytes { c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error":"file exceeds 1 MiB read limit"}); return }
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1)); if err != nil { c.JSON(http.StatusInternalServerError, gin.H{"error":"file read failed"}); return }
	c.JSON(http.StatusOK, gin.H{"path":c.Query("path"), "content":string(data), "size":len(data)})
}

type writeRequest struct { Path string `json:"path" binding:"required"`; Content string `json:"content"`; Create bool `json:"create"` }
func (s *Server) writeFile(c *gin.Context) {
	w, ok := s.workspace(c.Param("id")); if !ok { c.JSON(http.StatusNotFound, gin.H{"error":"workspace not found"}); return }
	var req writeRequest
	if err := c.ShouldBindJSON(&req); err != nil { c.JSON(http.StatusBadRequest, gin.H{"error":"path and content are required"}); return }
	if len(req.Content) > maxFileBytes { c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error":"write limit is 1 MiB"}); return }
	path, err := s.resolve(w, req.Path, req.Create); if err != nil { c.JSON(http.StatusBadRequest, gin.H{"error":err.Error()}); return }
	if !req.Create {
		info, statErr := os.Stat(path); if statErr != nil || !info.Mode().IsRegular() { c.JSON(http.StatusNotFound, gin.H{"error":"existing regular file required; set create=true to create"}); return }
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil { c.JSON(http.StatusInternalServerError, gin.H{"error":"cannot create parent directory"}); return }
	if err := os.WriteFile(path, []byte(req.Content), 0640); err != nil { c.JSON(http.StatusInternalServerError, gin.H{"error":"write failed"}); return }
	c.JSON(http.StatusOK, gin.H{"ok":true,"path":filepath.ToSlash(req.Path),"bytes":len(req.Content)})
}

func (s *Server) execute(c *gin.Context) {
	w, ok := s.workspace(c.Param("id")); if !ok { c.JSON(http.StatusNotFound, gin.H{"error":"workspace not found"}); return }
	var req executeRequest
	if err := c.ShouldBindJSON(&req); err != nil { c.JSON(http.StatusBadRequest, gin.H{"error":"action is required"}); return }
	root, err := s.resolve(w, ".", false); if err != nil { c.JSON(http.StatusBadRequest, gin.H{"error":"workspace root is unavailable"}); return }
	runtimeMode := strings.ToLower(strings.TrimSpace(req.Runtime))
	if runtimeMode == "" { runtimeMode = "native" }
	var args []string
	if runtimeMode == "docker" {
		args, err = dockerAction(root, req.Action)
	} else if runtimeMode == "native" {
		args, err = actionCommand(root, req.Action)
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error":"runtime must be native or docker"}); return
	}
	if err != nil { c.JSON(http.StatusBadRequest, gin.H{"error":err.Error()}); return }
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute); defer cancel()
	var cmd *exec.Cmd
	if runtimeMode == "docker" {
		cmd = exec.CommandContext(ctx, "docker", args...)
	} else if runtime.GOOS == "windows" {
		// The action is mapped to fixed command strings; no user-supplied shell text is interpolated.
		cmd = exec.CommandContext(ctx, "cmd.exe", append([]string{"/D","/S","/C"}, args...)...)
	} else { cmd = exec.CommandContext(ctx, args[0], args[1:]...) }
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CI=true", "NO_COLOR=1")
	output, runErr := cmd.CombinedOutput()
	if len(output) > maxCommandOutput { output = append(output[:maxCommandOutput], []byte("\n...[output truncated]")...) }
	status := "success"; code := 0
	if runErr != nil { status = "failed"; code = 1; var exitErr *exec.ExitError; if errors.As(runErr, &exitErr) { code = exitErr.ExitCode() } }
	c.JSON(http.StatusOK, gin.H{"action":req.Action,"runtime":runtimeMode,"status":status,"exit_code":code,"output":string(output),"error":errorString(runErr)})
}

func actionCommand(root, action string) ([]string, error) {
	switch action {
	case "git-status":
		return []string{"git","status","--short"}, nil
	case "go-test":
		if exists(filepath.Join(root,"go.mod")) { return []string{"go","test","./..."}, nil }
	case "go-build":
		if exists(filepath.Join(root,"go.mod")) { return []string{"go","build","./..."}, nil }
	case "npm-test", "npm-build", "npm-lint":
		if exists(filepath.Join(root,"package.json")) {
			script := map[string]string{"npm-test":"test","npm-build":"build","npm-lint":"lint"}[action]
			return []string{"npm","run",script}, nil
		}
	case "python-test":
		if exists(filepath.Join(root,"pytest.ini")) || exists(filepath.Join(root,"pyproject.toml")) || exists(filepath.Join(root,"tests")) {
			return []string{"python","-m","pytest"}, nil
		}
	}
	return nil, fmt.Errorf("action %q is not supported for this workspace; supported actions: git-status, go-test, go-build, npm-test, npm-build, npm-lint, python-test", action)
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }
func errorString(err error) string { if err == nil { return "" }; return err.Error() }

var _ = json.Valid
var _ = http.MethodGet
