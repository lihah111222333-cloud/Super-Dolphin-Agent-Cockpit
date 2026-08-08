#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel) || {
  printf '%s\n' 'trusted launcher build blocked: repository root is unavailable.' >&2
  exit 1
}
tree=${1:-}
if [[ ! "$tree" =~ ^([0-9a-f]{40}|[0-9a-f]{64})$ ]]; then
  printf '%s\n' 'trusted launcher build blocked: exact staged tree is required.' >&2
  exit 1
fi
temp_root=$(mktemp -d "${TMPDIR:-/tmp}/super-dolphin-launcher-builder.XXXXXX")
cleanup() {
  rm -rf -- "$temp_root"
}
trap cleanup EXIT
git -C "$repo_root" archive --format=tar "$tree" | tar -x -C "$temp_root"

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    printf '%s\n' 'trusted launcher build blocked: no system SHA-256 utility is available.' >&2
    return 1
  fi
}

# The bootstrap command itself is compiled from the archive. Recompute every
# bootstrap source digest from the Git tree before Go is allowed to read it.
bootstrap_paths=(
  cmd/build-trusted-gate-launcher
  internal/devtools/trustedlauncher
)
verified_bootstrap_files=0
while IFS= read -r file_path; do
  [[ -n "$file_path" ]] || continue
  expected=$(git -C "$repo_root" cat-file blob "${tree}:${file_path}" | sha256_file -)
  actual=$(sha256_file "$temp_root/$file_path")
  if [[ "$expected" != "$actual" ]]; then
    printf 'trusted launcher build blocked: bootstrap source digest mismatch: %s\n' "$file_path" >&2
    exit 1
  fi
  verified_bootstrap_files=$((verified_bootstrap_files + 1))
done < <(git -C "$repo_root" ls-tree -r --name-only "$tree" -- "${bootstrap_paths[@]}")
if [[ "$verified_bootstrap_files" -eq 0 ]]; then
  printf '%s\n' 'trusted launcher build blocked: exact tree has no bootstrap sources.' >&2
  exit 1
fi

go_binary=$(command -v go 2>/dev/null || true)
if [[ -z "$go_binary" || "$go_binary" != /* ]]; then
  printf '%s\n' 'trusted launcher build blocked: locked Go compiler is unavailable.' >&2
  exit 1
fi

cd "$temp_root"
env -i HOME="${HOME:?}" PATH=/usr/bin:/bin \
  GOENV=off GOFLAGS= GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
  "$go_binary" run -mod=readonly ./cmd/build-trusted-gate-launcher \
    --repository "$repo_root" \
    --tree "$tree"
