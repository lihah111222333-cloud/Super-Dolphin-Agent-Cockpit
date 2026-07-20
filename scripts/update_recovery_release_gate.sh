#!/usr/bin/env bash
set -euo pipefail

./scripts/test_with_guard.sh ./internal/platform/appupdaterecovery ./cmd/super-dolphin-updater ./cmd/super-dolphin-guard ./internal/app -count=1
