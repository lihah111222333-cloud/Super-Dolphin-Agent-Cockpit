#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

tag="${VERSION:-}"
stage_dir="${SUPER_DOLPHIN_RELEASE_STAGE_DIR:-}"
channel="${SUPER_DOLPHIN_UPDATE_CHANNEL:-gray}"
minimum_version="${SUPER_DOLPHIN_UPDATE_MINIMUM_VERSION:-0.0.0}"
dry_run=0
verify_existing=0
inspect_latest=0
print_context=0
download_previous_dmg=0
download_previous_dmg_dir=""
previous_dmg_mount=""
asset_paths=()
release_asset_specs=(
  "darwin-arm64|Super-Dolphin-darwin-arm64.dmg|Super-Dolphin-darwin-arm64.update.json"
)
distribution_asset_names=(
  "Super-Dolphin-windows-arm64.exe"
)

cleanup() {
  if [[ -n "$previous_dmg_mount" && -d "$previous_dmg_mount" ]]; then
    hdiutil detach "$previous_dmg_mount" >/dev/null 2>&1 || true
    rmdir "$previous_dmg_mount" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

usage() {
  local status="${1:-2}"
  cat >&2 <<'EOF'
usage: scripts/publish_github_release.sh [--inspect-latest|--print-context|--download-latest-previous-dmg DIR]
       VERSION=v1.0.4 SUPER_DOLPHIN_UPDATE_PUBLIC_KEY=... scripts/publish_github_release.sh [--dry-run] [--verify-existing] [--stage-dir DIR]

publish mode also requires SUPER_DOLPHIN_UPDATE_SIGNING_KEY.
publish, dry-run, and verify-existing modes require previous package proof env.
EOF
  exit "$status"
}

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

while [[ $# -gt 0 ]]; do
  case "$1" in
    --help|-h)
      usage 0
      ;;
    --dry-run)
      dry_run=1
      shift
      ;;
    --verify-existing)
      verify_existing=1
      shift
      ;;
    --inspect-latest)
      inspect_latest=1
      shift
      ;;
    --print-context)
      print_context=1
      shift
      ;;
    --download-latest-previous-dmg)
      [[ $# -ge 2 ]] || usage
      download_previous_dmg=1
      download_previous_dmg_dir="$2"
      shift 2
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

if (( dry_run + verify_existing + inspect_latest + print_context + download_previous_dmg > 1 )); then
  echo "--dry-run, --verify-existing, --inspect-latest, --print-context, and --download-latest-previous-dmg are mutually exclusive" >&2
  exit 2
fi

github_repo="$(require_github_release_repo)"

require_env() {
  local name="$1"
  if [[ -z "${!name:-}" ]]; then
    echo "$name is required" >&2
    exit 1
  fi
}

require_tag() {
  if [[ -z "$tag" ]]; then
    echo "VERSION is required, for example v1.0.4" >&2
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

previous_public_key_from_app() {
  local app="$1"
  local resources="$app/Contents/Resources"
  go run ./cmd/super-dolphin-release-manifest \
    -print-package-trust-public-key "$resources" \
    -platform darwin-arm64
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

read_previous_update_public_key() {
  local previous_public_key=""
  if [[ -n "${SUPER_DOLPHIN_UPDATE_PREVIOUS_APP:-}" ]]; then
    previous_public_key="$(previous_public_key_from_app "$SUPER_DOLPHIN_UPDATE_PREVIOUS_APP")"
  elif [[ -n "${SUPER_DOLPHIN_UPDATE_PREVIOUS_DMG:-}" ]]; then
    previous_public_key="$(previous_public_key_from_dmg "$SUPER_DOLPHIN_UPDATE_PREVIOUS_DMG")"
  else
    echo "SUPER_DOLPHIN_UPDATE_PREVIOUS_DMG or SUPER_DOLPHIN_UPDATE_PREVIOUS_APP is required to prove old clients trust this update key" >&2
    exit 1
  fi
  if [[ -z "$previous_public_key" ]]; then
    echo "previous package is missing a verified manifest_public_key" >&2
    exit 1
  fi
  printf '%s\n' "$previous_public_key"
}

resolve_update_public_key() {
  if [[ -n "${SUPER_DOLPHIN_UPDATE_PUBLIC_KEY:-}" ]]; then
    return
  fi
  local previous_public_key
  previous_public_key="$(read_previous_update_public_key)"
  SUPER_DOLPHIN_UPDATE_PUBLIC_KEY="$previous_public_key"
  export SUPER_DOLPHIN_UPDATE_PUBLIC_KEY
}

require_previous_update_public_key() {
  local previous_public_key
  previous_public_key="$(read_previous_update_public_key)"
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

require_existing_release() {
  gh release view "$tag" --repo "$github_repo" >/dev/null
}

require_existing_release_marked_latest() {
  local latest_tag
  latest_tag="$(gh api "repos/$github_repo/releases/latest" --jq '.tag_name')"
  if [[ "$latest_tag" != "$tag" ]]; then
    echo "release tag $tag must be marked latest; GitHub latest is $latest_tag" >&2
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
  asset_paths=()
  local spec platform artifact_name manifest_name artifact manifest
  for spec in "${release_asset_specs[@]}"; do
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
  local name
  for name in "${distribution_asset_names[@]}"; do
    artifact="$stage_dir/$name"
    if [[ ! -s "$artifact" ]]; then
      echo "missing or empty release asset: $artifact" >&2
      exit 1
    fi
    asset_paths+=("$artifact")
  done
}

inspect_latest_release_assets() {
  local latest_tag
  latest_tag="$(gh api "repos/$github_repo/releases/latest" --jq '.tag_name')"
  if [[ -z "$latest_tag" ]]; then
    echo "GitHub latest release tag_name is required" >&2
    exit 1
  fi
  local missing=0
  local spec platform artifact_name manifest_name name digest size
  for spec in "${release_asset_specs[@]}"; do
    IFS='|' read -r platform artifact_name manifest_name <<< "$spec"
    for name in "$artifact_name" "$manifest_name"; do
      digest="$(gh api "repos/$github_repo/releases/latest" --jq ".assets[] | select(.name == \"$name\") | .digest")"
      size="$(gh api "repos/$github_repo/releases/latest" --jq ".assets[] | select(.name == \"$name\") | .size")"
      if [[ -z "$digest" ]]; then
        echo "GitHub latest release $latest_tag missing asset: $name" >&2
        missing=1
        continue
      fi
      if [[ ! "$digest" =~ ^sha256:[0-9a-fA-F]{64}$ ]]; then
        echo "GitHub latest release asset $name digest must be sha256:<hex>" >&2
        missing=1
      fi
      if [[ ! "$size" =~ ^[0-9]+$ ]]; then
        echo "GitHub latest release asset $name size must be a positive integer" >&2
        missing=1
      elif (( size <= 0 )); then
        echo "GitHub latest release asset $name size must be > 0" >&2
        missing=1
      fi
    done
  done
  for name in "${distribution_asset_names[@]}"; do
    digest="$(gh api "repos/$github_repo/releases/latest" --jq ".assets[] | select(.name == \"$name\") | .digest")"
    size="$(gh api "repos/$github_repo/releases/latest" --jq ".assets[] | select(.name == \"$name\") | .size")"
    if [[ -z "$digest" ]]; then
      echo "GitHub latest release $latest_tag missing asset: $name" >&2
      missing=1
      continue
    fi
    if [[ ! "$digest" =~ ^sha256:[0-9a-fA-F]{64}$ ]]; then
      echo "GitHub latest release asset $name digest must be sha256:<hex>" >&2
      missing=1
    fi
    if [[ ! "$size" =~ ^[0-9]+$ ]]; then
      echo "GitHub latest release asset $name size must be a positive integer" >&2
      missing=1
    elif (( size <= 0 )); then
      echo "GitHub latest release asset $name size must be > 0" >&2
      missing=1
    fi
  done
  if [[ "$missing" != "0" ]]; then
    exit 1
  fi
  echo "GitHub latest release has canonical release assets: https://github.com/$github_repo/releases/tag/$latest_tag"
}

env_path_status() {
  local name="$1"
  local kind="$2"
  local value="${!name:-}"
  if [[ -z "$value" ]]; then
    echo "$name: unset"
    return
  fi
  case "$kind" in
    file)
      if [[ -f "$value" ]]; then
        echo "$name: configured (file exists)"
      else
        echo "$name: configured (file missing)"
      fi
      ;;
    dir)
      if [[ -d "$value" ]]; then
        echo "$name: configured (directory exists)"
      else
        echo "$name: configured (directory missing)"
      fi
      ;;
    *)
      echo "$name: configured"
      ;;
  esac
}

print_release_context() {
  local latest_tag latest_url spec platform artifact_name manifest_name name digest size local_path
  latest_tag="$(gh api "repos/$github_repo/releases/latest" --jq '.tag_name')"
  latest_url="$(gh api "repos/$github_repo/releases/latest" --jq '.html_url')"
  echo "GitHub release context"
  echo "repo: $github_repo"
  echo "latest: $latest_tag $latest_url"
  if [[ -z "$tag" ]]; then
    echo "candidate tag: unset"
  else
    echo "candidate tag: $tag"
    if version_gt "$tag" "$latest_tag"; then
      echo "candidate tag check: greater than latest"
    else
      echo "candidate tag check: not greater than latest"
    fi
    if gh release view "$tag" --repo "$github_repo" >/dev/null 2>&1; then
      echo "candidate release: already exists"
    else
      echo "candidate release: not found"
    fi
  fi
  echo
  for spec in "${release_asset_specs[@]}"; do
    IFS='|' read -r platform artifact_name manifest_name <<< "$spec"
    for name in "$artifact_name" "$manifest_name"; do
      digest="$(gh api "repos/$github_repo/releases/latest" --jq ".assets[] | select(.name == \"$name\") | .digest")"
      size="$(gh api "repos/$github_repo/releases/latest" --jq ".assets[] | select(.name == \"$name\") | .size")"
      if [[ -z "$digest" ]]; then
        echo "remote asset: $name missing"
      else
        echo "remote asset: $name present size=$size digest=$digest"
      fi
    done
  done
  for name in "${distribution_asset_names[@]}"; do
    digest="$(gh api "repos/$github_repo/releases/latest" --jq ".assets[] | select(.name == \"$name\") | .digest")"
    size="$(gh api "repos/$github_repo/releases/latest" --jq ".assets[] | select(.name == \"$name\") | .size")"
    if [[ -z "$digest" ]]; then
      echo "remote asset: $name missing"
    else
      echo "remote asset: $name present size=$size digest=$digest"
    fi
  done
  echo
  if [[ -z "$tag" && -z "$stage_dir" ]]; then
    echo "stage dir: set VERSION or SUPER_DOLPHIN_RELEASE_STAGE_DIR to check local staged assets"
  else
    if [[ -z "$stage_dir" ]]; then
      stage_dir="$root/dist/release/github/$tag"
    elif [[ "$stage_dir" != /* ]]; then
      stage_dir="$root/$stage_dir"
    fi
    echo "stage dir: $stage_dir"
    for spec in "${release_asset_specs[@]}"; do
      IFS='|' read -r platform artifact_name manifest_name <<< "$spec"
      for name in "$artifact_name" "$manifest_name"; do
        local_path="$stage_dir/$name"
        if [[ -s "$local_path" ]]; then
          echo "local staged: $name present size=$(file_size "$local_path")"
        else
          echo "local staged: $name missing"
        fi
      done
    done
    for name in "${distribution_asset_names[@]}"; do
      local_path="$stage_dir/$name"
      if [[ -s "$local_path" ]]; then
        echo "local staged: $name present size=$(file_size "$local_path")"
      else
        echo "local staged: $name missing"
      fi
    done
  fi
  echo
  if [[ -n "${SUPER_DOLPHIN_UPDATE_PUBLIC_KEY:-}" ]]; then
    echo "public key source: SUPER_DOLPHIN_UPDATE_PUBLIC_KEY configured"
  elif [[ -n "${SUPER_DOLPHIN_UPDATE_PREVIOUS_APP:-}${SUPER_DOLPHIN_UPDATE_PREVIOUS_DMG:-}" ]]; then
    echo "public key source: derive from previous package proof"
  else
    echo "public key source: missing"
  fi
  env_path_status SUPER_DOLPHIN_UPDATE_PREVIOUS_APP dir
  env_path_status SUPER_DOLPHIN_UPDATE_PREVIOUS_DMG file
  env_path_status SUPER_DOLPHIN_UPDATE_SIGNING_KEY file
}

download_latest_previous_dmg() {
  command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit 1; }
  local latest_tag asset_name url digest size dir target tmp actual_digest actual_size
  latest_tag="$(gh api "repos/$github_repo/releases/latest" --jq '.tag_name')"
  asset_name="Super-Dolphin-darwin-arm64.dmg"
  url="$(gh api "repos/$github_repo/releases/latest" --jq ".assets[] | select(.name == \"$asset_name\") | .browser_download_url")"
  digest="$(gh api "repos/$github_repo/releases/latest" --jq ".assets[] | select(.name == \"$asset_name\") | .digest")"
  size="$(gh api "repos/$github_repo/releases/latest" --jq ".assets[] | select(.name == \"$asset_name\") | .size")"
  [[ -n "$url" ]] || { echo "GitHub latest release $latest_tag missing asset: $asset_name" >&2; exit 1; }
  [[ "$url" =~ ^https://[^[:space:]]+$ ]] || { echo "GitHub release asset URL must be HTTPS: $url" >&2; exit 1; }
  [[ "$digest" =~ ^sha256:[0-9a-fA-F]{64}$ ]] || { echo "GitHub release asset $asset_name digest must be sha256:<hex>" >&2; exit 1; }
  [[ "$size" =~ ^[0-9]+$ && "$size" -gt 0 ]] || { echo "GitHub release asset $asset_name size must be > 0" >&2; exit 1; }
  dir="$download_previous_dmg_dir"
  [[ "$dir" == /* ]] || dir="$root/$dir"
  mkdir -p "$dir"
  target="$dir/Super-Dolphin-${latest_tag}-darwin-arm64.dmg"
  tmp="$target.tmp"
  curl -L --fail -o "$tmp" "$url"
  actual_digest="sha256:$(sha256_file "$tmp")"
  actual_size="$(file_size "$tmp")"
  [[ "$actual_digest" == "$digest" ]] || { rm -f "$tmp"; echo "downloaded previous DMG digest mismatch: expected $digest actual $actual_digest" >&2; exit 1; }
  [[ "$actual_size" == "$size" ]] || { rm -f "$tmp"; echo "downloaded previous DMG size mismatch: expected $size actual $actual_size" >&2; exit 1; }
  mv -f "$tmp" "$target"
  echo "previous DMG downloaded and verified: $target"
  printf 'export SUPER_DOLPHIN_UPDATE_PREVIOUS_DMG=%q\n' "$target"
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

verify_uploaded_asset_digests() {
  verify_asset_digests_impl
}

if [[ "$inspect_latest" == "1" ]]; then
  require_gh_access
  inspect_latest_release_assets
  exit 0
fi

if [[ "$print_context" == "1" ]]; then
  require_gh_access
  print_release_context
  exit 0
fi

if [[ "$download_previous_dmg" == "1" ]]; then
  require_gh_access
  download_latest_previous_dmg
  exit 0
fi

require_tag
if [[ -z "$stage_dir" ]]; then
  stage_dir="$root/dist/release/github/$tag"
elif [[ "$stage_dir" != /* ]]; then
  stage_dir="$root/$stage_dir"
fi

if [[ "$verify_existing" != "1" ]]; then
  require_env SUPER_DOLPHIN_UPDATE_SIGNING_KEY
  resolve_update_public_key
  require_previous_update_public_key
  go run ./cmd/super-dolphin-release-manifest -check-key \
    -signing-key "$SUPER_DOLPHIN_UPDATE_SIGNING_KEY" \
    -public-key "$SUPER_DOLPHIN_UPDATE_PUBLIC_KEY"
else
  resolve_update_public_key
  require_previous_update_public_key
fi
validate_release_assets
require_gh_access

if [[ "$verify_existing" == "1" ]]; then
  require_existing_release
  verify_uploaded_asset_digests
  require_existing_release_marked_latest
  echo "existing GitHub release verified: https://github.com/$github_repo/releases/tag/$tag"
  exit 0
fi

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

verify_uploaded_asset_digests

gh release edit "$tag" \
  --repo "$github_repo" \
  --draft=false \
  --latest

echo "GitHub release published: https://github.com/$github_repo/releases/tag/$tag"
