#!/usr/bin/env bash
set -euo pipefail

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  echo "❌ 请用 source 加载守卫环境，而不是直接执行。" >&2
  echo >&2
  echo "正确做法:" >&2
  echo "  source scripts/activate_guard_env.sh" >&2
  echo "  go test ./internal/provider/claudecli   # 此时会被包装器拦截" >&2
  exit 1
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WRAPPER_GO="$ROOT_DIR/scripts/go"
GLOBAL_GO_WRAPPER="/Users/mima0000/.local/bin/go"

resolve_real_go() {
  local candidate
  while IFS= read -r candidate; do
    [[ -n "$candidate" ]] || continue
    if [[ "$candidate" != "$WRAPPER_GO" && "$candidate" != "$GLOBAL_GO_WRAPPER" ]]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done < <(which -a go 2>/dev/null || true)
  return 1
}

REAL_GO_BIN_VALUE="$(resolve_real_go || true)"
if [[ -z "$REAL_GO_BIN_VALUE" ]]; then
  echo "❌ 未找到真实 go 二进制，无法激活守卫环境。" >&2
  return 1
fi

export REAL_GO_BIN="$REAL_GO_BIN_VALUE"
case ":$PATH:" in
  *":$ROOT_DIR/scripts:"*) ;;
  *) export PATH="$ROOT_DIR/scripts:$PATH" ;;
esac

cat <<EOF_MSG
✅ 已激活仓库 go 守卫环境
- 当前包装器: $WRAPPER_GO
- 当前 REAL_GO_BIN: $REAL_GO_BIN
- 现在在本 shell 中裸跑以下命令会被拦截并返回明确报错:
  - go test ...
  - go build ...
  - go vet ...
- 正确做法:
  - ./scripts/go_with_guard.sh test <args>
  - ./scripts/go_with_guard.sh build <args>
  - ./scripts/go_with_guard.sh vet <args>
- 退出当前 shell 或手动移除 PATH 前缀后，拦截失效
EOF_MSG
