package search

import (
	"strings"
	"testing"
)

func TestIdentifierLike(t *testing.T) {
	yes := []string{"Foo", "foo_bar", "pkg.Type", "Foo::bar", "a.b.c", "class_name UniqueWidget", "func Baz"}
	no := []string{"", "find the widget please now", "a,b", "how does auth work", "foo bar"}
	for _, q := range yes {
		if !IdentifierLike(q) {
			t.Fatalf("want true for %q", q)
		}
	}
	for _, q := range no {
		if IdentifierLike(q) {
			t.Fatalf("want false for %q", q)
		}
	}
	tok, ok := symbolQueryToken("class_name UniqueWidget")
	if !ok || tok != "UniqueWidget" {
		t.Fatalf("token=%q ok=%v", tok, ok)
	}
}

func TestMergeRRFExactName(t *testing.T) {
	chunkBody := "extends Node\nclass_name UniqueWidget\n\nfunc do_thing():\n\tpass\nfunc other():\n\treturn 1\n"
	chunks := []rankedHit{
		{Path: "noise.md", StartLine: 1, EndLine: 10, Snippet: "UniqueWidget docs"},
		{Path: "widget.gd", StartLine: 1, EndLine: 80, Snippet: chunkBody},
	}
	symbols := []rankedHit{
		{Path: "widget.gd", StartLine: 2, EndLine: 2, Name: "UniqueWidget", FromSym: true, Snippet: "class_name UniqueWidget"},
	}
	out := mergeRRFWithQuery(chunks, symbols, "UniqueWidget", 5, 2)
	if len(out) == 0 || out[0].Path != "widget.gd" {
		t.Fatalf("want widget.gd first, got %+v", out)
	}
	if out[0].StartLine != 2 {
		t.Fatalf("want symbol definition line 2, got %d", out[0].StartLine)
	}
	if out[0].EndLine < 80 {
		t.Fatalf("want expanded end covering chunk, got %d", out[0].EndLine)
	}
	if !strings.Contains(out[0].Snippet, "func do_thing") {
		t.Fatalf("want chunk body snippet, got %q", out[0].Snippet)
	}
}

func TestMergeRRFChunkAfterSymbolKeepsBody(t *testing.T) {
	chunkBody := "class_name LineAnchor\n\nfunc contest_state():\n\treturn {}\n"
	// Put a decoy first so the target chunk is not rank-0; apply() still processes
	// symbols before that chunk merges onto the symbol entry.
	symbols := []rankedHit{
		{Path: "line_anchor.gd", StartLine: 5, EndLine: 5, Name: "LineAnchor", FromSym: true, Snippet: "class_name LineAnchor"},
	}
	chunks := []rankedHit{
		{Path: "other.gd", StartLine: 1, EndLine: 10, Snippet: "noise"},
		{Path: "line_anchor.gd", StartLine: 1, EndLine: 40, Snippet: chunkBody},
	}
	out := mergeRRFWithQuery(chunks, symbols, "LineAnchor", 5, 2)
	if len(out) == 0 {
		t.Fatal("empty")
	}
	var hit rankedHit
	for _, h := range out {
		if h.Path == "line_anchor.gd" {
			hit = h
			break
		}
	}
	if hit.Path == "" {
		t.Fatalf("missing line_anchor.gd in %+v", out)
	}
	if hit.StartLine != 5 || hit.EndLine < 40 {
		t.Fatalf("want definition start 5 + chunk end, got %d-%d", hit.StartLine, hit.EndLine)
	}
	if !strings.Contains(hit.Snippet, "contest_state") {
		t.Fatalf("want body snippet, got %q", hit.Snippet)
	}
}

func TestMergeRRFLongSignatureDoesNotBeatChunk(t *testing.T) {
	longSig := "func with_lots_of_params(a: int, b: float, c: String, d: Node, e: Dictionary) -> void"
	chunkBody := "func foo():\n\treturn 1\n"
	chunks := []rankedHit{
		{Path: "a.gd", StartLine: 1, EndLine: 20, Snippet: chunkBody},
	}
	symbols := []rankedHit{
		{Path: "a.gd", StartLine: 1, EndLine: 1, Name: "foo", FromSym: true, Snippet: longSig},
	}
	out := mergeRRFWithQuery(chunks, symbols, "foo", 5, 2)
	if len(out) == 0 || !strings.Contains(out[0].Snippet, "return 1") {
		t.Fatalf("chunk body should win over long signature, got %q", out[0].Snippet)
	}
}

func TestMergeRRFLaterChunkDoesNotOverwriteEarlier(t *testing.T) {
	best := "class Foo\nfunc best():\n\treturn 1\n"
	worse := "class Foo\n# noise only\n"
	chunks := []rankedHit{
		{Path: "a.gd", StartLine: 1, EndLine: 40, Snippet: best},
		{Path: "a.gd", StartLine: 1, EndLine: 40, Snippet: worse},
	}
	symbols := []rankedHit{
		{Path: "a.gd", StartLine: 1, EndLine: 1, Name: "Foo", FromSym: true, Snippet: "class_name Foo"},
	}
	out := mergeRRFWithQuery(chunks, symbols, "Foo", 5, 2)
	if len(out) == 0 || !strings.Contains(out[0].Snippet, "best") {
		t.Fatalf("earlier chunk body should win, got %q", out[0].Snippet)
	}
}

func TestMergeRRFFirstSymbolKeepsDefStart(t *testing.T) {
	chunks := []rankedHit{
		{Path: "a.gd", StartLine: 1, EndLine: 80, Snippet: "class_name Foo\nfunc bar():\n\tpass\n"},
	}
	symbols := []rankedHit{
		{Path: "a.gd", StartLine: 1, EndLine: 1, Name: "Foo", FromSym: true, Snippet: "class_name Foo"},
		{Path: "a.gd", StartLine: 2, EndLine: 2, Name: "bar", FromSym: true, Snippet: "func bar"},
	}
	out := mergeRRFWithQuery(chunks, symbols, "Foo", 5, 2)
	if len(out) == 0 {
		t.Fatal("empty")
	}
	if out[0].StartLine != 1 || out[0].Name != "Foo" {
		t.Fatalf("first symbol should keep def, got start=%d name=%q", out[0].StartLine, out[0].Name)
	}
}

func TestRicherSnippet(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"abc", "", true},
		{"", "abc", false},
		{"", "", false},
		{"abcd", "abc", true},
		{"abc", "abcd", false},
		{"abc", "abc", false},
		{"  ", "x", false},
		{"a\nb\nc", "long single line signature here", true},
		{"one line", "a\nb", false},
	}
	for _, c := range cases {
		if got := richerSnippet(c.a, c.b); got != c.want {
			t.Errorf("richerSnippet(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
