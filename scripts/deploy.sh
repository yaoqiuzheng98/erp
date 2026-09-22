#!/usr/bin/env bash
# 一键部署到生产环境：本地编译 → 打镜像 → 上传 → 服务器 docker load + compose up
# 用法：scripts/deploy.sh           （在仓库根目录或任意目录执行均可）
# 可用环境变量覆盖：ERP_HOST / ERP_SSH_KEY / ERP_DIR / ERP_IMAGE
set -euo pipefail

HOST="${ERP_HOST:-23.249.19.239}"
SSH_KEY="${ERP_SSH_KEY:-$HOME/.ssh/erp_prod_key}"
REMOTE_DIR="${ERP_DIR:-/opt/erp}"
IMAGE="${ERP_IMAGE:-erp-app:latest}"
TAR="erp-app.tar.gz"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

echo "==> [1/5] 本地编译 linux/amd64 二进制"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o erp-linux .

echo "==> [2/5] 打镜像 $IMAGE（alpine + 二进制，无需拉取 golang）"
docker build -t "$IMAGE" -f - . <<'DOCKER'
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 erp
WORKDIR /opt/erp
COPY erp-linux /opt/erp/erp
USER erp
EXPOSE 8080
ENTRYPOINT ["/opt/erp/erp", "-config", "/opt/erp/config.toml"]
DOCKER

echo "==> [3/5] 导出镜像包"
docker save "$IMAGE" | gzip > "/tmp/$TAR"
ls -lh "/tmp/$TAR"

echo "==> [4/5] 上传到 $HOST:$REMOTE_DIR"
rsync --partial -e "ssh -i $SSH_KEY" "/tmp/$TAR" "root@$HOST:$REMOTE_DIR/" && rm -f "/tmp/$TAR"

echo "==> [5/5] 服务器部署"
ssh -i "$SSH_KEY" "root@$HOST" bash -s <<REMOTE
set -euo pipefail
cd "$REMOTE_DIR"
if [ ! -f config.prod.toml ]; then
  echo "ERROR: $REMOTE_DIR/config.prod.toml 不存在，先 cp deploy/config.prod.example.toml config.prod.toml 并修改" >&2
  exit 1
fi
git pull --ff-only 2>/dev/null || echo "warn: git pull 跳过（非仓库或有本地改动）"
docker load < "$TAR" && rm -f "$TAR"
docker compose up -d
docker compose ps
REMOTE

echo "==> 完成: https://erp.dokodemo.top"
