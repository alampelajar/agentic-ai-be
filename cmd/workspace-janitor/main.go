// Command workspace-janitor reports, and only on request removes, Cloud Workspaces
// that nobody has opened for a long time. Run without flags it changes nothing.
//
//	workspace-janitor                 list workspaces unused for 90 days
//	workspace-janitor -days 180       use a different retention (at least 7)
//	workspace-janitor -delete         delete the ones listed
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"agentic-ai-backend/config"
	"agentic-ai-backend/internal/cloudjanitor"
	"agentic-ai-backend/internal/cloudworkspace"
)

func main() {
	days := flag.Int("days", 90, "retention in days; workspaces not opened for longer are listed")
	remove := flag.Bool("delete", false, "delete the listed workspaces (default: only list them)")
	flag.Parse()

	retention := time.Duration(*days) * 24 * time.Hour
	if retention < cloudjanitor.MinRetention {
		log.Fatalf("retention must be at least %d days", int(cloudjanitor.MinRetention.Hours()/24))
	}
	root := strings.TrimSpace(os.Getenv("CLOUD_WORKSPACE_ROOT"))
	if root == "" {
		root = "data/workspaces"
	}
	storage, err := cloudworkspace.NewLocalDevelopmentStorage(root)
	if err != nil {
		log.Fatalf("workspace storage: %v", err)
	}
	config.ConnectDatabase()

	candidates, err := cloudjanitor.Inactive(config.DB, retention, time.Now())
	if err != nil {
		log.Fatalf("could not list workspaces: %v", err)
	}
	fmt.Printf("%d workspace(s) not used for %d days or more\n", len(candidates), *days)
	for _, workspace := range candidates {
		// Only ids and sizes are printed, never names or file content.
		fmt.Printf("  id=%d user=%d size=%d bytes files=%d\n", workspace.ID, workspace.UserID, workspace.SizeBytes, workspace.FileCount)
	}
	if !*remove {
		if len(candidates) > 0 {
			fmt.Println("Nothing was deleted. Run again with -delete to remove them.")
		}
		return
	}
	for _, workspace := range candidates {
		if err := cloudjanitor.Remove(config.DB, storage, workspace); err != nil {
			log.Printf("workspace %d was not removed: %v", workspace.ID, err)
			continue
		}
		fmt.Printf("  removed id=%d\n", workspace.ID)
	}
}
