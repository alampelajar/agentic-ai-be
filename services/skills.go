package services

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"agentic-ai-backend/models"
)

const (
	MaxSkillArchiveBytes  = 10 << 20
	MaxSkillExpandedBytes = 50 << 20
	MaxSkillFiles         = 500
)

var AllowedSkillTools = map[string]bool{
	"workspace.list": true, "workspace.read": true, "workspace.write": true,
	"workspace.execute": true, "desktop.status": true, "desktop.screenshot": true,
	"desktop.windows": true, "desktop.list_windows": true, "desktop.inspect": true, "desktop.focus_window": true, "desktop.activate_window": true,
	"desktop.launch_application": true, "desktop.click": true, "desktop.double_click": true, "desktop.type": true,
	"desktop.hotkey": true, "desktop.scroll": true, "desktop.wait": true, "desktop.stop": true,
	"workspace.list_files": true, "workspace.read_file": true, "workspace.write_file": true, "workspace.edit_file": true, "workspace.get_changes": true,
	"workspace.run_test": true, "workspace.build": true, "workspace.get_execution": true, "workspace.install_dependencies": true,
}

type ValidatedSkillPackage struct {
	Manifest     models.SkillManifest
	Instructions string
	SHA256       string
}

func ValidateSkillManifest(manifest models.SkillManifest, instructions string) error {
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,99}$`).MatchString(strings.TrimSpace(manifest.ID)) {
		return errors.New("manifest.id must be a lowercase slug")
	}
	if strings.TrimSpace(manifest.Name) == "" || len(manifest.Name) > 150 {
		return errors.New("manifest.name is required and must be at most 150 characters")
	}
	if !regexp.MustCompile(`^\d+\.\d+\.\d+([-.][0-9A-Za-z.-]+)?$`).MatchString(strings.TrimSpace(manifest.Version)) {
		return errors.New("manifest.version must use semantic version format")
	}
	if strings.TrimSpace(manifest.Instructions) == "" {
		manifest.Instructions = "SKILL.md"
	}
	if path.Clean(manifest.Instructions) != "SKILL.md" {
		return errors.New("manifest.instructions must reference SKILL.md")
	}
	if strings.TrimSpace(instructions) == "" || len(instructions) > 1<<20 {
		return errors.New("SKILL.md is required and must be at most 1 MiB")
	}
	if len(manifest.Tools) > 50 {
		return errors.New("manifest.tools exceeds the maximum of 50 tools")
	}
	seen := map[string]bool{}
	for _, tool := range manifest.Tools {
		tool = strings.TrimSpace(tool)
		if !AllowedSkillTools[tool] {
			return fmt.Errorf("tool %q is not in the known tool registry", tool)
		}
		if seen[tool] {
			return fmt.Errorf("duplicate tool %q", tool)
		}
		seen[tool] = true
	}
	for _, platform := range manifest.Platforms {
		if strings.TrimSpace(platform) == "" {
			return errors.New("manifest.platforms cannot contain empty values")
		}
	}
	return nil
}

func ValidateSkillArchive(data []byte) (ValidatedSkillPackage, error) {
	if len(data) == 0 || len(data) > MaxSkillArchiveBytes {
		return ValidatedSkillPackage{}, errors.New("skill archive is empty or exceeds 10 MiB")
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return ValidatedSkillPackage{}, fmt.Errorf("invalid ZIP archive: %w", err)
	}
	if len(reader.File) == 0 || len(reader.File) > MaxSkillFiles {
		return ValidatedSkillPackage{}, errors.New("skill archive has an invalid file count")
	}

	var manifest models.SkillManifest
	var manifestFound, skillFound bool
	var instructions string
	var expanded int64
	for _, file := range reader.File {
		name := strings.ReplaceAll(file.Name, "\\", "/")
		clean := path.Clean(name)
		if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." || strings.HasPrefix(clean, "/") || clean != name {
			return ValidatedSkillPackage{}, fmt.Errorf("unsafe archive path %q", file.Name)
		}
		if len(strings.Split(clean, "/")) > 10 {
			return ValidatedSkillPackage{}, errors.New("skill archive directory depth exceeds 10")
		}
		if file.FileInfo().Mode()&0o170000 == 0o120000 {
			return ValidatedSkillPackage{}, fmt.Errorf("symlink is not allowed: %s", name)
		}
		if file.UncompressedSize64 > MaxSkillExpandedBytes || expanded+int64(file.UncompressedSize64) > MaxSkillExpandedBytes {
			return ValidatedSkillPackage{}, errors.New("expanded skill archive exceeds 50 MiB")
		}
		if file.FileInfo().IsDir() {
			continue
		}
		if path.Base(clean) == "manifest.json" {
			content, readErr := readZipFile(file, 1<<20)
			if readErr != nil {
				return ValidatedSkillPackage{}, readErr
			}
			if err := json.Unmarshal(content, &manifest); err != nil {
				return ValidatedSkillPackage{}, fmt.Errorf("invalid manifest.json: %w", err)
			}
			manifestFound = true
		}
		if path.Base(clean) == "SKILL.md" {
			content, readErr := readZipFile(file, 1<<20)
			if readErr != nil {
				return ValidatedSkillPackage{}, readErr
			}
			instructions = string(content)
			skillFound = true
		}
		expanded += int64(file.UncompressedSize64)
	}
	if !manifestFound || !skillFound {
		return ValidatedSkillPackage{}, errors.New("skill archive must contain manifest.json and SKILL.md")
	}
	if err := ValidateSkillManifest(manifest, instructions); err != nil {
		return ValidatedSkillPackage{}, err
	}
	sum := sha256.Sum256(data)
	return ValidatedSkillPackage{Manifest: manifest, Instructions: instructions, SHA256: hex.EncodeToString(sum[:])}, nil
}

func readZipFile(file *zip.File, limit int64) ([]byte, error) {
	r, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("skill file exceeds size limit")
	}
	return data, nil
}

func DownloadPublicGitHubArchive(rawURL string) ([]byte, string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme != "https" || strings.ToLower(u.Hostname()) != "github.com" {
		return nil, "", errors.New("only https://github.com URLs are allowed")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return nil, "", errors.New("GitHub URL must contain owner and repository")
	}
	owner, repo := parts[0], strings.TrimSuffix(parts[1], ".git")
	archiveURL := fmt.Sprintf("https://codeload.github.com/%s/%s/zip/refs/heads/main", owner, repo)
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		if req.URL.Hostname() != "codeload.github.com" {
			return errors.New("redirected to an untrusted host")
		}
		return nil
	}}
	resp, err := client.Get(archiveURL)
	if err != nil {
		return nil, "", fmt.Errorf("GitHub download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("GitHub download returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > MaxSkillArchiveBytes {
		return nil, "", errors.New("GitHub skill archive exceeds 10 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxSkillArchiveBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > MaxSkillArchiveBytes {
		return nil, "", errors.New("GitHub skill archive exceeds 10 MiB")
	}
	return data, archiveURL, nil
}
