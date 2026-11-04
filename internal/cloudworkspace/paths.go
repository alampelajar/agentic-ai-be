// Package cloudworkspace holds the file side of Cloud Workspace: where
// workspaces live, how paths are kept inside them, and how files are listed,
// read, written, imported and exported. It never runs project code.
package cloudworkspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"agentic-ai-backend/internal/jobspec"
)

var (
	ErrInvalidPath = errors.New("path must be relative and stay inside the workspace")
	ErrSecretFile  = errors.New("this file may hold secrets and is not available through the workspace")
	ErrNotFound    = errors.New("file not found")
	ErrIsDirectory = errors.New("path is a directory")
	ErrTooLarge    = errors.New("file is too large")
	ErrBinary      = errors.New("binary files cannot be shown as text")
	ErrQuota       = errors.New("storage quota exceeded")
	// ErrGeneratedPath is returned for anything inside a dependency folder.
	ErrGeneratedPath = errors.New("this folder holds installed dependencies; it is created by the install step and is not available through the workspace")
)

// CleanRelative turns a client path into a slash-separated path relative to the
// workspace root, or refuses it. "" and "." mean the root.
func CleanRelative(raw string) (string, error) {
	if strings.ContainsRune(raw, 0) {
		return "", ErrInvalidPath
	}
	normalized := strings.ReplaceAll(raw, `\`, "/")
	if strings.HasPrefix(normalized, "/") || (len(normalized) >= 2 && normalized[1] == ':') {
		return "", ErrInvalidPath // absolute, or a Windows drive
	}
	for _, part := range strings.Split(normalized, "/") {
		if part == ".." {
			return "", ErrInvalidPath
		}
	}
	clean := path.Clean(normalized)
	if clean == "." || clean == "" {
		return "", nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", ErrInvalidPath
	}
	return clean, nil
}

// IsGeneratedDir reports whether a folder name is where installed dependencies
// live (node_modules, and the folder pip installs into). These folders can hold
// hundreds of megabytes and tens of thousands of files that nobody edits by
// hand, so they stay out of the file tree, change list, export and storage
// quota, are not readable or writable through the file API, and are not
// extracted from an uploaded archive. Test and build jobs still see them.
func IsGeneratedDir(name string) bool {
	return name == "node_modules" || name == jobspec.DependencyDir
}

// IsGeneratedPath is true when any part of a relative path is a generated folder.
func IsGeneratedPath(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if IsGeneratedDir(part) {
			return true
		}
	}
	return false
}

// GeneratedUsage adds up the files in every generated folder under filesDir.
func GeneratedUsage(filesDir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(filesDir, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && current != filesDir && IsGeneratedDir(entry.Name()) {
			bytes, _, err := dirUsage(current, false)
			total += bytes
			if err != nil {
				return err
			}
			return fs.SkipDir
		}
		return nil
	})
	return total, err
}

// RemoveGenerated deletes every generated folder under filesDir. Symlinks are
// removed, never followed.
func RemoveGenerated(filesDir string) error {
	var found []string
	err := filepath.WalkDir(filesDir, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && current != filesDir && IsGeneratedDir(entry.Name()) {
			found = append(found, current)
			return fs.SkipDir
		}
		return nil
	})
	for _, path := range found {
		err = errors.Join(err, os.RemoveAll(path))
	}
	return err
}

// IsSecretPath reports whether a file should never be read or written through
// the workspace API: environment files, private keys and credential stores.
func IsSecretPath(rel string) bool {
	for _, part := range strings.Split(strings.ToLower(rel), "/") {
		switch {
		case part == ".env", strings.HasPrefix(part, ".env."), strings.HasSuffix(part, ".env"):
			return true
		case part == ".npmrc", part == ".pypirc", part == ".netrc", part == ".git-credentials", part == "credentials.json":
			return true
		case strings.HasPrefix(part, "id_rsa"), strings.HasPrefix(part, "id_ed25519"), strings.HasPrefix(part, "id_ecdsa"):
			return true
		case strings.HasSuffix(part, ".pem"), strings.HasSuffix(part, ".key"), strings.HasSuffix(part, ".p12"), strings.HasSuffix(part, ".pfx"):
			return true
		}
	}
	return false
}

// Resolve maps a client path to a real path under root. Every existing component
// is checked, so a symlink inside the workspace that points elsewhere cannot be
// followed out. root itself must be a real directory.
func Resolve(root, raw string) (absolute, rel string, err error) {
	rel, err = CleanRelative(raw)
	if err != nil {
		return "", "", err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", fmt.Errorf("workspace is not available: %w", err)
	}
	current := realRoot
	if rel == "" {
		return current, "", nil
	}
	parts := strings.Split(rel, "/")
	for index, part := range parts {
		next := filepath.Join(current, part)
		info, statErr := os.Lstat(next)
		switch {
		case statErr == nil:
			if info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 { // irregular covers Windows junctions
				return "", "", ErrInvalidPath // no symlinks inside a workspace
			}
			current = next
		case errors.Is(statErr, os.ErrNotExist):
			// The rest does not exist yet (a file about to be written); it can only
			// be made of plain names, which CleanRelative already guaranteed.
			rest := filepath.Join(parts[index:]...)
			return filepath.Join(current, rest), rel, nil
		default:
			return "", "", statErr
		}
	}
	return current, rel, nil
}
