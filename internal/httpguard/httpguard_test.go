package httpguard

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

func serve(router *gin.Engine, method, path string, headers map[string]string, body io.Reader) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, body)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestParseOriginsKeepsOnlyBareHTTPOrigins(t *testing.T) {
	got := ParseOrigins(" https://App.Example.com/ , http://localhost:5173,*, https://x.example.com/path, ftp://x.example.com, https://u:p@x.example.com, https://x.example.com?a=1, javascript:alert(1), , null")
	want := []string{"https://app.example.com", "http://localhost:5173"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("origins = %v, want %v", got, want)
	}
	if len(ParseOrigins("*")) != 0 {
		t.Fatal("a wildcard must never be accepted: credentials are sent with every request")
	}
}

func TestCORSAllowsOnlyListedOrigins(t *testing.T) {
	router := gin.New()
	router.Use(CORS([]string{"https://app.example.com"}))
	router.GET("/data", func(c *gin.Context) { c.String(200, "secret") })

	allowed := serve(router, "GET", "/data", map[string]string{"Origin": "https://app.example.com"}, nil)
	if allowed.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" || allowed.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("allowed origin headers = %v", allowed.Header())
	}
	for _, origin := range []string{"https://evil.example.com", "https://app.example.com.evil.com", "http://app.example.com", "https://APP.example.com:8443", "null", ""} {
		denied := serve(router, "GET", "/data", map[string]string{"Origin": origin}, nil)
		if denied.Header().Get("Access-Control-Allow-Origin") != "" || denied.Header().Get("Access-Control-Allow-Credentials") != "" {
			t.Errorf("origin %q got CORS headers: %v", origin, denied.Header())
		}
	}
	if !strings.Contains(allowed.Header().Get("Vary"), "Origin") {
		t.Error("responses must vary by Origin so a shared cache cannot mix them up")
	}
}

func TestCORSPreflight(t *testing.T) {
	router := gin.New()
	router.Use(CORS([]string{"https://app.example.com"}))
	router.POST("/data", func(c *gin.Context) { c.String(200, "ran") })

	ok := serve(router, "OPTIONS", "/data", map[string]string{"Origin": "https://app.example.com", "Access-Control-Request-Method": "POST"}, nil)
	if ok.Code != http.StatusNoContent || !strings.Contains(ok.Header().Get("Access-Control-Allow-Headers"), "Authorization") || ok.Header().Get("Access-Control-Max-Age") == "" {
		t.Fatalf("preflight = %d %v", ok.Code, ok.Header())
	}
	bad := serve(router, "OPTIONS", "/data", map[string]string{"Origin": "https://evil.example.com", "Access-Control-Request-Method": "POST"}, nil)
	if bad.Code != http.StatusForbidden || bad.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("preflight from a foreign origin = %d %v", bad.Code, bad.Header())
	}
	// A preflight never reaches the handler.
	if strings.Contains(ok.Body.String(), "ran") {
		t.Fatal("the handler ran for a preflight")
	}
}

func TestSecurityHeaders(t *testing.T) {
	build := func(production bool) *gin.Engine {
		router := gin.New()
		router.Use(SecurityHeaders(production))
		router.GET("/", func(c *gin.Context) { c.String(200, "x") })
		return router
	}
	response := serve(build(false), "GET", "/", nil, nil)
	for name, want := range map[string]string{
		"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer", "Cache-Control": "no-store",
	} {
		if response.Header().Get(name) != want {
			t.Errorf("%s = %q, want %q", name, response.Header().Get(name), want)
		}
	}
	if !strings.Contains(response.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Error("no CSP")
	}
	if response.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS outside production")
	}
	if serve(build(true), "GET", "/", nil, nil).Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS on a plain HTTP request")
	}
	if serve(build(true), "GET", "/", map[string]string{"X-Forwarded-Proto": "https"}, nil).Header().Get("Strict-Transport-Security") == "" {
		t.Error("no HSTS in production over HTTPS")
	}
}

