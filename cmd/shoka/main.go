package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/rike422/shoka/internal/hook"
	"github.com/rike422/shoka/internal/index"
	"github.com/rike422/shoka/internal/mcpserver"
	"github.com/rike422/shoka/internal/root"
	"github.com/rike422/shoka/internal/search"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	if cmd == "transcript" && hasCLIFlag(args, "--verbose") {
		fmt.Fprintf(os.Stderr, "[transcript verbose] main entry args_count=%d\n", len(args))
	}

	var err error
	switch cmd {
	case "index":
		err = cmdIndex(args)
	case "search":
		err = cmdSearch(args)
	case "status":
		err = cmdStatus(args)
	case "mcp":
		err = cmdMCP(args)
	case "hook":
		err = cmdHook(args)
	case "session":
		err = cmdSession(args)
	case "transcript":
		err = cmdTranscript(args)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func hasCLIFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

func usage() {
	fmt.Fprintf(os.Stderr, `shoka — local BM25 code search for coding agents

Usage:
  shoka index [--root PATH] [--force]
  shoka search QUERY [--root PATH] [--top K] [--json]
  shoka status [--root PATH] [--json]
  shoka mcp [--root PATH]
  shoka hook install [--root PATH]
  shoka hook uninstall [--root PATH]
  shoka session sync [--agent all|codex|claude|opencode|cursor|pi] [--dry-run] [--json]
  shoka session list [--agent AGENT] [--repository PATH] [--json]
  shoka session show SESSION_ID [--json]
  shoka session search QUERY [--agent AGENT] [--event-type TYPE] [--file PATH] [--json]
  shoka session export SESSION_ID --output PATH
  shoka session reindex [--json]
  shoka transcript episode list [--agent AGENT] [--event-type TYPE] [--from DATE] [--to DATE] [--before N] [--after N] [--json] [--verbose]
  shoka transcript episode search QUERY [--before N] [--after N] [--json]
  shoka transcript episode export EPISODE_ID --output PATH

Environment:
  SHOKA_ROOT        Default project root (overridden by --root)
  SHOKA_STATE_DIR   Session index state directory
`)
}

func cmdIndex(args []string) error {
	rootPath, rest, err := parseFlags(args)
	if err != nil {
		return err
	}
	force := false
	for _, a := range rest {
		if a == "--force" {
			force = true
		}
	}
	r, err := root.Resolve(rootPath, true)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "indexing %s\n", r)
	st, err := index.Build(r, index.Options{Force: force})
	if err != nil {
		return err
	}
	if st.FullRebuild {
		fmt.Printf("full rebuild: %d files, %d chunks, %d symbols -> %s\n", st.Files, st.Chunks, st.Symbols, root.IndexDB(r))
		return nil
	}
	fmt.Printf("incremental: files=%d chunks=%d symbols=%d (+%d ~%d -%d unchanged=%d) -> %s\n",
		st.Files, st.Chunks, st.Symbols, st.Added, st.Updated, st.Removed, st.Unchanged, root.IndexDB(r))
	return nil
}

func cmdSearch(args []string) error {
	rootPath, rest, err := parseFlags(args)
	if err != nil {
		return err
	}
	jsonOut := false
	top := 0
	var queryParts []string
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--json":
			jsonOut = true
		case "--top":
			i++
			if i >= len(rest) {
				return fmt.Errorf("--top requires a value")
			}
			if _, err := fmt.Sscanf(rest[i], "%d", &top); err != nil {
				return fmt.Errorf("invalid --top: %s", rest[i])
			}
		default:
			queryParts = append(queryParts, rest[i])
		}
	}
	if len(queryParts) == 0 {
		return fmt.Errorf("search requires a QUERY")
	}
	q := joinQuery(queryParts)

	r, err := root.Resolve(rootPath, false)
	if err != nil {
		return err
	}
	hits, err := search.Query(r, q, search.Options{TopK: top})
	if err != nil {
		return err
	}
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(hits)
	}
	for _, h := range hits {
		fmt.Printf("%s:%d-%d  score=%.4f\n", h.Path, h.StartLine, h.EndLine, h.Score)
		for _, line := range previewLines(h.Snippet, 3) {
			fmt.Printf("  %s\n", line)
		}
		fmt.Println()
	}
	return nil
}

func cmdStatus(args []string) error {
	rootPath, rest, err := parseFlags(args)
	if err != nil {
		return err
	}
	jsonOut := false
	for _, a := range rest {
		if a == "--json" {
			jsonOut = true
		}
	}
	r, err := root.Resolve(rootPath, true)
	if err != nil {
		return err
	}
	st, err := index.Inspect(r)
	if err != nil {
		return err
	}
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(st)
	}
	fmt.Printf("root:      %s\n", st.Root)
	fmt.Printf("index:     %s\n", st.IndexPath)
	fmt.Printf("exists:    %v\n", st.Exists)
	if !st.Exists {
		return nil
	}
	fmt.Printf("schema:    %s (tokenizer %s)\n", st.SchemaVersion, st.TokenizerVersion)
	fmt.Printf("files:     %d\n", st.Files)
	fmt.Printf("chunks:    %d\n", st.Chunks)
	fmt.Printf("symbols:   %d\n", st.Symbols)
	if len(st.TreesitterLangs) > 0 {
		fmt.Printf("ts langs:  %s\n", strings.Join(st.TreesitterLangs, ","))
	} else {
		fmt.Printf("ts langs:  (disabled)\n")
	}
	fmt.Printf("head:      %s\n", st.HeadCommit)
	fmt.Printf("indexed:   %s\n", st.IndexedHead)
	fmt.Printf("stale:     %v\n", st.Stale)
	fmt.Printf("created:   %s\n", st.CreatedAt)
	return nil
}

func cmdMCP(args []string) error {
	rootPath, _, err := parseFlags(args)
	if err != nil {
		return err
	}
	r, err := root.Resolve(rootPath, false)
	if err != nil {
		return err
	}
	return mcpserver.Run(r)
}

func cmdHook(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: shoka hook install|uninstall [--root PATH]")
	}
	sub := args[0]
	rootPath, _, err := parseFlags(args[1:])
	if err != nil {
		return err
	}
	r, err := root.Resolve(rootPath, true)
	if err != nil {
		return err
	}
	switch sub {
	case "install":
		if err := hook.Install(r); err != nil {
			return err
		}
		fmt.Printf("installed post-commit hook in %s\n", r)
		return nil
	case "uninstall":
		if err := hook.Uninstall(r); err != nil {
			return err
		}
		fmt.Printf("removed post-commit hook from %s\n", r)
		return nil
	default:
		return fmt.Errorf("unknown hook subcommand: %s", sub)
	}
}

func parseFlags(args []string) (rootPath string, rest []string, err error) {
	for i := 0; i < len(args); i++ {
		if args[i] == "--root" {
			i++
			if i >= len(args) {
				return "", nil, fmt.Errorf("--root requires a path")
			}
			rootPath = args[i]
			continue
		}
		rest = append(rest, args[i])
	}
	return rootPath, rest, nil
}

func joinQuery(parts []string) string {
	if len(parts) == 1 {
		return parts[0]
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += " " + p
	}
	return out
}

func previewLines(s string, n int) []string {
	lines := splitNL(s)
	if len(lines) > n {
		lines = lines[:n]
	}
	return lines
}

func splitNL(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start <= len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
