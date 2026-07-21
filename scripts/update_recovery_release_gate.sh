#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

./scripts/test_with_guard.sh ./internal/platform/appupdaterecovery ./cmd/super-dolphin-updater ./cmd/super-dolphin-guard ./internal/app -count=1
