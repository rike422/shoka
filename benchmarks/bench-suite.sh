#!/usr/bin/env bash
# Multi-run index + retrieval metrics (Zoekt / Agent-Retrieval-Bench style) vs ripgrep.
# Usage: RUNS=5 INDEX_RUNS=3 ./benchmarks/bench-suite.sh /path/to/repo tasks.json
set -euo pipefail

ROOT="${1:-}"
TASKS="${2:-}"
if [[ -z "$ROOT" || -z "$TASKS" ]]; then
  echo "usage: RUNS=5 INDEX_RUNS=3 $0 /path/to/repo /path/to/tasks.json" >&2
  exit 2
fi
ROOT="$(cd "$ROOT" && pwd)"
TASKS="$(cd "$(dirname "$TASKS")" && pwd)/$(basename "$TASKS")"
RUNS="${RUNS:-5}"
INDEX_RUNS="${INDEX_RUNS:-3}"

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

export ROOT TASKS SHOKA RG_BIN RUNS INDEX_RUNS
python3 <<'PY'
import json, math, os, statistics, subprocess, sys, time
from pathlib import Path

root = os.environ["ROOT"]
tasks_path = os.environ["TASKS"]
shoka = os.environ["SHOKA"]
rg = os.environ["RG_BIN"]
runs = int(os.environ["RUNS"])
index_runs = int(os.environ["INDEX_RUNS"])
tasks = json.load(open(tasks_path))


def mean_std(xs):
    if not xs:
        return None, None
    if len(xs) == 1:
        return xs[0], 0.0
    return statistics.fmean(xs), statistics.stdev(xs)


def path_matches(path, wants):
    return any(w in path or path.endswith(w) for w in wants)


def matched_wants(paths, wants):
    hit = []
    for w in wants:
        for p in paths:
            if w in p or p.endswith(w):
                hit.append(w)
                break
    return hit


def first_rank(paths, wants):
    for i, p in enumerate(paths, 1):
        if path_matches(p, wants):
            return i, p
    return None, None


def file_stats(rel):
    p = Path(root) / rel
    try:
        data = p.read_bytes()
    except OSError:
        return 0, 0
    lines = data.count(b"\n") + (0 if data.endswith(b"\n") or not data else 1)
    return len(data), lines


def read_cost(paths, wants, cap):
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
        "est_tokens": bytes_n // 4 if bytes_n else 0,
    }


def time_cmd(argv, discard_stdout=True):
    t0 = time.perf_counter()
    proc = subprocess.run(argv, capture_output=True, text=True)
    ms = (time.perf_counter() - t0) * 1000
    return ms, proc.returncode, proc.stdout, proc.stderr


print(f"== index multi-run x{index_runs}", file=sys.stderr)
full_ms, noop_ms = [], []
db_bytes = None
status_snap = None
for i in range(index_runs):
    ms, rc, out, err = time_cmd([shoka, "index", "--force", "--root", root])
    if rc != 0:
        sys.stderr.write(err or out)
        raise SystemExit(f"index --force failed: {rc}")
    full_ms.append(ms)
    print(f"  full[{i+1}] {ms/1000:.2f}s", file=sys.stderr)
    ms, rc, out, err = time_cmd([shoka, "index", "--root", root])
    if rc != 0:
        raise SystemExit(f"index noop failed: {rc}")
    noop_ms.append(ms)
    print(f"  noop[{i+1}] {ms/1000:.3f}s", file=sys.stderr)

ms, rc, out, err = time_cmd([shoka, "status", "--root", root, "--json"], discard_stdout=False)
if rc == 0 and out.strip():
    try:
        status_snap = json.loads(out)
    except json.JSONDecodeError:
        status_snap = {"raw": out.strip()}
db = Path(root) / ".shoka" / "index.db"
if db.is_file():
    db_bytes = db.stat().st_size

full_mean, full_std = mean_std(full_ms)
noop_mean, noop_std = mean_std(noop_ms)


def shoka_search(query, top):
    ms, rc, out, err = time_cmd(
        [shoka, "search", query, "--root", root, "--top", str(top), "--json"]
    )
    if rc != 0:
        raise RuntimeError(err or out)
    hits = json.loads(out) or []
    paths = [h.get("path", "") for h in hits if isinstance(h, dict)]
    return paths, ms


