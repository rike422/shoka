# benchmarks/

Public retrieval / index benches for shoka. Private corpora and scratch logs stay in gitignored [`../local/`](../local/).

| Piece | Role |
| --- | --- |
| [`METRICS.md`](METRICS.md) | Success@k, MRR, lines-to-first-gold, protocol |
| [`bench-suite.sh`](bench-suite.sh) | Multi-run index + shoka vs ripgrep |
| [`compare-search.sh`](compare-search.sh) | Lighter single-pass compare |
| [`tasks/`](tasks/) | Task JSON (`query` + `want_paths`) |
| [`results/`](results/) | Recorded runs |

```bash
CGO_ENABLED=1 go build -tags fts5 -o dist/shoka ./cmd/shoka

# Example: Misskey @ recorded commit (clone separately; no pnpm install)
RUNS=5 INDEX_RUNS=3 ./benchmarks/bench-suite.sh /path/to/misskey \
  benchmarks/tasks/misskey-988e3c32a39c.json \
  > /tmp/misskey-rerun.json
```

Published Misskey run: [`results/misskey-988e3c32a39c/`](results/misskey-988e3c32a39c/) (`summary.json`).

**ripgrep baseline:** `rg -l -F` with a gold-aware token fallback in the harness — optimistic file-list order, not a relevance ranker.
