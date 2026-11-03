// Package cloudworkspace holds the file side of Cloud Workspace: where
// workspaces live, how paths are kept inside them, and how files are listed,
// read, written, imported and exported. It never runs project code.
package cloudworkspace

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var (
	ErrInvalidPath = errors.New("path must be relative and stay inside the workspace")
	ErrSecretFile  = errors.New("this file may hold secrets and is not available through the workspace")
	ErrNotFound    = errors.New("file not found")
	ErrIsDirectory = errors.New("path is a directory")
	ErrTooLarge    = errors.New("file is too large")
	ErrBinary      = errors.New("binary files cannot be shown as text")
	ErrQuota       = errors.New("storage quota exceeded")
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
