package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// GitHubOAuth signs users in to GitHub with an OAuth App so the backend can act on their
// repositories (private import, commits and pull requests). Settings come from the environment:
//
//	GITHUB_CLIENT_ID, GITHUB_CLIENT_SECRET   from the OAuth App on GitHub
//	GITHUB_OAUTH_REDIRECT_URL                the callback registered on GitHub
//	                                         (default http://localhost:8080/api/integrations/github/callback)
//
// The URL fields are fields only so tests can point them at a local server.
type GitHubOAuth struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	AuthURL      string
	TokenURL     string
	APIURL       string
	HTTP         *http.Client
}

// GitHubScope is what the app asks for: "repo" covers private repositories, creating branches,
// commits and pull requests. It is broad, which is why the user sees it on GitHub's consent page.
const GitHubScope = "repo"

func NewGitHubOAuth() *GitHubOAuth {
	redirect := strings.TrimSpace(os.Getenv("GITHUB_OAUTH_REDIRECT_URL"))
	if redirect == "" {
		redirect = "http://localhost:8080/api/integrations/github/callback"
	}
	return &GitHubOAuth{
		ClientID:     strings.TrimSpace(os.Getenv("GITHUB_CLIENT_ID")),
		ClientSecret: strings.TrimSpace(os.Getenv("GITHUB_CLIENT_SECRET")),
		RedirectURL:  redirect,
		AuthURL:      "https://github.com/login/oauth/authorize",
		TokenURL:     "https://github.com/login/oauth/access_token",
		APIURL:       "https://api.github.com",
		HTTP:         &http.Client{Timeout: 15 * time.Second},
	}
}

func (g *GitHubOAuth) Configured() bool {
	return g.ClientID != "" && g.ClientSecret != ""
}

// AuthorizeURL is where the browser is sent to approve the connection.
func (g *GitHubOAuth) AuthorizeURL(state string) string {
	query := url.Values{}
	query.Set("client_id", g.ClientID)
	query.Set("redirect_uri", g.RedirectURL)
	query.Set("scope", GitHubScope)
	query.Set("state", state)
	query.Set("allow_signup", "false")
	return g.AuthURL + "?" + query.Encode()
}

// Exchange trades the one-time code from the callback for an access token.
// Errors never contain the code, the secret or the response body.
func (g *GitHubOAuth) Exchange(ctx context.Context, code string) (token, scope string, err error) {
	form := url.Values{}
	form.Set("client_id", g.ClientID)
	form.Set("client_secret", g.ClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", g.RedirectURL)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, g.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", errors.New("github: could not build the token request")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")

	response, err := g.HTTP.Do(request)
	if err != nil {
		return "", "", errors.New("github: the token request failed")
	}
	defer response.Body.Close()
	var body struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<16)).Decode(&body); err != nil {
		return "", "", errors.New("github: unreadable token response")
	}
	if response.StatusCode != http.StatusOK || body.Error != "" || body.AccessToken == "" {
		return "", "", fmt.Errorf("github: the code was refused (%s)", safeReason(body.Error))
	}
	return body.AccessToken, body.Scope, nil
}

// safeReason keeps only a short machine word from GitHub's error field.
func safeReason(reason string) string {
	if reason == "" || len(reason) > 40 || strings.ContainsAny(reason, " \r\n") {
		return "unknown"
	}
	return reason
}

// User reads the account the token belongs to.
func (g *GitHubOAuth) User(ctx context.Context, token string) (id int64, login string, err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, g.APIURL+"/user", nil)
	if err != nil {
		return 0, "", errors.New("github: could not build the request")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")

	response, err := g.HTTP.Do(request)
	if err != nil {
		return 0, "", errors.New("github: could not read the account")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("github: reading the account answered HTTP %d", response.StatusCode)
	}
	var body struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<16)).Decode(&body); err != nil || body.ID == 0 || body.Login == "" {
		return 0, "", errors.New("github: unreadable account response")
	}
	return body.ID, body.Login, nil
}

// Revoke asks GitHub to invalidate the token. It is best effort: the connection is removed
// from our side either way.
func (g *GitHubOAuth) Revoke(ctx context.Context, token string) error {
	payload, _ := json.Marshal(map[string]string{"access_token": token})
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, fmt.Sprintf("%s/applications/%s/token", g.APIURL, url.PathEscape(g.ClientID)), strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	request.SetBasicAuth(g.ClientID, g.ClientSecret)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Content-Type", "application/json")
	response, err := g.HTTP.Do(request)
	if err != nil {
		return errors.New("github: the revoke request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusNotFound {
		return fmt.Errorf("github: revoking answered HTTP %d", response.StatusCode)
	}
	return nil
}
