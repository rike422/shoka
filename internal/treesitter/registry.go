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
	tsjavascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tspython "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tsruby "github.com/tree-sitter/tree-sitter-ruby/bindings/go"
	tstypescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"

	tsgdscript "github.com/rike422/shoka/third_party/tree_sitter/tree-sitter-gdscript/bindings/go"
)

//go:embed queries/go/tags.scm queries/python/tags.scm queries/gdscript/tags.scm queries/ruby/tags.scm queries/javascript/tags.scm queries/typescript/tags.scm queries/tsx/tags.scm
var queryFS embed.FS

// BundledLanguages is the user-facing allowlist for this build.
var BundledLanguages = []string{"go", "python", "gdscript", "ruby", "javascript", "typescript"}

// GrammarRevisions pins module versions used in this build (for fingerprint/docs).
var GrammarRevisions = map[string]string{
	"go":         "github.com/tree-sitter/tree-sitter-go@v0.25.0",
	"python":     "github.com/tree-sitter/tree-sitter-python@v0.25.0",
	"gdscript":   "github.com/PrestonKnopp/tree-sitter-gdscript@c5c8fa4861b5a4f04a7e60d97587fc3b6cc5639e",
	"ruby":       "github.com/tree-sitter/tree-sitter-ruby@v0.23.1",
	"javascript": "github.com/tree-sitter/tree-sitter-javascript@v0.25.0",
	"typescript": "github.com/tree-sitter/tree-sitter-typescript@v0.23.2",
	"tsx":        "github.com/tree-sitter/tree-sitter-typescript@v0.23.2",
}

const RuntimeVersion = "github.com/tree-sitter/go-tree-sitter@v0.25.0"
const SymbolTokenizerVersion = "1"

var extToLang = map[string]string{
	".go":  "go",
	".py":  "python",
	".gd":  "gdscript",
	".rb":  "ruby",
	".js":  "javascript",
	".jsx": "javascript",
	".mjs": "javascript",
	".cjs": "javascript",
	".ts":  "typescript",
	".tsx": "tsx",
	".mts": "typescript",
	".cts": "typescript",
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

// NormalizeEnabled validates and sorts language names into internal engine names.
// nil → all bundled; empty → none.
// User-facing "typescript" expands to internal engines "typescript" and "tsx".
func NormalizeEnabled(enabled []string) ([]string, error) {
	if enabled == nil {
		return expandUserLangs(BundledLanguages)
	}
	if len(enabled) == 0 {
		return nil, nil
	}
	seenUser := map[string]struct{}{}
	var user []string
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
		if _, ok := seenUser[name]; ok {
			return nil, fmt.Errorf("treesitter: duplicate language %q", name)
		}
		seenUser[name] = struct{}{}
		user = append(user, name)
	}
	return expandUserLangs(user)
}

func expandUserLangs(user []string) ([]string, error) {
	seen := map[string]struct{}{}
	var out []string
	add := func(name string) {
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	for _, name := range user {
		add(name)
		if name == "typescript" {
			add("tsx")
		}
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
	case "ruby":
		ptr = tsruby.Language()
	case "javascript":
		ptr = tsjavascript.Language()
	case "typescript":
		ptr = tstypescript.LanguageTypescript()
	case "tsx":
		ptr = tstypescript.LanguageTSX()
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

// Enabled returns sorted user-facing language names (tsx folded into typescript).
func (e *Extractor) Enabled() []string {
	if e == nil || len(e.byLang) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for k := range e.byLang {
		name := k
		if name == "tsx" {
			name = "typescript"
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// LanguageForPath maps a relative path to an enabled language engine name.
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
