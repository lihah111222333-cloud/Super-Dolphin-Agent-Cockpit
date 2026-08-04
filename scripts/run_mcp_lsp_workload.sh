#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd -- "${BASH_SOURCE[0]%/*}/.." && pwd)"
cd "$root_dir"
exec go run ./scripts/mcp_lsp_workload_runner "$@"
