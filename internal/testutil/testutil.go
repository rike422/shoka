package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// GitRepo creates a temp git repo with basic user config.
func GitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@example.com")
	run(t, dir, "git", "config", "user.name", "test")
	Write(t, filepath.Join(dir, ".gitignore"), ".shoka/\n")
	return dir
}

func Write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func CommitAll(t *testing.T, dir, msg string) {
	t.Helper()
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", msg)
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
