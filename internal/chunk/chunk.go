package chunk

import (
	"strings"

	"github.com/rike422/shoka/internal/limits"
)

// Chunk is a line-range slice of a file.
type Chunk struct {
	StartLine int // 1-based inclusive
	EndLine   int // 1-based inclusive
	Body      string
}

// Split breaks content into overlapping line windows, preferring blank-line ends.
func Split(content string) []Chunk {
	lines := splitLines(content)
	if len(lines) == 0 {
		return nil
	}

	size := limits.ChunkLines
	overlap := limits.ChunkOverlap
	if overlap >= size {
		overlap = size - 1
	}

	var out []Chunk
	start := 0
	for start < len(lines) {
		end := start + size
		if end > len(lines) {
			end = len(lines)
		} else {
			end = snapEnd(lines, start, end, size)
		}
		body := strings.Join(lines[start:end], "\n")
		if len(body) > limits.MaxChunkBytes {
			body = trimBytes(body, limits.MaxChunkBytes)
		}
		out = append(out, Chunk{
			StartLine: start + 1,
			EndLine:   end,
			Body:      body,
		})
		if end == len(lines) {
			break
		}
		next := end - overlap
		if next <= start {
			next = start + 1
		}
		next = snapStart(lines, start, next, end)
		start = next
	}
	return out
}

func snapEnd(lines []string, start, end, size int) int {
	minEnd := start + size/2
	if minEnd < start+1 {
		minEnd = start + 1
	}
	for i := end - 1; i >= minEnd; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			if i+1 > start {
				return i + 1 // exclusive end after blank
			}
		}
	}
	return end
}

func snapStart(lines []string, prevStart, next, end int) int {
	// Prefer starting just after a blank line near the overlap zone.
	maxLook := next + limits.ChunkOverlap
	if maxLook > end {
		maxLook = end
	}
	for i := next; i < maxLook; i++ {
		if i > 0 && strings.TrimSpace(lines[i-1]) == "" {
			return i
		}
	}
	if next <= prevStart {
		return prevStart + 1
	}
	return next
}

func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	raw := strings.Split(content, "\n")
	if len(raw) > 0 && raw[len(raw)-1] == "" {
		raw = raw[:len(raw)-1]
	}
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		if len(line) > limits.MaxLineBytes {
			line = line[:limits.MaxLineBytes]
		}
		out = append(out, line)
	}
	return out
}

func trimBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8Start(s[n]) {
		n--
	}
	return s[:n]
}

func utf8Start(b byte) bool {
	return b&0xC0 != 0x80
}
