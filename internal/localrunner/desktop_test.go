package localrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type mockDesktopProvider struct {
	calls   []string
	stopped bool
}

func TestDesktopActionApprovalDenyExpiryAndEmergencyStop(t *testing.T) {
	mock := &mockDesktopProvider{}
	runtime := &desktopRuntime{provider: mock, pending: map[string]pendingDesktopAction{}}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/request", runtime.requestAction)
	engine.POST("/approve/:id", runtime.approveAction)
	engine.POST("/deny/:id", runtime.denyAction)
	engine.POST("/emergency", runtime.emergencyStop)

	request := func(body any) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/request", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		engine.ServeHTTP(res, req)
		return res
	}
	first := request(map[string]any{"action": "desktop.focus_window", "agent": "coding", "reason": "Focus Notepad", "window": "Untitled - Notepad"})
	if first.Code != 202 {
		t.Fatalf("request status = %d", first.Code)
	}
	var payload struct {
		Action pendingDesktopAction `json:"action"`
	}
	_ = json.Unmarshal(first.Body.Bytes(), &payload)
	approve := httptest.NewRequest("POST", "/approve/"+payload.Action.ID, bytes.NewBufferString(`{"mode":"once"}`))
	approve.Header.Set("Content-Type", "application/json")
	approved := httptest.NewRecorder()
	engine.ServeHTTP(approved, approve)
	if approved.Code != 200 || len(mock.calls) != 1 {
		t.Fatalf("approve status=%d calls=%#v", approved.Code, mock.calls)
	}
	denied := request(map[string]any{"action": "desktop.type", "agent": "coding", "reason": "Type test", "window": "Untitled - Notepad", "arguments": map[string]any{"text": "x"}})
	var deniedPayload struct {
		Action pendingDesktopAction `json:"action"`
	}
	_ = json.Unmarshal(denied.Body.Bytes(), &deniedPayload)
	denyReq := httptest.NewRequest("POST", "/deny/"+deniedPayload.Action.ID, nil)
	denyRes := httptest.NewRecorder()
	engine.ServeHTTP(denyRes, denyReq)
	if denyRes.Code != 200 {
		t.Fatalf("deny status=%d", denyRes.Code)
	}
	expired := request(map[string]any{"action": "desktop.scroll", "agent": "coding", "reason": "Scroll test", "window": "Untitled - Notepad"})
	var expiredPayload struct {
		Action pendingDesktopAction `json:"action"`
	}
	_ = json.Unmarshal(expired.Body.Bytes(), &expiredPayload)
	runtime.mu.Lock()
	item := runtime.pending[expiredPayload.Action.ID]
	item.ExpiresAt = time.Now().Add(-time.Second)
	runtime.pending[item.ID] = item
	runtime.mu.Unlock()
	expireReq := httptest.NewRequest("POST", "/approve/"+item.ID, bytes.NewBufferString(`{"mode":"once"}`))
	expireRes := httptest.NewRecorder()
	engine.ServeHTTP(expireRes, expireReq)
	if expireRes.Code != 410 {
		t.Fatalf("expired status=%d", expireRes.Code)
	}
	emergencyReq := httptest.NewRequest("POST", "/emergency", nil)
	emergencyRes := httptest.NewRecorder()
	engine.ServeHTTP(emergencyRes, emergencyReq)
	if emergencyRes.Code != 200 || !mock.stopped || len(runtime.pending) != 0 {
		t.Fatalf("emergency failed: status=%d stopped=%v pending=%d", emergencyRes.Code, mock.stopped, len(runtime.pending))
	}
}

func TestDesktopActionIDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 5000; i++ {
		id := newActionID()
		if seen[id] {
			t.Fatalf("duplicate action id %q after %d ids", id, i)
		}
		seen[id] = true
	}
}

