#!/usr/bin/env bash
# 安装 super-agent-v3 git hooks
# 通过 core.hooksPath 指向 .githooks/，让 pre-commit / commit-msg / pre-push 等钩子随仓库分发。
# 用法：在仓库根目录执行 `bash scripts/install-hooks.sh`

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

if [[ ! -d .githooks ]]; then
    echo "❌ 未找到 .githooks 目录，请确认在仓库根目录执行" >&2
    exit 1
fi

CURRENT_HOOKS_PATH="$(git config --get core.hooksPath 2>/dev/null || true)"
if [[ -n "$CURRENT_HOOKS_PATH" && "$CURRENT_HOOKS_PATH" != ".githooks" ]]; then
    echo "existing core.hooksPath = $CURRENT_HOOKS_PATH; refusing to replace it automatically" >&2
    exit 1
fi

git config --local core.hooksPath .githooks
chmod +x .githooks/* 2>/dev/null || true

echo "✅ git hooks 已启用"
echo "   core.hooksPath = $(git config --get core.hooksPath)"
echo ""
echo "已安装钩子："
ls -1 .githooks | sed 's|^|   - |'
echo ""
echo "已有 hooks 不会被安装流程静默替换；请先显式检查并迁移后再重试。"
