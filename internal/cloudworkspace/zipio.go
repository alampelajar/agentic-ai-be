package cloudworkspace

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// maxZipEntryBytes caps one file inside an archive, whatever its header says.
const maxZipEntryBytes = 20 << 20

type ExtractResult struct {
	Files   int   `json:"files"`
	Bytes   int64 `json:"bytes"`
	Skipped int   `json:"skipped"` // secret files, symlinks and the like that were left out
}

// ExtractZip unpacks an archive into dest, which must be an empty or existing
// workspace directory. It refuses path traversal, absolute paths and symlinks,
// leaves secret files out, and enforces the size limits on what is really
// written, not on what the archive claims. budget is how many bytes the user
// may still store. A single top-level folder (as GitHub archives have) is
// removed.
func ExtractZip(data []byte, dest string, limits Limits, budget int64) (ExtractResult, error) {
	if len(data) == 0 {
		return ExtractResult{}, errors.New("the archive is empty")
	}
	if int64(len(data)) > limits.MaxZipBytes {
		return ExtractResult{}, fmt.Errorf("archive is larger than %d MiB", limits.MaxZipBytes>>20)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return ExtractResult{}, errors.New("the file is not a valid ZIP archive")
	}
	if len(reader.File) == 0 {
		return ExtractResult{}, errors.New("the archive is empty")
	}
	if len(reader.File) > limits.MaxZipFiles {
		return ExtractResult{}, fmt.Errorf("archive has more than %d files", limits.MaxZipFiles)
	}

	// Check every name as the archive wrote it before anything is stripped, so a
	// traversal or a drive letter can never be mistaken for a top-level folder.
	for _, file := range reader.File {
		if _, cleanErr := CleanRelative(file.Name); cleanErr != nil {
			return ExtractResult{}, fmt.Errorf("unsafe path in archive: %q", file.Name)
		}
	}
	prefix := commonTopFolder(reader.File)
	realDest, err := filepath.EvalSymlinks(dest)
	if err != nil {
		return ExtractResult{}, err
	}
	ceiling := limits.MaxExpandedBytes
	if budget < ceiling {
		ceiling = budget
	}

	var result ExtractResult
	for _, file := range reader.File {
		name := strings.TrimPrefix(strings.ReplaceAll(file.Name, `\`, "/"), prefix)
		if name == "" {
			continue
		}
		rel, cleanErr := CleanRelative(name)
		if cleanErr != nil || rel == "" {
			return ExtractResult{}, fmt.Errorf("unsafe path in archive: %q", file.Name)
		}
		if IsGeneratedPath(rel) {
			result.Skipped++ // node_modules and the like are not imported; install them again
			continue
		}
		mode := file.FileInfo().Mode()
		if mode&fs.ModeSymlink != 0 {
			return ExtractResult{}, fmt.Errorf("symlink in archive is not allowed: %q", file.Name)
		}
		if !file.FileInfo().IsDir() && !mode.IsRegular() {
			result.Skipped++
			continue
		}
		if len(rel) > maxPathLength || strings.Count(rel, "/") >= maxPathDepth {
			return ExtractResult{}, fmt.Errorf("path in archive is too long or too deep: %q", file.Name)
		}
		target := filepath.Join(realDest, filepath.FromSlash(rel))
		if !strings.HasPrefix(target, realDest+string(filepath.Separator)) {
			return ExtractResult{}, fmt.Errorf("unsafe path in archive: %q", file.Name)
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o750); err != nil {
				return ExtractResult{}, err
			}
			continue
		}
		if IsSecretPath(rel) {
			result.Skipped++
			continue
		}
		if file.UncompressedSize64 > maxZipEntryBytes {
			return ExtractResult{}, fmt.Errorf("file in archive is too large: %q", file.Name)
		}
		written, err := extractEntry(file, target, ceiling-result.Bytes)
		if err != nil {
			return ExtractResult{}, err
		}
		result.Bytes += written
		result.Files++
	}
	return result, nil
}

func extractEntry(file *zip.File, target string, remaining int64) (int64, error) {
	if remaining <= 0 {
		return 0, ErrQuota
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return 0, err
	}
	source, err := file.Open()
	if err != nil {
		return 0, err
	}
	defer source.Close()
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return 0, fmt.Errorf("cannot create %q (duplicate path?): %w", file.Name, err)
	}
	limit := int64(maxZipEntryBytes)
	if remaining < limit {
		limit = remaining
	}
	written, copyErr := io.Copy(out, io.LimitReader(source, limit+1))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		return 0, errors.Join(copyErr, closeErr)
	}
	if written > limit {
		_ = os.Remove(target)
		if limit == remaining {
			return 0, ErrQuota
		}
		return 0, fmt.Errorf("file in archive is too large: %q", file.Name)
	}
	return written, nil
}

// commonTopFolder returns "name/" when every entry sits in one folder, else "".
func commonTopFolder(files []*zip.File) string {
	top := ""
	for _, file := range files {
		name := strings.TrimPrefix(strings.ReplaceAll(file.Name, `\`, "/"), "./")
		index := strings.Index(name, "/")
		if index <= 0 {
			return "" // a file or folder at the top level
		}
		folder := name[:index+1]
		if top == "" {
			top = folder
		} else if folder != top {
			return ""
		}
	}
	return top
}

// ExportZip writes the project files as a ZIP. Secret files and symlinks are
// left out, and the total is bounded.
func ExportZip(filesDir string, out io.Writer, limits Limits) error {
	writer := zip.NewWriter(out)
	var total int64
	count := 0
	walkErr := filepath.WalkDir(filesDir, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && current != filesDir && IsGeneratedDir(entry.Name()) {
			return fs.SkipDir // dependencies are installed again, not exported
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
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		total += info.Size()
		count++
		if total > limits.MaxExpandedBytes || count > limits.MaxZipFiles {
			return errors.New("workspace is too large to export")
		}
		header, headerErr := zip.FileInfoHeader(info)
		if headerErr != nil {
			return headerErr
		}
		header.Name = slash
		header.Method = zip.Deflate
		part, createErr := writer.CreateHeader(header)
		if createErr != nil {
			return createErr
		}
		source, openErr := os.Open(current)
		if openErr != nil {
			return openErr
		}
		defer source.Close()
		_, copyErr := io.Copy(part, source)
		return copyErr
	})
	if walkErr != nil {
		_ = writer.Close()
		return walkErr
	}
	return writer.Close()
}
