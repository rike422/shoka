#!/usr/bin/env bash
set -euo pipefail

ROOT="${1:-}"
if [[ -z "$ROOT" ]]; then
  echo "usage: $0 /path/to/repo" >&2
  exit 2
fi
ROOT="$(cd "$ROOT" && pwd)"

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

echo "== sample searches"
for q in MatchEngine config "試合" player; do
  echo "--- search: $q"
  /usr/bin/time -p "$SHOKA" search "$q" --root "$ROOT" --top 5 2>/dev/null | head -20 || true
done
