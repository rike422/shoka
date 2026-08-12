package hook

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const marker = "# shoka-index-hook"

const hookBody = `#!/bin/sh
# shoka-index-hook
ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || exit 0
command -v shoka >/dev/null 2>&1 || exit 0
shoka index --root "$ROOT" >/dev/null 2>&1 || true
`

// Install writes .git/hooks/post-commit to run shoka index.
// Refuses to overwrite an existing foreign hook.
func Install(projectRoot string) error {
	hookPath, err := hookFile(projectRoot)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		return err
	}
	existing, err := os.ReadFile(hookPath)
	if err == nil {
		if strings.Contains(string(existing), marker) {
			return fmt.Errorf("shoka hook already installed")
		}
		return fmt.Errorf("post-commit hook already exists; remove or merge manually: %s", hookPath)
	}
	if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(hookPath, []byte(hookBody), 0o755)
}

// Uninstall removes the shoka post-commit hook when it is ours.
func Uninstall(projectRoot string) error {
	hookPath, err := hookFile(projectRoot)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(hookPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !strings.Contains(string(raw), marker) {
		return nil
	}
	return os.Remove(hookPath)
}

func hookFile(projectRoot string) (string, error) {
	cmd := exec.Command("git", "-C", projectRoot, "rev-parse", "--git-path", "hooks")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository: %w", err)
	}
	hooksDir := strings.TrimSpace(string(out))
	if hooksDir == "" {
		return "", fmt.Errorf("empty git hooks path")
	}
	if !filepath.IsAbs(hooksDir) {
		hooksDir = filepath.Join(projectRoot, hooksDir)
	}
	return filepath.Join(hooksDir, "post-commit"), nil
}
