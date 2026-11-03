package cloudworkspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"
)

var githubName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)

// ParseGitHubURL accepts only https://github.com/<owner>/<repo>, optionally with
// ".git" or a trailing slash. Anything that could point somewhere else is
// refused: other hosts, ports, credentials in the URL, IP addresses, queries.
func ParseGitHubURL(raw string) (owner, repo string, err error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", "", errors.New("invalid GitHub URL")
	}
	switch {
	case u.Scheme != "https":
		return "", "", errors.New("only https GitHub URLs are allowed")
	case u.User != nil:
		return "", "", errors.New("the URL must not contain a username or password")
	case strings.ToLower(u.Hostname()) != "github.com" || u.Port() != "":
		return "", "", errors.New("only https://github.com repositories are allowed")
	case u.RawQuery != "" || u.Fragment != "":
		return "", "", errors.New("the URL must not contain a query or fragment")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 {
		return "", "", errors.New("use the form https://github.com/owner/repository")
	}
	owner, repo = parts[0], strings.TrimSuffix(parts[1], ".git")
	if !githubName.MatchString(owner) || !githubName.MatchString(repo) || owner == "." || owner == ".." || repo == "." || repo == ".." {
		return "", "", errors.New("invalid GitHub owner or repository name")
	}
	return owner, repo, nil
}

// BlockedIP reports addresses a server-side download must never connect to:
// loopback, private ranges, link-local (which includes cloud metadata at
// 169.254.169.254), unspecified and multicast.
func BlockedIP(ip net.IP) bool {
	return ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.Equal(net.ParseIP("100.100.100.200"))
}

// guardedDial refuses to connect to a blocked address. It runs after DNS
// resolution, so a name that resolves to an internal address is stopped too.
func guardedDial(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if BlockedIP(net.ParseIP(host)) {
		return fmt.Errorf("connection to %s is not allowed", host)
	}
	return nil
}

// GitHubDownloader fetches the default branch of a public repository as a ZIP
// from codeload.github.com. BaseURL and Client are fields only so tests can
// point them at a local server.
type GitHubDownloader struct {
	BaseURL string
	Client  *http.Client
}

func NewGitHubDownloader() *GitHubDownloader {
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: guardedDial}
	transport := &http.Transport{DialContext: dialer.DialContext, TLSHandshakeTimeout: 10 * time.Second, Proxy: nil}
	client := &http.Client{
		Timeout:   60 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 3 || req.URL.Scheme != "https" || req.URL.Hostname() != "codeload.github.com" {
				return errors.New("redirected to an untrusted location")
			}
			return nil
		},
	}
	return &GitHubDownloader{BaseURL: "https://codeload.github.com", Client: client}
}

func (d *GitHubDownloader) Download(ctx context.Context, owner, repo string, limits Limits) ([]byte, error) {
	endpoint := fmt.Sprintf("%s/%s/%s/zip/HEAD", d.BaseURL, url.PathEscape(owner), url.PathEscape(repo))
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	response, err := d.Client.Do(request)
	if err != nil {
		return nil, errors.New("the repository could not be downloaded")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, errors.New("the repository was not found or is not public")
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub answered with HTTP %d", response.StatusCode)
	}
	if response.ContentLength > limits.MaxZipBytes {
		return nil, fmt.Errorf("repository is larger than %d MiB", limits.MaxZipBytes>>20)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limits.MaxZipBytes+1))
	if err != nil {
		return nil, errors.New("the repository download was interrupted")
	}
	if int64(len(data)) > limits.MaxZipBytes {
		return nil, fmt.Errorf("repository is larger than %d MiB", limits.MaxZipBytes>>20)
	}
	return data, nil
}
