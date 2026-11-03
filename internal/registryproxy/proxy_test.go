package registryproxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// echoServer accepts connections and writes back what it reads, upper-cased.
func echoServer(t *testing.T) (address string, accepted *atomic.Int32) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	accepted = new(atomic.Int32)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			go func() {
				defer conn.Close()
				data, _ := io.ReadAll(conn)
				_, _ = conn.Write([]byte(strings.ToUpper(string(data))))
			}()
		}
	}()
	return listener.Addr().String(), accepted
}

// testProxy answers every allowed name with the given addresses and dials the
// local echo server instead of port 443.
func testProxy(t *testing.T, dialTo string, answers map[string][]string) *Proxy {
	t.Helper()
	p := New(DefaultAllowedHosts)
	p.Resolve = func(_ context.Context, host string) ([]net.IP, error) {
		var ips []net.IP
		for _, text := range answers[host] {
			ips = append(ips, net.ParseIP(text))
		}
		return ips, nil
	}
	p.Dial = func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", dialTo)
	}
	return p
}

var publicAnswers = map[string][]string{
	"registry.npmjs.org": {"104.16.1.1"}, "proxy.golang.org": {"142.250.1.1"}, "pypi.org": {"151.101.1.1"},
	"files.pythonhosted.org": {"151.101.2.1"}, "sum.golang.org": {"142.250.1.2"},
}

// connect sends a CONNECT request and returns the status line and the connection.
func connect(t *testing.T, server *httptest.Server, authority string) (string, net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	_, _ = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", authority, authority)
	reader := bufio.NewReader(conn)
	status, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("no answer for %s: %v", authority, err)
	}
	return strings.TrimSpace(status), conn, reader
}

func TestAllowedRegistryIsTunnelled(t *testing.T) {
	echo, accepted := echoServer(t)
	server := httptest.NewServer(testProxy(t, echo, publicAnswers))
	defer server.Close()

	for _, authority := range []string{"registry.npmjs.org:443", "REGISTRY.NPMJS.ORG:443", "proxy.golang.org:443", "pypi.org:443", "files.pythonhosted.org:443", "sum.golang.org:443"} {
		status, conn, reader := connect(t, server, authority)
		if !strings.Contains(status, "200") {
			t.Fatalf("%s: %s", authority, status)
		}
		_, _ = reader.ReadString('\n') // blank line that ends the response head
		_, _ = conn.Write([]byte("hello registry"))
		_ = conn.(*net.TCPConn).CloseWrite()
		reply, _ := io.ReadAll(reader)
		if string(reply) != "HELLO REGISTRY" {
			t.Fatalf("%s: tunnel carried %q", authority, reply)
		}
	}
	if accepted.Load() != 6 {
		t.Fatalf("upstream saw %d connections", accepted.Load())
	}
}

func TestEverythingElseIsRefusedBeforeAnythingIsDialled(t *testing.T) {
	echo, accepted := echoServer(t)
	answers := map[string][]string{
		"registry.npmjs.org":     {"104.16.1.1"},
		"pypi.org":               {"169.254.169.254"},          // metadata address
		"proxy.golang.org":       {"10.0.0.5"},                 // private
		"sum.golang.org":         {"142.250.1.2", "127.0.0.1"}, // one good answer does not save a bad one
		"files.pythonhosted.org": {"::1"},
	}
	server := httptest.NewServer(testProxy(t, echo, answers))
	defer server.Close()

	for name, authority := range map[string]string{
		"unknown host":         "evil.example.com:443",
		"look-alike suffix":    "registry.npmjs.org.evil.example:443",
		"look-alike prefix":    "evilregistry.npmjs.org:443",
		"subdomain":            "x.registry.npmjs.org:443",
		"github":               "github.com:443",
		"plain http port":      "registry.npmjs.org:80",
		"ssh port":             "registry.npmjs.org:22",
		"no port":              "registry.npmjs.org",
		"database port":        "registry.npmjs.org:5432",
		"ip literal":           "104.16.1.1:443",
		"loopback literal":     "127.0.0.1:443",
		"metadata literal":     "169.254.169.254:443",
		"ipv6 literal":         "[::1]:443",
		"userinfo":             "registry.npmjs.org@evil.example:443",
		"trailing dot":         "registry.npmjs.org.:443",
		"allowed name -> meta": "pypi.org:443",
		"allowed name -> priv": "proxy.golang.org:443",
		"mixed answers":        "sum.golang.org:443",
		"allowed name -> ::1":  "files.pythonhosted.org:443",
	} {
		status, _, _ := connect(t, server, authority)
		// The HTTP server itself already turns away a malformed authority with 400.
		if !strings.Contains(status, "403") && !(name == "userinfo" && strings.Contains(status, "400")) {
			t.Errorf("%s (%s): %s", name, authority, status)
		}
	}
	if accepted.Load() != 0 {
		t.Fatalf("a refused request still reached the network (%d connections)", accepted.Load())
	}
}

