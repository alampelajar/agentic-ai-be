package localrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const desktopTimeout = 10 * time.Second
const desktopMaxResponse = 8 << 20

type DesktopProvider interface {
	Call(context.Context, desktopRequest) (desktopResponse, error)
	Stop()
}

type desktopRequest struct {
	Operation string         `json:"operation"`
	Window    string         `json:"window,omitempty"`
	Options   map[string]any `json:"options,omitempty"`
}

type desktopResponse struct {
	OK      bool           `json:"ok"`
	Status  string         `json:"status,omitempty"`
	Message string         `json:"message,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}

type desktopRuntime struct {
	provider DesktopProvider
	mu       sync.Mutex
	stopped  bool
}

func newDesktopRuntime(cfg Config) *desktopRuntime {
	return &desktopRuntime{provider: &sidecarProvider{enabled: cfg.DesktopEnabled, command: cfg.DesktopSidecarCommand}}
}

func (r *desktopRuntime) call(c *gin.Context, req desktopRequest) {
	if r == nil || r.provider == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "desktop runtime unavailable"})
		return
	}
	r.mu.Lock()
	stopped := r.stopped
	r.mu.Unlock()
	if stopped {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "error": "desktop runtime stopped"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), desktopTimeout)
	defer cancel()
	response, err := r.provider.Call(ctx, req)
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		c.JSON(status, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, response)
}

func (r *desktopRuntime) status(c *gin.Context) {
	if r == nil || r.provider == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "status": "disconnected"})
		return
	}
	r.call(c, desktopRequest{Operation: "status"})
}

func (r *desktopRuntime) stop() {
	r.mu.Lock()
	r.stopped = true
	r.mu.Unlock()
	r.provider.Stop()
}

func (s *Server) desktopStatus(c *gin.Context) { s.desktop.status(c) }
func (s *Server) desktopWindows(c *gin.Context) {
	s.desktop.call(c, desktopRequest{Operation: "windows"})
}
func (s *Server) desktopScreenshot(c *gin.Context) {
	s.desktop.call(c, desktopRequest{Operation: "screenshot"})
}
func (s *Server) desktopInspect(c *gin.Context) {
	var req struct {
		Window string `json:"window"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Window) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "window is required"})
		return
	}
	s.desktop.call(c, desktopRequest{Operation: "inspect", Window: strings.TrimSpace(req.Window)})
}
func (s *Server) desktopStop(c *gin.Context) {
	s.desktop.stop()
	c.JSON(http.StatusOK, gin.H{"ok": true, "status": "stopped"})
}

type sidecarProvider struct {
	enabled bool
	command string
	mu      sync.Mutex
	stopped bool
}

func (p *sidecarProvider) Call(ctx context.Context, req desktopRequest) (desktopResponse, error) {
	if !p.enabled {
		return desktopResponse{}, errors.New("desktop access is disabled; enable it explicitly")
	}
	if strings.TrimSpace(p.command) == "" {
		return desktopResponse{}, errors.New("desktop sidecar command is not configured")
	}
	p.mu.Lock()
	stopped := p.stopped
	p.mu.Unlock()
	if stopped {
		return desktopResponse{}, errors.New("desktop sidecar stopped")
	}
	command := exec.CommandContext(ctx, "cmd", "/C", p.command)
	payload, _ := json.Marshal(req)
	command.Stdin = bytes.NewReader(append(payload, '\n'))
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return desktopResponse{}, ctx.Err()
		}
		return desktopResponse{}, errors.New(strings.TrimSpace(output.String()))
	}
	if output.Len() > desktopMaxResponse {
		return desktopResponse{}, errors.New("desktop response exceeds size limit")
	}
	var response desktopResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		return desktopResponse{}, errors.New("invalid desktop sidecar response")
	}
	return response, nil
}

func (p *sidecarProvider) Stop() { p.mu.Lock(); p.stopped = true; p.mu.Unlock() }
