#!/usr/bin/env bash
set -euo pipefail

generated_dirs=(
  "internal/store/sqlc"
  "cmd/mcp-orch/store/sqlc"
)

root="$(git rev-parse --show-toplevel)"
cd "$root"

for dir in "${generated_dirs[@]}"; do
  if [[ ! -d "$dir" ]]; then
    echo "ERROR: missing generated SQLC directory for post-processing: $dir" >&2
    exit 1
  fi
  while IFS= read -r -d '' file; do
    gofmt -w -r 'interface{} -> any' "$file"
  done < <(find "$dir" -type f -name '*.go' -print0)
done

if rg -n --glob '*.go' 'interface\{\}' "${generated_dirs[@]}"; then
  echo "ERROR: SQLC post-processing left legacy interface{} syntax in generated output" >&2
  exit 1
fi

echo "sqlc-postprocess: normalized generated empty interfaces to any"
