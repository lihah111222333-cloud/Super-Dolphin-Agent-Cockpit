#!/usr/bin/env bash

real_go_resolver_script_dir() {
  local source_path="${BASH_SOURCE[0]}"
  case "$source_path" in
    */*) cd -- "${source_path%/*}" && pwd ;;
    *) pwd ;;
  esac
}

real_go_resolver_abs_path() {
  local path="$1"
  local dir base
  dir="${path%/*}"
  base="${path##*/}"
  if [[ "$dir" == "$path" ]]; then
    dir="."
  fi
  dir="$(cd -P -- "$dir" 2>/dev/null && pwd -P)" || return 1
  printf '%s/%s\n' "$dir" "$base"
}

real_go_resolver_real_path() {
  local path="$1"
  local abs target dir
  abs="$(real_go_resolver_abs_path "$path")" || return 1
  while [[ -L "$abs" ]]; do
    target="$(/usr/bin/readlink "$abs")" || return 1
    case "$target" in
      /*)
        abs="$(real_go_resolver_abs_path "$target")" || return 1
        ;;
      *)
        dir="${abs%/*}"
        abs="$(real_go_resolver_abs_path "$dir/$target")" || return 1
        ;;
    esac
  done
  printf '%s\n' "$abs"
}

real_go_resolver_error() {
  echo "❌ 未找到真实 go 二进制。请设置 REAL_GO_BIN=/absolute/path/to/go (absolute path)，或安装 Go 并确保 PATH 可见。" >&2
}

require_remote_test_execution() {
  if [[ "${SUPER_DOLPHIN_TEST_BACKEND:-}" == "remote-worker" ]]; then
    return 0
  fi
  cat >&2 <<'EOF_MSG'
❌ 禁止在宿主机直接执行仓库测试

所有测试请求必须先经过 super-dolphin-gate test 的共享 PASS 缓存与轻量判定。
包级、race、benchmark、Vitest、未知耗时或多个 cache miss 会自动进入 ECI；
宿主机只允许 CLI 放行的单个精确轻量 Go Test。
EOF_MSG
  return 2
}

resolve_real_go() {
  local scripts_dir wrapper_go global_wrapper candidate candidate_abs
  scripts_dir="$(real_go_resolver_script_dir)"
  wrapper_go="$scripts_dir/go"
  global_wrapper=""
  if [[ -n "${GLOBAL_GO_WRAPPER:-}" ]]; then
    if ! global_wrapper="$(real_go_resolver_abs_path "$GLOBAL_GO_WRAPPER" 2>/dev/null)"; then
      echo "❌ GLOBAL_GO_WRAPPER 必须是可解析路径: $GLOBAL_GO_WRAPPER" >&2
      return 1
    fi
    if [[ ! -e "$global_wrapper" ]]; then
      echo "❌ GLOBAL_GO_WRAPPER 不存在: $GLOBAL_GO_WRAPPER" >&2
      return 1
    fi
    global_wrapper="$(real_go_resolver_real_path "$global_wrapper")"
  fi

  if [[ -n "${REAL_GO_BIN:-}" ]]; then
    local real_go_abs real_go_real wrapper_go_real
    case "$REAL_GO_BIN" in
      /*) ;;
      *)
        echo "❌ REAL_GO_BIN 必须是 absolute path: $REAL_GO_BIN" >&2
        return 1
        ;;
    esac
    if ! real_go_abs="$(real_go_resolver_abs_path "$REAL_GO_BIN" 2>/dev/null)"; then
      echo "❌ REAL_GO_BIN 必须是可解析路径: $REAL_GO_BIN" >&2
      return 1
    fi
    if [[ ! -x "$real_go_abs" ]]; then
      echo "❌ REAL_GO_BIN 不可执行: $REAL_GO_BIN" >&2
      return 1
    fi
    real_go_real="$(real_go_resolver_real_path "$real_go_abs")"
    wrapper_go_real="$(real_go_resolver_real_path "$wrapper_go")"
    if [[ "$real_go_real" == "$wrapper_go_real" ]]; then
      echo "❌ REAL_GO_BIN 指向仓库 go wrapper，不是真实 go 二进制: $real_go_real" >&2
      return 1
    fi
    if [[ -n "$global_wrapper" && "$real_go_real" == "$global_wrapper" ]]; then
      echo "❌ REAL_GO_BIN 指向 GLOBAL_GO_WRAPPER，不是真实 go 二进制: $real_go_real" >&2
      return 1
    fi
    printf '%s\n' "$real_go_real"
    return 0
  fi

  while IFS= read -r candidate; do
    [[ -n "$candidate" ]] || continue
    [[ -x "$candidate" ]] || continue
    candidate_abs="$(real_go_resolver_abs_path "$candidate" 2>/dev/null || true)"
    [[ -n "$candidate_abs" ]] || continue
    candidate_abs="$(real_go_resolver_real_path "$candidate_abs")"
    if [[ "$candidate_abs" == "$(real_go_resolver_real_path "$wrapper_go")" ]]; then
      continue
    fi
    if [[ -n "$global_wrapper" && "$candidate_abs" == "$global_wrapper" ]]; then
      continue
    fi
    printf '%s\n' "$candidate_abs"
    return 0
  done < <(type -P -a go 2>/dev/null || true)

  real_go_resolver_error
  return 1
}
