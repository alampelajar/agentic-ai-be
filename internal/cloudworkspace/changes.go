package cloudworkspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

const baselineFile = "baseline.json"

type Change struct {
	Path   string `json:"path"`
	Status string `json:"status"` // added, modified or deleted
}

// manifest maps each project file to the hash of its content.
func manifest(filesDir string, limits Limits) (map[string]string, error) {
	result := map[string]string{}
	err := filepath.WalkDir(filesDir, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if current == filesDir || !entry.Type().IsRegular() {
			return nil
		}
		relative, relErr := filepath.Rel(filesDir, current)
		if relErr != nil {
			return relErr
		}
		slash := filepath.ToSlash(relative)
		if IsSecretPath(slash) {
			return nil // never reported, so never hashed
		}
		if len(result) >= limits.MaxZipFiles {
			return errors.New("too many files to compare")
		}
		source, openErr := os.Open(current)
		if openErr != nil {
			return openErr
		}
		defer source.Close()
		digest := sha256.New()
		if _, copyErr := io.Copy(digest, source); copyErr != nil {
			return copyErr
		}
		result[slash] = hex.EncodeToString(digest.Sum(nil))
		return nil
	})
	return result, err
}

// SaveBaseline records the current files as the state changes are measured from.
func SaveBaseline(filesDir, metaDir string, limits Limits) error {
	current, err := manifest(filesDir, limits)
	if err != nil {
		return err
	}
	data, err := json.Marshal(current)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(metaDir, 0o750); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(metaDir, baselineFile), data, 0o640)
}

// Changes lists what was added, modified or deleted since the baseline.
func Changes(filesDir, metaDir string, limits Limits) ([]Change, error) {
	baseline := map[string]string{}
	data, err := os.ReadFile(filepath.Join(metaDir, baselineFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal(data, &baseline); err != nil {
			return nil, err
		}
	}
	current, err := manifest(filesDir, limits)
	if err != nil {
		return nil, err
	}
	changes := []Change{}
	for path, hash := range current {
		before, existed := baseline[path]
		switch {
		case !existed:
			changes = append(changes, Change{Path: path, Status: "added"})
		case before != hash:
			changes = append(changes, Change{Path: path, Status: "modified"})
		}
	}
	for path := range baseline {
		if _, still := current[path]; !still {
			changes = append(changes, Change{Path: path, Status: "deleted"})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}
