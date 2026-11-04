package cloudworkspace

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxPathLength = 512
	maxPathDepth  = 30
)

type TreeEntry struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Type      string `json:"type"` // "file" or "dir"
	Size      int64  `json:"size"`
	Sensitive bool   `json:"sensitive,omitempty"`
	// Generated marks a dependency folder: listed so it is visible that it exists,
	// but never expanded (see IsGeneratedDir).
	Generated bool `json:"generated,omitempty"`
}

type Tree struct {
	Entries   []TreeEntry `json:"entries"`
	Truncated bool        `json:"truncated"`
}

// BuildTree lists everything under dir (a path inside the workspace; "" is the
// root), directories first and then alphabetically. Symlinks are left out.
func BuildTree(filesDir, dir string, limits Limits) (Tree, error) {
	start, rel, err := Resolve(filesDir, dir)
	if err != nil {
		return Tree{}, err
	}
	info, err := os.Stat(start)
	if errors.Is(err, os.ErrNotExist) {
		return Tree{}, ErrNotFound
	}
	if err != nil {
		return Tree{}, err
	}
	if !info.IsDir() {
		return Tree{}, ErrInvalidPath
	}
	realRoot, err := filepath.EvalSymlinks(filesDir)
	if err != nil {
		return Tree{}, err
	}
	tree := Tree{Entries: []TreeEntry{}}
	if IsGeneratedPath(rel) {
		return tree, nil // nothing is listed inside a dependency folder
	}
	walkErr := filepath.WalkDir(start, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if current == start {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil // never listed, never followed
		}
		if len(tree.Entries) >= limits.MaxTreeEntries {
			tree.Truncated = true
			return fs.SkipAll
		}
		relative, relErr := filepath.Rel(realRoot, current)
		if relErr != nil {
			return relErr
		}
		slash := filepath.ToSlash(relative)
		item := TreeEntry{Path: slash, Name: entry.Name(), Type: "file", Sensitive: IsSecretPath(slash)}
		if entry.IsDir() {
			item.Type = "dir"
			if IsGeneratedDir(entry.Name()) {
				item.Generated = true
				tree.Entries = append(tree.Entries, item)
				return fs.SkipDir
			}
		} else if entry.Type().IsRegular() {
			if fileInfo, infoErr := entry.Info(); infoErr == nil {
				item.Size = fileInfo.Size()
			}
		} else {
			return nil // sockets, devices and the like
		}
		tree.Entries = append(tree.Entries, item)
		return nil
	})
	if walkErr != nil {
		return Tree{}, walkErr
	}
	sort.SliceStable(tree.Entries, func(i, j int) bool {
		a, b := tree.Entries[i], tree.Entries[j]
		if filepath.Dir(a.Path) != filepath.Dir(b.Path) {
			return a.Path < b.Path
		}
		if a.Type != b.Type {
			return a.Type == "dir"
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return tree, nil
}

// LooksBinary reports content that is not text: a NUL byte or invalid UTF-8.
func LooksBinary(content []byte) bool {
	return bytes.IndexByte(content, 0) >= 0 || !utf8.Valid(content)
}

// ReadFile returns a text file. Secret files, directories, binary files and
// files over the read limit are refused.
func ReadFile(filesDir, raw string, limits Limits) (content string, size int64, err error) {
	absolute, rel, err := Resolve(filesDir, raw)
	if err != nil {
		return "", 0, err
	}
	if rel == "" {
		return "", 0, ErrIsDirectory
	}
	if IsSecretPath(rel) {
		return "", 0, ErrSecretFile
	}
	if IsGeneratedPath(rel) {
		return "", 0, ErrGeneratedPath
	}
	info, err := os.Lstat(absolute)
	if errors.Is(err, os.ErrNotExist) {
		return "", 0, ErrNotFound
	}
	if err != nil {
		return "", 0, err
	}
	if info.IsDir() {
		return "", 0, ErrIsDirectory
	}
	if !info.Mode().IsRegular() {
		return "", 0, ErrInvalidPath
	}
	if info.Size() > limits.MaxFileReadBytes {
		return "", info.Size(), ErrTooLarge
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return "", 0, err
	}
	if LooksBinary(data) {
		return "", info.Size(), ErrBinary
	}
	return string(data), info.Size(), nil
}

// WriteResult says what a write did to the workspace size.
type WriteResult struct {
	Created bool  `json:"created"`
	Size    int64 `json:"size"`
	Growth  int64 `json:"-"` // new size minus old size
}

// WriteFile stores a text file, creating folders as needed. budget is how many
// bytes the workspace may still grow by; a write that needs more fails with
// ErrQuota. The file is written to a temporary name and renamed, so a reader
// never sees half a file.
func WriteFile(filesDir, raw, content string, limits Limits, budget int64) (WriteResult, error) {
	absolute, rel, err := Resolve(filesDir, raw)
	if err != nil {
		return WriteResult{}, err
	}
	if rel == "" {
		return WriteResult{}, ErrIsDirectory
	}
	if len(rel) > maxPathLength || strings.Count(rel, "/") >= maxPathDepth {
		return WriteResult{}, ErrInvalidPath
	}
	if IsSecretPath(rel) {
		return WriteResult{}, ErrSecretFile
	}
	if IsGeneratedPath(rel) {
		return WriteResult{}, ErrGeneratedPath
	}
	data := []byte(content)
	if int64(len(data)) > limits.MaxFileWriteBytes {
		return WriteResult{}, ErrTooLarge
	}
	if LooksBinary(data) {
		return WriteResult{}, ErrBinary
	}

	var previous int64
	created := true
	if info, statErr := os.Lstat(absolute); statErr == nil {
		if info.IsDir() {
			return WriteResult{}, ErrIsDirectory
		}
		if !info.Mode().IsRegular() {
			return WriteResult{}, ErrInvalidPath
		}
		previous = info.Size()
		created = false
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return WriteResult{}, statErr
	}
	growth := int64(len(data)) - previous
	if growth > budget {
		return WriteResult{}, ErrQuota
	}

	directory := filepath.Dir(absolute)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return WriteResult{}, err
	}
	temp, err := os.CreateTemp(directory, ".write-*")
	if err != nil {
		return WriteResult{}, err
	}
	tempName := temp.Name()
	_, writeErr := temp.Write(data)
	closeErr := temp.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(tempName)
		return WriteResult{}, errors.Join(writeErr, closeErr)
	}
	if err := os.Chmod(tempName, 0o640); err != nil {
		_ = os.Remove(tempName)
		return WriteResult{}, err
	}
	if err := os.Rename(tempName, absolute); err != nil {
		_ = os.Remove(tempName)
		return WriteResult{}, err
	}
	return WriteResult{Created: created, Size: int64(len(data)), Growth: growth}, nil
}
