package root

import (
	"fmt"
	"os"
	"path/filepath"
)

// Resolve finds the repository root.
// Priority: explicit > SHOKA_ROOT > walk-up (.shoka then .git per level) > cwdFallback.
func Resolve(explicit string, cwdFallback bool) (string, error) {
	if explicit != "" {
		return normalize(explicit)
	}
	if env := os.Getenv("SHOKA_ROOT"); env != "" {
		return normalize(env)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}

	dir := cwd
	for {
		if info, err := os.Stat(filepath.Join(dir, ".shoka")); err == nil && info.IsDir() {
			return dir, nil
		}
		if hasGit(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	if cwdFallback {
		return cwd, nil
	}
	return "", fmt.Errorf("could not resolve project root (set --root or SHOKA_ROOT)")
}

func normalize(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("root %q: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("root %q is not a directory", abs)
	}
	return abs, nil
}

func hasGit(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && (info.IsDir() || info.Mode().IsRegular())
}

// IndexDB returns root/.shoka/index.db.
func IndexDB(root string) string {
	return filepath.Join(root, ".shoka", "index.db")
}
