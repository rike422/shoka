package search

import (
	"sort"
	"strings"
	"unicode"
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
	Score     float64
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
					if h.FromSym {
						e.hit.FromSym = true
						e.hit.Name = h.Name
						e.hit.StartLine = h.StartLine
						e.hit.EndLine = h.EndLine
						if h.Snippet != "" {
							e.hit.Snippet = h.Snippet
						}
					} else if e.hit.Snippet == "" {
						e.hit.Snippet = h.Snippet
					}
					merged = true
					break
				}
			}
			if !merged {
				entries = append(entries, entry{hit: h, score: inc})
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

func rangesOverlap(a1, a2, b1, b2 int) bool {
	return a1 <= b2 && b1 <= a2
}
