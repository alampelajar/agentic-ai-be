package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"agentic-ai-backend/config"
	"agentic-ai-backend/handlers"
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
		fmt.Println("✓ File .env berhasil dimuat")
	}

	// ============================================================
	// GOOGLE CLIENT ID
	// ============================================================

	if strings.TrimSpace(
		os.Getenv("GOOGLE_CLIENT_ID"),
	) == "" {
		fmt.Println(
			"⚠ GOOGLE_CLIENT_ID belum diatur",
		)
	} else {
		fmt.Println(
			"✓ GOOGLE_CLIENT_ID berhasil dimuat",
		)
	}

	// ============================================================
	// AI ENCRYPTION KEY
	// ============================================================

	if strings.TrimSpace(
		os.Getenv("AI_ENCRYPTION_KEY"),
	) == "" {
		fmt.Println(
			"⚠ AI_ENCRYPTION_KEY belum diatur. Penambahan custom provider tidak akan bisa mengenkripsi API key.",
		)
	}

	// ============================================================
	// DATABASE
	// ============================================================

	config.ConnectDatabase()

	// ============================================================
	// GIN ROUTER
	// ============================================================

	router := gin.Default()

	// ============================================================
	// CORS
	// ============================================================

	router.Use(func(c *gin.Context) {
		origin := c.GetHeader("Origin")

		allowedOrigins := map[string]bool{
			"http://localhost:5173": true,
			"http://localhost:5174": true,
		}

		if allowedOrigins[origin] {
			c.Header(
				"Access-Control-Allow-Origin",
				origin,
			)
		}

		c.Header(
			"Access-Control-Allow-Credentials",
			"true",
		)

		c.Header(
			"Access-Control-Allow-Headers",
			"Content-Type, Authorization",
		)

		c.Header(
			"Access-Control-Allow-Methods",
			"GET, POST, PUT, PATCH, DELETE, OPTIONS",
		)

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(
				http.StatusNoContent,
			)

			return
		}

		c.Next()
	})

	// ============================================================
	// API
	// ============================================================

	api := router.Group("/api")

	api.GET("/health", func(c *gin.Context) {
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
			handlers.Register,
		)

		auth.POST(
			"/login",
			handlers.Login,
		)

		auth.POST(
			"/google",
			handlers.GoogleLogin,
		)

		auth.POST(
			"/refresh",
			handlers.Refresh,
		)

		auth.POST(
			"/logout",
			handlers.Logout,
		)
	}

	// ============================================================
	// PROTECTED ROUTES
	// ============================================================

	protected := api.Group("")

	protected.Use(
		middleware.AuthMiddleware(),
	)

	{
		// ========================================================
		// CURRENT USER
		// ========================================================

		protected.GET(
			"/me",
			handlers.Me,
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
			handlers.AddTaskMessage,
		)

		// ========================================================
		// CHAT
		// ========================================================

		protected.POST(
			"/chat",
			handlers.Chat,
		)

		// ========================================================
		// GENERATOR
		// ========================================================

		protected.POST(
			"/generator/landing-page",
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
		handlers.AdminLogin,
	)

	// ============================================================
	// ADMIN PROTECTED ROUTES
	// ============================================================

	admin.Use(
		middleware.AuthMiddleware(),
	)

	{
		admin.GET(
			"/users",
			handlers.AdminGetUsers,
		)

		admin.GET(
			"/tasks",
			handlers.AdminGetTasks,
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
		port = "8080"
	}

	fmt.Printf(
		"Backend berjalan di http://localhost:%s\n",
		port,
	)

	// ============================================================
	// RUN SERVER
	// ============================================================

	if err := router.Run(":" + port); err != nil {
		log.Fatal(
			"Gagal menjalankan server:",
			err,
		)
	}
}