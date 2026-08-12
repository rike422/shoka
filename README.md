# shoka

Local BM25 code search for coding agents. Indexes a repository into SQLite FTS5 and returns ranked path / line-range / snippet hits so agents can skip broad Grep/Glob loops.

## Install

Requires Go 1.25+ with CGO and a C toolchain (Xcode CLT on macOS).

```bash
git clone https://github.com/rike422/shoka.git
cd shoka
CGO_ENABLED=1 go install -tags fts5 ./cmd/shoka
```

Or build a binary:

```bash
CGO_ENABLED=1 go build -tags fts5 -o shoka ./cmd/shoka
```

## Usage

```bash
# Build / refresh the index (incremental when possible)
shoka index [--root PATH]
shoka index --force   # full rebuild

# Search
shoka search "getUserProfile" [--root PATH] [--top 10] [--json]
shoka search "有効期限" --json

# Index health (HEAD freshness, file/chunk counts)
shoka status [--root PATH] [--json]

# Optional: re-index after every commit
shoka hook install [--root PATH]
shoka hook uninstall [--root PATH]

# MCP stdio server (search tool only)
SHOKA_ROOT=/path/to/repo shoka mcp
```

Add `.shoka/` to `.gitignore` (the tool warns if missing).

### `.shokaignore`

Tracked but useless for search (generated sidecars, huge fixtures, etc.) can be excluded
in the repo root without removing them from git:

```gitignore
# .shokaignore  (gitignore-like subset)
*.uid
*.import
fixtures/large/
/secret.env
```

Built-in skips already cover `.uid` / `.import` / `.shoka/` / `.git/`.  
`.shokaignore` is for project-specific extras. Supported: comments, `!` negation,
`dir/`, rooted `/path`, `*.ext`, and simple `dir/**`.

### Root resolution

1. `--root`
2. `SHOKA_ROOT`
3. Walk up from cwd: per directory prefer `.shoka/`, else `.git`
4. `index` / `status` may fall back to cwd; `search` / `mcp` error if unresolved or index missing

If HEAD moved since the last `index`, `search` fails with `index stale: run shoka index`.

## MCP (Cursor / Codex)

Index the target repo once, then register the server with a fixed root:

**Cursor** (`.cursor/mcp.json` or project MCP config):

```json
{
  "mcpServers": {
    "shoka": {
      "command": "shoka",
      "args": ["mcp"],
      "env": {
        "SHOKA_ROOT": "/absolute/path/to/your/repo"
      }
    }
  }
}
```

**Codex** (`~/.codex/config.toml`):

```toml
[mcp_servers.shoka]
command = "shoka"
args = ["mcp"]
env = { SHOKA_ROOT = "/absolute/path/to/your/repo" }
```

Tool: `search(query, top_k?)` — no `reindex` tool; refresh with CLI or the optional git hook.

## How it works

- Maintains a `files` table (path, mtime, size, content hash) for incremental updates
- Chunks files into ~80-line windows (blank-line aware, 10-line overlap)
- Pre-tokenizes identifiers (`getUserProfile`, `Foo::bar`) and Japanese character bigrams
- Stores raw text for snippets and tokenized text in FTS5
- Ranks with `bm25()` weighting **basename > path > body**
- Caps hits per file to reduce agent token waste
- Queries use the same tokenizer and safe AND-of-literals MATCH (no raw FTS syntax)

## Development

CGO + `fts5` build tag are required. Prefer the Makefile:

```bash
make check   # fmt + tidy + lint + test + build
make test
make lint    # golangci-lint (https://golangci-lint.run/welcome/install/)
make build
```

Or directly:

```bash
CGO_ENABLED=1 go test -tags fts5 ./...
CGO_ENABLED=1 go build -tags fts5 -o dist/shoka ./cmd/shoka
```

| Piece | Role |
| --- | --- |
| `.golangci.yml` | golangci-lint |
| `Makefile` | `fmt` / `lint` / `test` / `build` / `check` |
| `.editorconfig` | editor defaults |
| `.github/workflows/ci.yml` | tidy, gofmt, lint, test, build |
| `.github/dependabot.yml` | weekly `gomod` + Actions updates |

Search-quality fixtures: `testdata/quality/cases/` (exercised by `go test`).

## License

MIT
