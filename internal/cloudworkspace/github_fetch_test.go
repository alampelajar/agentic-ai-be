package cloudworkspace

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeGitHub answers the API calls Fetch makes. repos maps "owner/repo" to how it behaves.
func fakeGitHub(t *testing.T, archive []byte, seenAuth *[]string) *httptest.Server {
	t.Helper()
	json := func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seenAuth != nil {
			*seenAuth = append(*seenAuth, r.URL.Path+" auth="+r.Header.Get("Authorization"))
		}
		authorized := r.Header.Get("Authorization") == "Bearer good-token"
		switch {
		case r.URL.Path == "/repos/owner/secret", r.URL.Path == "/repos/owner/secret/commits/main", r.URL.Path == "/repos/owner/secret/zipball/abc123":
			if r.Header.Get("Authorization") != "" && !authorized {
				http.Error(w, "bad credentials", http.StatusUnauthorized)
				return
			}
			if !authorized {
				http.NotFound(w, r) // a private repository looks missing without access
				return
			}
			switch r.URL.Path {
			case "/repos/owner/secret":
				json(w, `{"default_branch":"main","private":true}`)
			case "/repos/owner/secret/commits/main":
				json(w, `{"sha":"abc123"}`)
			default:
				_, _ = w.Write(archive)
			}
		case r.URL.Path == "/repos/owner/open":
			json(w, `{"default_branch":"trunk","private":false}`)
		case r.URL.Path == "/repos/owner/open/commits/trunk":
			json(w, `{"sha":"def456"}`)
		case r.URL.Path == "/owner/open/zip/HEAD":
			_, _ = w.Write(archive)
		case r.URL.Path == "/repos/owner/empty":
			json(w, `{"default_branch":"main","private":false}`)
		case r.URL.Path == "/repos/owner/empty/commits/main":
			w.WriteHeader(http.StatusConflict)
		default:
			http.NotFound(w, r)
		}
	}))
}

func testFetcher(server *httptest.Server) *GitHubDownloader {
	return &GitHubDownloader{BaseURL: server.URL, APIURL: server.URL, Client: &http.Client{}}
}

func TestFetchPrivateRepositoryWithTheTokenRecordsWhereItStood(t *testing.T) {
	archive := makeZip(t, map[string]string{"secret-abc123/a.txt": "private"})
	var seen []string
	server := fakeGitHub(t, archive, &seen)
	defer server.Close()

	data, info, err := testFetcher(server).Fetch(context.Background(), "owner", "secret", "good-token", DefaultLimits())
	if err != nil || !bytes.Equal(data, archive) {
		t.Fatalf("fetch = %d bytes, %v", len(data), err)
	}
	if info.DefaultBranch != "main" || info.HeadSHA != "abc123" || !info.Private || info.Owner != "owner" || info.Repo != "secret" {
		t.Errorf("info = %+v", info)
	}
	// The download is of the commit that was recorded, not "whatever HEAD is by then".
	if !strings.Contains(strings.Join(seen, "\n"), "/repos/owner/secret/zipball/abc123 auth=Bearer good-token") {
		t.Errorf("the zipball of the recorded commit was not requested with the token: %v", seen)
	}
}

func TestFetchWithoutATokenCannotSeeAPrivateRepository(t *testing.T) {
	server := fakeGitHub(t, nil, nil)
	defer server.Close()
	_, _, err := testFetcher(server).Fetch(context.Background(), "owner", "secret", "", DefaultLimits())
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got %v", err)
	}
}

func TestFetchSaysWhenTheConnectionNoLongerWorks(t *testing.T) {
	server := fakeGitHub(t, nil, nil)
	defer server.Close()
	_, _, err := testFetcher(server).Fetch(context.Background(), "owner", "secret", "revoked-token", DefaultLimits())
	if !errors.Is(err, ErrGitHubToken) {
		t.Fatalf("a refused token should be ErrGitHubToken, got %v", err)
	}
}

func TestFetchWithATokenExplainsWhenTheAccountCannotSeeTheRepository(t *testing.T) {
	server := fakeGitHub(t, nil, nil)
	defer server.Close()
	_, _, err := testFetcher(server).Fetch(context.Background(), "owner", "nope", "good-token", DefaultLimits())
	if err == nil || !strings.Contains(err.Error(), "cannot see it") {
		t.Fatalf("got %v", err)
	}
}

func TestFetchPublicRepositoryStillWorksAndReadsTheCommitBestEffort(t *testing.T) {
	archive := makeZip(t, map[string]string{"open-def456/a.txt": "public"})
	server := fakeGitHub(t, archive, nil)
	defer server.Close()

	data, info, err := testFetcher(server).Fetch(context.Background(), "owner", "open", "", DefaultLimits())
	if err != nil || !bytes.Equal(data, archive) {
		t.Fatalf("fetch = %d bytes, %v", len(data), err)
	}
	if info.DefaultBranch != "trunk" || info.HeadSHA != "def456" || info.Private {
		t.Errorf("info = %+v", info)
	}

	// When the API cannot be read, a public repository still imports, just without the commit.
	noAPI := &GitHubDownloader{BaseURL: server.URL, APIURL: "http://127.0.0.1:1", Client: &http.Client{}}
	data, info, err = noAPI.Fetch(context.Background(), "owner", "open", "", DefaultLimits())
	if err != nil || !bytes.Equal(data, archive) || info.HeadSHA != "" {
		t.Fatalf("a failing lookup must not block a public import: %d bytes, %+v, %v", len(data), info, err)
	}
}

func TestFetchEmptyRepositoryWithATokenSaysSo(t *testing.T) {
	server := fakeGitHub(t, nil, nil)
	defer server.Close()
	_, _, err := testFetcher(server).Fetch(context.Background(), "owner", "empty", "good-token", DefaultLimits())
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("got %v", err)
	}
}

func TestTheTokenIsNotSentToTheArchiveHost(t *testing.T) {
	// The real client only follows redirects to codeload.github.com, and Go drops the Authorization
	// header when a redirect changes host, so the token never reaches the archive server.
	downloader := NewGitHubDownloader()
	if downloader.Client.CheckRedirect == nil {
		t.Fatal("redirects are not restricted")
	}
	req, _ := http.NewRequest(http.MethodGet, "https://evil.example.com/x", nil)
	if err := downloader.Client.CheckRedirect(req, nil); err == nil {
		t.Error("a redirect to another host must be refused")
	}
	req, _ = http.NewRequest(http.MethodGet, "https://codeload.github.com/o/r/legacy.zip/abc", nil)
	if err := downloader.Client.CheckRedirect(req, nil); err != nil {
		t.Errorf("the archive host must be allowed: %v", err)
	}
}