func TestLimiterAllowsABurstThenRefills(t *testing.T) {
	limiter := NewLimiter(60, 3) // one per second, three at once
	now := time.Now()
	limiter.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if ok, _ := limiter.Allow("a"); !ok {
			t.Fatalf("request %d of the burst was refused", i+1)
		}
	}
	ok, wait := limiter.Allow("a")
	if ok || wait <= 0 || wait > time.Second {
		t.Fatalf("over the burst: ok=%v wait=%s", ok, wait)
	}
	if other, _ := limiter.Allow("b"); !other {
		t.Fatal("another key was affected")
	}
	now = now.Add(1100 * time.Millisecond)
	if ok, _ := limiter.Allow("a"); !ok {
		t.Fatal("no token after the refill time")
	}
	if ok, _ := limiter.Allow("a"); ok {
		t.Fatal("more tokens than the rate allows")
	}
	now = now.Add(time.Hour)
	for i := 0; i < 3; i++ {
		limiter.Allow("a")
	}
	if ok, _ := limiter.Allow("a"); ok {
		t.Fatal("the bucket grew past its burst while idle")
	}
}

func TestLimiterMemoryIsBounded(t *testing.T) {
	limiter := NewLimiter(60, 1)
	now := time.Now()
	limiter.now = func() time.Time { return now }
	for i := 0; i < maxTrackedKeys+500; i++ {
		limiter.Allow(string(rune('a'+i%26)) + strings.Repeat("x", i%7) + itoa(i))
	}
	if len(limiter.buckets) > maxTrackedKeys+1 {
		t.Fatalf("%d buckets kept", len(limiter.buckets))
	}
	// Once the old ones have idled, the table makes room again.
	now = now.Add(time.Hour)
	limiter.Allow("fresh")
	if len(limiter.buckets) > 10 {
		t.Fatalf("idle buckets were not dropped: %d", len(limiter.buckets))
	}
}

func itoa(i int) string {
	digits := []byte{}
	for ; i > 0; i /= 10 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
	}
	return string(digits) + "n"
}

func TestClientKeyGroupsAnIPv6Prefix(t *testing.T) {
	if ClientKey("203.0.113.7") != "203.0.113.7" {
		t.Error("IPv4 changed")
	}
	a, b := ClientKey("2001:db8:1:2::1"), ClientKey("2001:db8:1:2:ffff:ffff:ffff:ffff")
	if a != b || !strings.HasSuffix(a, "/64") {
		t.Errorf("one /64 gave %q and %q", a, b)
	}
	if ClientKey("2001:db8:1:3::1") == a {
		t.Error("different /64 networks were merged")
	}
	if ClientKey("not an ip") != "not an ip" {
		t.Error("an unparsable value must stay as it is")
	}
}

func TestRateLimitMiddlewareAnswers429WithRetryAfter(t *testing.T) {
	router := gin.New()
	router.Use(RateLimit(NewLimiter(60, 2), ByIP))
	router.GET("/", func(c *gin.Context) { c.String(200, "ok") })
	for i := 0; i < 2; i++ {
		if got := serve(router, "GET", "/", nil, nil); got.Code != 200 {
			t.Fatalf("request %d = %d", i+1, got.Code)
		}
	}
	limited := serve(router, "GET", "/", nil, nil)
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") == "" || !strings.Contains(limited.Body.String(), `"ok":false`) {
		t.Fatalf("limited = %d %v %s", limited.Code, limited.Header(), limited.Body.String())
	}
}

