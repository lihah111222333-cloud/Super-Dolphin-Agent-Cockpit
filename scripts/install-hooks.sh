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

source "$REPO_ROOT/.githooks/trusted-gate-launcher.sh"
LAUNCHER="${SUPER_DOLPHIN_GATE_LAUNCHER:-$(git config --local --get superdolphin.gateLauncher 2>/dev/null || true)}"
if [[ -z "$LAUNCHER" ]]; then
    echo "super-dolphin gate launcher is not provisioned; rerun with SUPER_DOLPHIN_GATE_LAUNCHER=/absolute/path" >&2
    exit 1
fi
if ! validate_trusted_gate_launcher "$LAUNCHER"; then
    echo "super-dolphin gate launcher preflight failed; refusing to install hooks" >&2
    exit 1
fi

CURRENT_HOOKS_PATH="$(git config --get core.hooksPath 2>/dev/null || true)"
if [[ -n "$CURRENT_HOOKS_PATH" && "$CURRENT_HOOKS_PATH" != ".githooks" ]]; then
    echo "existing core.hooksPath = $CURRENT_HOOKS_PATH; refusing to replace it automatically" >&2
    exit 1
fi

git config --local core.hooksPath .githooks
git config --local superdolphin.gateLauncher "$LAUNCHER"
shopt -s nullglob
for hook in .githooks/*; do
    chmod +x "$hook"
done

echo "✅ git hooks 已启用"
echo "   core.hooksPath = $(git config --get core.hooksPath)"
echo "   trusted gate launcher = $(git config --local --get superdolphin.gateLauncher)"
echo ""
echo "已安装钩子："
for hook in .githooks/*; do
    printf '   - %s\n' "${hook#.githooks/}"
done
echo ""
echo "已有 hooks 不会被安装流程静默替换；请先显式检查并迁移后再重试。"