func TestOnlyConnectIsAccepted(t *testing.T) {
	echo, accepted := echoServer(t)
	server := httptest.NewServer(testProxy(t, echo, publicAnswers))
	defer server.Close()
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodHead} {
		response, err := http.DefaultClient.Do(mustRequest(t, method, server.URL, "registry.npmjs.org"))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s: status %d", method, response.StatusCode)
		}
	}
	if accepted.Load() != 0 {
		t.Fatal("a plain request reached the network")
	}
}

func mustRequest(t *testing.T, method, url, host string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = host
	return request
}

func TestTunnelLimit(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() { // holds every connection open
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
		}
	}()
	p := testProxy(t, listener.Addr().String(), publicAnswers)
	p.MaxTunnels = 2
	server := httptest.NewServer(p)
	defer server.Close()

	for i := 0; i < 2; i++ {
		if status, _, _ := connect(t, server, "pypi.org:443"); !strings.Contains(status, "200") {
			t.Fatalf("tunnel %d: %s", i, status)
		}
	}
	if status, _, _ := connect(t, server, "pypi.org:443"); !strings.Contains(status, "503") {
		t.Fatalf("third tunnel: %s", status)
	}
}

func TestIdleTunnelIsClosed(t *testing.T) {
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
		}
	}()
	p := testProxy(t, listener.Addr().String(), publicAnswers)
	p.IdleTimeout = 300 * time.Millisecond
	server := httptest.NewServer(p)
	defer server.Close()

	_, conn, reader := connect(t, server, "pypi.org:443")
	_, _ = reader.ReadString('\n')
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	started := time.Now()
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("expected the idle tunnel to be closed")
	}
	if time.Since(started) > 3*time.Second {
		t.Fatalf("closed after %s", time.Since(started))
	}
}

func TestTunnelStopsAtTheByteLimit(t *testing.T) {
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = conn.Write([]byte(strings.Repeat("x", 100000)))
			}()
		}
	}()
	p := testProxy(t, listener.Addr().String(), publicAnswers)
	p.MaxBytes = 5000
	server := httptest.NewServer(p)
	defer server.Close()

	_, conn, reader := connect(t, server, "pypi.org:443")
	_, _ = reader.ReadString('\n')
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	received, _ := io.ReadAll(reader)
	if len(received) != 5000 {
		t.Fatalf("received %d bytes through a 5000 byte limit", len(received))
	}
}

func TestDefaultListIsExactlyTheRegistries(t *testing.T) {
	p := New(DefaultAllowedHosts)
	for _, host := range DefaultAllowedHosts {
		if !p.Allowed(host) {
			t.Errorf("%s is not allowed", host)
		}
	}
	for _, host := range []string{"github.com", "codeload.github.com", "raw.githubusercontent.com", "npmjs.org", "example.com", ""} {
		if p.Allowed(host) {
			t.Errorf("%s must not be allowed", host)
		}
	}
	if len(DefaultAllowedHosts) != 5 {
		t.Errorf("the list changed: %v", DefaultAllowedHosts)
	}
}
