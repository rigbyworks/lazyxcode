package store

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ExpandResultBundle extracts a downloaded Cloud artifact into its own cache
// directory. Paths, entry types and expanded size are checked before writing.
func ExpandResultBundle(ctx context.Context, path string) (string, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open result archive: %w", err)
	}
	defer archive.Close()
	if len(archive.File) > 100000 {
		return "", errors.New("result archive has too many entries")
	}
	const maxSize = uint64(4 << 30)
	var total uint64
	root := ""
	for _, file := range archive.File {
		name := file.Name
		clean := filepath.Clean(filepath.FromSlash(name))
		if strings.Contains(name, "\\") || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
			return "", errors.New("result archive contains an unsafe path")
		}
		if file.Mode()&os.ModeSymlink != 0 || (!file.FileInfo().IsDir() && !file.Mode().IsRegular()) {
			return "", errors.New("result archive contains a non-regular entry")
		}
		if file.UncompressedSize64 > maxSize-total {
			return "", errors.New("result archive exceeds 4 GiB expanded size")
		}
		total += file.UncompressedSize64
		parts := strings.Split(filepath.ToSlash(clean), "/")
		for i, part := range parts {
			if strings.HasSuffix(part, ".xcresult") && !strings.HasPrefix(part, ".") {
				candidate := filepath.Join(parts[:i+1]...)
				if root != "" && root != candidate {
					return "", errors.New("result archive contains multiple result bundles")
				}
				root = candidate
				break
			}
		}
	}
	if root == "" {
		return "", errors.New("artifact contains no .xcresult bundle")
	}
	destination := path + ".expanded"
	if info, err := os.Stat(filepath.Join(destination, root)); err == nil && info.IsDir() {
		return filepath.Join(destination, root), nil
	}
	temporary, err := os.MkdirTemp(filepath.Dir(path), ".result-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(temporary)
	for _, file := range archive.File {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		target := filepath.Join(temporary, filepath.FromSlash(file.Name))
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0700); err != nil {
				return "", err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return "", err
		}
		if err := extractResultFile(ctx, file, target); err != nil {
			return "", err
		}
	}
	info, err := os.Stat(filepath.Join(temporary, root))
	if err != nil || !info.IsDir() {
		return "", errors.New("result bundle is not a directory")
	}
	if err := os.Rename(temporary, destination); err != nil {
		return "", err
	}
	return filepath.Join(destination, root), nil
}

func extractResultFile(ctx context.Context, file *zip.File, path string) error {
	source, err := file.Open()
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(target, contextReader{ctx: ctx, reader: io.LimitReader(source, int64(file.UncompressedSize64)+1)})
	closeErr := target.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
