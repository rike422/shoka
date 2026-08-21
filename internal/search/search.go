package search

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/rike422/shoka/internal/index"
	"github.com/rike422/shoka/internal/limits"
	"github.com/rike422/shoka/internal/tokenize"
)

// Hit is one search result.
type Hit struct {
	Path      string  `json:"path"`
	AbsPath   string  `json:"abs_path"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Score     float64 `json:"score"`
	Snippet   string  `json:"snippet"`
}

// Options configures a search.
type Options struct {
	TopK int
}

// Query runs BM25 (+ optional symbol RRF) against the project index.
func Query(projectRoot, q string, opt Options) ([]Hit, error) {
	if opt.TopK <= 0 {
		opt.TopK = limits.DefaultTopK
	}
	if opt.TopK > limits.MaxTopK {
		opt.TopK = limits.MaxTopK
	}

	match, ok := tokenize.FTS5Match(q)
	if !ok {
		return nil, fmt.Errorf("empty query after tokenization")
	}

	db, err := index.Open(projectRoot)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	if err := index.EnsureFresh(db, projectRoot); err != nil {
		return nil, err
	}

	limit := opt.TopK * limits.MaxChunksPerFile * 20
	if limit < opt.TopK {
		limit = opt.TopK
	}

	chunks, err := queryChunks(db, match, limit)
	if err != nil {
		return nil, err
	}
	if len(chunks) == 0 && !IdentifierLike(q) {
		if any, ok := tokenize.FTS5MatchAny(q); ok && any != match {
			chunks, err = queryChunks(db, any, limit)
			if err != nil {
				return nil, err
			}
		}
	}

	var symbols []rankedHit
	if symTok, ok := symbolQueryToken(q); ok && hasSymbolsFTS(db) {
		symMatch, sok := tokenize.FTS5Match(symTok)
		if sok {
			symbols, err = querySymbols(db, symMatch, limit)
			if err != nil {
				return nil, err
			}
			q = symTok // exact-name bonus against the identifier token
		}
	}

	if len(symbols) == 0 {
		return finalizeChunkOnly(projectRoot, chunks, opt.TopK), nil
	}

	merged := mergeRRFWithQuery(chunks, symbols, q, opt.TopK, limits.MaxChunksPerFile)
	out := make([]Hit, 0, len(merged))
	for _, h := range merged {
		out = append(out, Hit{
			Path:      h.Path,
			AbsPath:   filepath.Join(projectRoot, filepath.FromSlash(h.Path)),
			StartLine: h.StartLine,
			EndLine:   h.EndLine,
			Score:     h.Score,
			Snippet:   snippet(h.Snippet),
		})
	}
	return out, nil
}

func finalizeChunkOnly(projectRoot string, chunks []rankedHit, topK int) []Hit {
	perFile := map[string]int{}
	out := make([]Hit, 0, topK)
	for _, h := range chunks {
		if perFile[h.Path] >= limits.MaxChunksPerFile {
			continue
		}
		dup := false
		for _, prev := range out {
			if prev.Path == h.Path && chunkOverlap(prev.StartLine, prev.EndLine, h.StartLine, h.EndLine) {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		perFile[h.Path]++
		out = append(out, Hit{
			Path:      h.Path,
			AbsPath:   filepath.Join(projectRoot, filepath.FromSlash(h.Path)),
			StartLine: h.StartLine,
			EndLine:   h.EndLine,
			Score:     h.Score,
			Snippet:   snippet(h.Snippet),
		})
		if len(out) >= topK {
			break
		}
	}
	return out
}

func queryChunks(db *sql.DB, match string, limit int) ([]rankedHit, error) {
	rows, err := db.Query(`
		SELECT c.path, c.start_line, c.end_line, c.raw_body,
		       bm25(chunks_fts, 20.0, 8.0, 1.0) AS score
		FROM chunks_fts
		JOIN chunks c ON c.id = chunks_fts.rowid
		WHERE chunks_fts MATCH ?
		ORDER BY score ASC, c.path ASC, c.start_line ASC
		LIMIT ?
	`, match, limit)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()
	var hits []rankedHit
	for rows.Next() {
		var path, body string
		var start, end int
		var score float64
		if err := rows.Scan(&path, &start, &end, &body, &score); err != nil {
			return nil, err
		}
		hits = append(hits, rankedHit{
			Path: path, StartLine: start, EndLine: end,
			Snippet: body, Score: score,
		})
	}
	return hits, rows.Err()
}

func querySymbols(db *sql.DB, match string, limit int) ([]rankedHit, error) {
	rows, err := db.Query(`
		SELECT s.path, s.start_line, s.end_line, s.name, s.raw_signature,
		       bm25(symbols_fts, 30.0, 6.0, 2.0) AS score
		FROM symbols_fts
		JOIN symbols s ON s.id = symbols_fts.rowid
		WHERE symbols_fts MATCH ?
		ORDER BY score ASC, s.path ASC, s.start_line ASC
		LIMIT ?
	`, match, limit)
	if err != nil {
		return nil, fmt.Errorf("symbol search: %w", err)
	}
	defer rows.Close()
	var hits []rankedHit
	for rows.Next() {
		var path, name, sig string
		var start, end int
		var score float64
		if err := rows.Scan(&path, &start, &end, &name, &sig, &score); err != nil {
			return nil, err
		}
		snippetBody := sig
		if snippetBody == "" {
			snippetBody = name
		}
		hits = append(hits, rankedHit{
			Path: path, StartLine: start, EndLine: end,
			Snippet: snippetBody, Name: name, FromSym: true, Score: score,
		})
	}
	return hits, rows.Err()
}

func hasSymbolsFTS(db *sql.DB) bool {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='symbols_fts'`).Scan(&n)
	return err == nil && n > 0
}

func chunkOverlap(a1, a2, b1, b2 int) bool {
	return a1 <= b2 && b1 <= a2 && abs(a1-b1) < limits.ChunkOverlap
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func snippet(body string) string {
	body = strings.TrimSpace(body)
	if utf8.RuneCountInString(body) <= limits.MaxSnippetRunes {
		return body
	}
	runes := []rune(body)
	return string(runes[:limits.MaxSnippetRunes]) + "…"
}
