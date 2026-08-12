package root_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rike422/shoka/internal/root"
	"github.com/rike422/shoka/internal/testutil"
)

func TestResolveExplicit(t *testing.T) {
	dir := t.TempDir()
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := root.Resolve(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("got %s want %s", got, dir)
	}
}

func TestResolveWalkShoka(t *testing.T) {
	dir := t.TempDir()
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".shoka"), 0o755); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	if err := os.Chdir(sub); err != nil {
		t.Fatal(err)
	}
	got, err := root.Resolve("", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("got %s want %s", got, dir)
	}
}

func TestResolveWalkGit(t *testing.T) {
	dir := testutil.GitRepo(t)
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	if err := os.Chdir(sub); err != nil {
		t.Fatal(err)
	}
	got, err := root.Resolve("", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("got %s want %s", got, dir)
	}
}

func TestResolveEnv(t *testing.T) {
	dir := t.TempDir()
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHOKA_ROOT", dir)
	got, err := root.Resolve("", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("got %s want %s", got, dir)
	}
}

func TestResolveMissingNoFallback(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHOKA_ROOT", "")
	if _, err := root.Resolve("", false); err == nil {
		t.Fatal("expected error")
	}
}

func TestIndexDB(t *testing.T) {
	got := root.IndexDB("/tmp/proj")
	want := filepath.Join("/tmp/proj", ".shoka", "index.db")
	if got != want {
		t.Fatalf("%s != %s", got, want)
	}
}
