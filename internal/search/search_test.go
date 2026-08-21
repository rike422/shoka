package search_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rike422/shoka/internal/index"
	"github.com/rike422/shoka/internal/limits"
	"github.com/rike422/shoka/internal/search"
	"github.com/rike422/shoka/internal/testutil"
)

func TestIndexAndSearch(t *testing.T) {
	dir := testutil.GitRepo(t)
	testutil.Write(t, filepath.Join(dir, "auth", "user.go"), "package auth\n\nfunc getUserProfile() {}\n")
	testutil.Write(t, filepath.Join(dir, "auth", "UserService.go"), "package auth\n\ntype UserService struct{}\n")
	testutil.Write(t, filepath.Join(dir, "docs", "expiry.md"), "有効期限の設定について\n")
	testutil.CommitAll(t, dir, "init")

	st, err := index.Build(dir, index.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !st.FullRebuild || st.Files < 3 {
		t.Fatalf("stats=%+v", st)
	}

	hits, err := search.Query(dir, "getUserProfile", search.Options{TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || !strings.Contains(hits[0].Path, "user.go") {
		t.Fatalf("ident hits=%+v", hits)
	}

	hits, err = search.Query(dir, "有効期限", search.Options{TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || !strings.Contains(hits[0].Path, "expiry.md") {
		t.Fatalf("jp hits=%+v", hits)
	}

	hits, err = search.Query(dir, "UserService", search.Options{TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || !strings.Contains(hits[0].Path, "UserService.go") {
		t.Fatalf("basename hits=%+v", hits)
	}
}

func TestQualifiedSymbolSearch(t *testing.T) {
	dir := testutil.GitRepo(t)
	testutil.Write(t, filepath.Join(dir, "svc.go"), "package svc\n\nfunc call() { pkg.Type.Do(); Foo::bar() }\n")
	testutil.CommitAll(t, dir, "init")
	if _, err := index.Build(dir, index.Options{}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"Foo::bar", "pkg.Type"} {
		hits, err := search.Query(dir, q, search.Options{TopK: 5})
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) == 0 {
			t.Fatalf("no hits for %q", q)
		}
	}
}

func TestPerFileHitCap(t *testing.T) {
	dir := testutil.GitRepo(t)
	var b strings.Builder
	b.WriteString("package many\n")
	for i := 0; i < 300; i++ {
		b.WriteString("const UniqueTokenXYZ = 1\n")
	}
	testutil.Write(t, filepath.Join(dir, "many.go"), b.String())
	testutil.Write(t, filepath.Join(dir, "other.go"), "package other\nconst UniqueTokenXYZ = 2\n")
	testutil.CommitAll(t, dir, "init")
	if _, err := index.Build(dir, index.Options{}); err != nil {
		t.Fatal(err)
	}
	hits, err := search.Query(dir, "UniqueTokenXYZ", search.Options{TopK: 20})
	if err != nil {
		t.Fatal(err)
	}
	count := map[string]int{}
	for _, h := range hits {
		count[h.Path]++
	}
	if count["many.go"] > limits.MaxChunksPerFile {
		t.Fatalf("per-file cap broken: %v", count)
	}
}

func TestIncrementalIndex(t *testing.T) {
	dir := testutil.GitRepo(t)
	testutil.Write(t, filepath.Join(dir, "a.go"), "package a\nconst Alpha = 1\n")
	testutil.CommitAll(t, dir, "1")

	st1, err := index.Build(dir, index.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !st1.FullRebuild {
		t.Fatal("expected full rebuild")
	}

	st2, err := index.Build(dir, index.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if st2.FullRebuild || st2.Unchanged < 1 || st2.Added != 0 {
		t.Fatalf("expected incremental noop: %+v", st2)
	}

	time.Sleep(10 * time.Millisecond)
	testutil.Write(t, filepath.Join(dir, "a.go"), "package a\nconst Alpha = 2\nconst Beta = 3\n")
	testutil.Write(t, filepath.Join(dir, "b.go"), "package b\nconst Gamma = 1\n")
	testutil.CommitAll(t, dir, "2")

	st3, err := index.Build(dir, index.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if st3.FullRebuild || st3.Added < 1 || st3.Updated < 1 {
		t.Fatalf("expected add+update: %+v", st3)
	}

	for _, q := range []string{"Beta", "Gamma"} {
		hits, err := search.Query(dir, q, search.Options{TopK: 5})
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) == 0 {
			t.Fatalf("expected hit for %s", q)
		}
	}
}

func TestIncrementalDelete(t *testing.T) {
	dir := testutil.GitRepo(t)
	testutil.Write(t, filepath.Join(dir, "keep.go"), "package keep\nconst KeepMe = 1\n")
	testutil.Write(t, filepath.Join(dir, "drop.go"), "package drop\nconst DropMe = 1\n")
	testutil.CommitAll(t, dir, "1")
	if _, err := index.Build(dir, index.Options{}); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(dir, "drop.go")); err != nil {
		t.Fatal(err)
	}
	testutil.CommitAll(t, dir, "2")

	st, err := index.Build(dir, index.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if st.Removed < 1 {
		t.Fatalf("expected removal: %+v", st)
	}
	hits, err := search.Query(dir, "DropMe", search.Options{TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("deleted file still searchable: %+v", hits)
	}
}

func TestStaleHEAD(t *testing.T) {
	dir := testutil.GitRepo(t)
	testutil.Write(t, filepath.Join(dir, "a.go"), "package a\n")
	testutil.CommitAll(t, dir, "1")
	if _, err := index.Build(dir, index.Options{}); err != nil {
		t.Fatal(err)
	}
	testutil.Write(t, filepath.Join(dir, "b.go"), "package b\n")
	testutil.CommitAll(t, dir, "2")

	_, err := search.Query(dir, "package", search.Options{TopK: 5})
	if err == nil || !strings.Contains(err.Error(), "index stale") {
		t.Fatalf("expected stale error, got %v", err)
	}
}

func TestForceRebuild(t *testing.T) {
	dir := testutil.GitRepo(t)
	testutil.Write(t, filepath.Join(dir, "a.go"), "package a\n")
	testutil.CommitAll(t, dir, "1")
	if _, err := index.Build(dir, index.Options{}); err != nil {
		t.Fatal(err)
	}
	st, err := index.Build(dir, index.Options{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if !st.FullRebuild {
		t.Fatalf("%+v", st)
	}
}

func TestInspectStatus(t *testing.T) {
	dir := testutil.GitRepo(t)
	testutil.Write(t, filepath.Join(dir, "a.go"), "package a\n")
	testutil.CommitAll(t, dir, "1")
	st0, err := index.Inspect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st0.Exists {
		t.Fatal("should not exist yet")
	}
	if _, err := index.Build(dir, index.Options{}); err != nil {
		t.Fatal(err)
	}
	st, err := index.Inspect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Exists || st.Files < 1 || st.Chunks < 1 || st.SchemaVersion != limits.SchemaVersion {
		t.Fatalf("%+v", st)
	}
}

func TestEmptyQuery(t *testing.T) {
	dir := testutil.GitRepo(t)
	testutil.Write(t, filepath.Join(dir, "a.go"), "package a\n")
	testutil.CommitAll(t, dir, "1")
	if _, err := index.Build(dir, index.Options{}); err != nil {
		t.Fatal(err)
	}
	_, err := search.Query(dir, "!!!", search.Options{})
	if err == nil {
		t.Fatal("expected empty query error")
	}
}

func TestPhraseQueryRelaxesANDWhenEmpty(t *testing.T) {
	dir := testutil.GitRepo(t)
	testutil.Write(t, filepath.Join(dir, "notes.md"), "same-moment iteration processed events in the resolver\n")
	testutil.Write(t, filepath.Join(dir, "budget.md"), "event turn budget is tracked per window\n")
	testutil.CommitAll(t, dir, "init")
	if _, err := index.Build(dir, index.Options{}); err != nil {
		t.Fatal(err)
	}
	hits, err := search.Query(dir, "same-moment iteration processed event turn budget", search.Options{TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, h := range hits {
		found[h.Path] = true
	}
	if !found["notes.md"] || !found["budget.md"] {
		t.Fatalf("phrase OR fallback should hit both files, got %+v", hits)
	}
}

func TestAbsentIdentifierStaysEmptyDespitePieceMatches(t *testing.T) {
	dir := testutil.GitRepo(t)
	testutil.Write(t, filepath.Join(dir, "a.go"), "package a\n\n// the max tick per contests counter lives here\n")
	testutil.CommitAll(t, dir, "init")
	if _, err := index.Build(dir, index.Options{}); err != nil {
		t.Fatal(err)
	}
	hits, err := search.Query(dir, "MAX_CONTESTS_PER_TICK", search.Options{TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("absent identifier must stay empty, got %+v", hits)
	}
}

func TestGDScriptConstDefinitionRanksFirst(t *testing.T) {
	dir := testutil.GitRepo(t)
	var b strings.Builder
	b.WriteString("extends RefCounted\nclass_name ScheduledEvent\n\n")
	b.WriteString("const KIND_PHYSICAL_CONTEST := \"physical_contest\"\n\n")
	for i := 0; i < 90; i++ {
		b.WriteString("# pad line to push later references into another chunk\n")
	}
	b.WriteString("static func physical_contest():\n\treturn KIND_PHYSICAL_CONTEST\n\n")
	b.WriteString("static func rank(kind_value):\n\tmatch kind_value:\n")
	for i := 0; i < 8; i++ {
		b.WriteString("\t\tKIND_PHYSICAL_CONTEST:\n\t\t\treturn 1\n")
	}
	testutil.Write(t, filepath.Join(dir, "scheduled_event.gd"), b.String())
	testutil.CommitAll(t, dir, "init")
	if _, err := index.Build(dir, index.Options{}); err != nil {
		t.Fatal(err)
	}
	hits, err := search.Query(dir, "KIND_PHYSICAL_CONTEST", search.Options{TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Path != "scheduled_event.gd" {
		t.Fatalf("want scheduled_event.gd first, got %+v", hits)
	}
	if hits[0].StartLine != 4 {
		t.Fatalf("want const definition line 4, got %d (%s)", hits[0].StartLine, hits[0].Snippet)
	}
}
