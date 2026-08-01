#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "${BASH_SOURCE[0]%/*}/.." && pwd)"
source "$ROOT_DIR/scripts/real_go_resolver.sh"

if ! REAL_GO_BIN_VALUE="$(resolve_real_go)"; then
  exit 1
fi
export GOTOOLCHAIN=local

export REAL_GO_BIN="$REAL_GO_BIN_VALUE"
case ":$PATH:" in
  *":$ROOT_DIR/scripts:"*) ;;
  *) export PATH="$ROOT_DIR/scripts:$PATH" ;;
esac

cat <<EOF_MSG
✅ 已进入受守卫保护的 shell 环境
- 当前 REAL_GO_BIN: $REAL_GO_BIN
- 裸跑 'go test/build/vet' 将被拦截并给出替代命令
- 测试必须使用受信 super-dolphin-gate test；非轻量目标自动进入 ECI
EOF_MSG

exec "${SHELL:-/bin/zsh}"
