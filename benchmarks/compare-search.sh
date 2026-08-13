#!/usr/bin/env bash
# Compare shoka BM25 vs ripgrep on the same task set. JSON → stdout, summary → stderr.
set -euo pipefail

ROOT="${1:-}"
TASKS="${2:-}"
if [[ -z "$ROOT" || -z "$TASKS" ]]; then
  echo "usage: $0 /path/to/repo /path/to/tasks.json" >&2
  exit 2
fi
ROOT="$(cd "$ROOT" && pwd)"
TASKS="$(cd "$(dirname "$TASKS")" && pwd)/$(basename "$TASKS")"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SHOKA="${SCRIPT_DIR}/../dist/shoka"
[[ -x "$SHOKA" ]] || (cd "${SCRIPT_DIR}/.." && CGO_ENABLED=1 go build -tags fts5 -o dist/shoka ./cmd/shoka)

if [[ -z "${RG_BIN:-}" ]]; then
  if command -v rg >/dev/null 2>&1; then
    RG_BIN="$(command -v rg)"
  elif [[ -x "/Applications/Cursor.app/Contents/Resources/app/node_modules/@vscode/ripgrep/bin/rg" ]]; then
    RG_BIN="/Applications/Cursor.app/Contents/Resources/app/node_modules/@vscode/ripgrep/bin/rg"
  else
    echo "ripgrep (rg) not found" >&2
    exit 1
  fi
fi

"$SHOKA" index --root "$ROOT" >/dev/null

python3 - "$ROOT" "$TASKS" "$SHOKA" "$RG_BIN" <<'PY'
import json, os, subprocess, sys, time
from pathlib import Path

root, tasks_path, shoka, rg = sys.argv[1:5]
tasks = json.load(open(tasks_path))


def path_matches(path: str, wants: list[str]) -> bool:
    return any(w in path or path.endswith(w) for w in wants)


def first_hit_rank(paths: list[str], wants: list[str]):
    for i, p in enumerate(paths, 1):
        if path_matches(p, wants):
            return i, p
    return None, None


def file_stats(rel: str):
    p = Path(root) / rel
    try:
        data = p.read_bytes()
    except OSError:
        return 0, 0
    lines = data.count(b"\n") + (0 if data.endswith(b"\n") or not data else 1)
    return len(data), lines


