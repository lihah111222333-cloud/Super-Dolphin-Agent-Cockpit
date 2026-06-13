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
codex_bin="${SUPER_DOLPHIN_CODEX_ARTIFACT:-$(command -v codex || true)}"

if [[ -z "${SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN:-}" ]]; then
  echo "SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN is required; packaging helpers do not prompt for or accept privileged API keys" >&2
  exit 1
fi
bootstrap_token="$SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN"
if [[ -z "${bootstrap_token//[[:space:]]/}" ]]; then
  echo "bootstrap token must not be empty" >&2
  exit 1
fi
if [[ -z "${relay_url//[[:space:]]/}" ]]; then
  echo "SUPER_DOLPHIN_CODEX_RELAY_BASE_URL is required" >&2
  exit 1
fi
if [[ -n "${SUPER_DOLPHIN_CODEX_RELAY_API_KEY:-}" ]]; then
  echo "SUPER_DOLPHIN_CODEX_RELAY_API_KEY must not be set for packaging" >&2
  exit 1
fi

test -x "$codex_bin" || { echo "missing Codex artifact; set SUPER_DOLPHIN_CODEX_ARTIFACT" >&2; exit 1; }

package_one() {
  local profile="$1"
  local app_name="super-dolphin"
  if [[ "$profile" == "full" ]]; then
    app_name="super-dolphin-full-lsp"
  fi
  local lsp_dir="${SUPER_DOLPHIN_LSP_BUNDLE_DIR:-$root/.build-cache/lsp/$profile/$(go env GOOS)-$(go env GOARCH)}"

  echo "==> packaging Linux $profile profile as $app_name"
  SUPER_DOLPHIN_LSP_PROFILE="$profile" \
    SUPER_DOLPHIN_LSP_BUNDLE_DIR="$lsp_dir" \
    "$root/scripts/prepare_lsp_bundle_linux.sh"

  SUPER_DOLPHIN_LSP_PROFILE="$profile" \
    APP_NAME="$app_name" \
    SUPER_DOLPHIN_CODEX_ARTIFACT="$codex_bin" \
    SUPER_DOLPHIN_CODEX_SHA256="$(sha256sum "$codex_bin" | awk '{print $1}')" \
    SUPER_DOLPHIN_CODEX_VERSION="$($codex_bin --version | head -n1)" \
    SUPER_DOLPHIN_LSP_BUNDLE_DIR="$lsp_dir" \
    SUPER_DOLPHIN_CODEX_RELAY_BASE_URL="$relay_url" \
    SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN="$bootstrap_token" \
    SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF="local-private-package" \
    "$root/scripts/package_linux.sh"

  echo "Linux package ready under: $root/dist/package/linux"
}

unset SUPER_DOLPHIN_CODEX_RELAY_API_KEY
if [[ "$target" == "all" ]]; then
  package_one standard
  package_one full
else
  package_one "$target"
fi

echo "WARNING: local package contains the provided relay bootstrap token in .env; do not distribute it."
