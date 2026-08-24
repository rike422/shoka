package sessionfixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteEpisodeCorpusCreatesIsolatedCodexSources(t *testing.T) {
	root := filepath.Join(t.TempDir(), "fixture")
	corpus, err := WriteEpisodeCorpus(root, 64)
	if err != nil {
		t.Fatal(err)
	}
	if corpus.Home != filepath.Join(root, "home") || corpus.StateDir != filepath.Join(root, "state") {
		t.Fatalf("fixture paths escaped root: %+v", corpus)
	}
	if corpus.Sessions != 64 {
		t.Fatalf("sessions = %d, want 64", corpus.Sessions)
	}
	files, err := filepath.Glob(filepath.Join(corpus.Home, ".codex", "sessions", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 64 {
		t.Fatalf("source files = %d, want 64", len(files))
	}
	first, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{
		"Keep the export manifest stable",
		"Design direction:",
		"Only change the episode manifest scope",
		"違う、既存のexport contractを修正して",
		"go test ./... failed",
		"compiler error",
	} {
		if !strings.Contains(string(first), marker) {
			t.Errorf("scenario fixture lacks marker %q", marker)
		}
	}
}

func TestWriteEpisodeCorpusRejectsInvalidOrOccupiedDestination(t *testing.T) {
	if _, err := WriteEpisodeCorpus(t.TempDir(), 0); err == nil {
		t.Fatal("zero sessions were accepted")
	}
	if _, err := WriteEpisodeCorpus("", 1); err == nil {
		t.Fatal("empty destination was accepted")
	}
	root := filepath.Join(t.TempDir(), "fixture")
	if _, err := WriteEpisodeCorpus(root, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteEpisodeCorpus(root, 1); err == nil {
		t.Fatal("occupied fixture destination was overwritten")
	}
	parentFile := filepath.Join(t.TempDir(), "parent-file")
	if err := os.WriteFile(parentFile, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteEpisodeCorpus(filepath.Join(parentFile, "fixture"), 1); err == nil {
		t.Fatal("unwritable fixture parent was accepted")
	}
}
