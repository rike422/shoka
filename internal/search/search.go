package search

import (
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

// Query runs BM25 search against the project index.
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

	// weights: basename > path > body
	limit := opt.TopK * limits.MaxChunksPerFile * 20
	if limit < opt.TopK {
		limit = opt.TopK
	}
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

	var hits []Hit
	hits = make([]Hit, 0, opt.TopK)
	perFile := map[string]int{}
	for rows.Next() {
		var path, body string
		var start, end int
		var score float64
		if err := rows.Scan(&path, &start, &end, &body, &score); err != nil {
			return nil, err
		}
		if perFile[path] >= limits.MaxChunksPerFile {
			continue
		}
		dup := false
		for _, h := range hits {
			if h.Path == path && overlap(h.StartLine, h.EndLine, start, end) {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		perFile[path]++
		hits = append(hits, Hit{
			Path:      path,
			AbsPath:   filepath.Join(projectRoot, filepath.FromSlash(path)),
			StartLine: start,
			EndLine:   end,
			Score:     score,
			Snippet:   snippet(body),
		})
		if len(hits) >= opt.TopK {
			break
		}
	}
	return hits, rows.Err()
}

func overlap(a1, a2, b1, b2 int) bool {
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
