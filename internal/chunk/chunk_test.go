package chunk_test

import (
	"strings"
	"testing"

	"github.com/rike422/shoka/internal/chunk"
	"github.com/rike422/shoka/internal/limits"
)

func TestSplitBasic(t *testing.T) {
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = "line"
	}
	content := strings.Join(lines, "\n")
	chunks := chunk.Split(content)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	if chunks[0].StartLine != 1 {
		t.Fatalf("start=%d", chunks[0].StartLine)
	}
}

func TestSplitShort(t *testing.T) {
	chunks := chunk.Split("a\nb\nc")
	if len(chunks) != 1 {
		t.Fatalf("got %d", len(chunks))
	}
	if chunks[0].StartLine != 1 || chunks[0].EndLine != 3 {
		t.Fatalf("%+v", chunks[0])
	}
}

func TestSplitEmpty(t *testing.T) {
	if chunk.Split("") != nil {
		t.Fatal("expected nil")
	}
}

func TestSplitCRLF(t *testing.T) {
	chunks := chunk.Split("a\r\nb\r\nc")
	if len(chunks) != 1 || chunks[0].EndLine != 3 {
		t.Fatalf("%+v", chunks)
	}
}

func TestSplitPrefersBlankLine(t *testing.T) {
	var b strings.Builder
	for i := 0; i < limits.ChunkLines-5; i++ {
		b.WriteString("code\n")
	}
	b.WriteString("\n") // blank near end of first window
	for i := 0; i < 40; i++ {
		b.WriteString("more\n")
	}
	chunks := chunk.Split(b.String())
	if len(chunks) < 2 {
		t.Fatalf("chunks=%d", len(chunks))
	}
	// first chunk should end around the blank, not necessarily exactly 80
	if chunks[0].EndLine > limits.ChunkLines {
		t.Fatalf("end too large: %d", chunks[0].EndLine)
	}
}

func TestSplitLongLineTruncated(t *testing.T) {
	long := strings.Repeat("x", limits.MaxLineBytes+100)
	chunks := chunk.Split(long)
	if len(chunks) != 1 {
		t.Fatalf("got %d", len(chunks))
	}
	if len(chunks[0].Body) > limits.MaxLineBytes {
		t.Fatalf("line not truncated: %d", len(chunks[0].Body))
	}
}

func TestChunkRangesMonotonic(t *testing.T) {
	var lines []string
	for i := 0; i < 200; i++ {
		if i%17 == 0 {
			lines = append(lines, "")
		} else {
			lines = append(lines, "x")
		}
	}
	chunks := chunk.Split(strings.Join(lines, "\n"))
	prev := 0
	for _, c := range chunks {
		if c.StartLine < 1 || c.EndLine < c.StartLine {
			t.Fatalf("bad range %+v", c)
		}
		if c.StartLine < prev {
			t.Fatalf("not advancing: %+v", c)
		}
		prev = c.StartLine
	}
}
