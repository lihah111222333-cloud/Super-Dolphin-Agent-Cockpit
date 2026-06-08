#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

github_repo="${SUPER_DOLPHIN_UPDATE_GITHUB_REPO:-xiaoxiaotest9527-bit/-}"
tag="${VERSION:?VERSION is required, for example v1.0.4}"
stage_dir="${SUPER_DOLPHIN_RELEASE_STAGE_DIR:-$root/dist/release/github/$tag}"
channel="${SUPER_DOLPHIN_UPDATE_CHANNEL:-gray}"
minimum_version="${SUPER_DOLPHIN_UPDATE_MINIMUM_VERSION:-0.0.0}"
dry_run=0
previous_dmg_mount=""
asset_paths=()

cleanup() {
  if [[ -n "$previous_dmg_mount" && -d "$previous_dmg_mount" ]]; then
    hdiutil detach "$previous_dmg_mount" >/dev/null 2>&1 || true
    rmdir "$previous_dmg_mount" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

usage() {
  cat >&2 <<'EOF'
usage: VERSION=v1.0.4 SUPER_DOLPHIN_UPDATE_SIGNING_KEY=/private/key SUPER_DOLPHIN_UPDATE_PUBLIC_KEY=... scripts/publish_github_release.sh [--dry-run] [--stage-dir DIR]
EOF
  exit 2
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run)
      dry_run=1
      shift
      ;;
    --stage-dir)
      [[ $# -ge 2 ]] || usage
      stage_dir="$2"
      shift 2
      ;;
    *)
      usage
      ;;
  esac
done

if [[ "$stage_dir" != /* ]]; then
  stage_dir="$root/$stage_dir"
fi

require_env() {
  local name="$1"
  if [[ -z "${!name:-}" ]]; then
    echo "$name is required" >&2
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

file_size() {
  wc -c < "$1" | tr -d '[:space:]'
}

dotenv_value() {
  local env_file="$1"
  local key="$2"
  awk -v key="$key" '
    index($0, "=") {
      k = substr($0, 1, index($0, "=") - 1)
      if (k == key) {
        print substr($0, index($0, "=") + 1)
        found = 1
        exit
      }
    }
    END { if (!found) exit 1 }
  ' "$env_file"
}

previous_public_key_from_env_file() {
  local env_file="$1"
  if [[ ! -f "$env_file" ]]; then
    echo "previous update .env does not exist: $env_file" >&2
    exit 1
  fi
  dotenv_value "$env_file" SUPER_DOLPHIN_UPDATE_PUBLIC_KEY
}

previous_public_key_from_app() {
  local app="$1"
  local env_file="$app/Contents/Resources/.env"
  if [[ ! -f "$env_file" ]]; then
    echo "previous app update .env does not exist: $env_file" >&2
    exit 1
  fi
  previous_public_key_from_env_file "$env_file"
}

previous_public_key_from_dmg() {
  local dmg="$1"
  if [[ "$(uname -s)" != "Darwin" ]]; then
    echo "SUPER_DOLPHIN_UPDATE_PREVIOUS_DMG can only be inspected on macOS" >&2
    exit 1
  fi
  if [[ ! -f "$dmg" ]]; then
    echo "previous DMG does not exist: $dmg" >&2
    exit 1
  fi
  previous_dmg_mount="$(mktemp -d "${TMPDIR:-/tmp}/super-dolphin-previous-dmg.XXXXXX")"
  hdiutil attach "$dmg" -nobrowse -readonly -mountpoint "$previous_dmg_mount" >/dev/null
  local app
  app="$(find "$previous_dmg_mount" -maxdepth 1 -name '*.app' -type d -print | sort | head -n 1)"
  if [[ -z "$app" ]]; then
    echo "previous DMG does not contain an app bundle" >&2
    exit 1
  fi
  previous_public_key_from_app "$app"
}

require_previous_update_public_key() {
  local previous_public_key=""
  if [[ -n "${SUPER_DOLPHIN_UPDATE_PREVIOUS_ENV_FILE:-}" ]]; then
    previous_public_key="$(previous_public_key_from_env_file "$SUPER_DOLPHIN_UPDATE_PREVIOUS_ENV_FILE")"
  elif [[ -n "${SUPER_DOLPHIN_UPDATE_PREVIOUS_APP:-}" ]]; then
    previous_public_key="$(previous_public_key_from_app "$SUPER_DOLPHIN_UPDATE_PREVIOUS_APP")"
  elif [[ -n "${SUPER_DOLPHIN_UPDATE_PREVIOUS_DMG:-}" ]]; then
    previous_public_key="$(previous_public_key_from_dmg "$SUPER_DOLPHIN_UPDATE_PREVIOUS_DMG")"
  else
    echo "SUPER_DOLPHIN_UPDATE_PREVIOUS_DMG, SUPER_DOLPHIN_UPDATE_PREVIOUS_APP, or SUPER_DOLPHIN_UPDATE_PREVIOUS_ENV_FILE is required to prove old clients trust this update key" >&2
    exit 1
  fi
  if [[ -z "$previous_public_key" ]]; then
    echo "previous package is missing SUPER_DOLPHIN_UPDATE_PUBLIC_KEY" >&2
    exit 1
  fi
  if [[ "$previous_public_key" != "$SUPER_DOLPHIN_UPDATE_PUBLIC_KEY" ]]; then
    echo "previous package update public key does not match SUPER_DOLPHIN_UPDATE_PUBLIC_KEY" >&2
    exit 1
  fi
}

normalize_version_parts() {
  local raw="${1#v}"
  raw="${raw#V}"
  raw="${raw%%[-+]*}"
  if [[ ! "$raw" =~ ^[0-9]+([.][0-9]+){0,2}$ ]]; then
    echo "unsupported semantic version: $1" >&2
    exit 1
  fi
  local major minor patch
  IFS=. read -r major minor patch <<< "$raw"
  printf '%s %s %s\n' "$major" "${minor:-0}" "${patch:-0}"
}

version_gt() {
  local left_major left_minor left_patch right_major right_minor right_patch
  read -r left_major left_minor left_patch < <(normalize_version_parts "$1")
  read -r right_major right_minor right_patch < <(normalize_version_parts "$2")
  if (( left_major != right_major )); then
    (( left_major > right_major ))
    return
  fi
  if (( left_minor != right_minor )); then
    (( left_minor > right_minor ))
    return
  fi
  (( left_patch > right_patch ))
}

require_tag_greater_than_latest() {
  local latest_tag
  latest_tag="$(gh api "repos/$github_repo/releases/latest" --jq '.tag_name' 2>/dev/null || true)"
  if [[ -z "$latest_tag" ]]; then
    return
  fi
  if ! version_gt "$tag" "$latest_tag"; then
    echo "release tag $tag must be greater than latest $latest_tag" >&2
    exit 1
  fi
}

require_gh_access() {
  gh auth status --hostname github.com >/dev/null
  gh api "repos/$github_repo" >/dev/null
}

validate_manifest_for_asset() {
  local platform="$1"
  local artifact="$2"
  local manifest="$3"
  local asset_name="$4"
  local artifact_url="https://github.com/${github_repo}/releases/download/${tag}/${asset_name}"
  go run ./cmd/super-dolphin-release-manifest -verify-manifest "$manifest" \
    -artifact "$artifact" \
    -artifact-url "$artifact_url" \
    -app-id super-dolphin \
    -channel "$channel" \
    -version "$tag" \
    -minimum-version "$minimum_version" \
    -platform "$platform" \
    -public-key "$SUPER_DOLPHIN_UPDATE_PUBLIC_KEY"
}

validate_release_assets() {
  local specs=(
    "darwin-arm64|Super-Dolphin-darwin-arm64.dmg|Super-Dolphin-darwin-arm64.update.json"
    "windows-arm64|Super-Dolphin-windows-arm64.exe|Super-Dolphin-windows-arm64.update.json"
  )
  asset_paths=()
  local spec platform artifact_name manifest_name artifact manifest
  for spec in "${specs[@]}"; do
    IFS='|' read -r platform artifact_name manifest_name <<< "$spec"
    artifact="$stage_dir/$artifact_name"
    manifest="$stage_dir/$manifest_name"
    if [[ ! -s "$artifact" ]]; then
      echo "missing or empty release asset: $artifact" >&2
      exit 1
    fi
    if [[ ! -s "$manifest" ]]; then
      echo "missing or empty release manifest: $manifest" >&2
      exit 1
    fi
    validate_manifest_for_asset "$platform" "$artifact" "$manifest" "$artifact_name"
    asset_paths+=("$artifact" "$manifest")
  done
}

verify_asset_digests_impl() {
  local path name expected_digest actual_digest expected_size actual_size
  for path in "${asset_paths[@]}"; do
    name="$(basename "$path")"
    expected_digest="sha256:$(sha256_file "$path")"
    expected_size="$(file_size "$path")"
    actual_digest="$(gh api "repos/$github_repo/releases/tags/$tag" --jq ".assets[] | select(.name == \"$name\") | .digest")"
    actual_size="$(gh api "repos/$github_repo/releases/tags/$tag" --jq ".assets[] | select(.name == \"$name\") | .size")"
    if [[ -z "$actual_digest" ]]; then
      echo "GitHub release asset is missing digest: $name" >&2
      exit 1
    fi
    if [[ "$actual_digest" != "$expected_digest" ]]; then
      echo "GitHub release asset digest mismatch for $name: expected $expected_digest actual $actual_digest" >&2
      exit 1
    fi
    if [[ "$actual_size" != "$expected_size" ]]; then
      echo "GitHub release asset size mismatch for $name: expected $expected_size actual $actual_size" >&2
      exit 1
    fi
  done
}

require_env SUPER_DOLPHIN_UPDATE_SIGNING_KEY
require_env SUPER_DOLPHIN_UPDATE_PUBLIC_KEY

require_previous_update_public_key
go run ./cmd/super-dolphin-release-manifest -check-key \
  -signing-key "$SUPER_DOLPHIN_UPDATE_SIGNING_KEY" \
  -public-key "$SUPER_DOLPHIN_UPDATE_PUBLIC_KEY"
validate_release_assets
require_gh_access
require_tag_greater_than_latest

if [[ "$dry_run" == "1" ]]; then
  echo "dry run passed for $github_repo $tag with staging dir: $stage_dir"
  exit 0
fi

if gh release view "$tag" --repo "$github_repo" >/dev/null 2>&1; then
  echo "release already exists: $tag" >&2
  exit 1
fi

release_notes="${SUPER_DOLPHIN_RELEASE_NOTES:-Super Dolphin update release $tag.}"
gh release create "$tag" "${asset_paths[@]}" \
  --repo "$github_repo" \
  --title "Super Dolphin $tag" \
  --notes "$release_notes" \
  --draft

verify_uploaded_asset_digests() {
  verify_asset_digests_impl "$@"
}
verify_uploaded_asset_digests

gh release edit "$tag" \
  --repo "$github_repo" \
  --draft=false \
  --latest

echo "GitHub release published: https://github.com/$github_repo/releases/tag/$tag"
