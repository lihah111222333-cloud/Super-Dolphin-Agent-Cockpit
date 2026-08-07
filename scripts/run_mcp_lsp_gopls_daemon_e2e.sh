#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd -- "${BASH_SOURCE[0]%/*}/.." && pwd)"
cd "$root_dir"
exec ./scripts/test_with_guard.sh --quick-guard -tags=e2e ./cmd/mcp-lsp \
  -run '^TestMcpLSPBinaryRealGoplsDaemonExitsAfterLastForwarder_E2E$' \
  -v -timeout 20m -count=1
