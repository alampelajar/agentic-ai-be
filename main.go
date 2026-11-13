package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"agentic-ai-backend/config"
	"agentic-ai-backend/handlers"
	"agentic-ai-backend/internal/httpguard"
	"agentic-ai-backend/middleware"
)

func main() {
	// ============================================================
	// LOAD ENVIRONMENT
	// ============================================================

	err := godotenv.Load()

	if err != nil {
		fmt.Println(
			"Peringatan: file .env tidak ditemukan, menggunakan environment system.",
		)
	} else {
		fmt.Println("[OK] File .env berhasil dimuat")
	}

	// ============================================================
	// GOOGLE CLIENT ID
	// ============================================================

	if strings.TrimSpace(
		os.Getenv("GOOGLE_CLIENT_ID"),
	) == "" {
		fmt.Println(
			"[WARN] GOOGLE_CLIENT_ID belum diatur",
		)
	} else {
		fmt.Println(
			"[OK] GOOGLE_CLIENT_ID berhasil dimuat",
		)
	}

	// ============================================================
	// AI ENCRYPTION KEY
	// ============================================================

	if strings.TrimSpace(
		os.Getenv("AI_ENCRYPTION_KEY"),
	) == "" {
		fmt.Println(
			"[WARN] AI_ENCRYPTION_KEY belum diatur. Penambahan custom provider tidak akan bisa mengenkripsi API key.",
		)
	}

	// ============================================================
	// JWT SECRETS (every environment)
	// ============================================================

	// HMAC accepts an empty key: without real secrets anyone could sign an admin
	// token. Refuse to start rather than run that way, in development too.
	if problems := config.JWTProblems(); len(problems) > 0 {
		for _, problem := range problems {
			log.Printf("[CONFIG] %s", problem)
		}
		log.Fatal("JWT secret belum aman. Atur JWT_ACCESS_SECRET dan JWT_REFRESH_SECRET (lihat .env.example).")
	}

	// ============================================================
	// PRODUCTION CONFIGURATION CHECK
	// ============================================================

	if config.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
		if problems := config.ProductionProblems(); len(problems) > 0 {
			for _, problem := range problems {
				log.Printf("[CONFIG] %s", problem)
			}
			log.Fatal("APP_ENV=production tetapi konfigurasi belum aman. Perbaiki hal di atas.")
		}
	}

	// ============================================================
	// DATABASE
	// ============================================================

	config.ConnectDatabase()


	// ============================================================
	// GIN ROUTER
	// ============================================================

	// gin.New, not gin.Default: the default logger prints raw paths and query
	// strings, which can hold tokens. httpguard.AccessLog logs route patterns only.
	router := gin.New()

	// Trust X-Forwarded-For only from the proxies listed in TRUSTED_PROXIES (none
	// by default), so a client cannot pick its own address and dodge rate limits.
	if err := router.SetTrustedProxies(config.TrustedProxies()); err != nil {
		log.Fatalf("TRUSTED_PROXIES tidak valid: %v", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	generalLimit := httpguard.NewLimiter(600, 120) // per address, every request
	router.Use(
		httpguard.RequestID(),
		httpguard.AccessLog(logger),
		gin.Recovery(),
		httpguard.SecurityHeaders(config.IsProduction()),
		httpguard.CORS(config.CORSOrigins()),
		httpguard.BodyLimit(8<<20, 64<<20),
		httpguard.RateLimit(generalLimit, httpguard.ByIP),
	)

	// Stricter limits where a request is expensive or can be abused. Keyed by
	// address before sign-in and by user after it. In memory: see
	// docs/phase7-public-readiness.md for running more than one instance.
	loginLimit := httpguard.RateLimit(httpguard.NewLimiter(10, 10), httpguard.ByIP)
	registerLimit := httpguard.RateLimit(httpguard.NewLimiter(3, 5), httpguard.ByIP)
	adminLoginLimit := httpguard.RateLimit(httpguard.NewLimiter(5, 5), httpguard.ByIP)
	refreshLimit := httpguard.RateLimit(httpguard.NewLimiter(60, 20), httpguard.ByIP)
	// Each e-mail code endpoint gets a bucket of its own (10 a minute). Before sign-in the
	// key is the address; after it, the user, so people behind one address do not share it.
	otpLimit := func(key func(*gin.Context) string) gin.HandlerFunc {
		return httpguard.RateLimit(httpguard.NewLimiter(10, 10), key)
	}
	userLimit := httpguard.RateLimit(httpguard.NewLimiter(300, 60), httpguard.ByUserOrIP)
	chatLimit := httpguard.RateLimit(httpguard.NewLimiter(30, 10), httpguard.ByUserOrIP)
	importLimit := httpguard.RateLimit(httpguard.NewLimiter(10, 5), httpguard.ByUserOrIP)
	executionLimit := httpguard.RateLimit(httpguard.NewLimiter(20, 10), httpguard.ByUserOrIP)

	// Serve uploaded feedback images so the Admin ticket page can preview them.
	router.Static("/uploads", "./uploads")

	// ============================================================
	// API
	// ============================================================

	api := router.Group("/api")

	// Liveness: the process answers. Readiness: it can also reach its database, so a
	// load balancer stops sending traffic to an instance that cannot serve it.
	api.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	api.GET("/ready", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		sqlDB, err := config.DB.DB()
		if err == nil {
			err = sqlDB.PingContext(ctx)
		}
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// ============================================================
	// PUBLIC SYSTEM ROUTES
	// ============================================================

	api.GET(
		"/system/maintenance",
		handlers.GetMaintenanceStatus,
	)

	// ============================================================
	// AUTH
	// ============================================================

	auth := api.Group("/auth")

	{
		auth.POST(
			"/register",
			registerLimit,
			handlers.Register,
		)

		auth.POST(
			"/login",
			loginLimit,
			handlers.Login,
		)

		auth.POST(
			"/google",
			loginLimit,
			handlers.GoogleLogin,
		)

		auth.POST(
			"/refresh",
			refreshLimit,
			handlers.Refresh,
		)

		auth.POST(
			"/logout",
			handlers.Logout,
		)

		// Sign-up confirmed by an e-mailed code. Nothing is created until verify.
		auth.POST("/register/request", registerLimit, handlers.RequestRegisterOTP)
		auth.POST("/register/resend", otpLimit(httpguard.ByIP), handlers.ResendRegisterOTP)
		auth.POST("/register/verify", otpLimit(httpguard.ByIP), handlers.VerifyRegisterOTP)

		auth.POST(
			"/forgot-password/request",
			otpLimit(httpguard.ByIP),
			handlers.RequestPasswordReset,
		)

		auth.POST(
			"/forgot-password/verify",
			otpLimit(httpguard.ByIP),
			handlers.VerifyPasswordResetOTP,
		)

		auth.POST(
			"/forgot-password/reset",
			otpLimit(httpguard.ByIP),
			handlers.ResetPasswordWithOTP,
		)
	}

	// GitHub sends the browser back here; the one-time state says whose connection it is.
	api.GET("/integrations/github/callback", otpLimit(httpguard.ByIP), handlers.GitHubCallback)

	// ============================================================
	// PROTECTED ROUTES
	// ============================================================

	protected := api.Group("")

	protected.Use(
		middleware.AuthMiddleware(),
		userLimit,
	)

	// ============================================================
	// USER FEEDBACK
	// ============================================================
	protected.GET("/integrations/github", handlers.GetGitHubConnection)
	protected.POST("/integrations/github/connect", otpLimit(httpguard.ByUserOrIP), handlers.StartGitHubConnect)
	protected.DELETE("/integrations/github", otpLimit(httpguard.ByUserOrIP), handlers.DeleteGitHubConnection)
	protected.POST("/me/delete/request", otpLimit(httpguard.ByUserOrIP), handlers.RequestDeleteAccountOTP)
	protected.POST("/me/email/change/request", otpLimit(httpguard.ByUserOrIP), handlers.RequestChangeEmailOTP)
	protected.POST("/me/email/change/resend", otpLimit(httpguard.ByUserOrIP), handlers.ResendChangeEmailOTP)
	protected.POST("/me/email/change/verify", otpLimit(httpguard.ByUserOrIP), handlers.VerifyChangeEmailOTP)

	protected.GET("/feedback", handlers.GetMyFeedback)
	protected.POST("/feedback", handlers.CreateFeedback)
	protected.PATCH("/feedback/:id/rating", handlers.RateMyFeedback)
	protected.DELETE("/feedback/:id", handlers.DeleteMyFeedback)

	{
		// ========================================================
		// CURRENT USER
		// ========================================================

		protected.GET(
			"/me",
			handlers.Me,
		)

		protected.PUT(
			"/me",
			handlers.UpdateMe,
		)

		protected.PUT(
			"/me/password",
			handlers.ChangePassword,
		)
		protected.POST(
			"/me/verify-delete-password",
			handlers.VerifyDeleteAccountPassword,
		)

		protected.DELETE(
			"/me",
			handlers.DeleteMe,
		)

		// ========================================================
		// AGENTIC
		// ========================================================

		protected.POST(
			"/agent/plan",
			handlers.AgentPlan,
		)

		protected.GET(
			"/agents",
			handlers.GetAgents,
		)

		protected.GET(
			"/agents/:id",
			handlers.GetAgent,
		)

		// ========================================================
		// SKILLS
		// ========================================================

		protected.GET("/skills", handlers.GetSkills)
		protected.POST("/skills", handlers.CreateSkill)
		protected.GET("/skills/:id", handlers.GetSkill)
		protected.PUT("/skills/:id", handlers.UpdateSkill)
		protected.DELETE("/skills/:id", handlers.DeleteSkill)
		protected.POST("/skills/import/zip", importLimit, handlers.ImportSkillZIP)
		protected.POST("/skills/import/github", importLimit, handlers.ImportSkillGitHub)
		protected.POST("/skills/:id/validate", handlers.ValidateSkill)
		protected.GET("/agents/:id/skills", handlers.GetAgentSkills)
		protected.POST("/agents/:id/skills", handlers.AssignSkillToAgent)
		protected.PATCH("/agents/:id/skills/:skill_id", handlers.UpdateAgentSkill)
		protected.DELETE("/agents/:id/skills/:skill_id", handlers.RemoveSkillFromAgent)
		protected.GET("/memories", handlers.GetMemories)
		protected.POST("/memories", handlers.CreateMemory)
		protected.PATCH("/memories/:id", handlers.UpdateMemory)
		protected.DELETE("/memories/:id", handlers.DeleteMemory)

		// Design Studio
		protected.GET("/design-projects", handlers.GetDesignProjects)
		protected.POST("/design-projects", handlers.CreateDesignProject)
		protected.GET("/design-projects/:id", handlers.GetDesignProject)
		protected.PUT("/design-projects/:id", handlers.UpdateDesignProject)
		protected.DELETE("/design-projects/:id", handlers.DeleteDesignProject)
		protected.GET("/workspaces", handlers.GetWorkspaces)
		protected.POST("/workspaces", importLimit, handlers.CreateWorkspace)
		protected.GET("/workspaces/:id", handlers.GetWorkspace)
		protected.PATCH("/workspaces/:id", handlers.UpdateWorkspace)
		protected.DELETE("/workspaces/:id", handlers.DeleteWorkspace)
		protected.GET("/workspaces/:id/tree", handlers.GetWorkspaceTree)
		protected.GET("/workspaces/:id/file", handlers.GetWorkspaceFile)
		protected.PUT("/workspaces/:id/file", handlers.PutWorkspaceFile)
		protected.GET("/workspaces/:id/changes", handlers.GetWorkspaceChanges)
		protected.POST("/workspaces/:id/export", importLimit, handlers.ExportWorkspace)
		protected.POST("/workspaces/:id/github/publish", importLimit, handlers.PublishWorkspaceToGitHub)
		protected.GET("/workspaces/:id/executions", handlers.GetWorkspaceExecutions)
		protected.POST("/workspaces/:id/executions", executionLimit, handlers.CreateExecution)
		protected.GET("/executions/:id", handlers.GetExecution)
		protected.POST("/workspace-tools/run", executionLimit, handlers.RunWorkspaceTool)
		protected.POST("/workspace-tools/approvals/:id/approve", handlers.ApproveWorkspaceTool)
		protected.POST("/workspace-tools/approvals/:id/deny", handlers.DenyWorkspaceTool)
		protected.POST("/executions/:id/cancel", handlers.CancelExecution)
		protected.GET("/delegations", handlers.GetDelegations)
		protected.POST("/delegations", handlers.CreateDelegation)
		protected.DELETE("/delegations/:id", handlers.DeleteDelegation)
		protected.GET("/memory-settings", handlers.GetMemorySettings)
		protected.PUT("/memory-settings", handlers.UpdateMemorySettings)
		protected.POST("/tool-activities", handlers.CreateToolActivity)
		protected.GET("/tool-activities", handlers.GetToolActivities)

		// ========================================================
		// TASKS
		// ========================================================

		protected.GET(
			"/tasks",
			handlers.GetTasks,
		)

		protected.GET(
			"/tasks/:id",
			handlers.GetTask,
		)

		protected.POST(
			"/tasks",
			handlers.CreateTask,
		)

		protected.PATCH(
			"/tasks/:id",
			handlers.UpdateTask,
		)

		protected.DELETE(
			"/tasks/:id",
			handlers.DeleteTask,
		)

		protected.GET(
			"/tasks/:id/messages",
			handlers.GetTaskMessages,
		)

		protected.POST(
			"/tasks/:id/messages",
			chatLimit,
			handlers.AddTaskMessage,
		)

		protected.GET(
			"/tasks/:id/attachments/:attachmentId",
			handlers.GetTaskAttachment,
		)

		// ========================================================
		// CHAT
		// ========================================================

		protected.POST(
			"/chat",
			chatLimit,
			handlers.Chat,
		)

		// ========================================================
		// GENERATOR
		// ========================================================

		protected.POST(
			"/generator/landing-page",
			chatLimit,
			handlers.GenerateLandingPage,
		)

		// ========================================================
		// AI PROVIDERS
		// ========================================================

		// GET PROVIDERS
		protected.GET(
			"/ai/providers",
			handlers.GetAIProviders,
		)

		// CREATE PROVIDER
		protected.POST(
			"/ai/providers",
			handlers.CreateAIProvider,
		)

		// TEST CONNECTION
		protected.POST(
			"/ai/providers/test",
			handlers.TestAIProviderConnection,
		)

		// UPDATE PROVIDER
		protected.PUT(
			"/ai/providers/:id",
			handlers.UpdateAIProvider,
		)

		// DELETE PROVIDER
		protected.DELETE(
			"/ai/providers/:id",
			handlers.DeleteAIProvider,
		)

		// ========================================================
		// AI MODELS
		// ========================================================

		// GET ALL MODELS
		protected.GET(
			"/ai/models",
			handlers.GetAIModels,
		)

		// HOW EACH MODEL FARED, AND THE QUOTA LEFT PER PROVIDER
		protected.GET(
			"/ai/models/health",
			handlers.GetModelHealth,
		)

		// CREATE MODEL
		protected.POST(
			"/ai/models",
			handlers.CreateAIModel,
		)

		// UPDATE MODEL
		protected.PUT(
			"/ai/models/:id",
			handlers.UpdateAIModel,
		)

		// ========================================================
		// REMOVE MODEL FROM ONE AGENT
		//
		// Ini hanya menghapus relasi:
		//
		// agent_models
		//
		// Model AI tetap ada dan Agent lain yang memakai model
		// tersebut tidak akan terpengaruh.
		// ========================================================

		protected.DELETE(
			"/ai/models/:id/agents",
			handlers.RemoveModelFromAgent,
		)

		// ========================================================
		// DELETE MODEL
		//
		// Ini menghapus model AI beserta relasinya.
		// ========================================================

		protected.DELETE(
			"/ai/models/:id",
			handlers.DeleteAIModel,
		)

		// ========================================================
		// COMBOS (a model made of several models, owned by the user)
		// ========================================================

		protected.GET("/ai/combos", handlers.GetCombos)
		protected.POST("/ai/combos", handlers.CreateCombo)
		protected.PUT("/ai/combos/:id", handlers.UpdateCombo)
		protected.DELETE("/ai/combos/:id", handlers.DeleteCombo)
	}

	// ============================================================
	// ADMIN
	// ============================================================

	admin := api.Group("/admin")

	// ============================================================
	// ADMIN LOGIN
	// ============================================================

	admin.POST(
		"/login",
		adminLoginLimit,
		handlers.AdminLogin,
	)

	admin.POST(
		"/refresh",
		refreshLimit,
		handlers.AdminRefresh,
	)

	admin.POST(
		"/logout",
		handlers.AdminLogout,
	)

	// ============================================================
	// ADMIN PROTECTED ROUTES
	// ============================================================

	admin.Use(
		middleware.AuthMiddleware(),
		middleware.AdminMiddleware(),
	)

	{
		admin.GET(
			"/logs",
			handlers.AdminGetAuditLogs,
		)

		// FEEDBACK MANAGEMENT
		admin.GET("/feedback", handlers.AdminGetFeedback)
		admin.PATCH("/feedback/:id/status", handlers.AdminUpdateFeedbackStatus)
		admin.POST("/feedback/:id/replies", handlers.AdminReplyFeedback)
		admin.DELETE("/feedback/:id", handlers.AdminDeleteFeedback)

		admin.GET(
			"/agents",
			handlers.AdminGetAgents,
		)

		admin.GET(
			"/users",
			handlers.AdminGetUsers,
		)

		admin.GET(
			"/tasks",
			handlers.AdminGetTasks,
		)

		// GLOBAL AI MODEL CATALOG
		// Only verified admins can manage models provided by the system.
		admin.GET(
			"/ai/models",
			handlers.AdminGetSystemAIModels,
		)
		admin.POST(
			"/ai/models",
			handlers.AdminCreateSystemAIModel,
		)
		admin.PATCH(
			"/ai/models/:id",
			handlers.AdminUpdateSystemAIModel,
		)
		admin.DELETE(
			"/ai/models/:id",
			handlers.AdminDeleteSystemAIModel,
		)

		admin.POST(
			"/users",
			handlers.AdminCreateUser,
		)

		admin.PUT(
			"/users/:id",
			handlers.AdminUpdateUser,
		)

		admin.DELETE(
			"/users/:id",
			handlers.AdminDeleteUser,
		)

		// ========================================================
		// MAINTENANCE MODE
		// ========================================================

		admin.PUT(
			"/settings/maintenance",
			handlers.UpdateMaintenanceStatus,
		)
	}

	// ============================================================
	// PORT
	// ============================================================

	port := os.Getenv("PORT")

	if port == "" {
		port = "8081"
	}

	fmt.Printf(
		"Backend berjalan di http://localhost:%s\n",
		port,
	)

	// ============================================================
	// RUN SERVER
	// ============================================================

	// No WriteTimeout: chat responses stream and can legitimately take minutes.
	// The read-side timeouts stop slow-header and idle-connection attacks.
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}

	// A container is stopped with SIGTERM: finish the requests in flight first.
	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(
			"Gagal menjalankan server:",
			err,
		)
	}
}
