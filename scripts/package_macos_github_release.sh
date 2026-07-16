#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

require_github_release_repo() {
  local repo="${SUPER_DOLPHIN_UPDATE_GITHUB_REPO:-}"
  if [[ -z "${repo//[[:space:]]/}" ]]; then
    echo "SUPER_DOLPHIN_UPDATE_GITHUB_REPO is required" >&2
    exit 1
  fi
  if [[ "$repo" == "xiaoxiaotest9527-bit/-" ]]; then
    echo "known placeholder update repo is not allowed" >&2
    exit 1
  fi
  if [[ ! "$repo" =~ ^[^/[:space:]]+/[^/[:space:]]+$ ]]; then
    echo "SUPER_DOLPHIN_UPDATE_GITHUB_REPO must be owner/repo without whitespace" >&2
    exit 1
  fi
  printf '%s\n' "$repo"
}

release_dirty_whitelist_matches() {
  local path="$1" entry
  IFS=',' read -ra entries <<< "${SUPER_DOLPHIN_RELEASE_DIRTY_WHITELIST:-}"
  for entry in "${entries[@]}"; do
    entry="${entry#"${entry%%[![:space:]]*}"}"
    entry="${entry%"${entry##*[![:space:]]}"}"
    if [[ -n "$entry" && "$path" == "$entry" ]]; then
      return 0
    fi
  done
  return 1
}

require_clean_release_tree() {
  local dirty=0 status path
  while IFS= read -r line; do
    [[ -n "$line" ]] || continue
    status="${line:0:2}"
    path="${line:3}"
    if ! release_dirty_whitelist_matches "$path"; then
      echo "release worktree has uncommitted changes: $status $path" >&2
      dirty=1
    fi
  done < <(git status --porcelain=v1 --untracked-files=all)
  if [[ "$dirty" != "0" ]]; then
    echo "Set SUPER_DOLPHIN_RELEASE_DIRTY_WHITELIST to a comma-separated exact path list only for audited manual overrides." >&2
    exit 1
  fi
}

tag="${VERSION:?VERSION is required, for example v1.0.4}"
default_package_version="${tag#v}"
default_package_version="${default_package_version#V}"
package_version="${SUPER_DOLPHIN_PACKAGE_VERSION:-$default_package_version}"
github_release_repo="$(require_github_release_repo)"
channel="${SUPER_DOLPHIN_UPDATE_CHANNEL:-gray}"
minimum_version="${SUPER_DOLPHIN_UPDATE_MINIMUM_VERSION:-0.0.0}"
stage_dir="${SUPER_DOLPHIN_RELEASE_STAGE_DIR:-$root/dist/release/github/$tag}"
asset_name="Super-Dolphin-darwin-arm64.dmg"
manifest_name="Super-Dolphin-darwin-arm64.update.json"
artifact="$stage_dir/$asset_name"
manifest="$stage_dir/$manifest_name"
artifact_url="https://github.com/${github_release_repo}/releases/download/${tag}/${asset_name}"
previous_dmg_mount=""
build_commit="$(git rev-parse HEAD)"
export SUPER_DOLPHIN_RELEASE_BUILD_COMMIT="$build_commit"

cleanup() {
  if [[ -n "$previous_dmg_mount" && -d "$previous_dmg_mount" ]]; then
    hdiutil detach "$previous_dmg_mount" >/dev/null 2>&1 || true
    rmdir "$previous_dmg_mount" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

previous_public_key_from_app() {
  local app="$1"
  local resources="$app/Contents/Resources"
  go run "$root/cmd/super-dolphin-release-manifest" \
    -print-package-trust-public-key "$resources" \
    -platform darwin-arm64
}

previous_public_key_from_dmg() {
  local dmg="$1"
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

resolve_update_public_key() {
  if [[ -n "${SUPER_DOLPHIN_UPDATE_PUBLIC_KEY:-}" ]]; then
    return
  fi
  if [[ -n "${SUPER_DOLPHIN_UPDATE_PREVIOUS_APP:-}${SUPER_DOLPHIN_UPDATE_PREVIOUS_DMG:-}" && "${SUPER_DOLPHIN_ALLOW_LOCAL_PREVIOUS_RELEASE_TEST:-}" != "1" ]]; then
    echo "local previous APP/DMG overrides require SUPER_DOLPHIN_ALLOW_LOCAL_PREVIOUS_RELEASE_TEST=1 and are not formal release proof" >&2
    exit 1
  fi
  if [[ -n "${SUPER_DOLPHIN_UPDATE_PREVIOUS_APP:-}" ]]; then
    SUPER_DOLPHIN_UPDATE_PUBLIC_KEY="$(previous_public_key_from_app "$SUPER_DOLPHIN_UPDATE_PREVIOUS_APP")"
  elif [[ -n "${SUPER_DOLPHIN_UPDATE_PREVIOUS_DMG:-}" ]]; then
    SUPER_DOLPHIN_UPDATE_PUBLIC_KEY="$(previous_public_key_from_dmg "$SUPER_DOLPHIN_UPDATE_PREVIOUS_DMG")"
  else
    echo "SUPER_DOLPHIN_UPDATE_PUBLIC_KEY, SUPER_DOLPHIN_UPDATE_PREVIOUS_DMG, or SUPER_DOLPHIN_UPDATE_PREVIOUS_APP is required" >&2
    exit 1
  fi
  if [[ -z "$SUPER_DOLPHIN_UPDATE_PUBLIC_KEY" ]]; then
    echo "previous package is missing SUPER_DOLPHIN_UPDATE_PUBLIC_KEY" >&2
    exit 1
  fi
  export SUPER_DOLPHIN_UPDATE_PUBLIC_KEY
}

if [[ "$(go env GOOS)" != "darwin" || "$(go env GOARCH)" != "arm64" ]]; then
  echo "package_macos_github_release.sh must run on macOS arm64" >&2
  exit 1
fi
if [[ -z "${SUPER_DOLPHIN_UPDATE_SIGNING_KEY:-}" ]]; then
  echo "SUPER_DOLPHIN_UPDATE_SIGNING_KEY is required" >&2
  exit 1
fi
resolve_update_public_key

require_clean_release_tree

go run "$root/cmd/super-dolphin-release-manifest" \
  -check-key \
  -signing-key "$SUPER_DOLPHIN_UPDATE_SIGNING_KEY" \
  -public-key "$SUPER_DOLPHIN_UPDATE_PUBLIC_KEY"

export SUPER_DOLPHIN_RELEASE_PROFILE="${SUPER_DOLPHIN_RELEASE_PROFILE:-gray}"
export SUPER_DOLPHIN_RELEASE_BUILD=1
export SUPER_DOLPHIN_UPDATE_GITHUB_REPO="$github_release_repo"
export SUPER_DOLPHIN_UPDATE_CHANNEL="$channel"
export SUPER_DOLPHIN_UPDATE_VERSION="$package_version"
export VERSION="$package_version"

./scripts/package_macos.sh

mkdir -p "$stage_dir"
cp -f "$root/dist/package/macos/Super Dolphin.dmg" "$artifact"
printf '%s\n' "$build_commit" > "$stage_dir/release-build-commit.txt"

go run ./cmd/super-dolphin-release-manifest \
  -artifact "$artifact" \
  -artifact-url "$artifact_url" \
  -app-id super-dolphin \
  -channel "$channel" \
  -version "$tag" \
  -minimum-version "$minimum_version" \
  -platform darwin-arm64 \
  -signing-key "$SUPER_DOLPHIN_UPDATE_SIGNING_KEY" \
  -public-key "$SUPER_DOLPHIN_UPDATE_PUBLIC_KEY" \
  -out "$manifest"

go run ./cmd/super-dolphin-release-manifest \
  -verify-manifest "$manifest" \
  -artifact "$artifact" \
  -artifact-url "$artifact_url" \
  -app-id super-dolphin \
  -channel "$channel" \
  -version "$tag" \
  -minimum-version "$minimum_version" \
  -platform darwin-arm64 \
  -public-key "$SUPER_DOLPHIN_UPDATE_PUBLIC_KEY"

echo "macOS GitHub release assets ready under: $stage_dir"
