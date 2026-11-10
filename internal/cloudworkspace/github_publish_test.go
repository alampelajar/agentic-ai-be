package cloudworkspace

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type recordedCall struct {
	Method, Path string
	Body         map[string]any
}

// fakePublishGitHub plays the GitHub git data API. override lets a test change one answer.
func fakePublishGitHub(t *testing.T, headSHA string, override func(method, path string) (int, bool)) (*httptest.Server, *[]recordedCall) {
	t.Helper()
	var mu sync.Mutex
	calls := &[]recordedCall{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		*calls = append(*calls, recordedCall{r.Method, r.URL.Path, body})
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if override != nil {
			if status, handled := override(r.Method, r.URL.Path); handled {
				w.WriteHeader(status)
				return
			}
		}
		reply := func(status int, v any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(v)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/git/ref/heads/main":
			reply(200, map[string]any{"object": map[string]string{"sha": headSHA}})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/git/commits/base111":
			reply(200, map[string]any{"tree": map[string]string{"sha": "tree000"}})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/git/blobs":
			reply(201, map[string]string{"sha": "blob-" + body["content"].(string)[:4]})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/git/trees":
			reply(201, map[string]string{"sha": "tree999"})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/git/commits":
			reply(201, map[string]string{"sha": "commit777", "html_url": "https://github.com/o/r/commit/commit777"})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/git/refs":
			reply(201, map[string]string{"ref": "refs/heads/x"})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/pulls":
			reply(201, map[string]string{"html_url": "https://github.com/o/r/pull/5"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, calls
}

func publishRequest() PublishRequest {
	return PublishRequest{
		Owner: "o", Repo: "r", BaseBranch: "main", BaseSHA: "base111",
		Branch: "agentic/fix-readme", Message: "Update README",
		Files: []PublishFile{
			{Path: "README.md", Content: []byte("# Hello")},
			{Path: "old.txt", Deleted: true},
		},
		CreatePR: true, PRTitle: "Update README", PRBody: "Made in Agentic AI",
	}
}

func find(calls []recordedCall, method, path string) *recordedCall {
	for i := range calls {
		if calls[i].Method == method && calls[i].Path == path {
			return &calls[i]
		}
	}
	return nil
}

func TestPublishMakesOneCommitOnANewBranchAndOpensAPullRequest(t *testing.T) {
	server, calls := fakePublishGitHub(t, "base111", nil)
	d := &GitHubDownloader{APIURL: server.URL, Client: &http.Client{}}

	result, err := d.Publish(context.Background(), "tok", publishRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.CommitSHA != "commit777" || result.PullRequestURL != "https://github.com/o/r/pull/5" || result.Branch != "agentic/fix-readme" || result.BaseMoved {
		t.Errorf("result = %+v", result)
	}

	blob := find(*calls, http.MethodPost, "/repos/o/r/git/blobs")
	if blob == nil || blob.Body["encoding"] != "base64" || blob.Body["content"] != base64.StdEncoding.EncodeToString([]byte("# Hello")) {
		t.Fatalf("blob call = %+v", blob)
	}
	tree := find(*calls, http.MethodPost, "/repos/o/r/git/trees")
	if tree == nil || tree.Body["base_tree"] != "tree000" {
		t.Fatalf("tree call = %+v", tree)
	}
	entries := tree.Body["tree"].([]any)
	deleted := entries[1].(map[string]any)
	if deleted["path"] != "old.txt" || deleted["sha"] != nil {
		t.Errorf("a deleted file must be a tree entry with sha null, got %+v", deleted)
	}
	commit := find(*calls, http.MethodPost, "/repos/o/r/git/commits")
	if commit == nil || commit.Body["message"] != "Update README" || commit.Body["parents"].([]any)[0] != "base111" {
		t.Errorf("the commit must sit on the imported commit: %+v", commit)
	}
	ref := find(*calls, http.MethodPost, "/repos/o/r/git/refs")
	if ref == nil || ref.Body["ref"] != "refs/heads/agentic/fix-readme" || ref.Body["sha"] != "commit777" {
		t.Errorf("ref call = %+v", ref)
	}
	pull := find(*calls, http.MethodPost, "/repos/o/r/pulls")
	if pull == nil || pull.Body["head"] != "agentic/fix-readme" || pull.Body["base"] != "main" {
		t.Errorf("pull call = %+v", pull)
	}
	for _, call := range *calls {
		if call.Method == http.MethodPatch || call.Method == http.MethodPut || call.Method == http.MethodDelete {
			t.Errorf("nothing may be updated or deleted on GitHub, saw %s %s", call.Method, call.Path)
		}
	}
}

func TestPublishReportsThatTheBaseBranchMoved(t *testing.T) {
	server, _ := fakePublishGitHub(t, "newer999", nil)
	result, err := (&GitHubDownloader{APIURL: server.URL, Client: &http.Client{}}).Publish(context.Background(), "tok", publishRequest())
	if err != nil || !result.BaseMoved {
		t.Fatalf("result = %+v, %v", result, err)
	}
}

func TestPublishRefusesAnExistingBranch(t *testing.T) {
	server, _ := fakePublishGitHub(t, "base111", func(method, path string) (int, bool) {
		return http.StatusUnprocessableEntity, method == http.MethodPost && path == "/repos/o/r/git/refs"
	})
	_, err := (&GitHubDownloader{APIURL: server.URL, Client: &http.Client{}}).Publish(context.Background(), "tok", publishRequest())
	if !errors.Is(err, ErrBranchExists) {
		t.Fatalf("got %v", err)
	}
}

func TestPublishWithoutWriteAccess(t *testing.T) {
	server, _ := fakePublishGitHub(t, "base111", func(method, path string) (int, bool) {
		return http.StatusNotFound, method == http.MethodPost && path == "/repos/o/r/git/blobs"
	})
	_, err := (&GitHubDownloader{APIURL: server.URL, Client: &http.Client{}}).Publish(context.Background(), "tok", publishRequest())
	if !errors.Is(err, ErrNoPushAccess) {
		t.Fatalf("got %v", err)
	}
}

func TestPublishWithARevokedToken(t *testing.T) {
	server, _ := fakePublishGitHub(t, "base111", nil)
	_, err := (&GitHubDownloader{APIURL: server.URL, Client: &http.Client{}}).Publish(context.Background(), "revoked", publishRequest())
	if !errors.Is(err, ErrGitHubToken) {
		t.Fatalf("got %v", err)
	}
}

func TestPublishWhenTheBaseBranchIsGone(t *testing.T) {
	server, _ := fakePublishGitHub(t, "base111", func(method, path string) (int, bool) {
		return http.StatusNotFound, method == http.MethodGet && path == "/repos/o/r/git/ref/heads/main"
	})
	_, err := (&GitHubDownloader{APIURL: server.URL, Client: &http.Client{}}).Publish(context.Background(), "tok", publishRequest())
	if !errors.Is(err, ErrBaseGone) {
		t.Fatalf("got %v", err)
	}
}

func TestPublishKeepsTheBranchWhenThePullRequestFails(t *testing.T) {
	server, _ := fakePublishGitHub(t, "base111", func(method, path string) (int, bool) {
		return http.StatusUnprocessableEntity, method == http.MethodPost && path == "/repos/o/r/pulls"
	})
	result, err := (&GitHubDownloader{APIURL: server.URL, Client: &http.Client{}}).Publish(context.Background(), "tok", publishRequest())
	if err != nil || result.CommitSHA == "" || result.PullRequestURL != "" || !strings.Contains(result.PullRequestError, "branch was pushed") {
		t.Fatalf("result = %+v, %v", result, err)
	}
}

func TestPublishWithoutAPullRequest(t *testing.T) {
	server, calls := fakePublishGitHub(t, "base111", nil)
	req := publishRequest()
	req.CreatePR = false
	result, err := (&GitHubDownloader{APIURL: server.URL, Client: &http.Client{}}).Publish(context.Background(), "tok", req)
	if err != nil || result.PullRequestURL != "" || find(*calls, http.MethodPost, "/repos/o/r/pulls") != nil {
		t.Fatalf("no pull request was asked for: %+v, %v", result, err)
	}
}
