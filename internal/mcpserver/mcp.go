package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/rike422/shoka/internal/limits"
	"github.com/rike422/shoka/internal/search"
)

// mcpHit is the MCP-facing payload (relative path only; no host abs paths).
type mcpHit struct {
	Path      string  `json:"path"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Score     float64 `json:"score"`
	Snippet   string  `json:"snippet"`
}

func publicErr(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "index stale"):
		return "index stale: run shoka index"
	case strings.Contains(msg, "index not found"):
		return "index not found: run shoka index"
	case strings.Contains(msg, "empty query"):
		return "empty query"
	default:
		return "search failed"
	}
}

// Run starts a stdio MCP server bound to a fixed project root.
func Run(projectRoot string) error {
	s := server.NewMCPServer("shoka", "0.1.0")

	tool := mcp.NewTool("search",
		mcp.WithDescription("BM25 code search over the indexed repository. Recommended for locating definitions and relevant files; use Grep/Glob for exact pattern matching or occurrence counting. Phrase queries retry with OR if AND misses; a missing identifier stays empty. Returns path, line range, score, and a short snippet. Run `shoka index` if the index is missing or stale."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search query (identifiers, keywords, Japanese text)")),
		mcp.WithNumber("top_k", mcp.Description("Max results (default 10, max 50)")),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		q, err := req.RequireString("query")
		if err != nil {
			return mcp.NewToolResultError("query is required"), nil
		}
		topK := req.GetInt("top_k", limits.DefaultTopK)
		if topK <= 0 {
			topK = limits.DefaultTopK
		}
		hits, err := search.Query(projectRoot, q, search.Options{TopK: topK})
		if err != nil {
			return mcp.NewToolResultError(publicErr(err)), nil
		}
		out := make([]mcpHit, 0, len(hits))
		for _, h := range hits {
			out = append(out, mcpHit{
				Path:      h.Path,
				StartLine: h.StartLine,
				EndLine:   h.EndLine,
				Score:     h.Score,
				Snippet:   h.Snippet,
			})
		}
		payload, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return mcp.NewToolResultError("encode failed"), nil
		}
		return mcp.NewToolResultText(string(payload)), nil
	})

	fmt.Fprintf(os.Stderr, "shoka mcp: ready\n")
	return server.ServeStdio(s)
}