func TestForwardedForCannotBeUsedToDodgeALimitUnlessTheProxyIsTrusted(t *testing.T) {
	build := func(trusted []string) *gin.Engine {
		router := gin.New()
		if err := router.SetTrustedProxies(trusted); err != nil {
			t.Fatal(err)
		}
		router.Use(RateLimit(NewLimiter(60, 1), ByIP))
		router.GET("/", func(c *gin.Context) { c.String(200, "ok") })
		return router
	}
	// No trusted proxy: the header is ignored, so changing it does not help.
	router := build(nil)
	first := httptest.NewRequest("GET", "/", nil)
	first.RemoteAddr, _ = "198.51.100.1:1000", 0
	first.Header.Set("X-Forwarded-For", "1.1.1.1")
	second := httptest.NewRequest("GET", "/", nil)
	second.RemoteAddr = "198.51.100.1:1001"
	second.Header.Set("X-Forwarded-For", "2.2.2.2")
	one, two := httptest.NewRecorder(), httptest.NewRecorder()
	router.ServeHTTP(one, first)
	router.ServeHTTP(two, second)
	if one.Code != 200 || two.Code != http.StatusTooManyRequests {
		t.Fatalf("spoofed X-Forwarded-For: %d then %d", one.Code, two.Code)
	}
}

func TestLoginGuardLocksAPairAfterFiveFailures(t *testing.T) {
	guard := NewLoginGuard()
	now := time.Now()
	guard.now = func() time.Time { return now }
	for i := 0; i < 4; i++ {
		guard.Fail("a@example.com", "203.0.113.5")
	}
	if locked, _ := guard.Check("a@example.com", "203.0.113.5"); locked {
		t.Fatal("locked after four failures")
	}
	guard.Fail("a@example.com", "203.0.113.5")
	locked, wait := guard.Check("a@example.com", "203.0.113.5")
	if !locked || wait <= 0 || wait > 15*time.Minute {
		t.Fatalf("after five failures: locked=%v wait=%s", locked, wait)
	}
	// Someone else, or the same person from another address, is not locked out.
	if l, _ := guard.Check("a@example.com", "203.0.113.99"); l {
		t.Error("another address was locked")
	}
	if l, _ := guard.Check("b@example.com", "203.0.113.5"); l {
		t.Error("another account was locked")
	}
	// The email is compared without regard to case.
	if l, _ := guard.Check("A@Example.com", "203.0.113.5"); !l {
		t.Error("a differently cased email avoided the lock")
	}
	now = now.Add(16 * time.Minute)
	if l, _ := guard.Check("a@example.com", "203.0.113.5"); l {
		t.Error("still locked after the lock time")
	}
}

func TestLoginGuardForgetsOldFailuresAndClearsOnSuccess(t *testing.T) {
	guard := NewLoginGuard()
	now := time.Now()
	guard.now = func() time.Time { return now }
	for i := 0; i < 4; i++ {
		guard.Fail("a@example.com", "203.0.113.5")
	}
	now = now.Add(20 * time.Minute) // the window has passed
	guard.Fail("a@example.com", "203.0.113.5")
	if l, _ := guard.Check("a@example.com", "203.0.113.5"); l {
		t.Fatal("old failures still counted")
	}
	for i := 0; i < 3; i++ {
		guard.Fail("a@example.com", "203.0.113.5")
	}
	guard.Success("a@example.com", "203.0.113.5")
	guard.Fail("a@example.com", "203.0.113.5")
	if l, _ := guard.Check("a@example.com", "203.0.113.5"); l {
		t.Fatal("a successful sign-in did not reset the count")
	}
}

func TestLoginGuardStopsGuessingSpreadOverManyAddresses(t *testing.T) {
	guard := NewLoginGuard()
	for i := 0; i < 50; i++ {
		guard.Fail("victim@example.com", "198.51.100."+itoa(i))
	}
	if l, _ := guard.Check("victim@example.com", "192.0.2.200"); !l {
		t.Fatal("50 failures from 50 addresses did not lock the account")
	}
	if l, _ := guard.Check("someone-else@example.com", "192.0.2.200"); l {
		t.Fatal("another account was locked")
	}
}

