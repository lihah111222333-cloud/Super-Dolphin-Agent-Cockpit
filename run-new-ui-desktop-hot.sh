#!/usr/bin/env bash
# run-new-ui-desktop-hot.sh — start new UI desktop with Vite HMR and Go backend restart-on-change.
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"

export SUPER_DOLPHIN_BACKEND_HOT_RELOAD=1
export SUPER_DOLPHIN_DEV_ENTRYPOINT="${SUPER_DOLPHIN_DEV_ENTRYPOINT:-run-new-ui-desktop-hot.sh}"

exec "$PROJECT_DIR/run-new-ui-desktop.sh" "$@"