def rg_files(query):
    ms, rc, out, err = time_cmd(
        [
            rg, "-l", "-F", "--hidden",
            "--glob", "!.git/**",
            "--glob", "!.shoka/**",
            "--glob", "!.claude/**",
            "--glob", "!.cursor/**",
            query, root,
        ]
    )
    paths, seen = [], set()
    for line in (out or "").splitlines():
        line = line.strip()
        if not line:
            continue
        rel = os.path.relpath(line, root).replace("\\", "/")
        if rel.startswith("..") or rel in seen:
            continue
        seen.add(rel)
        paths.append(rel)
    return paths, ms


def rg_best(query, wants):
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
        rank, hit = first_rank(paths, wants)
        options.append({"query_used": q, "paths": paths, "ms": ms, "rank": rank, "hit": hit, "n": len(paths)})
    options.sort(key=lambda o: (
        0 if o["rank"] is not None else 1,
        o["rank"] or 10**9,
        o["n"],
        0 if o["query_used"] == query else 1,
    ))
    return options[0]


def metrics_for(paths, wants, ks=(1, 5, 10)):
    rank, hit = first_rank(paths, wants)
    out = {
        "first_rank": rank,
        "first_hit": hit,
        "mrr": (1.0 / rank) if rank else 0.0,
        "success_at": {},
        "coverage_at": {},
    }
    n_gold = max(1, len(wants))
    for k in ks:
        top = paths[:k]
        out["success_at"][str(k)] = 1.0 if first_rank(top, wants)[0] else 0.0
        out["coverage_at"][str(k)] = len(matched_wants(top, wants)) / n_gold
    return out


print(f"== retrieval ranking + latency x{runs}", file=sys.stderr)
rows = []
for t in tasks:
    q = t.get("query") or ""
    wants = t.get("want_paths") or []
    top = int(t.get("top_k") or 10)
    if not q or not wants:
        continue

    # ranking: single deterministic pass (also records paths)
    spaths, _ = shoka_search(q, max(top, 10))
    sm = metrics_for(spaths, wants)
    scost = read_cost(spaths, wants, top)

    best = rg_best(q, wants)
    rpaths = best["paths"]
    rm = metrics_for(rpaths, wants)
    rcost = read_cost(rpaths, wants, 30)

    # latency multi-run
    s_lat, r_lat = [], []
    for _ in range(runs):
        _, ms = shoka_search(q, top)
        s_lat.append(ms)
        _, ms = rg_files(best["query_used"])
        r_lat.append(ms)
    s_mean, s_std = mean_std(s_lat)
    r_mean, r_std = mean_std(r_lat)

    row = {
        "id": t.get("id"),
        "difficulty": t.get("difficulty"),
        "query": q,
        "want_n": len(wants),
        "top_k": top,
        "shoka": {
            "metrics": sm,
            "n_hits": len(spaths),
            "paths": spaths[:top],
            "read_until_hit": scost,
            "latency_ms": {"runs": runs, "mean": round(s_mean, 2), "stdev": round(s_std, 2), "samples": [round(x, 2) for x in s_lat]},
        },
        "ripgrep": {
            "query_used": best["query_used"],
            "n_files_matched": best["n"],
            "metrics": rm,
            "paths_top": rpaths[:top],
            "read_until_hit_cap30": rcost,
            "latency_ms": {"runs": runs, "mean": round(r_mean, 2), "stdev": round(r_std, 2), "samples": [round(x, 2) for x in r_lat]},
        },
    }
    rows.append(row)
    print(
        f"  {t.get('id')}: shoka@{sm['first_rank']} rg@{rm['first_rank']} "
        f"MRR {sm['mrr']:.2f}/{rm['mrr']:.2f} "
        f"lat {s_mean:.1f}±{s_std:.1f} / {r_mean:.1f}±{r_std:.1f} ms",
        file=sys.stderr,
    )


