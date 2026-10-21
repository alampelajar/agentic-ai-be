package localrunner

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type Server struct {
	cfg Config
}

func NewServer(cfg Config) (*gin.Engine, error) {
	gin.SetMode(gin.ReleaseMode)
	s := &Server{cfg: cfg}
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger(), s.cors(), s.authenticate())
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true, "service": "local-agent-runner"})
	})
	api := r.Group("/api")
	api.GET("/workspaces", s.listWorkspaces)
	api.GET("/workspaces/:id/tree", s.listTree)
	api.GET("/workspaces/:id/file", s.readFile)
	api.PUT("/workspaces/:id/file", s.writeFile)
	api.POST("/workspaces/:id/execute", s.execute)
	return r, nil
}

func (s *Server) Run(addr string) error {
	srv := &http.Server{Addr: addr, Handler: mustEngine(s), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 2 * time.Minute, IdleTimeout: 60 * time.Second}
	return srv.ListenAndServe()
}

// NewServer constructs the engine and stores it in a package-level-free wrapper
// by exposing Engine below. This helper is intentionally not used for requests.
func mustEngine(s *Server) http.Handler {
	engine, _ := NewServerWithoutRecursion(s)
	return engine
}

func NewServerWithoutRecursion(s *Server) (*gin.Engine, error) {
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger(), s.cors(), s.authenticate())
	r.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true, "service": "local-agent-runner"}) })
	api := r.Group("/api")
	api.GET("/workspaces", s.listWorkspaces)
	api.GET("/workspaces/:id/tree", s.listTree)
	api.GET("/workspaces/:id/file", s.readFile)
	api.PUT("/workspaces/:id/file", s.writeFile)
	api.POST("/workspaces/:id/execute", s.execute)
	return r, nil
}

func (s *Server) cors() gin.HandlerFunc {
	allowed := map[string]bool{}
	for _, origin := range s.cfg.AllowedOrigins { allowed[strings.TrimSpace(origin)] = true }
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && !allowed[origin] {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "origin not allowed"})
			return
		}
		if origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
			c.Header("Access-Control-Allow-Methods", "GET, PUT, POST, OPTIONS")
		}
		if c.Request.Method == http.MethodOptions { c.AbortWithStatus(http.StatusNoContent); return }
		c.Next()
	}
}

func (s *Server) authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == "/health" {
			c.Next()
			return
		}
		const prefix = "Bearer "
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, prefix) || subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(header, prefix)), []byte(s.cfg.Token)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "valid local runner bearer token required"})
			return
		}
		c.Next()
	}
}
