// Command site-runner keeps Website Builder sites running: the preview of every site
// someone is looking at and every published site, each in its own locked-down
// container on its own internal network, behind one gateway. It needs Docker and the
// site image (docker build -t agentic-site-node:1 deploy/site-image).
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"agentic-ai-backend/config"
	"agentic-ai-backend/internal/sitebuilder"
	"agentic-ai-backend/internal/siterunner"
)

func main() {
	_ = godotenv.Load()
	settings := sitebuilder.LoadSettings()
	bind := strings.TrimSpace(os.Getenv("SITE_GATEWAY_BIND"))
	if bind == "" {
		bind = "127.0.0.1"
	}
	runner := &siterunner.Runner{Settings: settings, Docker: "docker", Bind: bind}

	check, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	err := runner.Check(check)
	cancel()
	if err != nil {
		log.Fatalf("The site runner cannot start: %v", err)
	}

	config.ConnectDatabase()
	runner.DB = config.DB

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Printf("Site runner started: previews %s, published sites %s\n", settings.PreviewURLPattern(), settings.LiveURL("<name>"))
	runner.Run(ctx)
	fmt.Println("Site runner stopped. Running sites keep running until it starts again.")
}
