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
	// PRODUCTION CONFIGURATION CHECK
	// ============================================================

	if problems := config.AlwaysRequiredProblems(); len(problems) > 0 {
		for _, problem := range problems {
			log.Printf("[CONFIG] %s", problem)
		}
		log.Fatal("Konfigurasi belum aman. Perbaiki hal di atas di file .env lalu jalankan ulang.")
	}

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

	registerRoutes(router)

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

	// Accounts deleted more than 30 days ago are removed for good, now and every hour.
	handlers.StartAccountPurger()
	handlers.StartMessageNotifier()

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
