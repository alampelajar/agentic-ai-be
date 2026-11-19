package sitebuilder

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"agentic-ai-backend/internal/cloudworkspace"
)

// The owner may read the code of their website (the builder's "Kode" and "Perubahan"
// tabs). Only the text files of a version's source are shown; the person's pictures
// (web/public) and the built output are not.

// MaxReadableFile is the largest file the owner can open in the code view.
const MaxReadableFile = 256 * 1024

// VersionFile is one readable file of a version.
type VersionFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	// Change against the version before: "added", "changed" or "removed"; empty when
	// the file is the same, or when there is no version before.
	Change string `json:"change,omitempty"`
}

var readableExtensions = map[string]bool{
	".tsx": true, ".ts": true, ".jsx": true, ".js": true, ".mjs": true,
	".css": true, ".html": true, ".json": true, ".sql": true, ".md": true, ".txt": true,
}

// Readable reports whether a source path is one the owner may read.
func Readable(rel string) bool {
	if strings.HasPrefix(rel, "web/public/") {
		return false
	}
	return readableExtensions[strings.ToLower(path.Ext(rel))]
}

type sourceFile struct {
	size int64
	hash [32]byte
}

func versionSource(versionDir string) (map[string]sourceFile, error) {
	root := filepath.Join(versionDir, "source")
	files := map[string]sourceFile{}
	err := filepath.WalkDir(root, func(absolute string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil // folders are walked, symlinks and the rest skipped
		}
		relative, err := filepath.Rel(root, absolute)
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(relative)
		if !Readable(rel) {
			return nil
		}
		data, err := os.ReadFile(absolute)
		if err != nil {
			return err
		}
		files[rel] = sourceFile{size: int64(len(data)), hash: sha256.Sum256(data)}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return files, nil
	}
	return files, err
}

// CompareVersions lists the readable files of a version, each marked against the
// version before it (previousDir is "" when there is none). Removed files are listed
// too, so the owner sees what a change took away. Sorted by path.
func CompareVersions(versionDir, previousDir string) ([]VersionFile, error) {
	current, err := versionSource(versionDir)
	if err != nil {
		return nil, err
	}
	var before map[string]sourceFile
	if previousDir != "" {
		if before, err = versionSource(previousDir); err != nil {
			return nil, err
		}
	}
	list := make([]VersionFile, 0, len(current))
	for rel, file := range current {
		entry := VersionFile{Path: rel, Size: file.size}
		if before != nil {
			old, found := before[rel]
			switch {
			case !found:
				entry.Change = "added"
			case !bytes.Equal(old.hash[:], file.hash[:]):
				entry.Change = "changed"
			}
		}
		list = append(list, entry)
	}
	for rel, file := range before {
		if _, found := current[rel]; !found {
			list = append(list, VersionFile{Path: rel, Size: file.size, Change: "removed"})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Path < list[j].Path })
	return list, nil
}

// ErrNotReadable is returned for a path the owner may not read, or one too large.
var ErrNotReadable = errors.New("file is not readable")

// ReadVersionFile returns one readable file of a version through the workspace path
// checks. found is false when the version has no such file.
func ReadVersionFile(versionDir, raw string) (content string, found bool, err error) {
	rel, err := cloudworkspace.CleanRelative(raw)
	if err != nil || rel == "" || !Readable(rel) {
		return "", false, ErrNotReadable
	}
	absolute, _, err := cloudworkspace.Resolve(filepath.Join(versionDir, "source"), rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	info, err := os.Lstat(absolute)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() {
		return "", false, ErrNotReadable
	}
	if info.Size() > MaxReadableFile {
		return "", true, ErrNotReadable
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return "", false, err
	}
	return string(data), true, nil
}
