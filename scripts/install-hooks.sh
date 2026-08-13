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
CURRENT_HOOKS_PATH="$(git config --get core.hooksPath 2>/dev/null || true)"
if [[ -n "$CURRENT_HOOKS_PATH" && "$CURRENT_HOOKS_PATH" != ".githooks" ]]; then
    echo "existing core.hooksPath = $CURRENT_HOOKS_PATH; refusing to replace it automatically" >&2
    exit 1
fi
STAGED_TREE=$(git write-tree) || {
    echo "super-dolphin gate launcher build requires a readable staged tree" >&2
    exit 1
}
BUILDER_PATH=$(mktemp "${TMPDIR:-/tmp}/super-dolphin-launcher-builder.XXXXXX")
cleanup_builder() {
    rm -f -- "$BUILDER_PATH"
}
trap cleanup_builder EXIT HUP INT TERM
if ! git cat-file blob "$STAGED_TREE:scripts/build-trusted-gate-launcher.sh" >"$BUILDER_PATH"; then
    echo "super-dolphin gate launcher builder is missing from the exact staged tree" >&2
    exit 1
fi
chmod 0500 "$BUILDER_PATH"
LAUNCHER=$("$BUILDER_PATH" "$STAGED_TREE")
if [[ -z "$LAUNCHER" ]]; then
    echo "super-dolphin gate launcher is not provisioned" >&2
    exit 1
fi
if ! validate_trusted_gate_launcher "$LAUNCHER"; then
    echo "super-dolphin gate launcher preflight failed; refusing to install hooks" >&2
    exit 1
fi
if ! verify_trusted_gate_launcher_tree "$REPO_ROOT" "$LAUNCHER" "$STAGED_TREE"; then
    echo "super-dolphin gate launcher does not match the exact staged tree" >&2
    exit 1
fi
LAUNCHER_DIGEST_DIR=$(dirname "$LAUNCHER")
LAUNCHER_TREE_DIR=$(dirname "$LAUNCHER_DIGEST_DIR")
LAUNCHER_VERSION_DIR=$(dirname "$LAUNCHER_TREE_DIR")
LAUNCHER_ROOT=$(dirname "$LAUNCHER_VERSION_DIR")
if [[ "$(basename "$LAUNCHER")" != "super-dolphin-gate" \
    || "$(basename "$LAUNCHER_TREE_DIR")" != "$STAGED_TREE" \
    || "$(basename "$LAUNCHER_VERSION_DIR")" != "v2" \
    || ! "$(basename "$LAUNCHER_DIGEST_DIR")" =~ ^[0-9a-f]{64}$ ]]; then
    echo "super-dolphin gate launcher path is not content-addressed by the staged tree" >&2
    exit 1
fi

git config --local core.hooksPath .githooks
git config --local superdolphin.gateLauncher "$LAUNCHER"
git config --local superdolphin.gateLauncherRoot "$LAUNCHER_ROOT"
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
