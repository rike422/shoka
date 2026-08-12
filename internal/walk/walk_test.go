package walk_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rike422/shoka/internal/testutil"
	"github.com/rike422/shoka/internal/walk"
)

func TestListSkipsBinaryAndEmpty(t *testing.T) {
	dir := t.TempDir()
	testutil.Write(t, filepath.Join(dir, "ok.go"), "package ok\n")
	testutil.Write(t, filepath.Join(dir, "empty.txt"), "")
	if err := os.WriteFile(filepath.Join(dir, "bin.dat"), []byte{0, 1, 2, 3, 0}, 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := walk.List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].RelPath != "ok.go" {
		t.Fatalf("%+v", files)
	}
	if files[0].Hash == "" {
		t.Fatal("expected hash")
	}
}

func TestListSkipsSymlink(t *testing.T) {
	dir := t.TempDir()
	testutil.Write(t, filepath.Join(dir, "real.go"), "package real\n")
	if err := os.Symlink(filepath.Join(dir, "real.go"), filepath.Join(dir, "link.go")); err != nil {
		t.Fatal(err)
	}
	files, err := walk.List(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.RelPath == "link.go" {
			t.Fatal("symlink should be skipped")
		}
	}
}

func TestListRespectsGitignore(t *testing.T) {
	dir := testutil.GitRepo(t)
	testutil.Write(t, filepath.Join(dir, ".gitignore"), ".shoka/\nsecret.txt\n")
	testutil.Write(t, filepath.Join(dir, "keep.go"), "package keep\n")
	testutil.Write(t, filepath.Join(dir, "secret.txt"), "nope\n")
	testutil.CommitAll(t, dir, "init")

	files, err := walk.List(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.RelPath == "secret.txt" {
			t.Fatal("ignored file listed")
		}
	}
}

func TestListRespectsShokaIgnore(t *testing.T) {
	dir := testutil.GitRepo(t)
	testutil.Write(t, filepath.Join(dir, ".shokaignore"), "noise.txt\n*.tmp\n")
	testutil.Write(t, filepath.Join(dir, "keep.go"), "package keep\n")
	testutil.Write(t, filepath.Join(dir, "noise.txt"), "nope\n")
	testutil.Write(t, filepath.Join(dir, "x.tmp"), "tmp\n")
	testutil.CommitAll(t, dir, "init")

	files, err := walk.List(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.RelPath == "noise.txt" || f.RelPath == "x.tmp" {
			t.Fatalf("shokaignore failed: %+v", files)
		}
	}
	found := false
	for _, f := range files {
		if f.RelPath == "keep.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("keep.go missing: %+v", files)
	}
}

func TestListMetaAndRead(t *testing.T) {
	dir := t.TempDir()
	testutil.Write(t, filepath.Join(dir, "a.go"), "package a\n")
	metas, err := walk.ListMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 {
		t.Fatalf("%+v", metas)
	}
	f, ok, err := walk.Read(dir, metas[0])
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if f.Content != "package a\n" || f.Hash == "" {
		t.Fatalf("%+v", f)
	}
}
