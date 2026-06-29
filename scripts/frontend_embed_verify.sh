#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

dist_dir="${SUPER_DOLPHIN_FRONTEND_DIST_DIR:-frontend-app/dist}"
embed_dir="${SUPER_DOLPHIN_FRONTEND_EMBED_DIR:-cmd/agent-terminal/web-dist}"

if [[ "${SUPER_DOLPHIN_FRONTEND_EMBED_TRACKED_ARTIFACT:-0}" == "1" ]]; then
  echo "tracked frontend embed artifacts are out of scope for this lane" >&2
  exit 1
fi

require_file() {
  local path="$1"
  local label="$2"
  if [[ ! -f "$path" ]]; then
    echo "missing $label: $path" >&2
    exit 1
  fi
}

sha256_file() {
  local path="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$path" | awk '{print $1}'
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$path" | awk '{print $1}'
    return
  fi
  echo "missing SHA-256 tool; install sha256sum or shasum" >&2
  exit 1
}

write_manifest() {
  local root="$1"
  local out="$2"
  (
    cd "$root"
    find . -type f ! -name '.gitkeep' -print0 |
      sort -z |
      while IFS= read -r -d '' file; do
        printf '%s  %s\n' "$(sha256_file "$file")" "${file#./}"
      done
  ) > "$out"
}

if [[ "${SUPER_DOLPHIN_FRONTEND_EMBED_ASSUME_IGNORED:-0}" != "1" ]]; then
  if [[ -n "$(git ls-files -- cmd/agent-terminal/web-dist)" ]]; then
    echo "cmd/agent-terminal/web-dist contains tracked files; tracked artifact mode requires controller approval" >&2
    exit 1
  fi
  if ! git check-ignore -q cmd/agent-terminal/web-dist/index.html; then
    echo "cmd/agent-terminal/web-dist must remain ignored in default frontend embed mode" >&2
    exit 1
  fi
fi

require_file "$dist_dir/index.html" "frontend dist index"
require_file "$embed_dir/index.html" "embedded frontend index"

dist_manifest="$(mktemp -t frontend-dist-manifest.XXXXXX)"
embed_manifest="$(mktemp -t frontend-embed-manifest.XXXXXX)"
cleanup() {
  rm -f "$dist_manifest" "$embed_manifest"
}
trap cleanup EXIT

write_manifest "$dist_dir" "$dist_manifest"
write_manifest "$embed_dir" "$embed_manifest"

if ! diff -u "$dist_manifest" "$embed_manifest"; then
  echo "frontend embed manifest mismatch; run make frontend-app-build and do not hand-edit cmd/agent-terminal/web-dist" >&2
  exit 1
fi

smoke_hash="$(sha256_file "$embed_dir/index.html")"
echo "frontend embed smoke hash: $smoke_hash"
echo "frontend embed manifest verified: $embed_dir"
