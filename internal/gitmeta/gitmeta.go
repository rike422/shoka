package gitmeta

import (
	"bytes"
	"os/exec"
	"strings"
)

// HeadCommit returns the current HEAD sha, or empty if not a git repo.
func HeadCommit(root string) string {
	cmd := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// IsGitRepo reports whether root has a .git entry.
func IsGitRepo(root string) bool {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--is-inside-work-tree")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return bytes.Equal(bytes.TrimSpace(out), []byte("true"))
}
