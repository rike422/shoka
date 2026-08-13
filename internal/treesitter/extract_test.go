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

func TestExtractRuby(t *testing.T) {
	ex, err := treesitter.NewExtractor([]string{"ruby"})
	if err != nil {
		t.Fatal(err)
	}
	defer ex.Close()
	src := []byte(`module Bar
  class Foo
    def baz
    end
    def self.qux
    end
  end
end
`)
	syms, err := ex.Extract("a.rb", src)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range syms {
		got[s.Name] = s.Kind
	}
	if got["Bar"] != "module" || got["Foo"] != treesitter.KindClass {
		t.Fatalf("class/module missing: %v", got)
	}
	if got["baz"] != treesitter.KindMethod || got["qux"] != treesitter.KindMethod {
		t.Fatalf("methods missing: %v", got)
	}
}

func TestExtractJavaScript(t *testing.T) {
	ex, err := treesitter.NewExtractor([]string{"javascript"})
	if err != nil {
		t.Fatal(err)
	}
	defer ex.Close()
	src := []byte(`class Foo {
  bar() {}
}
function baz() {}
const qux = () => {};
`)
	syms, err := ex.Extract("a.js", src)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range syms {
		got[s.Name] = s.Kind
	}
	if got["Foo"] != treesitter.KindClass || got["bar"] != treesitter.KindMethod {
		t.Fatalf("class/method missing: %v", got)
	}
	if got["baz"] != treesitter.KindFunction || got["qux"] != treesitter.KindFunction {
		t.Fatalf("functions missing: %v", got)
	}
}

func TestExtractTypeScriptAndTSX(t *testing.T) {
	ex, err := treesitter.NewExtractor([]string{"typescript"})
	if err != nil {
		t.Fatal(err)
	}
	defer ex.Close()
	if lang, ok := ex.LanguageForPath("a.ts"); !ok || lang != "typescript" {
		t.Fatalf("ts map: %s %v", lang, ok)
	}
	if lang, ok := ex.LanguageForPath("a.tsx"); !ok || lang != "tsx" {
		t.Fatalf("tsx map: %s %v", lang, ok)
	}
	enabled := ex.Enabled()
	if len(enabled) != 1 || enabled[0] != "typescript" {
		t.Fatalf("display langs want [typescript], got %v", enabled)
	}

	ts := []byte(`interface Foo { x: number }
type Bar = string
abstract class Baz {
  abstract qux(): void
}
class Qux {}
function hello() {}
`)
	syms, err := ex.Extract("a.ts", ts)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range syms {
		got[s.Name] = s.Kind
	}
	for _, want := range []struct{ n, k string }{
		{"Foo", "interface"},
		{"Bar", treesitter.KindType},
		{"Baz", treesitter.KindClass},
		{"Qux", treesitter.KindClass},
		{"hello", treesitter.KindFunction},
	} {
		if got[want.n] != want.k {
			t.Fatalf("ts symbol %s: got %q want %q; all=%v", want.n, got[want.n], want.k, got)
		}
	}

	tsx := []byte(`export function Widget() { return null }
`)
	syms, err = ex.Extract("a.tsx", tsx)
	if err != nil {
		t.Fatal(err)
	}
	got = map[string]string{}
	for _, s := range syms {
		got[s.Name] = s.Kind
	}
	if got["Widget"] != treesitter.KindFunction {
		t.Fatalf("tsx function missing: %v", got)
	}
}

func TestNormalizeEnabled(t *testing.T) {
	all, err := treesitter.NormalizeEnabled(nil)
	if err != nil {
		t.Fatal(err)
	}
	// 6 user langs; typescript expands to typescript+tsx → 7 engines
	if len(all) != 7 {
		t.Fatalf("nil engines: %v", all)
	}
	none, err := treesitter.NormalizeEnabled([]string{})
	if err != nil || none != nil {
		t.Fatalf("empty: %v %v", none, err)
	}
	ts, err := treesitter.NormalizeEnabled([]string{"typescript"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 2 || ts[0] != "tsx" || ts[1] != "typescript" {
		t.Fatalf("typescript expand: %v", ts)
	}
	if _, err := treesitter.NormalizeEnabled([]string{"cobol"}); err == nil {
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
