package search

import "testing"

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
	chunks := []rankedHit{
		{Path: "noise.md", StartLine: 1, EndLine: 10, Snippet: "UniqueWidget docs"},
		{Path: "widget.gd", StartLine: 1, EndLine: 80, Snippet: "class_name UniqueWidget"},
	}
	symbols := []rankedHit{
		{Path: "widget.gd", StartLine: 2, EndLine: 2, Name: "UniqueWidget", FromSym: true, Snippet: "class_name UniqueWidget"},
	}
	out := mergeRRFWithQuery(chunks, symbols, "UniqueWidget", 5, 2)
	if len(out) == 0 || out[0].Path != "widget.gd" {
		t.Fatalf("want widget.gd first, got %+v", out)
	}
	if out[0].StartLine != 2 {
		t.Fatalf("want symbol line 2, got %d", out[0].StartLine)
	}
}
