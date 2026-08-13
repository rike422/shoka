package index_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rike422/shoka/internal/index"
	"github.com/rike422/shoka/internal/search"
	"github.com/rike422/shoka/internal/testutil"
)

func TestIndexSymbolsAndSearch(t *testing.T) {
	repo := testutil.GitRepo(t)
	testutil.Write(t, filepath.Join(repo, "actors/unique_widget.gd"), "extends Node\nclass_name UniqueWidget\nfunc do_thing():\n\tpass\n")
	testutil.Write(t, filepath.Join(repo, "docs/UniqueWidget.md"), "UniqueWidget UniqueWidget UniqueWidget\n")
	testutil.CommitAll(t, repo, "init")

	st, err := index.Build(repo, index.Options{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if st.Symbols < 1 {
		t.Fatalf("expected symbols, got %+v", st)
	}

	hits, err := search.Query(repo, "UniqueWidget", search.Options{TopK: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Path != "actors/unique_widget.gd" {
		t.Fatalf("want gd definition first, got %+v", hits)
	}
	if !strings.Contains(hits[0].Snippet, "func do_thing") {
		t.Fatalf("class-name query should keep chunk body, got %q", hits[0].Snippet)
	}
	if hits[0].EndLine <= hits[0].StartLine {
		t.Fatalf("want expanded line range, got %d-%d", hits[0].StartLine, hits[0].EndLine)
	}
}

func TestSymbolsDisabled(t *testing.T) {
	repo := testutil.GitRepo(t)
	testutil.Write(t, filepath.Join(repo, ".shoka.toml"), "[treesitter]\nlanguages = []\n")
	testutil.Write(t, filepath.Join(repo, "a.go"), "package p\nfunc Hello() {}\n")
	testutil.CommitAll(t, repo, "init")
	st, err := index.Build(repo, index.Options{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if st.Symbols != 0 {
		t.Fatalf("want 0 symbols when disabled, got %d", st.Symbols)
	}
}

func TestIncrementalUnchangedSkips(t *testing.T) {
	repo := testutil.GitRepo(t)
	testutil.Write(t, filepath.Join(repo, "a.go"), "package p\nfunc Hello() {}\n")
	testutil.CommitAll(t, repo, "init")
	if _, err := index.Build(repo, index.Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	st, err := index.Build(repo, index.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if st.FullRebuild || st.Unchanged < 1 {
		t.Fatalf("want incremental unchanged, got %+v", st)
	}
}

func TestFingerprintChangeRebuilds(t *testing.T) {
	repo := testutil.GitRepo(t)
	testutil.Write(t, filepath.Join(repo, "a.go"), "package p\nfunc Hello() {}\n")
	testutil.CommitAll(t, repo, "init")
	if _, err := index.Build(repo, index.Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".shoka.toml"), []byte("[treesitter]\nlanguages = [\"go\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := index.Build(repo, index.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !st.FullRebuild {
		t.Fatalf("language config change should full rebuild, got %+v", st)
	}
}
