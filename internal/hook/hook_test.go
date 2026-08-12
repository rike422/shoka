package hook_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rike422/shoka/internal/hook"
	"github.com/rike422/shoka/internal/testutil"
)

func TestInstallUninstall(t *testing.T) {
	dir := testutil.GitRepo(t)
	testutil.Write(t, filepath.Join(dir, "a.go"), "package a\n")
	testutil.CommitAll(t, dir, "init")

	if err := hook.Install(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".git", "hooks", "post-commit")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "shoka-index-hook") {
		t.Fatalf("%s", data)
	}
	if err := hook.Install(dir); err == nil {
		t.Fatal("expected already installed error")
	}
	if err := hook.Uninstall(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("hook still present")
	}
}

func TestInstallRejectsForeignHook(t *testing.T) {
	dir := testutil.GitRepo(t)
	testutil.Write(t, filepath.Join(dir, "a.go"), "package a\n")
	testutil.CommitAll(t, dir, "init")
	hookPath := filepath.Join(dir, ".git", "hooks", "post-commit")
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := hook.Install(dir); err == nil {
		t.Fatal("expected conflict")
	}
}
