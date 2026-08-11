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

ROOT_DIR="$(cd -- "${BASH_SOURCE[0]%/*}/.." && pwd)"
WRAPPER_GO="$ROOT_DIR/scripts/go"
source "$ROOT_DIR/scripts/real_go_resolver.sh"

if ! REAL_GO_BIN_VALUE="$(resolve_real_go)"; then
  return 1
fi
export GOTOOLCHAIN=local

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
  - 轻量/中量精确 Go 测试使用 scripts/test_with_guard.sh --host-test <light|medium>
  - race、benchmark、fuzz、整包、重型门禁和未知耗时测试使用 super-dolphin-gate test --target=remote 进入 ECI
  - guarded build / vet 通过受信 CI gate 执行
- 退出当前 shell 或手动移除 PATH 前缀后，拦截失效
EOF_MSG
