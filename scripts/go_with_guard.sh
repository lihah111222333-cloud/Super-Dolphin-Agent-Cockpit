#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GLOBAL_GO_WRAPPER="/Users/mima0000/.local/bin/go"

usage() {
  cat <<'USAGE'
Usage:
  scripts/go_with_guard.sh <go-subcommand> [args...]

Examples:
  scripts/go_with_guard.sh test ./internal/provider/claudecli/... -count=1
  scripts/go_with_guard.sh build ./...
  scripts/go_with_guard.sh vet ./...
USAGE
}

resolve_real_go() {
  if [[ -n "${REAL_GO_BIN:-}" && -x "${REAL_GO_BIN}" ]]; then
    printf '%s\n' "$REAL_GO_BIN"
    return 0
  fi

  local self_dir self_path candidate
  self_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  self_path="$self_dir/go"
  while IFS= read -r candidate; do
    [[ -n "$candidate" ]] || continue
    if [[ "$candidate" != "$self_path" && "$candidate" != "$GLOBAL_GO_WRAPPER" ]]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done < <(which -a go 2>/dev/null || true)

  echo "❌ 未找到真实 go 二进制，请先设置 REAL_GO_BIN。" >&2
  exit 1
}

run_guard() {
  local real_go="$1"
  (
    cd "$ROOT_DIR"
    ./scripts/forbid_raw_go_test.sh
    "$real_go" run ./scripts/code_size_guard.go
    "$real_go" test -run TestCodeSizeGuard ./internal/archtest/... -count=1
  )
}

main() {
  if [[ $# -lt 1 ]]; then
    usage
    exit 1
  fi

  case "$1" in
    --help|-h)
      usage
      exit 0
      ;;
  esac

  local real_go
  real_go="$(resolve_real_go)"
  case "$1" in
    test|build|vet)
      run_guard "$real_go"
      ;;
  esac

  (
    cd "$ROOT_DIR"
    "$real_go" "$@"
  )
}

main "$@"
