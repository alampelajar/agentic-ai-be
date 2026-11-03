// Command execution-worker runs Cloud Workspace jobs. It is a separate process
// from the API server: the API only queues jobs, this program runs them, and it
// refuses to start unless the sandbox answers. There is no unsandboxed fallback.
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
	"time"

	"agentic-ai-backend/config"
	"agentic-ai-backend/internal/cloudexec"
	"agentic-ai-backend/internal/cloudworkspace"
	"agentic-ai-backend/internal/jobspec"
)

func envInt(name string, fallback int) int {
	if value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil && value > 0 {
		return value
	}
	return fallback
}

func main() {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("CLOUD_EXECUTION_ENABLED")), "true") {
		log.Fatal("CLOUD_EXECUTION_ENABLED is not true; the worker will not start. Execution is off by default.")
	}

	root := strings.TrimSpace(os.Getenv("CLOUD_WORKSPACE_ROOT"))
	if root == "" {
		root = "data/workspaces"
	}
	storage, err := cloudworkspace.NewLocalDevelopmentStorage(root)
	if err != nil {
		log.Fatalf("workspace storage: %v", err)
	}

	dockerConfig := cloudexec.DefaultDockerConfig()
	dockerConfig.Images = jobspec.DefaultImages()
	for name, target := range map[string]*string{"CLOUD_EXEC_IMAGE_GO": &dockerConfig.Images.Go, "CLOUD_EXEC_IMAGE_NODE": &dockerConfig.Images.Node, "CLOUD_EXEC_IMAGE_PYTHON": &dockerConfig.Images.Python, "CLOUD_EXEC_MEMORY": &dockerConfig.Memory, "CLOUD_EXEC_CPUS": &dockerConfig.CPUs, "CLOUD_EXEC_RUN_AS": &dockerConfig.RunAs} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			*target = value
		}
	}
	dockerConfig.PidsLimit = envInt("CLOUD_EXEC_PIDS_LIMIT", dockerConfig.PidsLimit)
	executor := cloudexec.NewDockerExecutor(dockerConfig)

	check, cancelCheck := context.WithTimeout(context.Background(), 30*time.Second)
	err = executor.Available(check)
	cancelCheck()
	if err != nil {
		log.Fatalf("The sandbox is not available, so no job will be run: %v", err)
	}

	config.ConnectDatabase()

	worker := &cloudexec.Worker{
		DB:            config.DB,
		Storage:       storage,
		Executor:      executor,
		Images:        dockerConfig.Images,
		MaxConcurrent: envInt("CLOUD_EXEC_MAX_CONCURRENT", 2),
		MaxPerUser:    envInt("CLOUD_EXEC_MAX_PER_USER", 1),
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Printf("Execution worker started: %d at a time, %d per user, sandbox %s\n", worker.MaxConcurrent, worker.MaxPerUser, executor.Name())
	worker.Run(ctx)
	fmt.Println("Execution worker stopped.")
}
