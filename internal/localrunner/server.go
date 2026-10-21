package localrunner

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type Server struct { cfg Config }

func NewServer(cfg Config) (*gin.Engine, error) {
	gin.SetMode(gin.ReleaseMode)
	s := &Server{cfg: cfg}
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger(), s.cors(), s.authenticate())
	r.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true, "service": "local-agent-runner"}) })
	api := r.Group("/api")
	api.GET("/workspaces", s.listWorkspaces)
	api.POST("/workspaces/clone", s.cloneGitHub)
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
		if origin != "" && !allowed[origin] { c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error":"origin not allowed"}); return }
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
		if c.Request.URL.Path == "/health" { c.Next(); return }
		const prefix = "Bearer "
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, prefix) || subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(header, prefix)), []byte(s.cfg.Token)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error":"valid local runner bearer token required"})
			return
		}
		c.Next()
	}
}

// Run serves the runner with conservative HTTP timeouts. The frontend should
// only connect to the loopback address configured by the user.
func Run(cfg Config) error {
	engine, err := NewServer(cfg)
	if err != nil { return err }
	srv := &http.Server{Addr: cfg.ListenAddress, Handler: engine, ReadHeaderTimeout: 5*time.Second, ReadTimeout: 15*time.Second, WriteTimeout: 2*time.Minute, IdleTimeout: 60*time.Second}
	return srv.ListenAndServe()
}
