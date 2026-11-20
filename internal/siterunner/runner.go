// Package siterunner keeps the right Website Builder sites running: a preview for
// every site someone is looking at, and every published site. It talks to Docker
// through its CLI, like the execution worker, and is the only other part of the
// system that starts containers. See WEBSITE-BUILDER.md, sections 10 and 11.
package siterunner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"agentic-ai-backend/internal/sitebuilder"
)

const (
	gatewayName = "agentic-site-gateway"
	// PreviewIdle is how long a preview keeps running after the builder last asked for it.
	PreviewIdle = 30 * time.Minute
)

// Runner reconciles the containers with the database every Interval.
type Runner struct {
	DB       *gorm.DB
	Settings sitebuilder.Settings
	Docker   string // docker CLI
	Bind     string // host address the gateway listens on: 127.0.0.1 in development
	Interval time.Duration
	Logf     func(format string, args ...any)
}

func (r *Runner) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// docker runs the CLI with only what it needs to find the daemon: the server's own
// environment (database password, API keys) is not passed on.
func (r *Runner) docker(ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, r.Docker, args...)
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "SystemRoot=" + os.Getenv("SystemRoot"), "DOCKER_HOST=" + os.Getenv("DOCKER_HOST"), "USERPROFILE=" + os.Getenv("USERPROFILE"), "HOME=" + os.Getenv("HOME")}
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if err != nil {
		return stdout.String(), fmt.Errorf("docker %s: %s", args[0], strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Check confirms the daemon answers and the site image is present.
func (r *Runner) Check(ctx context.Context) error {
	if _, err := r.docker(ctx, "version", "--format", "{{.Server.Version}}"); err != nil {
		return fmt.Errorf("the Docker daemon is not reachable: %w", err)
	}
	if _, err := r.docker(ctx, "image", "inspect", "--format", "{{.Id}}", r.Settings.Image); err != nil {
		return fmt.Errorf("the site image %s is missing (docker build -t %s deploy/site-image)", r.Settings.Image, r.Settings.Image)
	}
	return nil
}

// Run reconciles until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) {
	if r.Interval <= 0 {
		r.Interval = 3 * time.Second
	}
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	for {
		step, cancel := context.WithTimeout(ctx, 2*time.Minute)
		if err := r.Reconcile(step); err != nil && ctx.Err() == nil {
			r.logf("[SITES] %v", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// wanted is one container that should be running.
type wanted struct {
	name    string
	siteID  uint
	kind    string // "p" preview, "l" live
	version int
	output  string
	data    string
}

func containerName(kind string, siteID uint) string {
	return fmt.Sprintf("agentic-site-%s-%d", kind, siteID)
}

func networkName(siteID uint) string { return fmt.Sprintf("agentic-site-net-%d", siteID) }

// ownerActive keeps only websites whose owner's account is not waiting to be deleted.
const ownerActive = "JOIN users ON users.id = sites.user_id AND users.deleted_at IS NULL"

// desired reads from the database what should run now.
func (r *Runner) desired() (map[string]wanted, map[string]uint, error) {
	want := map[string]wanted{}
	live := map[string]uint{}

	var previews []struct {
		ID     uint
		Number int
	}
	// Websites of an account waiting to be deleted stay offline (ownerActive); they come
	// back by themselves when an admin restores the account.
	err := r.DB.Table("sites").Select("sites.id, site_versions.number").
		Joins("JOIN site_versions ON site_versions.id = sites.current_version_id").
		Joins(ownerActive).
		Where("sites.preview_requested_at > ?", time.Now().Add(-PreviewIdle)).Scan(&previews).Error
	if err != nil {
		return nil, nil, err
	}
	for _, item := range previews {
		name := containerName("p", item.ID)
		want[name] = wanted{name: name, siteID: item.ID, kind: "p", version: item.Number,
			output: filepath.Join(r.Settings.VersionDir(item.ID, item.Number), "output"), data: r.Settings.DataDir(item.ID, "preview")}
	}

	var published []struct {
		ID     uint
		Slug   string
		Number int
	}
	err = r.DB.Table("sites").Select("sites.id, site_releases.slug, site_versions.number").
		Joins("JOIN site_releases ON site_releases.id = sites.published_release_id").
		Joins("JOIN site_versions ON site_versions.id = site_releases.site_version_id").
		Joins(ownerActive).
		Scan(&published).Error
	if err != nil {
		return nil, nil, err
	}
	for _, item := range published {
		name := containerName("l", item.ID)
		want[name] = wanted{name: name, siteID: item.ID, kind: "l", version: item.Number,
			output: filepath.Join(r.Settings.VersionDir(item.ID, item.Number), "output"), data: r.Settings.DataDir(item.ID, "live")}
		live[item.Slug] = item.ID
	}
	return want, live, nil
}

var siteContainer = regexp.MustCompile(`^agentic-site-([pl])-([0-9]+)$`)

// Reconcile makes the running containers match the database once.
func (r *Runner) Reconcile(ctx context.Context) error {
	want, live, err := r.desired()
	if err != nil {
		return fmt.Errorf("reading sites: %w", err)
	}
	if err := r.writeRoutes(live); err != nil {
		return fmt.Errorf("writing gateway routes: %w", err)
	}
	if err := r.ensureGateway(ctx); err != nil {
		return err
	}

	listing, err := r.docker(ctx, "ps", "-a", "--filter", "label=agentic.site", "--format", "{{.Names}}\t{{.Label \"agentic.version\"}}\t{{.State}}")
	if err != nil {
		return err
	}
	running := map[string]bool{}
	for _, line := range strings.Split(listing, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || !siteContainer.MatchString(fields[0]) {
			continue
		}
		name, version, state := fields[0], fields[1], fields[2]
		target, ok := want[name]
		if ok && state == "running" && version == strconv.Itoa(target.version) {
			running[name] = true
			continue
		}
		if _, err := r.docker(ctx, "rm", "-f", name); err != nil {
			r.logf("[SITES] removing %s: %v", name, err)
		} else if !ok {
			r.logf("[SITES] stopped %s", name)
		}
	}

	needed := map[uint]bool{}
	for name, target := range want {
		needed[target.siteID] = true
		if running[name] {
			continue
		}
		if err := r.start(ctx, target); err != nil {
			r.logf("[SITES] starting %s: %v", name, err)
			continue
		}
		r.logf("[SITES] started %s (version %d)", name, target.version)
	}
	return r.syncNetworks(ctx, needed)
}

func (r *Runner) ensureNetwork(ctx context.Context, siteID uint) error {
	name := networkName(siteID)
	if _, err := r.docker(ctx, "network", "inspect", "--format", "{{.Internal}}", name); err == nil {
		return nil
	}
	// --internal: the site has no route to the internet, the host or other sites.
	_, err := r.docker(ctx, "network", "create", "--internal", "--label", "agentic.site="+strconv.FormatUint(uint64(siteID), 10), name)
	return err
}

func (r *Runner) start(ctx context.Context, target wanted) error {
	if info, err := os.Stat(filepath.Join(target.output, "server.mjs")); err != nil || info.IsDir() {
		return fmt.Errorf("the built site is missing in %s", target.output)
	}
	if err := os.MkdirAll(target.data, 0o770); err != nil {
		return err
	}
	if err := r.ensureNetwork(ctx, target.siteID); err != nil {
		return err
	}
	site := strconv.FormatUint(uint64(target.siteID), 10)
	_, err := r.docker(ctx, "run", "-d",
		"--name", target.name,
		"--network", networkName(target.siteID),
		"--label", "agentic.site="+site,
		"--label", "agentic.kind="+target.kind,
		"--label", "agentic.version="+strconv.Itoa(target.version),
		"--user", "65534:65534",
		"--read-only",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", "64",
		"--memory", "256m", "--memory-swap", "256m",
		"--cpus", "0.5",
		"--ulimit", "nofile=1024:1024",
		"--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=32m",
		"--mount", "type=bind,source="+target.output+",target=/site,readonly",
		"--mount", "type=bind,source="+target.data+",target=/data",
		"--workdir", "/site",
		"--env", "PORT=3000", "--env", "DATA_DIR=/data", "--env", "NODE_ENV=production",
		r.Settings.Image, "node", "/site/server.mjs")
	return err
}

// gatewayConfig changes when the gateway must be started again with new settings.
func (r *Runner) gatewayConfig() (string, []string) {
	secret := string(sitebuilder.PreviewSecret())
	env := []string{"PREVIEW_DOMAIN=" + r.Settings.PreviewDomain, "SITES_DOMAIN=" + r.Settings.SitesDomain, "PREVIEW_SECRET=" + secret, "PORT=8080"}
	sum := sha256.Sum256([]byte(strings.Join(env, "\n") + r.Bind + r.Settings.GatewayPort + r.Settings.Image + "samples-v1"))
	return hex.EncodeToString(sum[:8]), env
}

func (r *Runner) ensureGateway(ctx context.Context) error {
	hash, env := r.gatewayConfig()
	state, err := r.docker(ctx, "inspect", "--format", "{{.State.Running}} {{index .Config.Labels \"agentic.config\"}}", gatewayName)
	if err == nil && state == "true "+hash {
		return nil
	}
	if err == nil {
		_, _ = r.docker(ctx, "rm", "-f", gatewayName)
	}
	if err := os.MkdirAll(r.Settings.SamplesDir(), 0o750); err != nil {
		return err
	}
	if err := os.MkdirAll(r.Settings.GatewayDir(), 0o750); err != nil {
		return err
	}
	args := []string{"run", "-d",
		"--name", gatewayName,
		"--label", "agentic.gateway=1",
		"--label", "agentic.config=" + hash,
		"--publish", r.Bind + ":" + r.Settings.GatewayPort + ":8080",
		"--user", "65534:65534",
		"--read-only",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", "128",
		"--memory", "256m",
		"--mount", "type=bind,source=" + r.Settings.GatewayDir() + ",target=/config,readonly",
		"--mount", "type=bind,source=" + r.Settings.SamplesDir() + ",target=/samples,readonly",
	}
	for _, variable := range env {
		args = append(args, "--env", variable)
	}
	args = append(args, r.Settings.Image, "node", "/opt/site/tools/gateway.mjs")
	if _, err := r.docker(ctx, args...); err != nil {
		return fmt.Errorf("starting the gateway: %w", err)
	}
	r.logf("[SITES] gateway listening on %s:%s", r.Bind, r.Settings.GatewayPort)
	return nil
}

// syncNetworks connects the gateway to the network of every site that runs, and
// removes the networks of sites that no longer run anything.
func (r *Runner) syncNetworks(ctx context.Context, needed map[uint]bool) error {
	raw, err := r.docker(ctx, "inspect", "--format", "{{json .NetworkSettings.Networks}}", gatewayName)
	if err != nil {
		return err
	}
	var attached map[string]json.RawMessage
	_ = json.Unmarshal([]byte(raw), &attached)
	for siteID := range needed {
		name := networkName(siteID)
		if _, ok := attached[name]; ok {
			continue
		}
		if err := r.ensureNetwork(ctx, siteID); err != nil {
			return err
		}
		if _, err := r.docker(ctx, "network", "connect", name, gatewayName); err != nil {
			return err
		}
	}
	networks, err := r.docker(ctx, "network", "ls", "--filter", "label=agentic.site", "--format", "{{.Name}}")
	if err != nil {
		return err
	}
	for _, name := range strings.Fields(networks) {
		id, convErr := strconv.ParseUint(strings.TrimPrefix(name, "agentic-site-net-"), 10, 64)
		if convErr != nil || needed[uint(id)] {
			continue
		}
		if _, ok := attached[name]; ok {
			_, _ = r.docker(ctx, "network", "disconnect", "-f", name, gatewayName)
		}
		_, _ = r.docker(ctx, "network", "rm", name)
	}
	return nil
}

// writeRoutes writes the published addresses for the gateway, only when they changed.
func (r *Runner) writeRoutes(live map[string]uint) error {
	if err := os.MkdirAll(r.Settings.GatewayDir(), 0o750); err != nil {
		return err
	}
	data, err := json.Marshal(map[string]any{"live": live})
	if err != nil {
		return err
	}
	file := filepath.Join(r.Settings.GatewayDir(), "routes.json")
	if current, err := os.ReadFile(file); err == nil && bytes.Equal(current, data) {
		return nil
	}
	temporary := file + ".tmp"
	if err := os.WriteFile(temporary, data, 0o640); err != nil {
		return err
	}
	return os.Rename(temporary, file)
}

// StopSite removes a site's containers and network at once (used when it is deleted).
func (r *Runner) StopSite(ctx context.Context, siteID uint) {
	for _, kind := range []string{"p", "l"} {
		_, _ = r.docker(ctx, "rm", "-f", containerName(kind, siteID))
	}
	_, _ = r.docker(ctx, "network", "disconnect", "-f", networkName(siteID), gatewayName)
	_, _ = r.docker(ctx, "network", "rm", networkName(siteID))
}
