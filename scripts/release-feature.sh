#!/usr/bin/env bash
# 生产上线合并：只允许把特性分支合入 main，禁止 test。
# 用法：scripts/release-feature.sh <feature-branch>
# 只做合并，不自动构建/部署；合并后按 AGENTS.md 打生产镜像。
set -euo pipefail

SRC="${1:-}"
if [ -z "$SRC" ]; then
  echo "用法: scripts/release-feature.sh <feature-branch>" >&2
  exit 1
fi
SRC="${SRC#refs/heads/}"

case "$SRC" in
  test|origin/test|refs/remotes/origin/test)
    echo "ERROR: 禁止把 test 合入 main；test 可能包含废弃功能，请合并具体特性分支。" >&2
    exit 1
    ;;
  main)
    echo "ERROR: 源分支不能是 main。" >&2
    exit 1
    ;;
esac

if [ "$(git branch --show-current)" != "main" ]; then
  echo "ERROR: 请先切到 main 再执行。" >&2
  exit 1
fi
if ! git show-ref --verify --quiet "refs/heads/$SRC"; then
  echo "ERROR: 必须是本地特性分支名（不要用 commit/远程分支）: $SRC" >&2
  exit 1
fi
if [ -n "$(git status --porcelain)" ]; then
  echo "ERROR: 工作区不干净，请先提交或清理。" >&2
  exit 1
fi
if ! git rev-parse --verify --quiet "$SRC^{commit}" >/dev/null; then
  echo "ERROR: 分支/提交不存在: $SRC" >&2
  exit 1
fi
if [ "$(git rev-list --count "main..$SRC")" -eq 0 ]; then
  echo "ERROR: $SRC 没有需要合并的新提交。" >&2
  exit 1
fi

# 特性分支如果曾经合并过 test，会把 test 的废弃功能带进来，拒绝上线。
if git log --merges --format='%s' "main..$SRC" | grep -Eq "Merge (remote-tracking )?branch '(origin/)?test'"; then
  echo "ERROR: $SRC 历史中包含 test 合并提交，可能带进废弃功能，拒绝上线。" >&2
  exit 1
fi

echo "==> 将要合并的提交："
git log --oneline --no-decorate "main..$SRC"

if [ "${CHECK_ONLY:-0}" = "1" ]; then
  echo "==> CHECK_ONLY=1，仅检查，未合并。"
  exit 0
fi

echo "==> 合并 $SRC 到 main（--no-ff）"
git merge --no-ff "$SRC" -m "merge: $SRC into main"
echo "==> 完成。下一步：从 main 构建生产镜像并部署。"
