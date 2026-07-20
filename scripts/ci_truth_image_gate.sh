#!/usr/bin/env bash
set -euo pipefail

gate_bin=$(command -v super-dolphin-gate 2>/dev/null || true)
if [[ -z "$gate_bin" || ! -x "$gate_bin" ]]; then
  printf '%s\n' 'CI truth-image gate blocked: trusted super-dolphin-gate CLI is not installed.' >&2
  exit 1
fi

if [[ $# -eq 0 ]]; then
  exec "$gate_bin" workflow-host
fi
if [[ $# -ne 1 ]]; then
  printf '%s\n' 'usage: ci_truth_image_gate.sh [local-fast|push|remote-required|release]' >&2
  exit 1
fi

profile=$1
case "$profile" in
  local-fast|push|remote-required|release) ;;
  *)
    printf 'CI truth-image gate blocked: unsupported profile %q.\n' "$profile" >&2
    exit 1
    ;;
esac

repo_root=$(git rev-parse --show-toplevel) || {
  printf '%s\n' 'CI truth-image gate blocked: repository root is unavailable.' >&2
  exit 1
}
cd "$repo_root"
object_format=$(git rev-parse --show-object-format)
commit_sha=$(git rev-parse HEAD)
staged_tree=$(git write-tree)
commit_tree=$(git rev-parse "${commit_sha}^{tree}")

submit_args=(
  submit --wait
  --profile "$profile"
  --object-format "$object_format"
)
if [[ "$profile" == "release" ]]; then
  if [[ "$staged_tree" != "$commit_tree" ]]; then
    printf '%s\n' 'CI truth-image gate blocked: release requires the staged index to match HEAD; commit or unstage the candidate changes first.' >&2
    exit 1
  fi
  submit_args+=(
    --commit "$commit_sha"
    --source-tree "$commit_tree"
  )
  exec "$gate_bin" _production-launcher "${submit_args[@]}"
fi
submit_args+=(
  --tree "$staged_tree"
  --parent "$commit_sha"
  --source-tree "$staged_tree"
)
exec "$gate_bin" "${submit_args[@]}"
