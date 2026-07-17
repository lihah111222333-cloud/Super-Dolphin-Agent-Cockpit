#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)
exec bash "$repo_root/scripts/tests/test_gate_hook_entrypoints.sh"
