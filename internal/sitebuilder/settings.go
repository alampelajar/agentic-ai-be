// Package sitebuilder is the Website Builder: the site template, the design
// directions, the prompts, and the agent worker that turns a brief into a working
// site. The plan and its rules are in WEBSITE-BUILDER.md (frontend repository).
package sitebuilder

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Settings come from the environment and are shared by the API server, the agent
// worker and the site runner. Every value has a development default.
type Settings struct {
	DataRoot      string // SITE_DATA_ROOT: versions, site data, gateway config
	PreviewDomain string // SITE_PREVIEW_DOMAIN: previews are p-...<domain>
	SitesDomain   string // SITES_DOMAIN: published sites are <slug>.<domain>
	Scheme        string // SITE_PUBLIC_SCHEME: http in development, https behind Caddy
	PublicPort    string // SITE_PUBLIC_PORT: port in the public address, empty for the default
	// Published sites may be served differently from previews, for example over
	// HTTPS by a tunnel while previews stay on this computer.
	LiveScheme  string // SITES_PUBLIC_SCHEME, SITE_PUBLIC_SCHEME when unset
	LivePort    string // SITES_PUBLIC_PORT, SITE_PUBLIC_PORT when unset; empty for the default
	GatewayPort string // SITE_GATEWAY_PORT: where the gateway listens on the host
	Image       string // CLOUD_EXEC_IMAGE_SITE
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func LoadSettings() Settings {
	gatewayPort := env("SITE_GATEWAY_PORT", "8090")
	scheme := env("SITE_PUBLIC_SCHEME", "http")
	publicPort, set := os.LookupEnv("SITE_PUBLIC_PORT")
	if !set {
		publicPort = gatewayPort
	}
	livePort, set := os.LookupEnv("SITES_PUBLIC_PORT")
	if !set {
		livePort = publicPort
	}
	root, _ := filepath.Abs(env("SITE_DATA_ROOT", filepath.Join("data", "sites")))
	return Settings{
		DataRoot:      root,
		PreviewDomain: strings.ToLower(env("SITE_PREVIEW_DOMAIN", "preview.localhost")),
		SitesDomain:   strings.ToLower(env("SITES_DOMAIN", "sites.localhost")),
		Scheme:        scheme,
		PublicPort:    strings.TrimSpace(publicPort),
		LiveScheme:    env("SITES_PUBLIC_SCHEME", scheme),
		LivePort:      strings.TrimSpace(livePort),
		GatewayPort:   gatewayPort,
		Image:         env("CLOUD_EXEC_IMAGE_SITE", "agentic-site-node:1"),
	}
}

func (s Settings) hostURL(host string) string { return address(s.Scheme, s.PublicPort, host) }

func address(scheme, port, host string) string {
	if port == "" || (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		return fmt.Sprintf("%s://%s/", scheme, host)
	}
	return fmt.Sprintf("%s://%s:%s/", scheme, host, port)
}

// PreviewURLPattern describes preview addresses for log lines.
func (s Settings) PreviewURLPattern() string {
	return s.hostURL("p-<site>-<signed>." + s.PreviewDomain)
}

// LiveURL is the public address of a published site.
func (s Settings) LiveURL(slug string) string {
	scheme := s.LiveScheme
	if scheme == "" {
		scheme = s.Scheme
	}
	return address(scheme, s.LivePort, slug+"."+s.SitesDomain)
}

// PreviewTTL is how long a preview address works. The builder asks for a new one
// before it runs out.
const PreviewTTL = 2 * time.Hour

// PreviewURL is a signed, short-lived address of a site's preview. The signature is
// in the host name, so every request for the page and its files carries it without a
// cookie. The gateway checks it (deploy/site-image/tools/gateway.mjs).
func (s Settings) PreviewURL(siteID uint, now time.Time) (string, time.Time) {
	expires := now.Add(PreviewTTL)
	expiry := strconv.FormatInt(expires.Unix(), 36)
	id := strconv.FormatUint(uint64(siteID), 10)
	label := fmt.Sprintf("p-%s-%s-%s", id, expiry, previewSignature(id, expiry))
	return s.hostURL(label + "." + s.PreviewDomain), expires
}

func previewSignature(id, expiry string) string {
	mac := hmac.New(sha256.New, PreviewSecret())
	fmt.Fprintf(mac, "preview:%s:%s", id, expiry)
	return hex.EncodeToString(mac.Sum(nil))[:24]
}

// PreviewSecret signs preview addresses. SITE_PREVIEW_SECRET when set; otherwise it
// is derived from JWT_ACCESS_SECRET, so a development setup needs nothing extra.
func PreviewSecret() []byte {
	if secret := strings.TrimSpace(os.Getenv("SITE_PREVIEW_SECRET")); len(secret) >= 32 {
		return []byte(secret)
	}
	mac := hmac.New(sha256.New, []byte(os.Getenv("JWT_ACCESS_SECRET")))
	mac.Write([]byte("website-builder-preview"))
	return []byte(hex.EncodeToString(mac.Sum(nil)))
}

func id(siteID uint) string { return strconv.FormatUint(uint64(siteID), 10) }

// SiteDir holds everything of one site outside its workspace.
func (s Settings) SiteDir(siteID uint) string { return filepath.Join(s.DataRoot, id(siteID)) }

// VersionDir holds one version: source/ (to go back to it) and output/ (to serve it).
func (s Settings) VersionDir(siteID uint, number int) string {
	return filepath.Join(s.SiteDir(siteID), "versions", strconv.Itoa(number))
}

// DataDir is the writable data of the running site: "preview" or "live", kept apart.
func (s Settings) DataDir(siteID uint, kind string) string {
	return filepath.Join(s.SiteDir(siteID), "data", kind)
}

// SamplesDir holds one finished example website per design direction, as static
// files: <SamplesDir>/<direction key>/index.html. The gateway serves them.
func (s Settings) SamplesDir() string { return filepath.Join(s.DataRoot, "_samples") }

// SampleURL is the public address of a direction's example website, or "" when it
// has not been made yet (cmd/builder-samples makes them).
func (s Settings) SampleURL(key string) string {
	if _, err := os.Stat(filepath.Join(s.SamplesDir(), key, "index.html")); err != nil {
		return ""
	}
	return s.hostURL("contoh-" + key + "." + s.PreviewDomain)
}

// GatewayDir holds routes.json, the one file the gateway reads.
func (s Settings) GatewayDir() string { return filepath.Join(s.DataRoot, "_gateway") }
