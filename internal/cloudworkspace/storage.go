package cloudworkspace

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// Limits bound what a workspace and a user may hold. They are plain values so a
// deployment can change them and tests can shrink them.
type Limits struct {
	MaxFileReadBytes     int64 // largest file the API returns as text
	MaxFileWriteBytes    int64 // largest file the API accepts
	MaxTreeEntries       int   // entries returned by one tree request
	MaxZipBytes          int64 // size of an uploaded or downloaded archive
	MaxExpandedBytes     int64 // total size after extraction
	MaxZipFiles          int   // files in one archive
	MaxUserBytes         int64 // storage quota per user
	MaxWorkspacesPerUser int
}

func DefaultLimits() Limits {
	return Limits{
		MaxFileReadBytes:     1 << 20,
		MaxFileWriteBytes:    2 << 20,
		MaxTreeEntries:       5000,
		MaxZipBytes:          50 << 20,
		MaxExpandedBytes:     200 << 20,
		MaxZipFiles:          10000,
		MaxUserBytes:         500 << 20,
		MaxWorkspacesPerUser: 10,
	}
}

// WorkspaceStorage is where workspace files live. The development implementation
// uses a directory; a production deployment can use an object storage adapter
// behind the same interface. Callers never build paths themselves.
type WorkspaceStorage interface {
	// Create makes an empty workspace and returns its storage reference.
	Create(userID uint) (ref string, err error)
	// FilesDir is the project directory: the only part users and containers see.
	FilesDir(userID uint, ref string) (string, error)
	// MetaDir holds bookkeeping (the change baseline). It is never mounted.
	MetaDir(userID uint, ref string) (string, error)
	// Delete removes the workspace completely.
	Delete(userID uint, ref string) error
	// Usage reports the bytes and files of one workspace.
	Usage(userID uint, ref string) (bytes int64, files int, err error)
}

var refPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

// LocalDevelopmentStorage keeps workspaces at <root>/<user id>/<ref>/{files,meta}.
type LocalDevelopmentStorage struct {
	root string
}

func NewLocalDevelopmentStorage(root string) (*LocalDevelopmentStorage, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o750); err != nil {
		return nil, err
	}
	return &LocalDevelopmentStorage{root: absolute}, nil
}

func (s *LocalDevelopmentStorage) base(userID uint, ref string) (string, error) {
	if userID == 0 || !refPattern.MatchString(ref) {
		return "", errors.New("invalid workspace reference")
	}
	return filepath.Join(s.root, strconv.FormatUint(uint64(userID), 10), ref), nil
}

func (s *LocalDevelopmentStorage) Create(userID uint) (string, error) {
	if userID == 0 {
		return "", errors.New("user is required")
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	ref := hex.EncodeToString(random)
	base, err := s.base(userID, ref)
	if err != nil {
		return "", err
	}
	for _, dir := range []string{"files", "meta"} {
		if err := os.MkdirAll(filepath.Join(base, dir), 0o750); err != nil {
			return "", err
		}
	}
	return ref, nil
}

func (s *LocalDevelopmentStorage) FilesDir(userID uint, ref string) (string, error) {
	base, err := s.base(userID, ref)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "files"), nil
}

func (s *LocalDevelopmentStorage) MetaDir(userID uint, ref string) (string, error) {
	base, err := s.base(userID, ref)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "meta"), nil
}

func (s *LocalDevelopmentStorage) Delete(userID uint, ref string) error {
	base, err := s.base(userID, ref)
	if err != nil {
		return err
	}
	return os.RemoveAll(base)
}

func (s *LocalDevelopmentStorage) Usage(userID uint, ref string) (int64, int, error) {
	dir, err := s.FilesDir(userID, ref)
	if err != nil {
		return 0, 0, err
	}
	return DirUsage(dir)
}

// DirUsage adds up the regular files under dir, leaving out generated dependency
// folders (they have their own limit, see GeneratedUsage). Symlinks are not followed.
func DirUsage(dir string) (int64, int, error) { return dirUsage(dir, true) }

func dirUsage(dir string, skipGenerated bool) (int64, int, error) {
	var total int64
	count := 0
	err := filepath.WalkDir(dir, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if skipGenerated && entry.IsDir() && current != dir && IsGeneratedDir(entry.Name()) {
			return fs.SkipDir
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			total += info.Size()
			count++
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("measure workspace: %w", err)
	}
	return total, count, nil
}
