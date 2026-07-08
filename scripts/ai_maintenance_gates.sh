#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "${BASH_SOURCE[0]%/*}/.." && pwd)"

main() {
  cd "$ROOT_DIR"
  go run ./scripts/ai_maintenance run "$@"
}

main "$@"
