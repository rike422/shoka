#!/usr/bin/env bash
# Evaluate recall@k for a tasks JSON against an already-indexed repo.
set -euo pipefail

ROOT="${1:-}"
TASKS="${2:-}"
if [[ -z "$ROOT" || -z "$TASKS" ]]; then
  echo "usage: $0 /path/to/repo /path/to/tasks.json" >&2
  exit 2
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SHOKA="${SCRIPT_DIR}/../dist/shoka"
[[ -x "$SHOKA" ]] || (cd "${SCRIPT_DIR}/.." && CGO_ENABLED=1 go build -tags fts5 -o dist/shoka ./cmd/shoka)

python3 - "$ROOT" "$TASKS" "$SHOKA" <<'PY'
import json, subprocess, sys
root, tasks_path, shoka = sys.argv[1:4]
tasks = json.load(open(tasks_path))
ok = total = 0
ranks = []
for t in tasks:
    q = t.get("query") or ""
    wants = t.get("want_paths") or []
    if not q or not wants:
        print(f"skip {t.get('id')}: missing query/want_paths")
        continue
    top = int(t.get("top_k") or 10)
    out = subprocess.check_output(
        [shoka, "search", q, "--root", root, "--top", str(top), "--json"],
        text=True,
    )
    hits = json.loads(out) or []
    if not isinstance(hits, list):
        print(f"NG\t{t.get('id')}\tbad json: {out[:200]}")
        total += 1
        continue
    paths = [h.get("path", "") for h in hits]
    rank = None
    matched = None
    for i, p in enumerate(paths, 1):
        for w in wants:
            if w in p or p.endswith(w):
                rank = i
                matched = p
                break
        if rank is not None:
            break
    total += 1
    ok += int(rank is not None)
    if rank is not None:
        ranks.append(rank)
        print(f"OK\t{t.get('id')}\trank={rank}\t{q}\t-> {matched}")
    else:
        print(f"NG\t{t.get('id')}\t{q}\tpaths={paths[:8]}")
avg = (sum(ranks) / len(ranks)) if ranks else 0
print(f"recall@k: {ok}/{total}  avg_rank_when_hit={avg:.2f}")
sys.exit(0 if ok == total and total else 1)
PY
