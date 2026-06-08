#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

tag="${VERSION:?VERSION is required, for example v1.0.4}"
default_package_version="${tag#v}"
default_package_version="${default_package_version#V}"
package_version="${SUPER_DOLPHIN_PACKAGE_VERSION:-$default_package_version}"
github_release_repo="${SUPER_DOLPHIN_UPDATE_GITHUB_REPO:-xiaoxiaotest9527-bit/-}"
channel="${SUPER_DOLPHIN_UPDATE_CHANNEL:-gray}"
minimum_version="${SUPER_DOLPHIN_UPDATE_MINIMUM_VERSION:-0.0.0}"
stage_dir="${SUPER_DOLPHIN_RELEASE_STAGE_DIR:-$root/dist/release/github/$tag}"
asset_name="Super-Dolphin-darwin-arm64.dmg"
manifest_name="Super-Dolphin-darwin-arm64.update.json"
artifact="$stage_dir/$asset_name"
manifest="$stage_dir/$manifest_name"
artifact_url="https://github.com/${github_release_repo}/releases/download/${tag}/${asset_name}"

if [[ "$(go env GOOS)" != "darwin" || "$(go env GOARCH)" != "arm64" ]]; then
  echo "package_macos_github_release.sh must run on macOS arm64" >&2
  exit 1
fi
if [[ -z "${SUPER_DOLPHIN_UPDATE_SIGNING_KEY:-}" ]]; then
  echo "SUPER_DOLPHIN_UPDATE_SIGNING_KEY is required" >&2
  exit 1
fi
if [[ -z "${SUPER_DOLPHIN_UPDATE_PUBLIC_KEY:-}" ]]; then
  echo "SUPER_DOLPHIN_UPDATE_PUBLIC_KEY is required" >&2
  exit 1
fi

go run "$root/cmd/super-dolphin-release-manifest" \
  -check-key \
  -signing-key "$SUPER_DOLPHIN_UPDATE_SIGNING_KEY" \
  -public-key "$SUPER_DOLPHIN_UPDATE_PUBLIC_KEY"

export SUPER_DOLPHIN_RELEASE_PROFILE="${SUPER_DOLPHIN_RELEASE_PROFILE:-gray}"
export SUPER_DOLPHIN_UPDATE_GITHUB_REPO="$github_release_repo"
export SUPER_DOLPHIN_UPDATE_CHANNEL="$channel"
export SUPER_DOLPHIN_UPDATE_VERSION="$package_version"
export VERSION="$package_version"

./scripts/package_macos.sh

mkdir -p "$stage_dir"
cp -f "$root/dist/package/macos/Super Dolphin.dmg" "$artifact"

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
