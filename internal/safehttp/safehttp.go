// Package safehttp builds HTTP clients for requests to addresses a user chose
// (an AI provider's base URL, say). Without a guard, such a field lets any signed-in
// user make the server call anything it can reach: cloud metadata, the database,
// other services on a private network.
//
// The check happens where it cannot be bypassed: when the TCP connection is made,
// on the address actually being dialled. A host name that resolves to an internal
// address, a name that changes its answer between a check and the request (DNS
// rebinding), and a redirect to an internal address are all stopped there.
package safehttp

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"

	"agentic-ai-backend/internal/cloudworkspace"
)

// ErrBlocked is wrapped by every error caused by the guard.
var ErrBlocked = errors.New("connection to this address is not allowed")

// NewClient returns a client that refuses to connect to loopback, private,
// link-local, multicast and other non-public addresses unless allowPrivate is
// true (local development against a model on the same computer). It never uses
// proxy settings from the environment when the guard is on, never follows more
// than five redirects, and refuses to be redirected from https to http.
func NewClient(timeout time.Duration, allowPrivate bool) *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			if allowPrivate {
				return nil
			}
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if cloudworkspace.BlockedIP(net.ParseIP(host)) {
				return fmt.Errorf("%w: %s", ErrBlocked, host)
			}
			return nil
		},
	}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		MaxIdleConns:          20,
		IdleConnTimeout:       60 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	if allowPrivate {
		transport.Proxy = http.ProxyFromEnvironment
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return errors.New("redirect from https to http refused")
			}
			return nil
		},
	}
}
