package treesitter

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unsafe"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tsgo "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tspython "github.com/tree-sitter/tree-sitter-python/bindings/go"

	tsgdscript "github.com/rike422/shoka/third_party/tree_sitter/tree-sitter-gdscript/bindings/go"
)

//go:embed queries/go/tags.scm queries/python/tags.scm queries/gdscript/tags.scm
var queryFS embed.FS

// BundledLanguages is the allowlist for this build (v1 slice).
var BundledLanguages = []string{"go", "python", "gdscript"}

// GrammarRevisions pins module versions used in this build (for fingerprint/docs).
var GrammarRevisions = map[string]string{
	"go":       "github.com/tree-sitter/tree-sitter-go@v0.25.0",
	"python":   "github.com/tree-sitter/tree-sitter-python@v0.25.0",
	"gdscript": "github.com/PrestonKnopp/tree-sitter-gdscript@c5c8fa4861b5a4f04a7e60d97587fc3b6cc5639e",
}

const RuntimeVersion = "github.com/tree-sitter/go-tree-sitter@v0.25.0"
const SymbolTokenizerVersion = "1"

var extToLang = map[string]string{
	".go": "go",
	".py": "python",
	".gd": "gdscript",
}

type langEngine struct {
	name     string
	language *sitter.Language
	query    *sitter.Query
	querySrc string
}

// Extractor owns compiled languages/queries for enabled langs.
type Extractor struct {
	byLang map[string]*langEngine
	fp     string
}

// NewExtractor compiles queries for enabled languages (subset of BundledLanguages).
// enabled nil means all bundled; empty slice means symbols disabled.
func NewExtractor(enabled []string) (*Extractor, error) {
	langs, err := NormalizeEnabled(enabled)
	if err != nil {
		return nil, err
	}
	ex := &Extractor{byLang: map[string]*langEngine{}}
	for _, name := range langs {
		eng, err := newLangEngine(name)
		if err != nil {
			ex.Close()
			return nil, err
		}
		ex.byLang[name] = eng
	}
	ex.fp = computeFingerprint(langs, ex.byLang)
	return ex, nil
}

// NormalizeEnabled validates and sorts language names.
// nil → all bundled; empty → none.
func NormalizeEnabled(enabled []string) ([]string, error) {
	if enabled == nil {
		out := append([]string(nil), BundledLanguages...)
		sort.Strings(out)
		return out, nil
	}
	if len(enabled) == 0 {
		return nil, nil
	}
	seen := map[string]struct{}{}
	var out []string
	bundled := map[string]struct{}{}
	for _, b := range BundledLanguages {
		bundled[b] = struct{}{}
	}
	for _, raw := range enabled {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			return nil, fmt.Errorf("treesitter: empty language name")
		}
		if _, ok := bundled[name]; !ok {
			return nil, fmt.Errorf("treesitter: unknown or unbound language %q", name)
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("treesitter: duplicate language %q", name)
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

func newLangEngine(name string) (*langEngine, error) {
	var ptr unsafe.Pointer
	switch name {
	case "go":
		ptr = tsgo.Language()
	case "python":
		ptr = tspython.Language()
	case "gdscript":
		ptr = tsgdscript.Language()
	default:
		return nil, fmt.Errorf("treesitter: no grammar for %q", name)
	}
	language := sitter.NewLanguage(ptr)
	if language == nil {
		return nil, fmt.Errorf("treesitter: failed to load grammar %q", name)
	}
	qsrc, err := queryFS.ReadFile("queries/" + name + "/tags.scm")
	if err != nil {
		return nil, fmt.Errorf("treesitter: read tags for %s: %w", name, err)
	}
	query, qerr := sitter.NewQuery(language, string(qsrc))
	if qerr != nil {
		return nil, fmt.Errorf("treesitter: compile tags for %s: %w", name, qerr)
	}
	return &langEngine{name: name, language: language, query: query, querySrc: string(qsrc)}, nil
}

// Close releases query resources.
func (e *Extractor) Close() {
	if e == nil {
		return
	}
	for _, eng := range e.byLang {
		if eng.query != nil {
			eng.query.Close()
			eng.query = nil
		}
	}
	e.byLang = nil
}

// Fingerprint is stable across processes for the same enabled set + queries.
func (e *Extractor) Fingerprint() string {
	if e == nil {
		return computeFingerprint(nil, nil)
	}
	return e.fp
}

// Enabled returns sorted enabled language names.
func (e *Extractor) Enabled() []string {
	if e == nil || len(e.byLang) == 0 {
		return nil
	}
	out := make([]string, 0, len(e.byLang))
	for k := range e.byLang {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// LanguageForPath maps a relative path to an enabled language.
func (e *Extractor) LanguageForPath(relPath string) (string, bool) {
	if e == nil || len(e.byLang) == 0 {
		return "", false
	}
	ext := strings.ToLower(filepath.Ext(relPath))
	lang, ok := extToLang[ext]
	if !ok {
		return "", false
	}
	_, ok = e.byLang[lang]
	return lang, ok
}

func computeFingerprint(langs []string, engines map[string]*langEngine) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "runtime=%s\n", RuntimeVersion)
	_, _ = fmt.Fprintf(h, "symbol_tokenizer=%s\n", SymbolTokenizerVersion)
	for _, name := range langs {
		_, _ = fmt.Fprintf(h, "lang=%s\n", name)
		_, _ = fmt.Fprintf(h, "grammar=%s\n", GrammarRevisions[name])
		if eng, ok := engines[name]; ok {
			sum := sha256.Sum256([]byte(eng.querySrc))
			_, _ = fmt.Fprintf(h, "query=%s\n", hex.EncodeToString(sum[:]))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
