#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "${BASH_SOURCE[0]%/*}/../.." && pwd)"
exec "$ROOT_DIR/scripts/guard_env_test.sh"
