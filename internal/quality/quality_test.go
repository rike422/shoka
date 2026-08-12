package quality_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rike422/shoka/internal/index"
	"github.com/rike422/shoka/internal/search"
	"github.com/rike422/shoka/internal/testutil"
)

type caseFile struct {
	Name     string  `json:"name"`
	Query    string  `json:"query"`
	WantPath string  `json:"want_path"`
	TopK     int     `json:"top_k"`
	Files    []qfile `json:"files"`
}

type qfile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func TestQualityFixtures(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	dir := filepath.Join(filepath.Dir(thisFile), "..", "..", "testdata", "quality", "cases")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		e := e
		t.Run(e.Name(), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			var c caseFile
			if err := json.Unmarshal(raw, &c); err != nil {
				t.Fatal(err)
			}
			repo := testutil.GitRepo(t)
			for _, f := range c.Files {
				testutil.Write(t, filepath.Join(repo, f.Path), f.Content)
			}
			testutil.CommitAll(t, repo, "fixture")
			if _, err := index.Build(repo, index.Options{}); err != nil {
				t.Fatal(err)
			}
			topK := c.TopK
			if topK == 0 {
				topK = 5
			}
			hits, err := search.Query(repo, c.Query, search.Options{TopK: topK})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, h := range hits {
				if h.Path == c.WantPath || strings.HasSuffix(h.Path, c.WantPath) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("query %q: want path %q in top-%d, got %+v", c.Query, c.WantPath, topK, hits)
			}
		})
	}
}
