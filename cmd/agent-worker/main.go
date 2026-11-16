// Command agent-worker runs Website Builder agent runs: it calls the AI models,
// writes the site's files into its workspace and has the execution worker build and
// check the site in the sandbox. It runs no site code itself. Runs continue when the
// person closes the browser.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/joho/godotenv"

	"agentic-ai-backend/config"
	"agentic-ai-backend/handlers"
	"agentic-ai-backend/internal/cloudworkspace"
	"agentic-ai-backend/internal/sitebuilder"
)

func main() {
	_ = godotenv.Load()
	root := strings.TrimSpace(os.Getenv("CLOUD_WORKSPACE_ROOT"))
	if root == "" {
		root = "data/workspaces"
	}
	storage, err := cloudworkspace.NewLocalDevelopmentStorage(root)
	if err != nil {
		log.Fatalf("workspace storage: %v", err)
	}
	config.ConnectDatabase()

	concurrent, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("BUILDER_MAX_CONCURRENT")))
	worker := &sitebuilder.Worker{
		DB:            config.DB,
		Storage:       storage,
		Models:        handlers.BuilderModels{},
		Settings:      sitebuilder.LoadSettings(),
		MaxConcurrent: concurrent,
	}
	// One agent worker per database for now: runs a stopped worker left behind are
	// failed at once instead of waiting for the stale-run timeout.
	if strings.TrimSpace(os.Getenv("BUILDER_MULTIPLE_WORKERS")) != "true" {
		if count := worker.RecoverOrphans(); count > 0 {
			fmt.Printf("Marked %d run(s) left by the previous worker as failed.\n", count)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Println("Agent worker started: Website Builder runs.")
	worker.Run(ctx)
	fmt.Println("Agent worker stopped.")
}
