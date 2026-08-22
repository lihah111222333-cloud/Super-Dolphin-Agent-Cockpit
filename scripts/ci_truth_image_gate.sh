#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel) || {
  printf '%s\n' 'CI truth-image gate blocked: repository root is unavailable.' >&2
  exit 1
}
source "$repo_root/.githooks/trusted-gate-launcher.sh"

if [[ $# -ne 1 ]]; then
  printf '%s\n' 'usage: ci_truth_image_gate.sh [local-fast|push|release]' >&2
  exit 1
fi

profile=$1
case "$profile" in
  local-fast|push|release) ;;
  *)
    printf 'CI truth-image gate blocked: unsupported profile %q.\n' "$profile" >&2
    exit 1
    ;;
esac

cd "$repo_root"

staged_tree=$(git write-tree) || {
  printf '%s\n' 'CI truth-image gate blocked: cannot capture the staged tree for local checks.' >&2
  exit 1
}
if ! gate_bin=$(trusted_gate_launcher_for_tree "$repo_root" "$staged_tree"); then
  printf '%s\n' 'CI truth-image gate blocked: trusted launcher receipt does not match the staged tree.' >&2
  exit 1
fi

case "$profile" in
  local-fast|push|release)
    "$gate_bin" project-map check --tree "$staged_tree"
    "$gate_bin" closure check --tree "$staged_tree"
    ;;
  *)
    printf 'CI truth-image gate blocked: unsupported local profile %q.\n' "$profile" >&2
    exit 1
    ;;
esac
