#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
required_update_github_repo="xiaoxiaotest9527-bit/-"
update_platform="${SUPER_DOLPHIN_UPDATE_PLATFORM:-$(go env GOOS)-$(go env GOARCH)}"
update_github_repo="${SUPER_DOLPHIN_UPDATE_GITHUB_REPO:-}"
artifact_url="${SUPER_DOLPHIN_UPDATE_ARTIFACT_URL:-}"

fail() {
  echo "$*" >&2
  exit 1
}

require_command() {
  local name="$1"
  command -v "$name" >/dev/null 2>&1 || fail "$name is required"
}

require_file() {
  local path="$1"
  [[ -f "$path" ]] || fail "missing file: $path"
}

require_env() {
  local name="$1"
  local value="${!name:-}"
  [[ -n "${value//[[:space:]]/}" ]] || fail "$name is required"
}

asset_extension_for_platform() {
  case "$1" in
    darwin-*) printf '.dmg\n' ;;
    windows-*) printf '.exe\n' ;;
    *) fail "unsupported update platform: $1" ;;
  esac
}

default_artifact_dir_for_platform() {
  case "$1" in
    darwin-*) printf '%s\n' "$root/dist/package/macos" ;;
    windows-*) printf '%s\n' "$root/dist/package/windows" ;;
    *) fail "unsupported update platform: $1" ;;
  esac
}

run_local_manifest_verification() {
  case "$update_platform" in
    darwin-*)
      (
        cd "$root"
        SUPER_DOLPHIN_UPDATE_PLATFORM="$update_platform" DMG_PATH="$artifact_path" LATEST_JSON_PATH="$manifest_path" docs/scripts/macos_release_smoke.sh manifest
      )
      ;;
    windows-*)
      require_env VERSION
      require_env SUPER_DOLPHIN_UPDATE_SIGNING_KEY
      require_env SUPER_DOLPHIN_UPDATE_MINIMUM_VERSION
      local tmp_dir generated_manifest
      tmp_dir="$(mktemp -d)"
      generated_manifest="$tmp_dir/$manifest_name"
      (
        cd "$root"
        go run ./cmd/super-dolphin-release-manifest \
          -artifact "$artifact_path" \
          -artifact-url "$artifact_url" \
          -app-id "${SUPER_DOLPHIN_UPDATE_APP_ID:-super-dolphin}" \
          -channel "${SUPER_DOLPHIN_UPDATE_CHANNEL:-gray}" \
          -version "$VERSION" \
          -minimum-version "$SUPER_DOLPHIN_UPDATE_MINIMUM_VERSION" \
          -platform "$update_platform" \
          -signing-key "$SUPER_DOLPHIN_UPDATE_SIGNING_KEY" \
          -out "$generated_manifest"
      )
      if ! cmp -s "$generated_manifest" "$manifest_path"; then
        rm -rf "$tmp_dir"
        fail "platform update manifest does not match fresh output from cmd/super-dolphin-release-manifest"
      fi
      rm -rf "$tmp_dir"
      ;;
    *)
      fail "unsupported update platform: $update_platform"
      ;;
  esac
}

artifact_extension="$(asset_extension_for_platform "$update_platform")"
artifact_name="Super-Dolphin-$update_platform$artifact_extension"
manifest_name="Super-Dolphin-$update_platform.update.json"
artifact_dir="$(default_artifact_dir_for_platform "$update_platform")"
case "$update_platform" in
  darwin-*) artifact_path="${ARTIFACT_PATH:-${DMG_PATH:-$artifact_dir/$artifact_name}}" ;;
  windows-*) artifact_path="${ARTIFACT_PATH:-${EXE_PATH:-$artifact_dir/$artifact_name}}" ;;
esac
manifest_path="${LATEST_JSON_PATH:-$artifact_dir/$manifest_name}"

if [[ "$update_github_repo" != "$required_update_github_repo" ]]; then
  fail "SUPER_DOLPHIN_UPDATE_GITHUB_REPO must be xiaoxiaotest9527-bit/-"
fi
if [[ "$(basename "$artifact_path")" != "$artifact_name" ]]; then
  fail "artifact asset must be named $artifact_name"
fi
if [[ "$(basename "$manifest_path")" != "$manifest_name" ]]; then
  fail "manifest asset must be named $manifest_name"
fi

expected_url_prefix="https://github.com/$update_github_repo/releases/download/"
expected_url_suffix="/$artifact_name"
if [[ "$artifact_url" != "$expected_url_prefix"*"$expected_url_suffix" ]]; then
  fail "SUPER_DOLPHIN_UPDATE_ARTIFACT_URL must match https://github.com/$update_github_repo/releases/download/<tag>/$artifact_name"
fi
release_tag="${artifact_url#"$expected_url_prefix"}"
release_tag="${release_tag%"$expected_url_suffix"}"
if [[ -z "$release_tag" ]]; then
  fail "release tag must be present in SUPER_DOLPHIN_UPDATE_ARTIFACT_URL"
fi

require_command gh
require_file "$artifact_path"
require_file "$manifest_path"

run_local_manifest_verification

gh release upload "$release_tag" "$artifact_path" "$manifest_path" --repo "$update_github_repo" --clobber
release_assets="$(gh release view "$release_tag" --repo "$update_github_repo" --json assets --jq '.assets[].name')"

verify_release_asset() {
  local name="$1"
  if ! printf '%s\n' "$release_assets" | grep -Fxq "$name"; then
    fail "GitHub release $release_tag missing asset $name"
  fi
}

verify_release_asset "$artifact_name"
verify_release_asset "$manifest_name"
echo "GitHub update assets published and verified for $release_tag: $artifact_name, $manifest_name"
