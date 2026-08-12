package ignore_test

import (
	"path/filepath"
	"testing"

	"github.com/rike422/shoka/internal/ignore"
	"github.com/rike422/shoka/internal/testutil"
)

func TestLoadMissing(t *testing.T) {
	dir := t.TempDir()
	s, err := ignore.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Match("a.go") {
		t.Fatal("empty set should not match")
	}
}

func TestPatterns(t *testing.T) {
	dir := t.TempDir()
	testutil.Write(t, filepath.Join(dir, ".shokaignore"), `# comment
*.uid
build/
/secret.txt
!keep.uid
docs/**
`)
	s, err := ignore.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"foo.uid":        true,
		"a/b.uid":        true,
		"keep.uid":       false, // negated last for basename *.uid... wait order: *.uid sets true, !keep.uid sets false
		"build/x.go":     true,
		"src/build/x.go": true, // dir name build anywhere with build/
		"secret.txt":     true,
		"pkg/secret.txt": false, // rooted
		"docs/a.md":      true,
		"docs/a/b.md":    true,
		"ok.go":          false,
	}
	for path, want := range cases {
		if got := s.Match(path); got != want {
			t.Fatalf("%s: got %v want %v", path, got, want)
		}
	}
}
