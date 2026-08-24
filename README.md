# shoka

Local code and agent-session search for coding agents. Shoka indexes a repository into SQLite FTS5 and can separately collect normalized local transcripts from supported coding agents.

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

# Collect local coding-agent sessions into the user-level session index
shoka session sync --agent all
shoka session sync --agent codex --dry-run --json

# Browse and search prior sessions
shoka session list [--agent codex] [--repository /path/to/repo] [--json]
shoka session show codex:<session-id> [--json]
shoka session search "sqlite migration" --agent codex --event-type error --json

# Export normalized, source-linked session-evidence/v1 (mode 0600)
shoka session export codex:<session-id> --output evidence.json

# List all observable design-direction episodes without a text query
shoka transcript episode list --agent codex --event-type design_direction --from 2026-08-01 --to 2026-08-31 --json

# Search and export correction/result episodes only
shoka transcript episode search "migration test failed" --agent codex --before 3 --after 8 --json
shoka transcript episode export codex:<transcript-id>:<event-id>:test_failure --output episode.json --before 3 --after 8

# Delete the derived session index and rebuild it from raw agent logs
shoka session reindex --json
```

Add `.shoka/` to `.gitignore` (the tool warns if missing).

### Config: `.shoka.toml`

Optional file at the **repository root** (not under `.shoka/`, so it can be committed):

```toml
[treesitter]
# omit this file → all bundled languages enabled
# languages = []                 # disable symbol extraction
# languages = ["go", "gdscript"] # subset only

[projects.aliases]
# Optional explicit identity for clones/worktrees whose Git metadata is
# unavailable or ambiguous.
# "/absolute/path/to/clone" = "company/product"
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

## Agent sessions

The session index is separate from the per-repository code index. It defaults to
`~/.local/state/shoka/sessions.db`; set `SHOKA_STATE_DIR` to override it, or
`XDG_STATE_HOME` to change the state root. The directory is mode `0700` and the
database is mode `0600` on POSIX systems.

For local development, create a small isolated database without reading or
reindexing real agent logs:

```bash
go run -tags fts5 ./cmd/shoka-testfixture \
  --output .tmp/shoka-fixtures/episode-pipeline \
  --sessions 64

SHOKA_STATE_DIR=/absolute/path/to/shoka/.tmp/shoka-fixtures/episode-pipeline/state \
  shoka transcript episode list --from 2026-08-20 --to 2026-08-20 --json
```

The fixture command writes native Codex JSONL first and passes it through the
production sync path to create `sessions.db`; it never inserts directly into
SQLite. The output directory must be new, preventing an existing database from
being overwritten. Generated fixtures under `.tmp/` are ignored by Git.

Initial adapters:

| Agent | Source |
| --- | --- |
| Codex | `$CODEX_HOME/sessions/**/*.jsonl` and `archived_sessions` (`~/.codex` by default) |
| Claude Code | `$CLAUDE_CONFIG_DIR/projects/**/*.jsonl` (`~/.claude` by default) |
| OpenCode | `$OPENCODE_DB` or `$XDG_DATA_HOME/opencode/opencode.db`, opened read-only with an allowlisted schema |
| Cursor | `~/.cursor/projects/**/agent-transcripts/**/*.jsonl` |
| Pi | `$PI_CODING_AGENT_SESSION_DIR/**/*.jsonl` or `~/.pi/agent/sessions/**/*.jsonl` |

JSONL sources are incrementally read from the last complete newline. An
incomplete final line is retried on the next sync; malformed complete lines are
reported and skipped. Append-only changes preserve existing event IDs, while
truncation or a changed prefix rebuilds only that source. Missing source files
remain searchable and are marked `source_missing`.

For a growing JSONL file, Shoka verifies the previous snapshot with the file
identity and a bounded multi-point guard, resumes the saved SHA-256 state over
only the appended bytes, and indexes only new retained payloads. A full source
read is reserved for the initial sync or a replaced/rewritten source; an
explicit `session reindex` rebuilds everything.

Shoka stores allowlisted normalized events rather than copying raw logs. Hidden
reasoning/thinking blocks, heartbeat/progress records, and large base64-like
data are excluded before persistence. Text is retained as bounded head/tail
snippets: 4 KiB for successful command output, 8 KiB for unknown tool outcomes,
16 KiB for failures, and 32 KiB for user/assistant messages. Exact duplicate
occurrences share one retained payload. High-confidence token/key patterns are
replaced with `[REDACTED]`; this is not a guarantee that arbitrary secrets in a
transcript can be detected, so review exported evidence before sharing it.

Session search uses contentless FTS5/BM25 over retained snippets. Text discarded
from the middle of oversized output is intentionally not searchable. Filters
for agent, session, repository, event type, file, and date are applied as exact
relational filters, not as search terms.

