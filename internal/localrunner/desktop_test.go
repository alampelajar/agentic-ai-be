package localrunner

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

type mockDesktopProvider struct {
	calls   []string
	stopped bool
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
	engine.POST("/stop", func(c *gin.Context) { runtime.stop(); c.Status(204) })

	for _, path := range []string{"/status", "/windows"} {
		req := httptest.NewRequest("GET", path, nil)
		res := httptest.NewRecorder()
		engine.ServeHTTP(res, req)
		if res.Code != 200 {
			t.Fatalf("%s status = %d", path, res.Code)
		}
	}
	stopReq := httptest.NewRequest("POST", "/stop", nil)
	stopRes := httptest.NewRecorder()
	engine.ServeHTTP(stopRes, stopReq)
	if !mock.stopped {
		t.Fatal("expected provider stop")
	}
	if len(mock.calls) != 2 || mock.calls[0] != "status" || mock.calls[1] != "windows" {
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
