package walk

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/rike422/shoka/internal/ignore"
	"github.com/rike422/shoka/internal/limits"
)

// Meta is file identity without content.
type Meta struct {
	RelPath string
	Mtime   int64
	Size    int64
}

// File is Meta plus content and hash.
type File struct {
	Meta
	Content string
	Hash    string
}

// ListMeta returns candidate text-file paths with mtime/size (no content read beyond size check).
func ListMeta(root string) ([]Meta, error) {
	paths, err := listPaths(root)
	if err != nil {
		return nil, err
	}
	ig, err := ignore.Load(root)
	if err != nil {
		return nil, fmt.Errorf("load .shokaignore: %w", err)
	}
	var out []Meta
	for _, rel := range paths {
		rel = filepath.ToSlash(rel)
		if shouldSkipPath(rel) || ig.Match(rel) {
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(abs)
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		if info.Size() > limits.MaxFileBytes || info.Size() == 0 {
			continue
		}
		out = append(out, Meta{
			RelPath: rel,
			Mtime:   info.ModTime().UnixNano(),
			Size:    info.Size(),
		})
	}
	return out, nil
}

// Read loads content and computes sha256. Returns ok=false if not indexable text.
func Read(root string, m Meta) (File, bool, error) {
	abs := filepath.Join(root, filepath.FromSlash(m.RelPath))
	data, err := os.ReadFile(abs)
	if err != nil {
		return File{}, false, err
	}
	if !isTextUTF8(data) {
		return File{}, false, nil
	}
	sum := sha256.Sum256(data)
	return File{
		Meta:    m,
		Content: string(data),
		Hash:    hex.EncodeToString(sum[:]),
	}, true, nil
}

// List reads all indexable files (full scan).
func List(root string) ([]File, error) {
	metas, err := ListMeta(root)
	if err != nil {
		return nil, err
	}
	var out []File
	for _, m := range metas {
		f, ok, err := Read(root, m)
		if err != nil {
			continue
		}
		if !ok {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

func listPaths(root string) ([]string, error) {
	if hasGit(root) {
		cmd := exec.Command("git", "-C", root, "ls-files", "-co", "--exclude-standard", "-z")
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git ls-files: %w", err)
		}
		parts := bytes.Split(out, []byte{0})
		var paths []string
		for _, p := range parts {
			if len(p) == 0 {
				continue
			}
			paths = append(paths, string(p))
		}
		return paths, nil
	}
	return walkAll(root)
}

func walkAll(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			base := d.Name()
			if base == ".git" || base == ".shoka" || base == "node_modules" || base == "vendor" {
				return filepath.SkipDir
			}
			if d.Type()&os.ModeSymlink != 0 {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if shouldSkipPath(rel) {
			return nil
		}
		paths = append(paths, rel)
		return nil
	})
	return paths, err
}

func shouldSkipPath(rel string) bool {
	if rel == "." || rel == "" {
		return true
	}
	parts := strings.Split(rel, "/")
	for _, p := range parts {
		if p == ".git" || p == ".shoka" {
			return true
		}
	}
	base := parts[len(parts)-1]
	// Godot uid sidecars / import metadata are search noise.
	if strings.HasSuffix(base, ".uid") || strings.HasSuffix(base, ".import") {
		return true
	}
	return false
}

func hasGit(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".git"))
	return err == nil
}

func isTextUTF8(data []byte) bool {
	if bytes.IndexByte(data, 0) >= 0 {
		return false
	}
	if !utf8.Valid(data) {
		return false
	}
	n := len(data)
	if n == 0 {
		return false
	}
	if n > 8000 {
		n = 8000
	}
	ctrl := 0
	for _, b := range data[:n] {
		if b < 0x09 || (b > 0x0d && b < 0x20 && b != 0x1b) {
			ctrl++
		}
	}
	return ctrl*100/n < 5
}