def estimate_read_cost(paths: list[str], wants: list[str], cap: int):
    bytes_n = lines_n = files_n = 0
    hit = None
    for p in paths[:cap]:
        b, ln = file_stats(p)
        bytes_n += b
        lines_n += ln
        files_n += 1
        if path_matches(p, wants):
            hit = p
            break
    return {
        "found": hit is not None,
        "hit_path": hit,
        "files_read": files_n,
        "bytes_read": bytes_n,
        "lines_read": lines_n,
        "est_tokens": (bytes_n // 4) if bytes_n else 0,
    }


def run_shoka(query: str, top: int):
    t0 = time.perf_counter()
    out = subprocess.check_output(
        [shoka, "search", query, "--root", root, "--top", str(top), "--json"],
        text=True,
    )
    ms = (time.perf_counter() - t0) * 1000
    hits = json.loads(out) or []
    paths = [h.get("path", "") for h in hits if isinstance(h, dict)]
    return paths, ms


def rg_files(query: str):
    t0 = time.perf_counter()
    proc = subprocess.run(
        [
            rg, "-l", "-F", "--hidden",
            "--glob", "!.git/**",
            "--glob", "!.shoka/**",
            "--glob", "!.claude/**",
            "--glob", "!.cursor/**",
            query, root,
        ],
        capture_output=True, text=True, check=False,
    )
    ms = (time.perf_counter() - t0) * 1000
    paths, seen = [], set()
    for line in (proc.stdout or "").splitlines():
        line = line.strip()
        if not line:
            continue
        rel = os.path.relpath(line, root).replace("\\", "/")
        if rel.startswith("..") or rel in seen:
            continue
        seen.add(rel)
        paths.append(rel)
    return paths, ms


def run_rg_best(query: str, wants: list[str]):
    candidates = [query]
    toks = sorted(
        (t for t in query.replace("　", " ").split() if len(t) >= 2),
        key=len, reverse=True,
    )
    for t in toks:
        if t not in candidates:
            candidates.append(t)

    options = []
    for q in candidates:
        paths, ms = rg_files(q)
        rank, hit = first_hit_rank(paths, wants)
        options.append({
            "query_used": q,
            "paths": paths,
            "ms": ms,
            "rank": rank,
            "hit": hit,
            "n_files": len(paths),
        })

    options.sort(key=lambda o: (
        0 if o["rank"] is not None else 1,
        o["rank"] or 10**9,
        o["n_files"],
        0 if o["query_used"] == query else 1,
    ))
    return options[0], options


rows = []
for t in tasks:
    q = t.get("query") or ""
    wants = t.get("want_paths") or []
    top = int(t.get("top_k") or 10)
    if not q or not wants:
        continue

    spaths, sms = run_shoka(q, top)
    srank, shit = first_hit_rank(spaths, wants)
    scost = estimate_read_cost(spaths, wants, top)

    best, _ = run_rg_best(q, wants)
    rpaths = best["paths"]
    rrank, rhit = best["rank"], best["hit"]
    rcost = estimate_read_cost(rpaths, wants, 30)
    rcost_topk = estimate_read_cost(rpaths, wants, top)

    row = {
        "id": t.get("id"),
        "difficulty": t.get("difficulty"),
        "query": q,
        "prompt": t.get("prompt"),
        "want_paths": wants,
        "top_k": top,
        "shoka": {
            "latency_ms": round(sms, 2),
            "n_hits": len(spaths),
            "rank": srank,
            "hit_path": shit,
            "paths": spaths,
            "read_until_hit": scost,
        },
        "ripgrep": {
            "latency_ms": round(best["ms"], 2),
            "query_used": best["query_used"],
            "n_files_matched": best["n_files"],
            "rank": rrank,
            "hit_path": rhit,
            "paths_top": rpaths[:top],
            "read_until_hit_cap30": rcost,
            "read_until_hit_topk": rcost_topk,
        },
    }
    rows.append(row)
    tag = "BOTH" if srank and rrank else ("S-only" if srank else ("R-only" if rrank else "MISS"))
    print(
        f"{tag}\t{row['id']}\tshoka_rank={srank}\trg_rank={rrank}\t"
        f"shoka_lines={scost['lines_read']}\trg_lines={rcost['lines_read']}",
        file=sys.stderr,
    )


def side_summary(get_rank, get_latency, get_cost):
    hit = [r for r in rows if get_rank(r) is not None]
    costs = [get_cost(r) for r in rows]
    return {
        "recall": f"{len(hit)}/{len(rows)}",
        "recall_n": len(hit),
        "total": len(rows),
        "avg_rank_when_hit": round(sum(get_rank(r) for r in hit) / len(hit), 2) if hit else None,
        "avg_latency_ms": round(sum(get_latency(r) for r in rows) / len(rows), 2) if rows else None,
        "sum_lines_read": sum(c["lines_read"] for c in costs),
        "sum_files_read": sum(c["files_read"] for c in costs),
        "sum_bytes_read": sum(c["bytes_read"] for c in costs),
        "sum_est_tokens": sum(c["est_tokens"] for c in costs),
        "found_n": sum(1 for c in costs if c["found"]),
    }


shoka_s = side_summary(
    lambda r: r["shoka"]["rank"],
    lambda r: r["shoka"]["latency_ms"],
    lambda r: r["shoka"]["read_until_hit"],
)
rg30 = side_summary(
    lambda r: r["ripgrep"]["rank"],
    lambda r: r["ripgrep"]["latency_ms"],
    lambda r: r["ripgrep"]["read_until_hit_cap30"],
)
rg_top = side_summary(
    lambda r: r["ripgrep"]["rank"],
    lambda r: r["ripgrep"]["latency_ms"],
    lambda r: r["ripgrep"]["read_until_hit_topk"],
)

s_lines, r_lines = shoka_s["sum_lines_read"], rg30["sum_lines_read"]
s_tok, r_tok = shoka_s["sum_est_tokens"], rg30["sum_est_tokens"]

summary = {
    "repo": root,
    "tasks": tasks_path,
    "measured_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    "method": {
        "shoka": "BM25 FTS5; open ranked hits until want_path (cap top_k)",
        "ripgrep": "rg -l -F on query or longest token; open file-list order until want_path (cap 30)",
        "est_tokens": "bytes_read // 4 (proxy, not a model tokenizer)",
        "layer": "B+ retrieval/read-cost — not live agent session tokens (layer C)",
    },
    "shoka": shoka_s,
    "ripgrep_cap30": rg30,
    "ripgrep_topk": rg_top,
    "reduction": {
        "lines_read_ratio_shoka_over_rg": round(s_lines / r_lines, 3) if r_lines else None,
        "est_tokens_ratio_shoka_over_rg": round(s_tok / r_tok, 3) if r_tok else None,
        "lines_saved": r_lines - s_lines,
        "est_tokens_saved": r_tok - s_tok,
        "files_saved": rg30["sum_files_read"] - shoka_s["sum_files_read"],
    },
}

json.dump({"summary": summary, "tasks": rows}, sys.stdout, ensure_ascii=False, indent=2)
print()
print(
    f"summary: shoka {shoka_s['recall']} lines={s_lines} tok≈{s_tok} | "
    f"rg {rg30['recall']} lines={r_lines} tok≈{r_tok} | "
    f"line_ratio={summary['reduction']['lines_read_ratio_shoka_over_rg']}",
    file=sys.stderr,
)
PY
