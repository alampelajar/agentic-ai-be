package sitebuilder

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// Progress records which parts of a run are finished, so a run that stopped (a model
// error, the time limit) can continue where it stopped instead of starting again
// (WEBSITE-BUILDER.md, R5). It lives beside the site's versions, outside the files the
// agent may write, and is valid only for the same work: the same brief, look and
// request.
type Progress struct {
	Key     string          `json:"key"`
	Layout  bool            `json:"layout,omitempty"`
	Pages   []string        `json:"pages,omitempty"`
	Written bool            `json:"written,omitempty"`
	Brief   json.RawMessage `json:"brief,omitempty"` // the brief as the stopped run left it (a revise may change the pages)
	Summary string          `json:"summary,omitempty"`
}

// ProgressKey names one piece of work: a run of this kind and request on this brief
// and look. Another key means other work, and the record does not apply.
func ProgressKey(kind, request, briefJSON, direction, variant string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + request + "\x00" + briefJSON + "\x00" + direction + "\x00" + variant))
	return hex.EncodeToString(sum[:16])
}

// ProgressFile is where a site's record is kept.
func (s Settings) ProgressFile(siteID uint) string {
	return filepath.Join(s.SiteDir(siteID), "progress.json")
}

// LoadProgress reads the record; found is false when there is none.
func LoadProgress(file string) (progress Progress, found bool) {
	data, err := os.ReadFile(file)
	if err != nil || json.Unmarshal(data, &progress) != nil || progress.Key == "" {
		return Progress{}, false
	}
	return progress, true
}

// SaveProgress writes the record.
func SaveProgress(file string, progress Progress) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		return err
	}
	data, err := json.Marshal(progress)
	if err != nil {
		return err
	}
	temporary := file + ".tmp"
	if err := os.WriteFile(temporary, data, 0o640); err != nil {
		return err
	}
	return os.Rename(temporary, file)
}

// ClearProgress forgets the record (after a finished run, or when the person goes back
// to another version).
func ClearProgress(file string) { _ = os.Remove(file) }

// HasPage is true when the page file was finished.
func (p Progress) HasPage(file string) bool { return contains(p.Pages, file) }
