package services

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func testGitHubOAuth(server *httptest.Server) *GitHubOAuth {
	return &GitHubOAuth{
		ClientID: "client-id-123", ClientSecret: "client-secret-xyz", RedirectURL: "http://localhost:8080/cb",
		AuthURL: server.URL + "/login/oauth/authorize", TokenURL: server.URL + "/login/oauth/access_token",
		APIURL: server.URL, HTTP: &http.Client{Timeout: 5 * time.Second},
	}
}

func TestGitHubAuthorizeURLCarriesTheStateAndAsksForRepoScope(t *testing.T) {
	g := &GitHubOAuth{ClientID: "abc", ClientSecret: "s", RedirectURL: "http://localhost:8080/cb", AuthURL: "https://github.com/login/oauth/authorize"}
	parsed, err := url.Parse(g.AuthorizeURL("state-value"))
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	if parsed.Host != "github.com" || q.Get("client_id") != "abc" || q.Get("state") != "state-value" || q.Get("scope") != "repo" || q.Get("redirect_uri") != "http://localhost:8080/cb" {
		t.Errorf("unexpected authorize URL: %s", parsed)
	}
	if strings.Contains(parsed.String(), "client_secret") {
		t.Error("the client secret must never be in the browser address")
	}
}

func TestGitHubConfiguredNeedsBothIDAndSecret(t *testing.T) {
	if (&GitHubOAuth{ClientID: "a"}).Configured() || (&GitHubOAuth{ClientSecret: "b"}).Configured() || (&GitHubOAuth{}).Configured() {
		t.Error("one missing value must leave GitHub unconfigured")
	}
	if !(&GitHubOAuth{ClientID: "a", ClientSecret: "b"}).Configured() {
		t.Error("both values set must be configured")
	}
}

func TestGitHubExchangeAndUser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			_ = r.ParseForm()
			if r.Form.Get("code") != "good-code" || r.Form.Get("client_secret") != "client-secret-xyz" || r.Header.Get("Accept") != "application/json" {
				_, _ = io.WriteString(w, `{"error":"bad_verification_code"}`)
				return
			}
			_, _ = io.WriteString(w, `{"access_token":"gho_tokenvalue","scope":"repo","token_type":"bearer"}`)
		case "/user":
			if r.Header.Get("Authorization") != "Bearer gho_tokenvalue" {
				http.Error(w, "no", http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(w, `{"id":4242,"login":"rina"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	g := testGitHubOAuth(server)

	token, scope, err := g.Exchange(context.Background(), "good-code")
	if err != nil || token != "gho_tokenvalue" || scope != "repo" {
		t.Fatalf("exchange = %q, %q, %v", token, scope, err)
	}
	id, login, err := g.User(context.Background(), token)
	if err != nil || id != 4242 || login != "rina" {
		t.Fatalf("user = %d, %q, %v", id, login, err)
	}
	if _, _, err := g.User(context.Background(), "wrong"); err == nil {
		t.Error("a wrong token must not read an account")
	}
}

func TestGitHubExchangeRefusalNeverEchoesSecretsOrCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"error":"bad_verification_code","error_description":"The code passed is incorrect"}`)
	}))
	defer server.Close()
	_, _, err := testGitHubOAuth(server).Exchange(context.Background(), "secret-one-time-code")
	if err == nil {
		t.Fatal("a refused code must be an error")
	}
	for _, leaked := range []string{"secret-one-time-code", "client-secret-xyz", "incorrect"} {
		if strings.Contains(err.Error(), leaked) {
			t.Errorf("the error leaks %q: %v", leaked, err)
		}
	}
	if !strings.Contains(err.Error(), "bad_verification_code") {
		t.Errorf("the short reason should be kept: %v", err)
	}
}

func TestGitHubRevokeSendsBasicAuthAndAcceptsGone(t *testing.T) {
	status := http.StatusNoContent
	var gotUser, gotPass, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, _ = r.BasicAuth()
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(status)
	}))
	defer server.Close()
	g := testGitHubOAuth(server)

	if err := g.Revoke(context.Background(), "gho_tokenvalue"); err != nil {
		t.Fatal(err)
	}
	if gotUser != "client-id-123" || gotPass != "client-secret-xyz" || !strings.Contains(gotBody, "gho_tokenvalue") {
		t.Errorf("revoke request: user=%q body=%q", gotUser, gotBody)
	}
	status = http.StatusNotFound // already revoked: fine
	if err := g.Revoke(context.Background(), "gho_tokenvalue"); err != nil {
		t.Errorf("an already revoked token should not be an error: %v", err)
	}
	status = http.StatusInternalServerError
	if err := g.Revoke(context.Background(), "gho_tokenvalue"); err == nil {
		t.Error("a server error should be reported")
	}
}
