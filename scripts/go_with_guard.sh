#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "${BASH_SOURCE[0]%/*}/.." && pwd)"
source "$ROOT_DIR/scripts/real_go_resolver.sh"

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
  if ! real_go="$(resolve_real_go)"; then
    exit 1
  fi
  case "$1" in
    test|build|vet)
      (
        unset GOOS GOARCH CGO_ENABLED
        run_guard "$real_go"
      )
      ;;
  esac

  (
    cd "$ROOT_DIR"
    "$real_go" "$@"
  )
}

main "$@"
