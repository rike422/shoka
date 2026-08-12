#!/usr/bin/env bash
set -euo pipefail

ROOT="${1:-}"
if [[ -z "$ROOT" ]]; then
  echo "usage: $0 /path/to/repo [query ...]" >&2
  echo "  or:  QUERIES='foo bar' $0 /path/to/repo" >&2
  exit 2
fi
ROOT="$(cd "$ROOT" && pwd)"
shift

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SHOKA="${SCRIPT_DIR}/../dist/shoka"
if [[ ! -x "$SHOKA" ]]; then
  echo "building shoka..." >&2
  (cd "${SCRIPT_DIR}/.." && CGO_ENABLED=1 go build -tags fts5 -o dist/shoka ./cmd/shoka)
fi

echo "== target: $ROOT"
echo "== full rebuild"
/usr/bin/time -p "$SHOKA" index --force --root "$ROOT"
"$SHOKA" status --root "$ROOT"
DB="$ROOT/.shoka/index.db"
if [[ -f "$DB" ]]; then
  echo "db_bytes=$(wc -c < "$DB")"
fi

echo "== incremental noop"
/usr/bin/time -p "$SHOKA" index --root "$ROOT"

# Smoke searches: extra args, else QUERIES env, else generic defaults.
if [[ $# -gt 0 ]]; then
  SAMPLE_QUERIES=("$@")
elif [[ -n "${QUERIES:-}" ]]; then
  # shellcheck disable=SC2206
  SAMPLE_QUERIES=($QUERIES)
else
  SAMPLE_QUERIES=(main config error index)
fi

echo "== sample searches"
for q in "${SAMPLE_QUERIES[@]}"; do
  echo "--- search: $q"
  /usr/bin/time -p "$SHOKA" search "$q" --root "$ROOT" --top 5 2>/dev/null | head -20 || true
done
