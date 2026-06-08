#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

target="${1:-${SUPER_DOLPHIN_PACKAGE_TARGET:-standard}}"
case "$target" in
  standard|full|all)
    ;;
  *)
    echo "usage: $0 [standard|full|all]" >&2
    exit 2
    ;;
esac

relay_url="${SUPER_DOLPHIN_CODEX_RELAY_BASE_URL:-}"
relay_api_key_env="SUPER_DOLPHIN_CODEX_RELAY_API_KEY"
relay_api_key="${SUPER_DOLPHIN_CODEX_RELAY_API_KEY:-}"
postgres_dist="${SUPER_DOLPHIN_POSTGRES_DIST:-$root/.build-cache/postgres/16.14/$(go env GOOS)-$(go env GOARCH)}"
codex_bin="${SUPER_DOLPHIN_CODEX_ARTIFACT:-$(command -v codex || true)}"
ffmpeg_bin_env="SUPER_DOLPHIN_FFMPEG_BIN"
ffmpeg_bin=""

real_file_path() {
  local path="$1"
  if command -v realpath >/dev/null 2>&1; then
    realpath "$path"
    return
  fi
  local dir base
  dir="$(dirname "$path")"
  base="$(basename "$path")"
  printf '%s/%s\n' "$(cd "$dir" && pwd -P)" "$base"
}

is_macho() {
  file -b "$1" 2>/dev/null | grep -q 'Mach-O'
}

resolve_local_codex_binary() {
  local candidate="$1"
  if is_macho "$candidate"; then
    printf '%s\n' "$candidate"
    return
  fi
  if ! grep -q 'PLATFORM_PACKAGE_BY_TARGET' "$candidate" 2>/dev/null; then
    echo "local Codex artifact is not a Mach-O binary or recognized npm launcher: $candidate" >&2
    exit 1
  fi

  local source_real source_pkg platform_pkg target_triple arch resolved
  source_real="$(real_file_path "$candidate")"
  source_pkg="$(cd "$(dirname "$source_real")/.." && pwd -P)"
  arch="$(uname -m)"
  case "$arch" in
    arm64|aarch64)
      platform_pkg="@openai/codex-darwin-arm64"
      target_triple="aarch64-apple-darwin"
      ;;
    x86_64|amd64)
      platform_pkg="@openai/codex-darwin-x64"
      target_triple="x86_64-apple-darwin"
      ;;
    *)
      echo "unsupported macOS architecture for local Codex artifact: $arch" >&2
      exit 1
      ;;
  esac
  resolved="$source_pkg/node_modules/$platform_pkg/vendor/$target_triple/codex/codex"
  if [[ ! -x "$resolved" ]]; then
    echo "local Codex npm launcher is missing native binary: $resolved" >&2
    exit 1
  fi
  if ! is_macho "$resolved"; then
    echo "local Codex artifact resolved to non-Mach-O binary: $resolved" >&2
    exit 1
  fi
  printf '%s\n' "$resolved"
}

phase_start() {
  phase_label="$1"
  phase_started_at="$(date +%s)"
  echo "==> [$phase_label] start $(date '+%H:%M:%S')" >&2
}

phase_end() {
  local finished_at elapsed
  finished_at="$(date +%s)"
  elapsed=$((finished_at - phase_started_at))
  echo "==> [$phase_label] done in ${elapsed}s $(date '+%H:%M:%S')" >&2
}

verify_host_ffmpeg() {
  local candidate="$1"
  if [[ ! -x "$candidate" ]]; then
    return 1
  fi
  "$candidate" -version >/dev/null 2>&1
}

resolve_host_ffmpeg_candidate() {
  local candidate
  if [[ -n "${SUPER_DOLPHIN_FFMPEG_BIN:-}" ]]; then
    printf '%s\n' "$SUPER_DOLPHIN_FFMPEG_BIN"
    return
  fi
  if candidate="$(command -v ffmpeg 2>/dev/null)" && [[ -n "$candidate" ]]; then
    printf '%s\n' "$candidate"
    return
  fi
  if command -v brew >/dev/null 2>&1; then
    local brew_prefix
    if brew_prefix="$(brew --prefix ffmpeg 2>/dev/null)" && [[ -x "$brew_prefix/bin/ffmpeg" ]]; then
      printf '%s\n' "$brew_prefix/bin/ffmpeg"
    fi
  fi
}

