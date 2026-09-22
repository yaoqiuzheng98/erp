#!/usr/bin/env bash
# MongoDB 备份：读取 config.toml 的 mongo.uri / mongo.database，输出到 backups/<db>-<ts>/
set -euo pipefail

CONFIG="${1:-config.toml}"
OUT_DIR="${2:-backups}"

uri=$(grep -E '^\s*uri\s*=' "$CONFIG" | head -1 | sed -E 's/.*"([^"]+)".*/\1/')
db=$(grep -E '^\s*database\s*=' "$CONFIG" | head -1 | sed -E 's/.*"([^"]+)".*/\1/')
uri="${uri:-mongodb://localhost:27017}"
db="${db:-erp}"

ts=$(date +%Y%m%d-%H%M%S)
dest="$OUT_DIR/${db}-${ts}"
mkdir -p "$dest"

if command -v mongodump >/dev/null 2>&1; then
  mongodump --uri="$uri" --db="$db" --out="$dest"
elif docker ps --format '{{.Names}}' | grep -qx erp-mongo; then
  docker exec erp-mongo mongodump --uri="$uri" --db="$db" --out=/dump >/dev/null
  docker cp "erp-mongo:/dump/$db" "$dest/$db"
  docker exec erp-mongo rm -rf /dump
else
  echo "ERROR: mongodump 不可用且未发现 erp-mongo 容器" >&2
  exit 1
fi
echo "backup done -> $dest"
