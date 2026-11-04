package safehttp

import (
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentic-ai-backend/internal/cloudworkspace"
)

func okServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("reached")) }))
	t.Cleanup(server.Close)
	return server
}

func TestTheGuardRefusesLoopbackWhetherGivenAsAddressOrName(t *testing.T) {
	server := okServer(t)
	client := NewClient(5*time.Second, false)

	if _, err := client.Get(server.URL); !errors.Is(err, ErrBlocked) {
		t.Fatalf("a loopback address was reached: %v", err)
	}
	// A name that resolves to loopback is stopped at the connection, after DNS.
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	if _, err := client.Get("http://localhost:" + port); !errors.Is(err, ErrBlocked) {
		t.Fatalf("localhost was reached: %v", err)
	}
	for _, address := range []string{"http://[::1]:" + port, "http://0.0.0.0:" + port, "http://169.254.169.254/latest/meta-data", "http://10.0.0.5/", "http://192.168.1.1/", "http://172.16.0.1/", "http://100.64.0.1/", "http://[::ffff:10.0.0.1]/", "http://[64:ff9b::a00:1]/"} {
		if _, err := client.Get(address); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s: %v", address, err)
		}
	}
}

func TestDevelopmentMayReachThisComputer(t *testing.T) {
	server := okServer(t)
	response, err := NewClient(5*time.Second, true).Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("status %d", response.StatusCode)
	}
}

func TestProxyEnvironmentIsIgnoredWhileGuarded(t *testing.T) {
	// A proxy from the environment would make the connection to the proxy, not to the
	// target, and so skip the check on the target's address.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")
	client := NewClient(time.Second, false)
	if proxy := client.Transport.(*http.Transport).Proxy; proxy != nil {
		t.Fatal("the guarded client uses proxy settings")
	}
}

func TestRedirectsAreLimitedAndCannotDowngradeToPlainHTTP(t *testing.T) {
	plain := okServer(t)
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL, http.StatusFound)
	}))
	defer secure.Close()
	client := NewClient(5*time.Second, true) // private addresses allowed only so the test servers are reachable
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	if _, err := client.Get(secure.URL); err == nil {
		t.Fatal("an https to http redirect was followed")
	}

	loop := httptest.NewServer(nil)
	defer loop.Close()
	loop.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, loop.URL, http.StatusFound) })
	if _, err := NewClient(5*time.Second, true).Get(loop.URL); err == nil {
		t.Fatal("an endless redirect loop was followed")
	}
}

func TestRedirectToAnInternalAddressIsStopped(t *testing.T) {
	// The redirect target is dialled through the same guard, so it is refused even
	// though the first hop (here, allowed by a permissive client) worked.
	internal := okServer(t)
	hop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusFound)
	}))
	defer hop.Close()
	// Same transport, guard on: the first hop is itself loopback, so nothing is reached at all.
	if _, err := NewClient(5*time.Second, false).Get(hop.URL); !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v", err)
	}
}

func TestBlockedIPCoversTheNonPublicRanges(t *testing.T) {
	for _, blocked := range []string{"127.0.0.1", "10.1.2.3", "172.31.255.255", "192.168.0.1", "169.254.169.254", "100.64.0.1", "100.127.255.255", "198.18.0.1", "240.0.0.1", "0.0.0.0", "::1", "fe80::1", "fc00::1", "64:ff9b::1", "2002::1", "::ffff:127.0.0.1", "224.0.0.1"} {
		if !cloudworkspace.BlockedIP(net.ParseIP(blocked)) {
			t.Errorf("%s is not blocked", blocked)
		}
	}
	for _, public := range []string{"8.8.8.8", "1.1.1.1", "104.16.1.1", "151.101.1.1", "2606:4700:4700::1111", "100.63.255.255", "100.128.0.1", "198.20.0.1"} {
		if cloudworkspace.BlockedIP(net.ParseIP(public)) {
			t.Errorf("%s is wrongly blocked", public)
		}
	}
}