func TestDesktopOwnershipIsClaimedOnlyOnApproval(t *testing.T) {
	mock := &mockDesktopProvider{}
	runtime := &desktopRuntime{provider: mock, pending: map[string]pendingDesktopAction{}}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/request", runtime.requestAction)
	engine.POST("/approve/:id", runtime.approveAction)
	engine.POST("/deny/:id", runtime.denyAction)
	engine.POST("/revoke", runtime.revokePermission)

	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		engine.ServeHTTP(res, req)
		return res
	}
	request := func(agent string) (int, string) {
		res := post("/request", `{"action":"desktop.focus_window","agent":"`+agent+`","reason":"test","window":"1"}`)
		var payload struct {
			Action pendingDesktopAction `json:"action"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &payload)
		return res.Code, payload.Action.ID
	}

	// A denied request must not lock other agents out.
	code, id := request("agent-a")
	if code != 202 {
		t.Fatalf("request status = %d", code)
	}
	if res := post("/deny/"+id, ""); res.Code != 200 {
		t.Fatalf("deny status = %d", res.Code)
	}
	if code, _ := request("agent-b"); code != 202 {
		t.Fatalf("agent-b blocked by a denied request from agent-a: status %d", code)
	}

	// An expired approval must not claim ownership either.
	runtime.mu.Lock()
	runtime.pending = map[string]pendingDesktopAction{}
	runtime.mu.Unlock()
	_, expiredID := request("agent-a")
	runtime.mu.Lock()
	item := runtime.pending[expiredID]
	item.ExpiresAt = time.Now().Add(-time.Second)
	runtime.pending[expiredID] = item
	runtime.mu.Unlock()
	if res := post("/approve/"+expiredID, `{"mode":"once"}`); res.Code != 410 {
		t.Fatalf("expired approve status = %d", res.Code)
	}
	if code, _ := request("agent-b"); code != 202 {
		t.Fatalf("agent-b blocked by an expired approval from agent-a: status %d", code)
	}

	// Approving claims the session; the other agent is then rejected, including
	// a request that was already pending before the claim.
	runtime.mu.Lock()
	runtime.pending = map[string]pendingDesktopAction{}
	runtime.mu.Unlock()
	_, aID := request("agent-a")
	_, bID := request("agent-b")
	if res := post("/approve/"+aID, `{"mode":"once"}`); res.Code != 200 {
		t.Fatalf("approve agent-a status = %d", res.Code)
	}
	callsBefore := len(mock.calls)
	if res := post("/approve/"+bID, `{"mode":"once"}`); res.Code != 409 {
		t.Fatalf("approve of an overlapping agent-b request status = %d, want 409", res.Code)
	}
	if len(mock.calls) != callsBefore {
		t.Fatal("agent-b action must not execute while agent-a owns the session")
	}
	if code, _ := request("agent-b"); code != 409 {
		t.Fatalf("new agent-b request status = %d, want 409", code)
	}
	if code, _ := request("agent-a"); code != 202 {
		t.Fatalf("owner agent-a request status = %d, want 202", code)
	}
	if res := post("/revoke", ""); res.Code != 200 {
		t.Fatalf("revoke status = %d", res.Code)
	}
	if code, _ := request("agent-b"); code != 202 {
		t.Fatalf("agent-b after revoke status = %d, want 202", code)
	}
}

func (m *mockDesktopProvider) Call(_ context.Context, req desktopRequest) (desktopResponse, error) {
	m.calls = append(m.calls, req.Operation)
	return desktopResponse{OK: true, Status: "desktop_ready"}, nil
}
func (m *mockDesktopProvider) Stop() { m.stopped = true }

func TestDesktopRuntimeUsesReadOnlyProviderAndStops(t *testing.T) {
	mock := &mockDesktopProvider{}
	runtime := &desktopRuntime{provider: mock}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/status", runtime.status)
	engine.GET("/windows", func(c *gin.Context) { runtime.call(c, desktopRequest{Operation: "windows"}) })
	engine.GET("/screenshot", func(c *gin.Context) { runtime.call(c, desktopRequest{Operation: "screenshot"}) })
	engine.POST("/inspect", func(c *gin.Context) {
		var req struct {
			Window string `json:"window"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Window) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "window is required"})
			return
		}
		runtime.call(c, desktopRequest{Operation: "inspect", Window: strings.TrimSpace(req.Window)})
	})
	engine.POST("/stop", func(c *gin.Context) { runtime.stop(); c.Status(204) })

	for _, path := range []string{"/status", "/windows", "/screenshot"} {
		req := httptest.NewRequest("GET", path, nil)
		res := httptest.NewRecorder()
		engine.ServeHTTP(res, req)
		if res.Code != 200 {
			t.Fatalf("%s status = %d", path, res.Code)
		}
	}
	inspectReq := httptest.NewRequest("POST", "/inspect", bytes.NewBufferString(`{"window":"Untitled - Notepad"}`))
	inspectReq.Header.Set("Content-Type", "application/json")
	inspectRes := httptest.NewRecorder()
	engine.ServeHTTP(inspectRes, inspectReq)
	if inspectRes.Code != 200 {
		t.Fatalf("/inspect status = %d", inspectRes.Code)
	}

	stopReq := httptest.NewRequest("POST", "/stop", nil)
	stopRes := httptest.NewRecorder()
	engine.ServeHTTP(stopRes, stopReq)
	if !mock.stopped {
		t.Fatal("expected provider stop")
	}
	if len(mock.calls) != 4 || mock.calls[0] != "status" || mock.calls[1] != "windows" || mock.calls[2] != "screenshot" || mock.calls[3] != "inspect" {
		t.Fatalf("unexpected calls: %#v", mock.calls)
	}
}

func TestDesktopRuntimeTimeout(t *testing.T) {
	runtime := &desktopRuntime{provider: blockingDesktopProvider{}}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/windows", func(c *gin.Context) { runtime.call(c, desktopRequest{Operation: "windows"}) })
	request := httptest.NewRequest("GET", "/windows", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != 504 {
		t.Fatalf("status = %d, want 504", response.Code)
	}
}

type blockingDesktopProvider struct{}

func (blockingDesktopProvider) Call(ctx context.Context, _ desktopRequest) (desktopResponse, error) {
	<-ctx.Done()
	return desktopResponse{}, ctx.Err()
}
func (blockingDesktopProvider) Stop() {}
