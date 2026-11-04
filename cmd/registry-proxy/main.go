// Command registry-proxy is the egress point for dependency installation jobs.
// It runs in a container on two networks: the internal one the install jobs are
// on, and one with internet access. It tunnels HTTPS to the package registries
// and refuses everything else. See internal/registryproxy and
// deploy/registry-proxy/README.md.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"agentic-ai-backend/internal/registryproxy"
)

func main() {
	address := strings.TrimSpace(os.Getenv("PROXY_ADDR"))
	if address == "" {
		address = ":3128"
	}
	hosts := registryproxy.DefaultAllowedHosts
	if custom := strings.TrimSpace(os.Getenv("PROXY_ALLOWED_HOSTS")); custom != "" {
		hosts = strings.Split(custom, ",") // a deployment may narrow or replace the list, never a request
	}

	proxy := registryproxy.New(hosts)
	proxy.Logf = log.Printf
	server := &http.Server{Addr: address, Handler: proxy, ReadHeaderTimeout: 10 * time.Second}

	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	log.Printf("registry proxy on %s, allowing %s", address, strings.Join(hosts, ", "))
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
