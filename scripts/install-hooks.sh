#!/usr/bin/env bash
# 安装 super-agent-v3 git hooks
# 通过 core.hooksPath 指向 .githooks/ 绝对路径，让普通 checkout 和 linked worktree 使用同一套钩子。
# 用法：在仓库根目录执行 `bash scripts/install-hooks.sh`

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

if [[ ! -d .githooks ]]; then
    echo "❌ 未找到 .githooks 目录，请确认在仓库根目录执行" >&2
    exit 1
fi

HOOKS_DIR="$REPO_ROOT/.githooks"
git config core.hooksPath "$HOOKS_DIR"
chmod +x .githooks/* 2>/dev/null || true

echo "✅ git hooks 已启用"
echo "   core.hooksPath = $(git config --get core.hooksPath)"
echo ""
echo "已安装钩子："
ls -1 .githooks | sed 's|^|   - |'
echo ""
echo "提示：紧急事故修复可临时绕过 → git commit --no-verify（事后必须补跑守卫）"