def aggregate(side):
    n = len(rows)
    def avg_success(k):
        return sum(r[side]["metrics"]["success_at"][str(k)] for r in rows) / n
    def avg_cov(k):
        return sum(r[side]["metrics"]["coverage_at"][str(k)] for r in rows) / n
    mrr = sum(r[side]["metrics"]["mrr"] for r in rows) / n
    ranks = [r[side]["metrics"]["first_rank"] for r in rows if r[side]["metrics"]["first_rank"]]
    cost_key = "read_until_hit" if side == "shoka" else "read_until_hit_cap30"
    costs = [r[side][cost_key] for r in rows]
    lats = [r[side]["latency_ms"]["mean"] for r in rows]
    return {
        "n": n,
        "success_at_1": round(avg_success(1), 4),
        "success_at_5": round(avg_success(5), 4),
        "success_at_10": round(avg_success(10), 4),
        "coverage_at_1": round(avg_cov(1), 4),
        "coverage_at_5": round(avg_cov(5), 4),
        "coverage_at_10": round(avg_cov(10), 4),
        "mrr": round(mrr, 4),
        "avg_first_rank_when_hit": round(sum(ranks) / len(ranks), 2) if ranks else None,
        "hit_n": len(ranks),
        "sum_lines_read": sum(c["lines_read"] for c in costs),
        "sum_files_read": sum(c["files_read"] for c in costs),
        "sum_est_tokens": sum(c["est_tokens"] for c in costs),
        "latency_ms_mean_of_means": round(statistics.fmean(lats), 2) if lats else None,
    }


shoka_a = aggregate("shoka")
rg_a = aggregate("ripgrep")

summary = {
    "measured_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    "corpus": {
        "label": "mid-size application repository (anonymized)",
        "indexed_files": (status_snap or {}).get("files") or (status_snap or {}).get("Files"),
        "chunks": (status_snap or {}).get("chunks") or (status_snap or {}).get("Chunks"),
        "index_db_bytes": db_bytes,
        "note": "Counts after ignore rules. On-disk tree may be much larger.",
    },
    "protocol": {
        "metrics_doc": "eval/METRICS.md",
        "references": [
            "Zoekt e2e: Recall@1, Recall@5, MRR",
            "Agent Retrieval Bench: Recall@k coverage, MRR, budgeted yield (approx via lines-to-gold)",
            "Baseline: ripgrep -l -F",
        ],
        "latency_runs": runs,
        "index_runs": index_runs,
        "ranking_deterministic": True,
    },
    "index": {
        "full_rebuild_ms": {
            "runs": index_runs,
            "samples": [round(x, 1) for x in full_ms],
            "mean": round(full_mean, 1),
            "stdev": round(full_std, 1),
            "mean_sec": round(full_mean / 1000, 3),
            "stdev_sec": round(full_std / 1000, 3),
        },
        "incremental_noop_ms": {
            "runs": index_runs,
            "samples": [round(x, 1) for x in noop_ms],
            "mean": round(noop_mean, 1),
            "stdev": round(noop_std, 1),
            "mean_sec": round(noop_mean / 1000, 3),
            "stdev_sec": round(noop_std / 1000, 3),
        },
    },
    "shoka": shoka_a,
    "ripgrep": rg_a,
    "reduction": {
        "lines_ratio_shoka_over_rg": round(shoka_a["sum_lines_read"] / rg_a["sum_lines_read"], 3)
        if rg_a["sum_lines_read"]
        else None,
        "est_tokens_ratio": round(shoka_a["sum_est_tokens"] / rg_a["sum_est_tokens"], 3)
        if rg_a["sum_est_tokens"]
        else None,
        "lines_saved": rg_a["sum_lines_read"] - shoka_a["sum_lines_read"],
        "mrr_delta": round(shoka_a["mrr"] - rg_a["mrr"], 4),
    },
}

# fill corpus from status text if json shape differs
if summary["corpus"]["indexed_files"] is None:
    ms, rc, out, err = time_cmd([shoka, "status", "--root", root])
    text = out or ""
    import re
    m = re.search(r"files:\s+(\d+)", text)
    if m:
        summary["corpus"]["indexed_files"] = int(m.group(1))
    m = re.search(r"chunks:\s+(\d+)", text)
    if m:
        summary["corpus"]["chunks"] = int(m.group(1))

out = {"summary": summary, "tasks": rows}
json.dump(out, sys.stdout, ensure_ascii=False, indent=2)
print()
print(
    f"summary Success@1 shoka={shoka_a['success_at_1']:.2f} rg={rg_a['success_at_1']:.2f} | "
    f"Success@5 {shoka_a['success_at_5']:.2f}/{rg_a['success_at_5']:.2f} | "
    f"MRR {shoka_a['mrr']:.3f}/{rg_a['mrr']:.3f} | "
    f"lines {shoka_a['sum_lines_read']}/{rg_a['sum_lines_read']} | "
    f"index {full_mean/1000:.2f}±{full_std/1000:.2f}s",
    file=sys.stderr,
)
PY