`session export` writes the deterministic, model-independent
[`session-evidence/v1`](docs/session-evidence-v1.schema.json) contract. It keeps
observable tasks, user corrections, commands and outcomes, changed paths,
explicit failures, observed patch text, the final assistant response, and
source references. It does not rank lessons or generate Skill candidates;
downstream tools such as `agent-codify` own that semantic step. Exported JSON is
bounded to 1 MiB and records deterministic omission counts.

`transcript episode list` is the queryless enumeration path for downstream
analysis. Its `--event-type` selects an observable episode trigger such as
`design_direction` or `scope_revision`; `--from` and `--to` filter the trigger
timestamp, not merely the enclosing session lifetime. When neither date is
specified, list defaults to the previous seven days. Plain-text output uses a
metadata-only path; JSON output includes bounded context by default. Add
`--verbose` to print progress and memory diagnostics to stderr without
printing transcript content.

`transcript episode search` and `transcript episode export` use the separate
[`transcript-episode/v1`](docs/transcript-episode-v1.schema.json) contract. Episodes
start only at observable correction anchors, including repeated requests,
scope/design directions, explicit errors, and test failures. They retain
bounded context, actions, outcomes, task status, source event IDs/hashes, and
search reasons. Search output reports raw event, session, task-lineage, and
project counts separately. Episode derivation never generates Skills, infers
root causes, or synchronizes sessions implicitly.

The session database is a disposable derived index. `session reindex` first
checks that raw sources are discoverable, then deletes `sessions.db` (including
WAL/SHM files) and rebuilds it. Sync and reindex are mutually exclusive. There
is no database migration or backup path; rerun reindex after an interrupted
build.

The `session` commands and database fields refer to the native agent-session
index. The public correction/result evidence layer is exposed as
`transcript episode`, so a transcript is the evidence stream while
`native_session_id` remains an upstream identity concept.

The Pi adapter is covered by a synthetic version-3 JSONL fixture based on Pi's
documented session format. It has not yet been verified against a Pi log on the
development machine.

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
- Phrase queries retry with OR if AND of all tokens misses (identifier queries stay AND, so a missing name stays empty)
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

### Public corpus: Misskey

Same harness on a named public tree: [`misskey-dev/misskey`](https://github.com/misskey-dev/misskey) @ [`988e3c32a39c`](https://github.com/misskey-dev/misskey/commit/988e3c32a39c1b0a30f71ffcf1f3baf19e5fcd5f) (shallow `develop`, no `pnpm install`). 12 tasks (identifier, exact phrase EN/JA, multi-word flow). Modes: BM25-only, BM25 + TypeScript symbols, ripgrep. Index n=3, search latency n=5 / query.

| | |
| --- | --- |
| Indexed files | 2,953 |
| Chunks | 8,066 |
| Index DB (+ TypeScript symbols) | ~59 MB |

| Metric | BM25 only | + TypeScript symbols | ripgrep |
| --- | ---: | ---: | ---: |
| Success@1 | 0.58 | **0.67** | 0.50 |
| Success@5 | **0.83** | **0.83** | 0.67 |
| MRR | 0.68 | **0.74** | 0.58 |
| Lines to first gold (sum) | **29k** | **29k** | 215–262k |
| Search latency (mean of means) | 18 ms | 18 ms | 35 ms |
| Full rebuild | 2.16 ± 0.25 s | 3.38 ± 0.09 s | — |

TypeScript symbols lift Success@1 on class-name lookups (e.g. `NoteCreateService` rank 3 → 1). Exact Japanese phrases in source still rank well on the chunk path; space-separated Japanese concept queries are weaker (tokenizer gap). Read cost stays far below ripgrep when matches are numerous.

Tasks and raw JSON: [`benchmarks/tasks/misskey-988e3c32a39c.json`](benchmarks/tasks/misskey-988e3c32a39c.json), [`benchmarks/results/misskey-988e3c32a39c/`](benchmarks/results/misskey-988e3c32a39c/).

**Baseline note:** ripgrep here is `rg -l -F` with a gold-aware fallback to the longest query token when the full string misses — an optimistic file-list order, not a relevance ranker. MRR for rg should be read as “first-file ordering under that harness,” not as ranked retrieval quality.

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
| `benchmarks/` | public Misskey (and future) retrieval benches |
| `testdata/quality/cases/` | search-quality fixtures |
| `internal/treesitter/queries/` | vendored / shoka-owned `tags.scm` |
| `third_party/tree_sitter/` | vendored GDScript grammar sources |

## References

Built after reading these — BM25 as a candidate search step for coding agents, rather than letting the agent explore the file tree alone:

- Pengyu Wang et al., [*BM25 Wins at Scale: A Scaling Study of Retrieval-Augmented Generation Paradigms*](https://arxiv.org/abs/2607.26497), arXiv:2607.26497, 2026.
- 須藤英寿 / ナレッジセンス, [*BM25を使用してCodexのトークンの消費を30%抑える*](https://zenn.dev/knowledgesense/articles/9e55a3bb67729c), Zenn, 2026.

## License

MIT
