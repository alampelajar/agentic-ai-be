package cloudworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// RepoInfo is where a repository stood when it was fetched. Publishing later compares the
// branch head with BaseSHA, so changes made on GitHub in the meantime are noticed instead of overwritten.
type RepoInfo struct {
	Owner         string
	Repo          string
	DefaultBranch string
	HeadSHA       string
	Private       bool
}

// ErrGitHubToken means the stored access token no longer works (revoked or expired).
var ErrGitHubToken = errors.New("the GitHub connection is no longer valid: reconnect GitHub in Settings > Integrations")

// Fetch downloads the default branch of a repository.
//
// With a token it goes through the GitHub API, so private repositories the person can read work.
// Without one it takes the public archive, and reads the branch and commit best effort (a public
// repository imports even if that lookup fails). The token is sent only to the GitHub API: the
// archive is fetched from a signed address GitHub redirects to, and the redirect rules in
// NewGitHubDownloader allow no other host.
func (d *GitHubDownloader) Fetch(ctx context.Context, owner, repo, token string, limits Limits) ([]byte, RepoInfo, error) {
	info := RepoInfo{Owner: owner, Repo: repo}
	if token == "" {
		data, err := d.Download(ctx, owner, repo, limits)
		if err != nil {
			return nil, info, err
		}
		if d.APIURL != "" {
			if found, lookupErr := d.lookup(ctx, owner, repo, ""); lookupErr == nil {
				info = found
			}
		}
		return data, info, nil
	}

	if d.APIURL == "" {
		return nil, info, errors.New("GitHub access is not available")
	}
	found, err := d.lookup(ctx, owner, repo, token)
	if err != nil {
		return nil, info, err
	}
	data, err := d.zipball(ctx, found, token, limits)
	if err != nil {
		return nil, found, err
	}
	return data, found, nil
}

func (d *GitHubDownloader) apiGet(ctx context.Context, path, token string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, d.APIURL+path, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := d.Client.Do(request)
	if err != nil {
		return nil, errors.New("GitHub could not be reached")
	}
	return response, nil
}

// lookup reads the default branch, whether the repository is private, and the commit the branch is at.
func (d *GitHubDownloader) lookup(ctx context.Context, owner, repo, token string) (RepoInfo, error) {
	info := RepoInfo{Owner: owner, Repo: repo}
	base := fmt.Sprintf("/repos/%s/%s", url.PathEscape(owner), url.PathEscape(repo))

	response, err := d.apiGet(ctx, base, token)
	if err != nil {
		return info, err
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return info, ErrGitHubToken
	case http.StatusNotFound:
		if token != "" {
			return info, errors.New("the repository was not found, or your GitHub account cannot see it")
		}
		return info, errors.New("the repository was not found or is not public (if it is private, connect GitHub in Settings > Integrations)")
	default:
		return info, fmt.Errorf("GitHub answered with HTTP %d", response.StatusCode)
	}
	var repository struct {
		DefaultBranch string `json:"default_branch"`
		Private       bool   `json:"private"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&repository); err != nil || repository.DefaultBranch == "" {
		return info, errors.New("GitHub gave an unreadable answer about the repository")
	}
	info.DefaultBranch, info.Private = repository.DefaultBranch, repository.Private

	commit, err := d.apiGet(ctx, base+"/commits/"+url.PathEscape(info.DefaultBranch), token)
	if err != nil {
		return info, err
	}
	defer commit.Body.Close()
	switch commit.StatusCode {
	case http.StatusOK:
	case http.StatusConflict:
		return info, errors.New("the repository is empty: add at least one commit (for example a README) and try again")
	default:
		return info, fmt.Errorf("GitHub answered with HTTP %d", commit.StatusCode)
	}
	var head struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(io.LimitReader(commit.Body, 1<<20)).Decode(&head); err != nil || head.SHA == "" {
		return info, errors.New("GitHub gave an unreadable answer about the latest commit")
	}
	info.HeadSHA = head.SHA
	return info, nil
}

// zipball downloads the repository at the commit that lookup found, so the files and the recorded
// commit always agree even if someone pushes while the download runs.
func (d *GitHubDownloader) zipball(ctx context.Context, info RepoInfo, token string, limits Limits) ([]byte, error) {
	response, err := d.apiGet(ctx, fmt.Sprintf("/repos/%s/%s/zipball/%s", url.PathEscape(info.Owner), url.PathEscape(info.Repo), url.PathEscape(info.HeadSHA)), token)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, ErrGitHubToken
	case http.StatusNotFound:
		return nil, errors.New("the repository was not found, or your GitHub account cannot see it")
	default:
		return nil, fmt.Errorf("GitHub answered with HTTP %d", response.StatusCode)
	}
	if response.ContentLength > limits.MaxZipBytes {
		return nil, fmt.Errorf("repository is larger than %d MiB", limits.MaxZipBytes>>20)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limits.MaxZipBytes+1))
	if err != nil {
		return nil, errors.New("the repository download was interrupted")
	}
	if int64(len(data)) > limits.MaxZipBytes {
		return nil, fmt.Errorf("repository is larger than %d MiB", limits.MaxZipBytes>>20)
	}
	return data, nil
}
