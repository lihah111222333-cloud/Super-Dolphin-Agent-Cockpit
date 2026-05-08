#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GLOBAL_GO_WRAPPER="${GLOBAL_GO_WRAPPER:-}"

resolve_real_go() {
  local candidate
  while IFS= read -r candidate; do
    [[ -n "$candidate" ]] || continue
    if [[ "$candidate" != "$GLOBAL_GO_WRAPPER" ]]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done < <(which -a go 2>/dev/null || true)
  return 1
}

REAL_GO_BIN_VALUE="${REAL_GO_BIN:-$(resolve_real_go || true)}"

if [[ -z "$REAL_GO_BIN_VALUE" ]]; then
  echo "❌ 未找到真实 go 二进制，无法启动受守卫保护的 shell。" >&2
  exit 1
fi

export REAL_GO_BIN="$REAL_GO_BIN_VALUE"
case ":$PATH:" in
  *":$ROOT_DIR/scripts:"*) ;;
  *) export PATH="$ROOT_DIR/scripts:$PATH" ;;
esac

cat <<EOF_MSG
✅ 已进入受守卫保护的 shell 环境
- 当前 REAL_GO_BIN: $REAL_GO_BIN
- 裸跑 'go test/build/vet' 将被拦截并给出替代命令
- 正确做法也可以直接使用: ./scripts/go_with_guard.sh ...
EOF_MSG

exec "${SHELL:-/bin/zsh}"
