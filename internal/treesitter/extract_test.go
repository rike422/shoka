package treesitter_test

import (
	"testing"

	"github.com/rike422/shoka/internal/treesitter"
)

func TestExtractGo(t *testing.T) {
	ex, err := treesitter.NewExtractor([]string{"go"})
	if err != nil {
		t.Fatal(err)
	}
	defer ex.Close()
	src := []byte(`package p

type Foo struct{}

func (f *Foo) Bar() {}

func Baz() {}
`)
	syms, err := ex.Extract("a.go", src)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range syms {
		got[s.Name] = s.Kind
	}
	for _, want := range []struct{ n, k string }{
		{"Foo", treesitter.KindType},
		{"Bar", treesitter.KindMethod},
		{"Baz", treesitter.KindFunction},
	} {
		if got[want.n] != want.k {
			t.Fatalf("symbol %s: got kind %q want %q; all=%v", want.n, got[want.n], want.k, got)
		}
	}
}

func TestExtractPython(t *testing.T) {
	ex, err := treesitter.NewExtractor([]string{"python"})
	if err != nil {
		t.Fatal(err)
	}
	defer ex.Close()
	src := []byte("class Foo:\n    def bar(self):\n        pass\n\ndef baz():\n    pass\n")
	syms, err := ex.Extract("a.py", src)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range syms {
		got[s.Name] = s.Kind
	}
	if got["Foo"] != treesitter.KindClass || got["bar"] != treesitter.KindFunction || got["baz"] != treesitter.KindFunction {
		t.Fatalf("unexpected symbols: %v", got)
	}
}

func TestExtractGDScriptClassName(t *testing.T) {
	ex, err := treesitter.NewExtractor([]string{"gdscript"})
	if err != nil {
		t.Fatal(err)
	}
	defer ex.Close()
	src := []byte(`extends Node
class_name UniqueWidget
signal clicked
func do_thing():
	pass
`)
	syms, err := ex.Extract("widget.gd", src)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range syms {
		got[s.Name] = s.Kind
	}
	if got["UniqueWidget"] != treesitter.KindClass {
		t.Fatalf("class_name missing: %v", got)
	}
	if got["clicked"] != treesitter.KindSignal {
		t.Fatalf("signal missing: %v", got)
	}
	if got["do_thing"] != treesitter.KindFunction {
		t.Fatalf("func missing: %v", got)
	}
}

func TestNormalizeEnabled(t *testing.T) {
	all, err := treesitter.NormalizeEnabled(nil)
	if err != nil || len(all) != 3 {
		t.Fatalf("nil: %v %v", all, err)
	}
	none, err := treesitter.NormalizeEnabled([]string{})
	if err != nil || none != nil {
		t.Fatalf("empty: %v %v", none, err)
	}
	if _, err := treesitter.NormalizeEnabled([]string{"ruby"}); err == nil {
		t.Fatal("expected unknown lang error")
	}
	if _, err := treesitter.NormalizeEnabled([]string{"go", "go"}); err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestDisabledExtractor(t *testing.T) {
	ex, err := treesitter.NewExtractor([]string{})
	if err != nil {
		t.Fatal(err)
	}
	defer ex.Close()
	if lang, ok := ex.LanguageForPath("a.go"); ok || lang != "" {
		t.Fatalf("disabled should not map: %s %v", lang, ok)
	}
	syms, err := ex.Extract("a.go", []byte("package p\nfunc F(){}\n"))
	if err != nil || len(syms) != 0 {
		t.Fatalf("disabled extract: %v %v", syms, err)
	}
}
