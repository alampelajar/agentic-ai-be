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

	// Dependency installation is opt-in: it needs the registry-only network made by
	// deploy/registry-proxy/setup.sh. Without it, install jobs are refused.
	dockerConfig.InstallNetwork = strings.TrimSpace(os.Getenv("CLOUD_EXEC_INSTALL_NETWORK"))
	if dockerConfig.InstallNetwork != "" {
		dockerConfig.InstallProxy = strings.TrimSpace(os.Getenv("CLOUD_EXEC_INSTALL_PROXY"))
		if dockerConfig.InstallProxy == "" {
			dockerConfig.InstallProxy = "cloudexec-registry-proxy:3128"
		}
	}
	executor := cloudexec.NewDockerExecutor(dockerConfig)

	check, cancelCheck := context.WithTimeout(context.Background(), 30*time.Second)
	err = executor.Available(check)
	cancelCheck()
	if err != nil {
		log.Fatalf("The sandbox is not available, so no job will be run: %v", err)
	}

	if dockerConfig.InstallNetwork == "" {
		fmt.Println("Dependency installation: off (CLOUD_EXEC_INSTALL_NETWORK is not set).")
	} else {
		installCheck, cancelInstall := context.WithTimeout(context.Background(), 30*time.Second)
		if err := executor.InstallReady(installCheck); err != nil {
			// Not fatal: tests and builds work, and install jobs fail with this reason.
			fmt.Printf("Dependency installation: NOT READY, install jobs will fail: %v\n", err)
		} else {
			fmt.Printf("Dependency installation: on, network %s, proxy %s.\n", dockerConfig.InstallNetwork, dockerConfig.InstallProxy)
		}
		cancelInstall()
	}

	config.ConnectDatabase()

	worker := &cloudexec.Worker{
		DB:            config.DB,
		Storage:       storage,
		Executor:      executor,
		Images:        dockerConfig.Images,
		MaxConcurrent: envInt("CLOUD_EXEC_MAX_CONCURRENT", 2),
		MaxPerUser:    envInt("CLOUD_EXEC_MAX_PER_USER", 1),
		// Megabytes in the environment, bytes in the worker; 0 keeps the default.
		MaxDependencyBytes: int64(envInt("CLOUD_EXEC_MAX_DEPENDENCY_MB", 0)) << 20,
		MaxWorkspaceBytes:  int64(envInt("CLOUD_EXEC_MAX_WORKSPACE_MB", 0)) << 20,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Printf("Execution worker started: %d at a time, %d per user, sandbox %s\n", worker.MaxConcurrent, worker.MaxPerUser, executor.Name())
	worker.Run(ctx)
	fmt.Println("Execution worker stopped.")
}