func TestRequestIDIsKeptWhenSafeAndReplacedWhenNot(t *testing.T) {
	router := gin.New()
	router.Use(RequestID())
	router.GET("/", func(c *gin.Context) { c.String(200, c.GetString("request_id")) })

	if got := serve(router, "GET", "/", map[string]string{"X-Request-ID": "req-12345678"}, nil); got.Header().Get("X-Request-ID") != "req-12345678" || got.Body.String() != "req-12345678" {
		t.Fatalf("a good id was not kept: %v", got.Header())
	}
	for _, bad := range []string{"short", "has space in it 12345", "line\nbreak-12345678", strings.Repeat("a", 65), "<script>12345678"} {
		got := serve(router, "GET", "/", map[string]string{"X-Request-ID": bad}, nil)
		id := got.Header().Get("X-Request-ID")
		if id == bad || len(id) != 24 {
			t.Errorf("%q was accepted as %q", bad, id)
		}
	}
	if a, b := serve(router, "GET", "/", nil, nil).Header().Get("X-Request-ID"), serve(router, "GET", "/", nil, nil).Header().Get("X-Request-ID"); a == b || a == "" {
		t.Error("generated ids repeat")
	}
}

func TestAccessLogRecordsTheRouteNotTheSecrets(t *testing.T) {
	var out bytes.Buffer
	router := gin.New()
	router.Use(RequestID(), AccessLog(slog.New(slog.NewJSONHandler(&out, nil))))
	router.GET("/api/items/:id", func(c *gin.Context) {
		c.Set("user_id", uint(7))
		c.String(200, "response-body-secret")
	})
	serve(router, "GET", "/api/items/12345?token=SUPERSECRET&email=a@b.c", map[string]string{"Authorization": "Bearer HEADERSECRET", "Cookie": "s=COOKIESECRET"}, nil)
	serve(router, "GET", "/nothing/here", nil, nil)
	line := out.String()
	for _, leaked := range []string{"SUPERSECRET", "HEADERSECRET", "COOKIESECRET", "response-body-secret", "a@b.c", "12345?"} {
		if strings.Contains(line, leaked) {
			t.Errorf("the log contains %q:\n%s", leaked, line)
		}
	}
	for _, want := range []string{`"route":"/api/items/:id"`, `"status":200`, `"user_id":7`, `"request_id"`, `"route":"unmatched"`, `"level":"WARN"`} {
		if !strings.Contains(line, want) {
			t.Errorf("the log lacks %s:\n%s", want, line)
		}
	}
}

func TestBodyLimit(t *testing.T) {
	router := gin.New()
	router.Use(BodyLimit(100, 1000))
	handlerRan := 0
	router.POST("/", func(c *gin.Context) {
		handlerRan++
		if _, err := io.ReadAll(c.Request.Body); err != nil {
			c.String(http.StatusRequestEntityTooLarge, "cut off")
			return
		}
		c.String(200, "ok")
	})
	post := func(contentType string, size int, declared bool) *httptest.ResponseRecorder {
		request := httptest.NewRequest("POST", "/", strings.NewReader(strings.Repeat("x", size)))
		request.Header.Set("Content-Type", contentType)
		if !declared {
			request.ContentLength = -1 // unknown length: only the reader can stop it
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}
	if got := post("application/json", 100, true); got.Code != 200 {
		t.Errorf("at the limit = %d", got.Code)
	}
	ranBefore := handlerRan
	if got := post("application/json", 101, true); got.Code != http.StatusRequestEntityTooLarge || handlerRan != ranBefore {
		t.Errorf("declared over the limit = %d (handler ran: %v); it must be refused before the handler", got.Code, handlerRan != ranBefore)
	}
	if got := post("application/json", 500, false); got.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("undeclared over the limit = %d", got.Code)
	}
	if got := post("multipart/form-data; boundary=x", 900, true); got.Code != 200 {
		t.Errorf("an upload under its limit = %d", got.Code)
	}
	if got := post("MULTIPART/form-data; boundary=x", 1001, true); got.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("an upload over its limit = %d", got.Code)
	}
}
