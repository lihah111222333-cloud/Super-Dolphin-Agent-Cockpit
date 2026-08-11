#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

ALLOWLIST=(
  "$ROOT_DIR/scripts/test_with_guard.sh"
  "$ROOT_DIR/scripts/go_with_guard.sh"
  "$ROOT_DIR/scripts/activate_guard_env.sh"
  "$ROOT_DIR/scripts/forbid_raw_go_test.sh"
)

is_allowlisted() {
  local candidate="$1"
  local allowed
  for allowed in "${ALLOWLIST[@]}"; do
    if [[ "$candidate" == "$allowed" ]]; then
      return 0
    fi
  done
  return 1
}

collect_targets() {
  printf '%s\n' "$ROOT_DIR/Makefile"
  if [[ -d "$ROOT_DIR/.github/workflows" ]]; then
    find "$ROOT_DIR/.github/workflows" -type f \( -name '*.yml' -o -name '*.yaml' \)
  fi
  if [[ -d "$ROOT_DIR/scripts" ]]; then
    find "$ROOT_DIR/scripts" -type f -name '*.sh'
  fi
}

scan_file() {
  local file="$1"
  local rel="${file#$ROOT_DIR/}"
  local line_no=0
  local line
  while IFS= read -r line || [[ -n "$line" ]]; do
    ((line_no += 1))
    [[ "$line" =~ ^[[:space:]]*# ]] && continue
    if [[ "$line" =~ (^|[[:space:];|&])go[[:space:]]+test([[:space:]]|$) ]]; then
      printf '%s:%d:%s\n' "$rel" "$line_no" "$line"
    fi
  done < "$file"
}

main() {
  local -a violations=()
  local target
  while IFS= read -r target; do
    [[ -n "$target" && -f "$target" ]] || continue
    if is_allowlisted "$target"; then
      continue
    fi
    while IFS= read -r violation; do
      [[ -n "$violation" ]] && violations+=("$violation")
    done < <(scan_file "$target")
  done < <(collect_targets | sort -u)

  if (( ${#violations[@]} > 0 )); then
    echo "❌ 入口守卫: 发现裸跑 go test 入口 (${#violations[@]} 项)" >&2
    echo >&2
    echo "原因:" >&2
    echo "  仓库级测试必须先经过代码守卫，否则会绕过 CodeSizeGuard。" >&2
    echo "  请不要在 Makefile / scripts / CI workflow 中直接写裸 'go test ...'。" >&2
    echo >&2
    echo "正确做法:" >&2
    echo "  - 轻量/中量精确 Go 测试使用 scripts/test_with_guard.sh --host-test <light|medium>" >&2
    echo "  - race、benchmark、fuzz、整包、重型门禁和未知耗时测试使用 super-dolphin-gate test --target=remote 进入 ECI" >&2
    echo "  - 宿主结果固定为 LOCAL_NON_AUTHORITATIVE，不得替代 ECI PASS" >&2
    echo >&2
    echo "违规位置:" >&2
    local item
    for item in "${violations[@]}"; do
      echo "  • $item" >&2
    done
    exit 1
  fi

  echo "✅ 入口守卫: 未发现裸跑 go test 入口。"
}

main "$@"
