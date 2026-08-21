package tokenize

import (
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Text converts source text into whitespace-separated search tokens (index-time).
func Text(s string) string {
	return text(s, false)
}

// QueryText tokenizes a user query. Long CJK runs contribute bigrams only so
// partial matches work against longer indexed strings.
func QueryText(s string) string {
	return text(s, true)
}

func text(s string, query bool) string {
	var out []string
	seen := map[string]struct{}{}
	add := func(t string) {
		if t == "" {
			return
		}
		if _, ok := seen[t]; ok {
			return
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}

	for _, qn := range qualifiedNames(s) {
		if !query {
			add(strings.ToLower(qn))
		}
		add(strings.ToLower(stripQualifiers(qn)))
	}

	for _, p := range splitRaw(s) {
		lower := strings.ToLower(p)
		cjkOnly := isAllCJK(lower)
		if !query || !cjkOnly || utf8.RuneCountInString(lower) <= 2 {
			add(lower)
		}
		// Split camel/snake on original case, then lowercase pieces.
		for _, piece := range splitIdent(p) {
			add(strings.ToLower(piece))
		}
		for _, bg := range japaneseBigrams(lower) {
			add(bg)
		}
	}
	return strings.Join(out, " ")
}

// PathTerms tokenizes a relative path (dirs + basename + ext).
func PathTerms(relPath string) string {
	relPath = strings.ReplaceAll(relPath, "\\", "/")
	var b strings.Builder
	for _, r := range relPath {
		if r == '/' || r == '.' || r == '-' {
			b.WriteByte(' ')
		} else {
			b.WriteRune(r)
		}
	}
	return Text(b.String())
}

// BasenameTerms tokenizes only the file name (stem + extension).
func BasenameTerms(relPath string) string {
	relPath = strings.ReplaceAll(relPath, "\\", "/")
	base := filepath.Base(relPath)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	ext = strings.TrimPrefix(ext, ".")
	return Text(stem + " " + ext)
}

func splitRaw(s string) []string {
	var parts []string
	var cur []rune
	var kind int // 0 none, 1 alnum, 2 cjk
	flush := func() {
		if len(cur) > 0 {
			parts = append(parts, string(cur))
			cur = cur[:0]
			kind = 0
		}
	}
	for _, r := range s {
		// soft-split qualified names; segments still indexed
		if r == '.' || r == ':' {
			flush()
			continue
		}
		k := 0
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			if isCJK(r) {
				k = 2
			} else {
				k = 1
			}
		} else if isCJK(r) {
			k = 2
		}
		if k == 0 {
			flush()
			continue
		}
		if kind != 0 && kind != k {
			flush()
		}
		kind = k
		cur = append(cur, r)
	}
	flush()
	return parts
}

// qualifiedNames finds Foo::bar / pkg.Type chains and all prefixes.
func qualifiedNames(s string) []string {
	var out []string
	runes := []rune(s)
	for i := 0; i < len(runes); {
		if !isIdentStart(runes[i]) {
			i++
			continue
		}
		j := i + 1
		for j < len(runes) && isIdentCont(runes[j]) {
			j++
		}
		parts := []string{string(runes[i:j])}
		k := j
		for k < len(runes) {
			sep := 0
			if runes[k] == ':' && k+1 < len(runes) && runes[k+1] == ':' {
				sep = 2
			} else if runes[k] == '.' {
				sep = 1
			} else {
				break
			}
			n := k + sep
			if n >= len(runes) || !isIdentStart(runes[n]) {
				break
			}
			m := n + 1
			for m < len(runes) && isIdentCont(runes[m]) {
				m++
			}
			parts = append(parts, string(runes[n:m]))
			k = m
		}
		if len(parts) > 1 {
			for n := 2; n <= len(parts); n++ {
				sub := parts[:n]
				out = append(out, strings.Join(sub, "::"))
				out = append(out, strings.Join(sub, "."))
			}
		}
		i = k
		if i == j {
			i++
		}
	}
	return out
}

func stripQualifiers(s string) string {
	s = strings.ReplaceAll(s, "::", "")
	s = strings.ReplaceAll(s, ".", "")
	return s
}

func isIdentStart(r rune) bool {
	return unicode.IsLetter(r) || r == '_'
}

func isIdentCont(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func splitIdent(s string) []string {
	if s == "" {
		return nil
	}
	if strings.Contains(s, "_") {
		var out []string
		for _, p := range strings.Split(s, "_") {
			if p == "" {
				continue
			}
			out = append(out, p)
			out = append(out, splitCamel(p)...)
		}
		return out
	}
	return splitCamel(s)
}

func splitCamel(s string) []string {
	runes := []rune(s)
	if len(runes) < 2 {
		return nil
	}
	var parts []string
	start := 0
	for i := 1; i < len(runes); i++ {
		prev, cur := runes[i-1], runes[i]
		nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
		boundary := (unicode.IsLower(prev) && unicode.IsUpper(cur)) ||
			(unicode.IsUpper(prev) && unicode.IsUpper(cur) && nextLower) ||
			(unicode.IsLetter(prev) && unicode.IsDigit(cur)) ||
			(unicode.IsDigit(prev) && unicode.IsLetter(cur))
		if boundary {
			parts = append(parts, string(runes[start:i]))
			start = i
		}
	}
	parts = append(parts, string(runes[start:]))
	if len(parts) == 1 {
		return nil
	}
	return parts
}

func japaneseBigrams(s string) []string {
	var cjk []rune
	for _, r := range s {
		if isCJK(r) {
			cjk = append(cjk, r)
		}
	}
	if len(cjk) < 2 {
		return nil
	}
	out := make([]string, 0, len(cjk)-1)
	for i := 0; i < len(cjk)-1; i++ {
		out = append(out, string(cjk[i:i+2]))
	}
	return out
}

func isCJK(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana)
}

func isAllCJK(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !isCJK(r) {
			return false
		}
	}
	return true
}

// QueryTokens returns deduplicated tokens for an AND query.
func QueryTokens(q string) []string {
	tok := QueryText(q)
	if tok == "" {
		return nil
	}
	return strings.Fields(tok)
}

// FTS5Match builds a safe FTS5 MATCH expression (AND of quoted literals).
func FTS5Match(q string) (string, bool) {
	return fts5MatchJoin(q, " AND ")
}

// FTS5MatchAny is FTS5Match joined with OR (phrase recall when AND is empty).
func FTS5MatchAny(q string) (string, bool) {
	return fts5MatchJoin(q, " OR ")
}

func fts5MatchJoin(q, sep string) (string, bool) {
	tokens := QueryTokens(q)
	if len(tokens) == 0 {
		return "", false
	}
	parts := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if !utf8.ValidString(t) {
			continue
		}
		parts = append(parts, `"`+strings.ReplaceAll(t, `"`, `""`)+`"`)
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, sep), true
}
