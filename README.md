# shoka

Local code search for coding agents. Indexes a repository into SQLite FTS5 and returns ranked path / line-range / snippet hits so agents can skip broad Grep/Glob loops.

Search combines:

- **Chunk BM25** — line-windowed body / path / basename
- **Symbol FTS** — tree-sitter definition tags (Go / Python / GDScript / Ruby / JavaScript / TypeScript), merged for identifier-like queries via weighted RRF

## Install

Requires Go 1.25+ with CGO and a C toolchain (Xcode CLT on macOS).

```bash
git clone https://github.com/rike422/shoka.git
cd shoka
CGO_ENABLED=1 go install -tags fts5 ./cmd/shoka
```

Or build a release binary:

```bash
make build   # dist/shoka (−trimpath −ldflags="-s -w")
# or:
CGO_ENABLED=1 go build -tags fts5 -trimpath -ldflags='-s -w' -o dist/shoka ./cmd/shoka
```

### Prebuilt binaries

Tagged releases (`v*`) build CGO binaries on GitHub Actions and attach them to the GitHub Release:

- `shoka-linux-amd64` / `shoka-linux-arm64`
- `shoka-darwin-arm64` / `shoka-darwin-amd64`

Each file has a sibling `.sha256`. Manual/workflow_dispatch builds upload the same artifacts without creating a release.

```bash
# example
curl -fsSL -o shoka https://github.com/rike422/shoka/releases/latest/download/shoka-darwin-arm64
chmod +x shoka
```

## Usage

```bash
# Build / refresh the index (incremental when possible)
shoka index [--root PATH]
shoka index --force   # full rebuild

# Search
shoka search "getUserProfile" [--root PATH] [--top 10] [--json]
shoka search "class_name UniqueWidget" --json
shoka search "有効期限" --json

# Index health (files / chunks / symbols / HEAD freshness)
shoka status [--root PATH] [--json]

# Optional: re-index after every commit
shoka hook install [--root PATH]
shoka hook uninstall [--root PATH]

# MCP stdio server (search tool only)
SHOKA_ROOT=/path/to/repo shoka mcp
```

Add `.shoka/` to `.gitignore` (the tool warns if missing).

### Config: `.shoka.toml`

Optional file at the **repository root** (not under `.shoka/`, so it can be committed):

```toml
[treesitter]
# omit this file → all bundled languages enabled
# languages = []                 # disable symbol extraction
# languages = ["go", "gdscript"] # subset only
```

Bundled languages in this build: `go`, `python`, `gdscript`, `ruby`, `javascript`, `typescript` (`.ts` / `.tsx` / `.mts` / `.cts`).  
Unknown names error at index start. Grammar revisions and licenses: `THIRD_PARTY_NOTICES.md`.

### `.shokaignore`

Exclude tracked-but-useless paths from the index without removing them from git:

```gitignore
# .shokaignore  (gitignore-like subset)
*.uid
*.import
fixtures/large/
/secret.env
```

Built-in skips already cover `.uid` / `.import` / `.shoka/` / `.git/`.  
Supported: comments, `!` negation, `dir/`, rooted `/path`, `*.ext`, and simple `dir/**`.

### Root resolution

1. `--root`
2. `SHOKA_ROOT`
3. Walk up from cwd: prefer `.shoka/`, else `.git`
4. `index` / `status` may fall back to cwd; `search` / `mcp` error if unresolved or index missing

If HEAD moved since the last `index`, `search` fails with `index stale: run shoka index`.

## MCP (Cursor / Codex)

Index the target repo once, then register the server with a fixed root.

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

Tool: `search(query, top_k?)` — same ranking as the CLI. No separate symbols tool; refresh with `shoka index` or the git hook.

## How it works

**Indexing**

- Walks the tree with ignore rules; stores `files` (path, mtime, size, content hash) for incremental updates
- Chunks text into ~80-line windows (blank-line aware, 10-line overlap) → `chunks` + `chunks_fts`
- For enabled languages, runs tree-sitter + `tags.scm` → `symbols` + `symbols_fts` (definitions only)
- Same tokenizer at index and query time (camel/snake split, Japanese bigrams, qualified names)
- `symbol_fingerprint` (langs + grammar + query hashes) forces a full rebuild when extraction config changes

**Search**

- Always queries chunk FTS (basename > path > body BM25 weights)
- Identifier-like queries (e.g. `Foo`, `pkg.Type`, `class_name Bar`) also query symbols and merge with weighted RRF + exact-name bonus
- Caps hits per file; keeps the existing hit JSON shape (`path`, lines, score, snippet)

Unsupported extensions / per-file parse failures still get chunk indexing; only that file’s symbols are skipped.

## Benchmarks (anonymized)

Re-measured in one session on a mid-size application repository (implementation + design docs; **project name omitted**). Counts are **after ignore rules**.

| | |
| --- | --- |
| Indexed files | 1,093 |
| Chunks | 2,841 |
| Symbols (when enabled) | 6,978 |
| Tasks | 12 agent-style lookups |
| Modes | BM25-only (`languages = []`), BM25 + symbols (default), ripgrep (`rg -l -F`) |
| Timing | index n=5, search latency n=5 / query; ranking deterministic |

### Retrieval quality

| Metric | BM25 only | + symbols | ripgrep |
| --- | ---: | ---: | ---: |
| Success@1 | 0.75 | **0.83** | 0.75 |
| Success@5 | **1.00** | **1.00** | 0.83 |
| MRR | 0.88 | **0.92** | 0.78 |
| Lines to first gold (sum) | **2.0k** | **2.2k** | 15–22k |
| Search latency (mean of means) | 22 ms | 20 ms | 46–51 ms |

Symbols help identifier / `class_name …` queries (e.g. definition file over a factory that only references the name). Natural-language doc queries stay on the chunk path. Top-5 and read cost remain better than ripgrep when matches are numerous.

### Index cost

| | BM25 only | + symbols |
| --- | ---: | ---: |
| Full rebuild | **1.16 ± 0.30 s** | **2.69 ± 0.38 s** |
| Incremental (no changes) | 0.07 s | 0.09 s |
| Index DB | ~28 MB | ~33 MB |

Symbol extraction roughly doubles full-index time at this size; absolute cost stays a few seconds. Release binary (strip) is on the order of **~13 MB** on macOS arm64 with the six bundled language grammars (TypeScript includes TSX).

## Development

CGO + `fts5` are required:

```bash
make check   # fmt + tidy + lint + test + build
make test
make lint
make build
```

```bash
CGO_ENABLED=1 go test -tags fts5 ./...
```

| Piece | Role |
| --- | --- |
| `.golangci.yml` | golangci-lint |
| `Makefile` | `fmt` / `lint` / `test` / `build` / `check` |
| `.github/workflows/ci.yml` | tidy, gofmt, lint, test, build |
| `testdata/quality/cases/` | search-quality fixtures |
| `internal/treesitter/queries/` | vendored / shoka-owned `tags.scm` |
| `third_party/tree_sitter/` | vendored GDScript grammar sources |

## References

Built after reading these — BM25 as a candidate search step for coding agents, rather than letting the agent explore the file tree alone:

- Pengyu Wang et al., [*BM25 Wins at Scale: A Scaling Study of Retrieval-Augmented Generation Paradigms*](https://arxiv.org/abs/2607.26497), arXiv:2607.26497, 2026.
- 須藤英寿 / ナレッジセンス, [*BM25を使用してCodexのトークンの消費を30%抑える*](https://zenn.dev/knowledgesense/articles/9e55a3bb67729c), Zenn, 2026.

## License

MIT
