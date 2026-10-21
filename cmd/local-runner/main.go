package main

import (
	"log"
	"os"

	"agentic-ai-backend/internal/localrunner"
)

func main() {
	cfg, err := localrunner.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	server, err := localrunner.NewServer(cfg)
	if err != nil {
		log.Fatal(err)
	}
	addr := cfg.ListenAddress
	log.Printf("Local Agent Runner listening on %s", addr)
	log.Printf("Configured workspaces: %d", len(cfg.Workspaces))
	if err := server.Run(addr); err != nil {
		log.Printf("runner stopped: %v", err)
		os.Exit(1)
	}
}