resolve_or_install_host_ffmpeg() {
  local candidate
  candidate="$(resolve_host_ffmpeg_candidate || true)"
  if [[ -n "$candidate" ]] && verify_host_ffmpeg "$candidate"; then
    ffmpeg_bin="$(real_file_path "$candidate")"
    echo "ffmpeg verified: $ffmpeg_bin" >&2
    return
  fi
  if [[ -n "${SUPER_DOLPHIN_FFMPEG_BIN:-}" ]]; then
    echo "$ffmpeg_bin_env does not point to a working ffmpeg executable: $SUPER_DOLPHIN_FFMPEG_BIN" >&2
    echo "Install ffmpeg with Homebrew: brew install ffmpeg" >&2
    exit 1
  fi
  if [[ "${SUPER_DOLPHIN_AUTO_INSTALL_FFMPEG:-1}" == "0" ]]; then
    echo "missing ffmpeg on the packaging machine; install it with: brew install ffmpeg" >&2
    echo "Alternatively set $ffmpeg_bin_env to a working ffmpeg binary." >&2
    exit 1
  fi
  if ! command -v brew >/dev/null 2>&1; then
    echo "missing ffmpeg and Homebrew is not installed." >&2
    echo "Install Homebrew from https://brew.sh, then run: brew install ffmpeg" >&2
    echo "Alternatively set $ffmpeg_bin_env to a working ffmpeg binary." >&2
    exit 1
  fi
  echo "ffmpeg not found; attempting to install with Homebrew: brew install ffmpeg" >&2
  if ! brew install ffmpeg; then
    echo "Homebrew failed to install ffmpeg." >&2
    echo "Run manually: brew install ffmpeg" >&2
    echo "Alternatively set $ffmpeg_bin_env to a working ffmpeg binary." >&2
    exit 1
  fi
  candidate="$(resolve_host_ffmpeg_candidate || true)"
  if [[ -z "$candidate" ]] || ! verify_host_ffmpeg "$candidate"; then
    echo "ffmpeg installation finished but ffmpeg is still not executable." >&2
    echo "Check Homebrew output, then run: brew install ffmpeg" >&2
    echo "Alternatively set $ffmpeg_bin_env to a working ffmpeg binary." >&2
    exit 1
  fi
  ffmpeg_bin="$(real_file_path "$candidate")"
  echo "ffmpeg verified: $ffmpeg_bin" >&2
}

bootstrap_token="${SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN:-$relay_api_key}"
if [[ -z "${bootstrap_token:-}" ]]; then
  echo "SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN or SUPER_DOLPHIN_CODEX_RELAY_API_KEY is required" >&2
  exit 1
fi
if [[ -z "${bootstrap_token//[[:space:]]/}" ]]; then
  echo "bootstrap token must not be empty" >&2
  exit 1
fi
if [[ -z "${relay_url//[[:space:]]/}" ]]; then
  echo "SUPER_DOLPHIN_CODEX_RELAY_BASE_URL is required" >&2
  exit 1
fi
test -x "$codex_bin" || { echo "missing Codex artifact: $codex_bin" >&2; exit 1; }
codex_artifact="$(resolve_local_codex_binary "$codex_bin")"
test -d "$postgres_dist" || { echo "missing PostgreSQL dist: $postgres_dist" >&2; exit 1; }
resolve_or_install_host_ffmpeg

package_one() {
  local profile="$1"
  local app_name="Super Dolphin"
  if [[ "$profile" == "full" ]]; then
    app_name="Super Dolphin Full LSP"
  fi
  local lsp_dir="${SUPER_DOLPHIN_LSP_BUNDLE_DIR:-$root/.build-cache/lsp/$profile/$(go env GOOS)-$(go env GOARCH)}"

  echo "==> packaging $profile profile as $app_name"
  phase_start "prepare lsp bundle ($profile)"
  SUPER_DOLPHIN_LSP_PROFILE="$profile" \
    SUPER_DOLPHIN_LSP_BUNDLE_DIR="$lsp_dir" \
    "$root/scripts/prepare_lsp_bundle_macos.sh"
  phase_end

  phase_start "package macos ($profile)"
  env \
    "$relay_api_key_env=$relay_api_key" \
    SUPER_DOLPHIN_LSP_PROFILE="$profile" \
    APP_NAME="$app_name" \
    SUPER_DOLPHIN_POSTGRES_DIST="$postgres_dist" \
    SUPER_DOLPHIN_CODEX_ARTIFACT="$codex_artifact" \
    SUPER_DOLPHIN_CODEX_SHA256="$(shasum -a 256 "$codex_artifact" | awk '{print $1}')" \
    SUPER_DOLPHIN_CODEX_VERSION="${SUPER_DOLPHIN_CODEX_VERSION:-$($codex_artifact --version 2>/dev/null | head -n1)}" \
    SUPER_DOLPHIN_LSP_BUNDLE_DIR="$lsp_dir" \
    SUPER_DOLPHIN_FFMPEG_BIN="$ffmpeg_bin" \
    SUPER_DOLPHIN_CODEX_RELAY_BASE_URL="$relay_url" \
    SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN="$bootstrap_token" \
    SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF="local-private-package" \
    SUPER_DOLPHIN_PACKAGE_INCLUDE_VIDEO_API_KEY="${SUPER_DOLPHIN_PACKAGE_INCLUDE_VIDEO_API_KEY:-0}" \
    SILICONFLOW_API_KEY="${SILICONFLOW_API_KEY:-}" \
    SUPER_DOLPHIN_MACOS_MIN_VERSION="${SUPER_DOLPHIN_MACOS_MIN_VERSION:-13.0}" \
    "$root/scripts/package_macos.sh"
  phase_end

  echo "DMG ready: $root/dist/package/macos/$app_name.dmg"
}

if [[ "$target" == "all" ]]; then
  package_one standard
  package_one full
else
  package_one "$target"
fi

echo "WARNING: local package contains the provided relay bootstrap token in Contents/Resources/.env; do not distribute it."
