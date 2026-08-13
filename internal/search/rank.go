package search

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const rrfK = 60.0
const chunkRRFWeight = 1.0
const symbolRRFWeight = 2.0
const exactNameBonus = 1.0 / 30.0

type rankedHit struct {
	Path      string
	StartLine int
	EndLine   int
	Snippet   string
	Name      string
	FromSym   bool
	// defStart is the symbol definition line when known; preferred over chunk start.
	defStart int
	Score    float64
}

// IdentifierLike reports whether q should consult symbols_fts.
// Accepts a bare/qualified identifier, or a short phrase whose last token is one
// (e.g. "class_name UniqueWidget").
func IdentifierLike(q string) bool {
	_, ok := symbolQueryToken(q)
	return ok
}

var symbolLeadKeywords = map[string]struct{}{
	"class_name": {},
	"class":      {},
	"func":       {},
	"function":   {},
	"def":        {},
	"type":       {},
	"struct":     {},
	"interface":  {},
	"signal":     {},
	"const":      {},
	"var":        {},
	"method":     {},
	"fn":         {},
}

func symbolQueryToken(q string) (string, bool) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "", false
	}
	if isIdentOrQualified(q) {
		return q, true
	}
	parts := strings.Fields(q)
	if len(parts) == 2 {
		lead := strings.ToLower(parts[0])
		if _, ok := symbolLeadKeywords[lead]; ok && isIdentOrQualified(parts[1]) {
			return parts[1], true
		}
	}
	return "", false
}

func isIdentOrQualified(q string) bool {
	ok := false
	for _, r := range q {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			ok = true
			continue
		}
		if r == '.' || r == ':' || r == '/' {
			continue
		}
		return false
	}
	return ok
}

func exactNameMatch(symbolName, query string) bool {
	q := strings.TrimSpace(query)
	if symbolName == q {
		return true
	}
	seg := q
	if i := strings.LastIndexAny(q, ".:/"); i >= 0 && i+1 < len(q) {
		seg = q[i+1:]
	}
	return symbolName == seg
}

func mergeRRFWithQuery(chunks, symbols []rankedHit, query string, topK, maxPerFile int) []rankedHit {
	type entry struct {
		hit   rankedHit
		score float64
	}
	var entries []entry

	apply := func(list []rankedHit, weight float64) {
		for i, h := range list {
			inc := weight / (rrfK + float64(i+1))
			if h.FromSym && exactNameMatch(h.Name, query) {
				inc += exactNameBonus
			}
			merged := false
			for j := range entries {
				e := &entries[j]
				if e.hit.Path == h.Path && rangesOverlap(e.hit.StartLine, e.hit.EndLine, h.StartLine, h.EndLine) {
					e.score += inc
					mergeOverlappingHit(&e.hit, h)
					merged = true
					break
				}
			}
			if !merged {
				hit := h
				if hit.FromSym {
					hit.defStart = hit.StartLine
				}
				entries = append(entries, entry{hit: hit, score: inc})
			}
		}
	}
	apply(chunks, chunkRRFWeight)
	apply(symbols, symbolRRFWeight)

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].score != entries[j].score {
			return entries[i].score > entries[j].score
		}
		if entries[i].hit.Path != entries[j].hit.Path {
			return entries[i].hit.Path < entries[j].hit.Path
		}
		return entries[i].hit.StartLine < entries[j].hit.StartLine
	})

	perFile := map[string]int{}
	out := make([]rankedHit, 0, topK)
	for _, e := range entries {
		if perFile[e.hit.Path] >= maxPerFile {
			continue
		}
		perFile[e.hit.Path]++
		h := e.hit
		h.Score = e.score
		out = append(out, h)
		if len(out) >= topK {
			break
		}
	}
	return out
}

// mergeOverlappingHit unions line ranges, anchors StartLine to the first symbol
// definition when known, and prefers chunk bodies over symbol signatures without
// letting a later lower-ranked chunk overwrite an earlier chunk body.
func mergeOverlappingHit(dst *rankedHit, src rankedHit) {
	end := dst.EndLine
	if src.EndLine > end {
		end = src.EndLine
	}
	start := dst.StartLine
	if src.StartLine < start {
		start = src.StartLine
	}

	dstHadSym := dst.FromSym
	srcIsSym := src.FromSym
	alreadyHasChunkBody := !dstHadSym && strings.TrimSpace(dst.Snippet) != ""

	if srcIsSym {
		dst.FromSym = true
		// First symbol wins: later overlapping symbols must not steal Name/defStart.
		if dst.defStart == 0 {
			dst.Name = src.Name
			dst.defStart = src.StartLine
		}
	}
	if dst.defStart > 0 {
		dst.StartLine = dst.defStart
	} else {
		dst.StartLine = start
	}
	dst.EndLine = end

	switch {
	case !srcIsSym && strings.TrimSpace(src.Snippet) != "":
		if !alreadyHasChunkBody {
			dst.Snippet = src.Snippet
		}
	case srcIsSym && !dstHadSym:
		if strings.TrimSpace(dst.Snippet) == "" {
			dst.Snippet = src.Snippet
		}
	default:
		if richerSnippet(src.Snippet, dst.Snippet) {
			dst.Snippet = src.Snippet
		}
	}
}

func rangesOverlap(a1, a2, b1, b2 int) bool {
	return a1 <= b2 && b1 <= a2
}

// richerSnippet reports whether a should replace b as the displayed snippet.
func richerSnippet(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" {
		return false
	}
	if b == "" {
		return true
	}
	aLines := strings.Count(a, "\n")
	bLines := strings.Count(b, "\n")
	if aLines != bLines {
		return aLines > bLines
	}
	return utf8.RuneCountInString(a) > utf8.RuneCountInString(b)
}
