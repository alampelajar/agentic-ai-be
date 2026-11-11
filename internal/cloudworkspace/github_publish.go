package cloudworkspace

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// PublishFile is one changed file. Deleted files carry no content.
type PublishFile struct {
	Path    string
	Content []byte
	Deleted bool
}

// PublishRequest sends a workspace's changes to GitHub as one commit on a NEW branch made from the
// commit the workspace was imported at, and optionally opens a pull request into the branch it came
// from. Nothing is ever written to an existing branch: GitHub refuses to create a branch that exists.
type PublishRequest struct {
	Owner, Repo         string
	BaseBranch, BaseSHA string
	Branch, Message     string
	Files               []PublishFile
	CreatePR            bool
	PRTitle, PRBody     string
}

type PublishResult struct {
	Branch         string `json:"branch"`
	CommitSHA      string `json:"commit_sha"`
	CommitURL      string `json:"commit_url"`
	PullRequestURL string `json:"pull_request_url"`
	// PullRequestError is set when the branch was pushed but the pull request could not be opened.
	PullRequestError string `json:"pull_request_error,omitempty"`
	// BaseMoved means the base branch has new commits since the import; the pull request shows
	// any conflict instead of anything being overwritten.
	BaseMoved bool `json:"base_moved"`
}

var (
	ErrBranchExists = errors.New("a branch with this name already exists on GitHub: choose another name")
	ErrNoPushAccess = errors.New("your GitHub account cannot push to this repository (ask for write access; forks are not supported yet)")
	ErrBaseGone     = errors.New("the branch this workspace was imported from no longer exists on GitHub")
)

// apiSend makes a JSON API call and decodes the answer into out (when given).
func (d *GitHubDownloader) apiSend(ctx context.Context, method, path, token string, body, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, d.APIURL+path, reader)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := d.Client.Do(request)
	if err != nil {
		return 0, errors.New("GitHub could not be reached")
	}
	defer response.Body.Close()
	if out != nil && response.StatusCode >= 200 && response.StatusCode < 300 {
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(out); err != nil {
			return response.StatusCode, errors.New("GitHub gave an unreadable answer")
		}
		return response.StatusCode, nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<16))
	return response.StatusCode, nil
}

// writeFailure maps a refused write. 404 is what GitHub answers when the account cannot see or write.
func writeFailure(step string, status int) error {
	switch status {
	case http.StatusUnauthorized:
		return ErrGitHubToken
	case http.StatusForbidden, http.StatusNotFound:
		return ErrNoPushAccess
	default:
		return fmt.Errorf("GitHub refused %s (HTTP %d)", step, status)
	}
}

func (d *GitHubDownloader) Publish(ctx context.Context, token string, req PublishRequest) (PublishResult, error) {
	result := PublishResult{Branch: req.Branch}
	if d.APIURL == "" || token == "" {
		return result, errors.New("GitHub access is not available")
	}
	repo := fmt.Sprintf("/repos/%s/%s", url.PathEscape(req.Owner), url.PathEscape(req.Repo))

	// Where the base branch is now. It only matters for the report: the new branch starts from the
	// imported commit either way, so nothing on GitHub is overwritten.
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	status, err := d.apiSend(ctx, http.MethodGet, repo+"/git/ref/heads/"+url.PathEscape(req.BaseBranch), token, nil, &ref)
	switch {
	case err != nil:
		return result, err
	case status == http.StatusUnauthorized:
		return result, ErrGitHubToken
	case status == http.StatusNotFound:
		return result, ErrBaseGone
	case status != http.StatusOK:
		return result, fmt.Errorf("GitHub answered with HTTP %d", status)
	}
	result.BaseMoved = ref.Object.SHA != req.BaseSHA

	var base struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	status, err = d.apiSend(ctx, http.MethodGet, repo+"/git/commits/"+url.PathEscape(req.BaseSHA), token, nil, &base)
	if err != nil {
		return result, err
	}
	if status != http.StatusOK || base.Tree.SHA == "" {
		return result, fmt.Errorf("the imported commit could not be read on GitHub (HTTP %d)", status)
	}

	type treeEntry struct {
		Path string  `json:"path"`
		Mode string  `json:"mode"`
		Type string  `json:"type"`
		SHA  *string `json:"sha"` // null deletes the file
	}
	entries := make([]treeEntry, 0, len(req.Files))
	for _, file := range req.Files {
		if file.Deleted {
			entries = append(entries, treeEntry{Path: file.Path, Mode: "100644", Type: "blob", SHA: nil})
			continue
		}
		var blob struct {
			SHA string `json:"sha"`
		}
		status, err := d.apiSend(ctx, http.MethodPost, repo+"/git/blobs", token,
			map[string]string{"content": base64.StdEncoding.EncodeToString(file.Content), "encoding": "base64"}, &blob)
		if err != nil {
			return result, err
		}
		if status != http.StatusCreated || blob.SHA == "" {
			return result, writeFailure("the file "+file.Path, status)
		}
		sha := blob.SHA
		entries = append(entries, treeEntry{Path: file.Path, Mode: "100644", Type: "blob", SHA: &sha})
	}

	var tree struct {
		SHA string `json:"sha"`
	}
	status, err = d.apiSend(ctx, http.MethodPost, repo+"/git/trees", token, map[string]any{"base_tree": base.Tree.SHA, "tree": entries}, &tree)
	if err != nil {
		return result, err
	}
	if status != http.StatusCreated || tree.SHA == "" {
		return result, writeFailure("the file tree", status)
	}

	var commit struct {
		SHA     string `json:"sha"`
		HTMLURL string `json:"html_url"`
	}
	status, err = d.apiSend(ctx, http.MethodPost, repo+"/git/commits", token,
		map[string]any{"message": req.Message, "tree": tree.SHA, "parents": []string{req.BaseSHA}}, &commit)
	if err != nil {
		return result, err
	}
	if status != http.StatusCreated || commit.SHA == "" {
		return result, writeFailure("the commit", status)
	}
	result.CommitSHA, result.CommitURL = commit.SHA, commit.HTMLURL

	// Creating (never updating) the ref: an existing branch is refused by GitHub with 422.
	status, err = d.apiSend(ctx, http.MethodPost, repo+"/git/refs", token, map[string]string{"ref": "refs/heads/" + req.Branch, "sha": commit.SHA}, nil)
	if err != nil {
		return result, err
	}
	switch status {
	case http.StatusCreated:
	case http.StatusUnprocessableEntity:
		return result, ErrBranchExists
	default:
		return result, writeFailure("the new branch", status)
	}

	if req.CreatePR {
		var pull struct {
			HTMLURL string `json:"html_url"`
		}
		status, err = d.apiSend(ctx, http.MethodPost, repo+"/pulls", token,
			map[string]string{"title": req.PRTitle, "head": req.Branch, "base": req.BaseBranch, "body": req.PRBody}, &pull)
		switch {
		case err != nil:
			result.PullRequestError = err.Error()
		case status != http.StatusCreated || pull.HTMLURL == "":
			result.PullRequestError = fmt.Sprintf("the branch was pushed, but GitHub did not open the pull request (HTTP %d); open it from the branch on GitHub", status)
		default:
			result.PullRequestURL = pull.HTMLURL
		}
	}
	return result, nil
}
