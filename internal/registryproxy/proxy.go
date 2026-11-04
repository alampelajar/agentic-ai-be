// Package registryproxy is the only way out of the network that dependency
// installation jobs run in. The job container sits on a Docker network with no
// route to the host or the internet; this proxy sits on that network and on a
// network with internet access, and tunnels HTTPS to a short list of package
// registries and nothing else.
//
// It is a CONNECT-only tunnel. It never decrypts anything, never follows
// redirects and never talks plain HTTP. A destination is allowed only when the
// host name is on the list, the port is 443 and every address the name resolves
// to is public: a name that points at a private, loopback or link-local address
// (cloud metadata included) is refused, and the connection goes to the address
// that was checked, so DNS cannot change its answer between the check and the dial.
package registryproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"agentic-ai-backend/internal/cloudworkspace"
)

// DefaultAllowedHosts are the registries the three approved toolchains use.
var DefaultAllowedHosts = []string{
	"registry.npmjs.org",                 // npm packages
	"proxy.golang.org", "sum.golang.org", // Go modules and their checksum database
	"pypi.org", "files.pythonhosted.org", // Python index and wheels
}

// Proxy is an http.Handler for CONNECT requests. The zero value is not usable;
// build one with New. The function fields exist so tests can stand in for DNS and
// the network.
type Proxy struct {
	allowed map[string]bool

	Resolve func(ctx context.Context, host string) ([]net.IP, error)
	Dial    func(ctx context.Context, address string) (net.Conn, error)
	Blocked func(ip net.IP) bool
	Logf    func(format string, args ...any)

	MaxTunnels  int           // at the same time
	IdleTimeout time.Duration // no byte in either direction
	MaxLifetime time.Duration // one tunnel, however busy
	MaxBytes    int64         // per direction, per tunnel

	slots chan struct{}
	once  sync.Once
}

// New builds a proxy that allows exactly the given host names.
func New(hosts []string) *Proxy {
	allowed := map[string]bool{}
	for _, host := range hosts {
		if host = strings.ToLower(strings.TrimSpace(host)); host != "" {
			allowed[host] = true
		}
	}
	return &Proxy{
		allowed: allowed,
		Resolve: func(ctx context.Context, host string) ([]net.IP, error) {
			addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			ips := make([]net.IP, 0, len(addresses))
			for _, address := range addresses {
				ips = append(ips, address.IP)
			}
			return ips, err
		},
		Dial: func(ctx context.Context, address string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", address)
		},
		Blocked:     cloudworkspace.BlockedIP,
		Logf:        func(string, ...any) {},
		MaxTunnels:  32,
		IdleTimeout: 60 * time.Second,
		MaxLifetime: 10 * time.Minute,
		MaxBytes:    512 << 20,
	}
}

// Allowed reports whether a host name is on the list (case-insensitive).
func (p *Proxy) Allowed(host string) bool { return p.allowed[strings.ToLower(host)] }

var errNotAllowed = errors.New("destination is not allowed")

// target checks a CONNECT authority and returns the address to dial.
func (p *Proxy) target(ctx context.Context, authority string) (address, host string, err error) {
	host, port, splitErr := net.SplitHostPort(authority)
	if splitErr != nil || port != "443" {
		return "", "", errNotAllowed
	}
	host = strings.ToLower(host)
	if net.ParseIP(host) != nil || !p.allowed[host] { // an address literal skips the name list
		return "", "", errNotAllowed
	}
	resolveCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ips, resolveErr := p.Resolve(resolveCtx, host)
	if resolveErr != nil || len(ips) == 0 {
		return "", host, fmt.Errorf("cannot resolve %s", host)
	}
	for _, ip := range ips {
		if p.Blocked(ip) {
			return "", host, errNotAllowed // one internal address is enough to refuse the name
		}
	}
	return net.JoinHostPort(ips[0].String(), "443"), host, nil
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.once.Do(func() { p.slots = make(chan struct{}, p.MaxTunnels) })
	if r.Method != http.MethodConnect {
		w.Header().Set("Allow", http.MethodConnect)
		http.Error(w, "only CONNECT to the package registries is supported", http.StatusMethodNotAllowed)
		return
	}
	address, host, err := p.target(r.Context(), r.Host)
	if err != nil {
		p.Logf("refused %q: %v", r.Host, err)
		if errors.Is(err, errNotAllowed) {
			http.Error(w, "destination not allowed", http.StatusForbidden)
		} else {
			http.Error(w, "cannot reach the registry", http.StatusBadGateway)
		}
		return
	}
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}

	upstream, err := p.Dial(r.Context(), address)
	if err != nil {
		p.Logf("dial %s failed: %v", host, err)
		http.Error(w, "cannot reach the registry", http.StatusBadGateway)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		upstream.Close()
		http.Error(w, "unsupported", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		upstream.Close()
		return
	}
	_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	p.Logf("tunnel to %s", host)
	p.tunnel(client, buffered, upstream)
}

// tunnel copies both ways until both sides are done or a limit is hit.
func (p *Proxy) tunnel(client net.Conn, buffered *bufio.ReadWriter, upstream net.Conn) {
	lifetime := time.AfterFunc(p.MaxLifetime, func() { client.Close(); upstream.Close() })
	defer lifetime.Stop()
	defer client.Close()
	defer upstream.Close()

	var wg sync.WaitGroup
	pipe := func(dst, src net.Conn, source io.Reader) {
		defer wg.Done()
		_, _ = io.Copy(idleWriter{dst, p.IdleTimeout}, io.LimitReader(idleReader{src, source, p.IdleTimeout}, p.MaxBytes))
		if closer, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = closer.CloseWrite() // tell the far side this direction is over
		} else {
			dst.Close()
		}
	}
	wg.Add(2)
	go pipe(upstream, client, buffered.Reader) // includes anything the client sent right behind CONNECT
	go pipe(client, upstream, upstream)
	wg.Wait()
}

type idleReader struct {
	conn    net.Conn
	reader  io.Reader
	timeout time.Duration
}

func (r idleReader) Read(buffer []byte) (int, error) {
	_ = r.conn.SetReadDeadline(time.Now().Add(r.timeout))
	return r.reader.Read(buffer)
}

type idleWriter struct {
	conn    net.Conn
	timeout time.Duration
}

func (w idleWriter) Write(buffer []byte) (int, error) {
	_ = w.conn.SetWriteDeadline(time.Now().Add(w.timeout))
	return w.conn.Write(buffer)
}
