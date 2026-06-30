#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
app_name="${APP_NAME:-Super Dolphin}"
bundle_id="${BUNDLE_ID:-com.superdolphin.app}"
version="${VERSION:-0.1.0}"
release_profile="${SUPER_DOLPHIN_RELEASE_PROFILE:-dev-local}"
codesign_identity="${CODESIGN_IDENTITY:--}"
goos="$(go env GOOS)"
goarch="$(go env GOARCH)"
platform="${goos}-${goarch}"

if [[ "$goos" != "darwin" ]]; then
  echo "package_macos.sh must run on macOS; current GOOS=$goos" >&2
  exit 1
fi

codex_relay_base_url_env="SUPER_DOLPHIN_CODEX_RELAY_BASE_URL"
codex_relay_bootstrap_token_env="SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN"
codex_relay_bootstrap_proof_env="SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF"
codex_relay_privileged_api_key_env="SUPER_DOLPHIN_CODEX_RELAY_API_KEY"
video_api_key_env="SILICONFLOW_API_KEY"
package_video_api_key_opt_in_env="SUPER_DOLPHIN_PACKAGE_INCLUDE_VIDEO_API_KEY"
macos_min_version_env="SUPER_DOLPHIN_MACOS_MIN_VERSION"
packaged_relay_base_url=""
packaged_relay_api_key=""
packaged_relay_bootstrap_token=""
packaged_relay_bootstrap_proof=""
packaged_video_api_key=""
macos_min_version=""
update_manifest_url=""
update_github_repo=""
update_public_key=""
update_channel=""
update_allow_unsigned="0"
codex_artifact_env="SUPER_DOLPHIN_CODEX_ARTIFACT"
codex_sha256_env="SUPER_DOLPHIN_CODEX_SHA256"
codex_version_env="SUPER_DOLPHIN_CODEX_VERSION"
lsp_bundle_dir_env="SUPER_DOLPHIN_LSP_BUNDLE_DIR"
ffmpeg_bin_env="SUPER_DOLPHIN_FFMPEG_BIN"
lsp_manifest_name="lsp-manifest.json"
lsp_checksums_name="lsp-checksums.sha256"
require_bundled_codex="${SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX:-1}"
packaged_codex_artifact=""
packaged_codex_sha256=""
packaged_codex_version=""
packaged_lsp_bundle_dir=""
packaged_ffmpeg_bin=""
lsp_profile="${SUPER_DOLPHIN_LSP_PROFILE:-standard}"
case "$lsp_profile" in
  standard|full)
    ;;
  *)
    echo "unsupported SUPER_DOLPHIN_LSP_PROFILE=$lsp_profile; expected standard or full" >&2
    exit 1
    ;;
esac
lsp_server_specs=(
  "gopls|bin/gopls"
  "typescript-language-server|bin/typescript-language-server"
  "vscode-langservers-extracted|bin/vscode-css-language-server"
  "pyright|bin/pyright-langserver"
  "rust-analyzer|bin/rust-analyzer"
  "bash-language-server|bin/bash-language-server"
  "shellcheck|bin/shellcheck"
  "sg|bin/sg"
  "go|bin/go"
)
lsp_shadow_execs=(python python3)
if [[ "$lsp_profile" == "full" ]]; then
  lsp_server_specs+=("jdtls|bin/jdtls")
fi

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

# 增量构建缓存：对 phase 产物做哈希指纹，命中则跳过耗时构建。
# 设置 SUPER_DOLPHIN_SKIP_BUILD_CACHE=1 可强制全量重建。
_build_cache_dir="${root}/.build-cache/phases"

_phase_hash() {
  local item
  for item in "$@"; do
    if [[ "$item" == input:* ]]; then
      printf 'input\t%s\n' "${item#input:}"
    elif [[ -f "$item" ]]; then
      printf 'file\t%s\t%s\n' "$item" "$(shasum -a 256 "$item" | awk '{print $1}')"
    elif [[ -d "$item" ]]; then
      find "$item" -type f -print0 | sort -z | while IFS= read -r -d '' file; do
        printf 'file\t%s\t%s\n' "$file" "$(shasum -a 256 "$file" | awk '{print $1}')"
      done
    else
      echo "missing build phase input: $item" >&2
      return 1
    fi
  done | shasum -a 256 | awk '{print $1}'
}

phase_cache_check() {
  [[ "${SUPER_DOLPHIN_SKIP_BUILD_CACHE:-0}" == "1" ]] && return 1
  [[ "${SUPER_DOLPHIN_RELEASE_BUILD:-0}" == "1" ]] && return 1
  local name="$1"; shift
  local hash; hash="$(_phase_hash "$@")"
  local marker="$_build_cache_dir/$name/$hash.ok"
  if [[ -f "$marker" ]]; then
    echo "==> [$name] cache hit ($hash), skipping" >&2
    return 0
  fi
  # 把哈希存到全局变量，phase_cache_save 时使用
  _current_phase_name="$name"
  _current_phase_hash="$hash"
  return 1
}

phase_cache_save() {
  [[ "${SUPER_DOLPHIN_SKIP_BUILD_CACHE:-0}" == "1" ]] && return 0
  [[ "${SUPER_DOLPHIN_RELEASE_BUILD:-0}" == "1" ]] && return 0
  local name="${_current_phase_name:-}"
  local hash="${_current_phase_hash:-}"
  [[ -z "$name" || -z "$hash" ]] && return 0
  mkdir -p "$_build_cache_dir/$name"
  # 清理同一 phase 的旧缓存标记（只保留最新）
  rm -f "$_build_cache_dir/$name/"*.ok
  touch "$_build_cache_dir/$name/$hash.ok"
}

frontend_node_version_input() {
  local version
  version="$(node --version)"
  [[ -n "${version//[[:space:]]/}" ]] || { echo "node --version returned empty output" >&2; exit 1; }
  printf '%s\n' "$version"
}

frontend_npm_version_input() {
  local version
  version="$(npm --version)"
  [[ -n "${version//[[:space:]]/}" ]] || { echo "npm --version returned empty output" >&2; exit 1; }
  printf '%s\n' "$version"
}

run_packaged_smoke_check() {
  local label="$1"
  shift

  local attempt status
  for attempt in 1 2 3; do
    if "$@" >/dev/null 2>&1; then
      return 0
    fi
    status=$?
    if [[ "$attempt" -eq 3 ]]; then
      echo "$label failed after $attempt attempts (exit $status)" >&2
      return "$status"
    fi
    echo "$label failed during packaged smoke check (exit $status); retrying" >&2
    sleep 1
  done
}

xml_escape() {
  local value="$1"
  value="${value//&/&amp;}"
  value="${value//</&lt;}"
  value="${value//>/&gt;}"
  value="${value//\"/&quot;}"
  value="${value//\'/&apos;}"
  printf '%s' "$value"
}

validate_env_file_value() {
  local label="$1"
  local value="$2"
  if [[ ! "$value" =~ [^[:space:]] ]]; then
    echo "$label is required and must not be whitespace-only" >&2
    exit 1
  fi
  if [[ "$value" == *$'
'* || "$value" == *$'
'* ]]; then
    echo "$label must not contain newline characters" >&2
    exit 1
  fi
}

base64_decode_to_file() {
  local value="$1"
  local dest="$2"
  if printf '%s' "$value" | base64 --decode > "$dest" 2>/dev/null; then
    return
  fi
  if printf '%s' "$value" | base64 -D > "$dest" 2>/dev/null; then
    return
  fi
  echo "SUPER_DOLPHIN_UPDATE_PUBLIC_KEY must be valid base64" >&2
  exit 1
}

require_developer_id_codesign() {
  if [[ "$codesign_identity" != Developer\ ID\ Application:* ]]; then
    echo "CODESIGN_IDENTITY must be a Developer ID Application identity for gray releases" >&2
    exit 1
  fi
}

resolve_release_profile() {
  case "$release_profile" in
    dev-local|gray|gray-unsigned)
      ;;
    *)
      echo "unsupported SUPER_DOLPHIN_RELEASE_PROFILE=$release_profile; expected dev-local, gray, or gray-unsigned" >&2
      exit 1
      ;;
  esac

  if [[ "$release_profile" != "gray" ]]; then
    return
  fi
  require_developer_id_codesign
  if [[ -z "${NOTARY_PROFILE:-}" ]]; then
    echo "NOTARY_PROFILE is required for gray releases" >&2
    exit 1
  fi
  validate_env_file_value "NOTARY_PROFILE" "$NOTARY_PROFILE"
}

updates_enabled_for_profile() {
  [[ "$release_profile" == "gray" || "$release_profile" == "gray-unsigned" ]]
}

is_placeholder_update_repo() {
  local repo="$1"
  [[ "$repo" == "xiaoxiaotest9527-bit/-" ]]
}

resolve_update_config() {
  if ! updates_enabled_for_profile; then
    return
  fi

  update_manifest_url="${SUPER_DOLPHIN_UPDATE_MANIFEST_URL:-}"
  update_github_repo="${SUPER_DOLPHIN_UPDATE_GITHUB_REPO:-}"
  update_public_key="${SUPER_DOLPHIN_UPDATE_PUBLIC_KEY:-}"
  update_channel="${SUPER_DOLPHIN_UPDATE_CHANNEL:-gray}"
  if [[ "$release_profile" == "gray-unsigned" ]]; then
    update_allow_unsigned="1"
  fi
  if [[ -z "${update_manifest_url//[[:space:]]/}" && -z "${update_github_repo//[[:space:]]/}" ]]; then
    echo "SUPER_DOLPHIN_UPDATE_MANIFEST_URL or SUPER_DOLPHIN_UPDATE_GITHUB_REPO is required when app update is enabled" >&2
    exit 1
  fi
  if [[ -n "${update_manifest_url//[[:space:]]/}" && -n "${update_github_repo//[[:space:]]/}" ]]; then
    echo "SUPER_DOLPHIN_UPDATE_MANIFEST_URL and SUPER_DOLPHIN_UPDATE_GITHUB_REPO are mutually exclusive" >&2
    exit 1
  fi
  if [[ -n "${update_manifest_url//[[:space:]]/}" ]]; then
    validate_env_file_value "SUPER_DOLPHIN_UPDATE_MANIFEST_URL" "$update_manifest_url"
    if [[ ! "$update_manifest_url" =~ ^https://[^/?#]+($|[/?#]) ]]; then
      echo "SUPER_DOLPHIN_UPDATE_MANIFEST_URL must be an HTTPS URL with a host" >&2
      exit 1
    fi
  fi
  if [[ -n "${update_github_repo//[[:space:]]/}" ]]; then
    validate_env_file_value "SUPER_DOLPHIN_UPDATE_GITHUB_REPO" "$update_github_repo"
    if [[ ! "$update_github_repo" =~ ^[^/[:space:]]+/[^/[:space:]]+$ ]]; then
      echo "SUPER_DOLPHIN_UPDATE_GITHUB_REPO must be owner/repo without whitespace" >&2
      exit 1
    fi
    if is_placeholder_update_repo "$update_github_repo"; then
      echo "known placeholder update repo is not allowed" >&2
      exit 1
    fi
  fi
  validate_env_file_value "SUPER_DOLPHIN_UPDATE_PUBLIC_KEY" "$update_public_key"
  validate_env_file_value "SUPER_DOLPHIN_UPDATE_CHANNEL" "$update_channel"

  local decoded_key byte_count
  decoded_key="$(mktemp)"
  base64_decode_to_file "$update_public_key" "$decoded_key"
  byte_count="$(wc -c < "$decoded_key" | tr -d '[:space:]')"
  rm -f "$decoded_key"
  if [[ "$byte_count" != "32" ]]; then
    echo "decoded SUPER_DOLPHIN_UPDATE_PUBLIC_KEY must be 32 bytes" >&2
    exit 1
  fi
}

resolve_packaged_relay_env() {
  packaged_relay_base_url="${SUPER_DOLPHIN_CODEX_RELAY_BASE_URL:-}"
  packaged_relay_api_key="${SUPER_DOLPHIN_CODEX_RELAY_API_KEY:-}"
  packaged_relay_bootstrap_token="${SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN:-}"
  if [[ -n "${packaged_relay_api_key//[[:space:]]/}" ]]; then
    echo "$codex_relay_privileged_api_key_env must not be set for macOS packaging; use $codex_relay_bootstrap_token_env" >&2
    exit 1
  fi
  packaged_relay_bootstrap_proof="${SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF:-}"
  validate_env_file_value "$codex_relay_base_url_env" "$packaged_relay_base_url"
  validate_env_file_value "$codex_relay_bootstrap_token_env" "$packaged_relay_bootstrap_token"
  validate_env_file_value "$codex_relay_bootstrap_proof_env" "$packaged_relay_bootstrap_proof"
  validate_release_relay_url
}

resolve_packaged_video_env() {
  local opt_in="${SUPER_DOLPHIN_PACKAGE_INCLUDE_VIDEO_API_KEY:-0}"
  case "$opt_in" in
    1|true|yes|on)
      packaged_video_api_key="${SILICONFLOW_API_KEY:-}"
      validate_env_file_value "$video_api_key_env" "$packaged_video_api_key"
      ;;
    ""|0|false|no|off)
      packaged_video_api_key=""
      ;;
    *)
      echo "$package_video_api_key_opt_in_env must be 1, true, yes, on, 0, false, no, or off" >&2
      exit 1
      ;;
  esac
}

resolve_macos_min_version() {
  macos_min_version="${SUPER_DOLPHIN_MACOS_MIN_VERSION:-13.0}"
  if [[ ! "$macos_min_version" =~ ^[0-9]+([.][0-9]+){1,2}$ ]]; then
    echo "$macos_min_version_env must be a dotted numeric version such as 13.0" >&2
    exit 1
  fi
}

version_gt() {
  local left="$1"
  local right="$2"
  local left_parts=()
  local right_parts=()
  local i left_num right_num
  IFS=. read -r -a left_parts <<< "$left"
  IFS=. read -r -a right_parts <<< "$right"
  for i in 0 1 2; do
    left_num=$((10#${left_parts[$i]:-0}))
    right_num=$((10#${right_parts[$i]:-0}))
    if ((left_num > right_num)); then
      return 0
    fi
    if ((left_num < right_num)); then
      return 1
    fi
  done
  return 1
}

macho_minos_versions() {
  local file="$1"
  otool -l "$file" 2>/dev/null | awk '
    /LC_BUILD_VERSION/ { in_build = 1; next }
    in_build && $1 == "minos" { print $2; in_build = 0; next }
    in_build && $1 == "ntools" { in_build = 0; next }
    /LC_VERSION_MIN_MACOSX/ { in_version_min = 1; next }
    in_version_min && $1 == "version" { print $2; in_version_min = 0; next }
    in_version_min && $1 == "sdk" { in_version_min = 0; next }
  '
}

verify_macho_macos_compatibility() {
  local label="$1"
  local max_version="$2"
  shift 2
  local failed=0 file minos_values found minos
  for file in "$@"; do
    if [[ ! -e "$file" ]]; then
      echo "missing $label for macOS compatibility check: $file" >&2
      failed=1
      continue
    fi
    is_macho "$file" || continue
    if ! minos_values="$(macho_minos_versions "$file")"; then
      echo "unable to read $label Mach-O minimum macOS version: $file" >&2
      failed=1
      continue
    fi
    found=0
    while IFS= read -r minos; do
      [[ -n "$minos" ]] || continue
      found=1
      if version_gt "$minos" "$max_version"; then
        echo "$label requires macOS $minos but target is $max_version: $file" >&2
        failed=1
      fi
    done <<< "$minos_values"
    if [[ "$found" != "1" ]]; then
      echo "unable to find $label Mach-O minimum macOS version: $file" >&2
      failed=1
    fi
  done
  if [[ "$failed" != "0" ]]; then
    exit 1
  fi
}

verify_startup_macos_compatibility() {
  local max_version="$1"
  verify_macho_macos_compatibility "startup binary" "$max_version" \
    "$macos/agent-terminal" \
    "$resources/bin/mcp-orch" \
    "$resources/bin/mcp-lsp"
}

validate_release_relay_url() {
  if ! updates_enabled_for_profile; then
    return
  fi
  if [[ ! "$packaged_relay_base_url" =~ ^https://[^/?#]+($|[/?#]) ]]; then
    echo "$codex_relay_base_url_env must be an HTTPS URL with host for $release_profile releases" >&2
    exit 1
  fi
  case "$packaged_relay_base_url" in
    http://127.*|https://127.*|http://localhost*|https://localhost*|http://0.0.0.0*|https://0.0.0.0*|*.invalid*|*.test*)
      echo "$codex_relay_base_url_env must not use a local or placeholder host for $release_profile releases: $packaged_relay_base_url" >&2
      exit 1
      ;;
  esac
}

write_packaged_relay_env() {
  local resources="$1"
  local env_path="$resources/.env"
  {
    printf '%s=%s
' "$codex_relay_base_url_env" "$packaged_relay_base_url"
    printf '%s=%s
' "$codex_relay_bootstrap_token_env" "$packaged_relay_bootstrap_token"
    printf '%s=%s
' "$codex_relay_bootstrap_proof_env" "$packaged_relay_bootstrap_proof"
  } > "$env_path"
  chmod 600 "$env_path"
}

write_packaged_video_env() {
  local resources="$1"
  if [[ -z "$packaged_video_api_key" ]]; then
    return
  fi
  local env_path="$resources/.env"
  printf '%s=%s\n' "$video_api_key_env" "$packaged_video_api_key" >> "$env_path"
  chmod 600 "$env_path"
}

write_packaged_update_env() {
  local resources="$1"
  if ! updates_enabled_for_profile; then
    return
  fi
  local env_path="$resources/.env"
  {
    printf 'SUPER_DOLPHIN_UPDATE_ENABLED=1\n'
    if [[ -n "$update_manifest_url" ]]; then
      printf 'SUPER_DOLPHIN_UPDATE_MANIFEST_URL=%s\n' "$update_manifest_url"
    fi
    if [[ -n "$update_github_repo" ]]; then
      printf 'SUPER_DOLPHIN_UPDATE_GITHUB_REPO=%s\n' "$update_github_repo"
    fi
    printf 'SUPER_DOLPHIN_UPDATE_PUBLIC_KEY=%s\n' "$update_public_key"
    printf 'SUPER_DOLPHIN_UPDATE_CHANNEL=%s\n' "$update_channel"
    printf 'SUPER_DOLPHIN_UPDATE_VERSION=%s\n' "$version"
    if [[ "$update_allow_unsigned" == "1" ]]; then
      printf 'SUPER_DOLPHIN_UPDATE_ALLOW_UNSIGNED=1\n'
    fi
  } >> "$env_path"
  chmod 600 "$env_path"
}

json_escape() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  printf '%s' "$value"
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

write_dmg_checksum() {
  local dmg="$1"
  sha256_file "$dmg" > "$dmg.sha256"
}

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

resolve_packaged_codex_artifact() {
  packaged_codex_artifact="${SUPER_DOLPHIN_CODEX_ARTIFACT:-}"
  packaged_codex_sha256="${SUPER_DOLPHIN_CODEX_SHA256:-}"
  packaged_codex_version="${SUPER_DOLPHIN_CODEX_VERSION:-}"
  if [[ -z "$packaged_codex_artifact" ]]; then
    if [[ "$require_bundled_codex" == "1" ]]; then
      echo "packaged Codex CLI artifact is required; set $codex_artifact_env to a release artifact and $codex_sha256_env to its trusted SHA-256" >&2
      exit 1
    fi
    echo "Codex CLI artifact not bundled because SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX=$require_bundled_codex" >&2
    return
  fi
  if [[ ! -f "$packaged_codex_artifact" ]]; then
    echo "packaged Codex CLI artifact does not exist: $packaged_codex_artifact" >&2
    exit 1
  fi
  if [[ ! -x "$packaged_codex_artifact" ]]; then
    echo "packaged Codex CLI artifact is not executable: $packaged_codex_artifact" >&2
    exit 1
  fi
  if [[ -z "$packaged_codex_sha256" ]]; then
    echo "packaged Codex CLI checksum is required; set $codex_sha256_env from a trusted release manifest or signature verification" >&2
    exit 1
  fi
  if [[ ! "$packaged_codex_sha256" =~ ^[[:xdigit:]]{64}$ ]]; then
    echo "$codex_sha256_env must be a 64-character hex SHA-256" >&2
    exit 1
  fi
  if [[ -z "$packaged_codex_version" ]]; then
    echo "packaged Codex CLI version is required; set $codex_version_env" >&2
    exit 1
  fi
  validate_env_file_value "$codex_sha256_env" "$packaged_codex_sha256"
  validate_env_file_value "$codex_version_env" "$packaged_codex_version"
  local expected actual
  expected="$(printf '%s' "$packaged_codex_sha256" | tr 'A-F' 'a-f')"
  actual="$(sha256_file "$packaged_codex_artifact")"
  if [[ "$actual" != "$expected" ]]; then
    echo "Codex CLI artifact checksum mismatch: $packaged_codex_artifact" >&2
    echo "  expected: $expected" >&2
    echo "  actual:   $actual" >&2
    exit 1
  fi
  packaged_codex_sha256="$expected"
  echo "Codex CLI artifact checksum verified: $packaged_codex_artifact" >&2
}

resolve_packaged_codex_binary() {
  local artifact="$1"
  if is_macho "$artifact"; then
    printf '%s\n' "$artifact"
    return
  fi
  if ! grep -q 'PLATFORM_PACKAGE_BY_TARGET' "$artifact" 2>/dev/null; then
    echo "packaged Codex CLI artifact is not a Mach-O binary or recognized npm launcher: $artifact" >&2
    exit 1
  fi

  local source_real source_pkg platform_pkg target_triple candidate arch
  source_real="$(real_file_path "$artifact")"
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
      echo "unsupported macOS architecture for packaged Codex CLI: $arch" >&2
      exit 1
      ;;
  esac
  candidate="$source_pkg/node_modules/$platform_pkg/vendor/$target_triple/codex/codex"
  if [[ ! -x "$candidate" ]]; then
    echo "packaged Codex CLI npm launcher is missing native binary: $candidate" >&2
    exit 1
  fi
  if ! is_macho "$candidate"; then
    echo "packaged Codex CLI artifact resolved to non-Mach-O binary: $candidate" >&2
    exit 1
  fi
  printf '%s\n' "$candidate"
}

copy_packaged_codex() {
  local bundle_root="$1"
  local dest="$2"
  if [[ -z "$packaged_codex_artifact" ]]; then
    return
  fi
  local source_binary
  source_binary="$(resolve_packaged_codex_binary "$packaged_codex_artifact")"
  mkdir -p "$(dirname "$dest")"
  cp -f "$source_binary" "$dest"
  chmod 755 "$dest"
  if ! run_packaged_smoke_check "Codex CLI app-server" "$dest" app-server --help; then
    echo "packaged Codex CLI failed app-server validation: $dest" >&2
    exit 1
  fi
}

require_lsp_relative_path() {
  local label="$1"
  local rel_path="$2"
  case "$rel_path" in
    ""|/*|..|../*|*/..|*/../*)
      echo "$label must be a relative path inside the LSP bundle: $rel_path" >&2
      exit 1
      ;;
  esac
}

json_value_at_path() {
  local manifest="$1"
  local target_path="$2"
  local mode="$3"
  awk -v target_path="$target_path" -v mode="$mode" '
    function fail() { exit 2 }
    function skip_ws() {
      while (pos <= length(json) && substr(json, pos, 1) ~ /^[[:space:]]$/) {
        pos++
      }
    }
    function child_path(path, key) {
      if (path == "") {
        return key
      }
      return path "." key
    }
    function emit(start) {
      if (mode == "json") {
        print substr(json, start, pos - start)
      }
      found = 1
      exit
    }
    function parse_string(    out, c, esc) {
      if (substr(json, pos, 1) != "\"") {
        fail()
      }
      pos++
      out = ""
      while (pos <= length(json)) {
        c = substr(json, pos, 1)
        if (c == "\"") {
          pos++
          return out
        }
        if (c == "\\") {
          pos++
          if (pos > length(json)) {
            fail()
          }
          esc = substr(json, pos, 1)
          if (esc == "n") {
            out = out "\n"
            pos++
            continue
          }
          if (esc == "r") {
            out = out "\r"
            pos++
            continue
          }
          if (esc == "t") {
            out = out "\t"
            pos++
            continue
          }
          if (esc == "b") {
            out = out "\b"
            pos++
            continue
          }
          if (esc == "f") {
            out = out "\f"
            pos++
            continue
          }
          if (esc == "u") {
            out = out "\\u" substr(json, pos + 1, 4)
            pos += 5
            continue
          }
          out = out esc
          pos++
          continue
        }
        out = out c
        pos++
      }
      fail()
    }
    function parse_literal(    c) {
      while (pos <= length(json)) {
        c = substr(json, pos, 1)
        if (c == "," || c == "}" || c == "]" || c ~ /^[[:space:]]$/) {
          return
        }
        pos++
      }
    }
    function parse_array(path,    c) {
      pos++
      skip_ws()
      if (substr(json, pos, 1) == "]") {
        pos++
        return
      }
      while (pos <= length(json)) {
        parse_value(path "[]")
        skip_ws()
        c = substr(json, pos, 1)
        if (c == ",") {
          pos++
          skip_ws()
          continue
        }
        if (c == "]") {
          pos++
          return
        }
        fail()
      }
      fail()
    }
    function parse_object(path,    key, c) {
      pos++
      skip_ws()
      if (substr(json, pos, 1) == "}") {
        pos++
        return
      }
      while (pos <= length(json)) {
        key = parse_string()
        skip_ws()
        if (substr(json, pos, 1) != ":") {
          fail()
        }
        pos++
        parse_value(child_path(path, key))
        skip_ws()
        c = substr(json, pos, 1)
        if (c == ",") {
          pos++
          skip_ws()
          continue
        }
        if (c == "}") {
          pos++
          return
        }
        fail()
      }
      fail()
    }
    function parse_value(path,    start, c, value) {
      skip_ws()
      start = pos
      c = substr(json, pos, 1)
      if (c == "{") {
        parse_object(path)
        if (path == target_path && mode == "json") {
          emit(start)
        }
        return
      }
      if (c == "[") {
        parse_array(path)
        if (path == target_path && mode == "json") {
          emit(start)
        }
        return
      }
      if (c == "\"") {
        value = parse_string()
        if (path == target_path) {
          if (mode == "string") {
            print value
          } else {
            print substr(json, start, pos - start)
          }
          found = 1
          exit
        }
        return
      }
      parse_literal()
      if (path == target_path && mode == "json") {
        emit(start)
      }
    }
    {
      json = json $0 "\n"
    }
    END {
      pos = 1
      found = 0
      parse_value("")
      if (!found) {
        exit 1
      }
    }
  ' "$manifest"
}

lsp_manifest_value() {
  local manifest="$1"
  local server="$2"
  local field="$3"
  json_value_at_path "$manifest" "servers.$server.$field" string
}

lsp_manifest_json_value() {
  local manifest="$1"
  local server="$2"
  local field="$3"
  json_value_at_path "$manifest" "servers.$server.$field" json
}

verify_lsp_checksums_file() {
  if (
    cd "$packaged_lsp_bundle_dir"
    if command -v sha256sum >/dev/null 2>&1; then
      sha256sum -c "$lsp_checksums_name"
      exit $?
    fi
    if command -v shasum >/dev/null 2>&1; then
      shasum -a 256 -c "$lsp_checksums_name"
      exit $?
    fi
    echo "missing SHA-256 tool; install sha256sum or shasum" >&2
    exit 1
  ) >/dev/null; then
    return
  fi
  echo "packaged LSP bundle checksum mismatch: $packaged_lsp_bundle_dir/$lsp_checksums_name" >&2
  exit 1
}

resolve_packaged_lsp_bundle() {
  packaged_lsp_bundle_dir="${SUPER_DOLPHIN_LSP_BUNDLE_DIR:-}"
  if [[ -z "$packaged_lsp_bundle_dir" ]]; then
    echo "packaged LSP bundle is required; set $lsp_bundle_dir_env to a prepared $lsp_profile bundle containing $lsp_manifest_name, $lsp_checksums_name, gopls, typescript-language-server, vscode-langservers-extracted, pyright, rust-analyzer, bash-language-server, shellcheck, sg, and jdtls only for full profile" >&2
    exit 1
  fi
  if [[ ! -d "$packaged_lsp_bundle_dir" ]]; then
    echo "packaged LSP bundle does not exist: $packaged_lsp_bundle_dir" >&2
    exit 1
  fi
  local manifest="$packaged_lsp_bundle_dir/$lsp_manifest_name"
  local checksums="$packaged_lsp_bundle_dir/$lsp_checksums_name"
  if [[ ! -f "$manifest" ]]; then
    echo "packaged LSP bundle missing manifest: $manifest" >&2
    exit 1
  fi
  if [[ ! -f "$checksums" ]]; then
    echo "packaged LSP bundle missing checksums: $checksums" >&2
    exit 1
  fi
  verify_lsp_checksums_file
  local spec server_id rel_path manifest_path version expected_sha actual_sha src
  for spec in "${lsp_server_specs[@]}"; do
    IFS='|' read -r server_id rel_path <<< "$spec"
    if ! manifest_path="$(lsp_manifest_value "$manifest" "$server_id" path)"; then
      echo "LSP manifest missing path for $server_id: $manifest" >&2
      exit 1
    fi
    require_lsp_relative_path "LSP manifest path for $server_id" "$manifest_path"
    if [[ "$manifest_path" != "$rel_path" ]]; then
      echo "LSP manifest path mismatch for $server_id: expected $rel_path, got $manifest_path" >&2
      exit 1
    fi
    if ! version="$(lsp_manifest_value "$manifest" "$server_id" version)" || [[ -z "$version" ]]; then
      echo "LSP manifest missing version for $server_id: $manifest" >&2
      exit 1
    fi
    if ! expected_sha="$(lsp_manifest_value "$manifest" "$server_id" sha256)"; then
      echo "LSP manifest missing sha256 for $server_id: $manifest" >&2
      exit 1
    fi
    if [[ ! "$expected_sha" =~ ^[[:xdigit:]]{64}$ ]]; then
      echo "LSP manifest sha256 for $server_id must be a 64-character hex SHA-256" >&2
      exit 1
    fi
    expected_sha="$(printf '%s' "$expected_sha" | tr 'A-F' 'a-f')"
    src="$packaged_lsp_bundle_dir/$rel_path"
    if [[ ! -x "$src" ]]; then
      if [[ "$server_id" == "go" ]]; then
        echo "packaged LSP bundle missing Go toolchain executable: $src" >&2
        exit 1
      fi
      echo "packaged LSP bundle missing executable $server_id: $src" >&2
      exit 1
    fi
    actual_sha="$(sha256_file "$src")"
    if [[ "$actual_sha" != "$expected_sha" ]]; then
      echo "packaged LSP bundle checksum mismatch for $server_id: $src" >&2
      echo "  expected: $expected_sha" >&2
      echo "  actual:   $actual_sha" >&2
      exit 1
    fi
  done
  local shadow_exec
  for shadow_exec in "${lsp_shadow_execs[@]}"; do
    if [[ ! -x "$packaged_lsp_bundle_dir/bin/$shadow_exec" ]]; then
      echo "packaged LSP bundle missing Python shadow executable: $packaged_lsp_bundle_dir/bin/$shadow_exec" >&2
      exit 1
    fi
  done
  echo "LSP bundle checksums verified: $packaged_lsp_bundle_dir" >&2
}

lsp_node_prebuild_arch() {
  case "$goarch" in
    amd64)
      printf '%s\n' "x64"
      ;;
    arm64)
      printf '%s\n' "arm64"
      ;;
    *)
      printf '%s\n' "$goarch"
      ;;
  esac
}

prune_packaged_lsp_non_macos_prebuilds() {
  local dest_root="$1"
  local node_modules="$dest_root/node_modules"
  [[ -d "$node_modules" ]] || return

  local prebuild_arch prebuilds_dir platform_dir platform_name
  prebuild_arch="$(lsp_node_prebuild_arch)"
  while IFS= read -r -d '' prebuilds_dir; do
    while IFS= read -r -d '' platform_dir; do
      platform_name="$(basename "$platform_dir")"
      case "$platform_name" in
        "darwin-$prebuild_arch"|darwin-"$prebuild_arch"-*|darwin-"$prebuild_arch"+*)
          ;;
        *)
          rm -rf "$platform_dir"
          ;;
      esac
    done < <(find "$prebuilds_dir" -mindepth 1 -maxdepth 1 -type d -print0)
  done < <(find "$dest_root/node_modules" -type d -name prebuilds -print0)
}

copy_packaged_lsp_bundle() {
  local resources="$1"
  local dest_root="$resources/lsp"
  rm -rf "$dest_root"
  mkdir -p "$dest_root" "$resources/bin"
  rsync -aL --delete "$packaged_lsp_bundle_dir"/ "$dest_root"/
  prune_packaged_lsp_non_macos_prebuilds "$dest_root"
  local spec server_id rel_path bin_name link_path
  for spec in "${lsp_server_specs[@]}"; do
    IFS='|' read -r server_id rel_path <<< "$spec"
    if [[ ! -x "$dest_root/$rel_path" ]]; then
      echo "packaged LSP bundle did not copy executable $server_id: $dest_root/$rel_path" >&2
      exit 1
    fi
    bin_name="$(basename "$rel_path")"
    link_path="$resources/bin/$bin_name"
    rm -f "$link_path"
    ln -s "../lsp/$rel_path" "$link_path"
    if [[ ! -x "$link_path" ]]; then
      echo "packaged LSP bundle did not expose executable $server_id: $link_path" >&2
      exit 1
    fi
  done
  local shadow_exec
  for shadow_exec in "${lsp_shadow_execs[@]}"; do
    if [[ ! -x "$dest_root/bin/$shadow_exec" ]]; then
      echo "packaged LSP bundle did not copy Python shadow executable: $dest_root/bin/$shadow_exec" >&2
      exit 1
    fi
  done
}

write_lsp_manifest() {
  local resources="$1"
  local source_manifest="$resources/lsp/$lsp_manifest_name"
  local manifest_tmp="$resources/lsp/$lsp_manifest_name.tmp"
  if [[ ! -f "$source_manifest" ]]; then
    echo "missing copied LSP manifest before package manifest write: $source_manifest" >&2
    exit 1
  fi
  cat > "$manifest_tmp" <<JSON
{
  "schema_version": 1,
  "bundle_path": "lsp",
  "servers": {
JSON
  local first=1 spec server_id rel_path version languages_json checksum server_json rel_path_json version_json
  for spec in "${lsp_server_specs[@]}"; do
    IFS='|' read -r server_id rel_path <<< "$spec"
    if [[ ! -x "$resources/lsp/$rel_path" ]]; then
      echo "missing packaged LSP server executable before manifest write: $resources/lsp/$rel_path" >&2
      exit 1
    fi
    if ! version="$(lsp_manifest_value "$source_manifest" "$server_id" version)" || [[ -z "$version" ]]; then
      echo "LSP manifest missing version for $server_id before package manifest write: $source_manifest" >&2
      exit 1
    fi
    if ! languages_json="$(lsp_manifest_json_value "$source_manifest" "$server_id" languages)" || [[ -z "$languages_json" ]]; then
      echo "LSP manifest missing languages for $server_id before package manifest write: $source_manifest" >&2
      exit 1
    fi
    case "$languages_json" in
      \[*)
        ;;
      *)
        echo "LSP manifest languages for $server_id must be a JSON array: $source_manifest" >&2
        exit 1
        ;;
    esac
    checksum="$(sha256_file "$resources/lsp/$rel_path")"
    server_json="$(json_escape "$server_id")"
    rel_path_json="$(json_escape "lsp/$rel_path")"
    version_json="$(json_escape "$version")"
    if [[ "$first" != "1" ]]; then
      printf ',\n' >> "$manifest_tmp"
    fi
    first=0
    cat >> "$manifest_tmp" <<JSON
    "$server_json": {
      "path": "$rel_path_json",
      "version": "$version_json",
      "sha256": "$checksum",
      "languages": $languages_json
    }
JSON
  done
  cat >> "$manifest_tmp" <<JSON

  }
}
JSON
  mv "$manifest_tmp" "$source_manifest"
}

write_codex_manifest() {
  local bundle_root="$1"
  if [[ -z "$packaged_codex_artifact" ]]; then
    return
  fi
  local version_json package_sha256
  version_json="$(json_escape "$packaged_codex_version")"
  package_sha256="$(sha256_file "$bundle_root/bin/codex")"
  cat > "$bundle_root/codex-manifest.json" <<JSON
{
  "codex": {
    "path": "bin/codex",
    "version": "$version_json",
    "source_sha256": "$packaged_codex_sha256",
    "package_sha256": "$package_sha256"
  }
}
JSON
}

write_runtime_manifest() {
  local resources="$1"
  local platform="$2"
  cat > "$resources/runtime-manifest.json" <<JSON
{
  "bundled_codex_path": "bin/codex",
  "bundled_gopls_path": "bin/gopls",
  "lsp_bundle_path": "lsp",
  "lsp_manifest_path": "lsp/lsp-manifest.json",
  "model_registry_path": "models.yaml"
}
JSON
}

dylib_refs_for() {
  local file="$1"
  otool -L "$file" 2>/dev/null \
    | awk '/^[[:space:]]*(\/.*\.dylib|@loader_path\/.*\.dylib|@rpath\/.*\.dylib)/{print $1}' \
    | grep -Ev '^(\/usr\/lib\/|\/System\/Library\/)' || true
}

rpaths_for() {
  local file="$1"
  otool -l "$file" 2>/dev/null \
    | awk '
      $1 == "cmd" && $2 == "LC_RPATH" { in_rpath = 1; next }
      in_rpath && $1 == "path" { print $2; in_rpath = 0; next }
    '
}

resolve_macho_path_token() {
  local file="$1"
  local path="$2"
  case "$path" in
    @loader_path/*)
      printf '%s\n' "$(dirname "$file")/${path#@loader_path/}"
      ;;
    @executable_path/*)
      printf '%s\n' "$(dirname "$file")/${path#@executable_path/}"
      ;;
    *)
      printf '%s\n' "$path"
      ;;
  esac
}

resolve_dylib_ref() {
  local file="$1"
  local ref="$2"
  case "$ref" in
    /*.dylib)
      printf '%s\n' "$ref"
      ;;
    @loader_path/*.dylib)
      local candidate
      candidate="$(dirname "$file")/${ref#@loader_path/}"
      if [[ -f "$candidate" ]]; then
        printf '%s\n' "$candidate"
      fi
      ;;
    @rpath/*.dylib)
      local rpath candidate suffix
      suffix="${ref#@rpath/}"
      while IFS= read -r rpath; do
        [[ -n "$rpath" ]] || continue
        candidate="$(resolve_macho_path_token "$file" "$rpath")/$suffix"
        if [[ -f "$candidate" ]]; then
          printf '%s\n' "$candidate"
          return
        fi
      done < <(rpaths_for "$file")
      for candidate in \
        "$(dirname "$file")/$suffix" \
        "$(dirname "$file")/../lib/$suffix" \
        "$(dirname "$file")/../../lib/$suffix"; do
        if [[ -f "$candidate" ]]; then
          printf '%s\n' "$candidate"
          return
        fi
      done
      ;;
  esac
}

is_macho() {
  file -b "$1" 2>/dev/null | grep -q 'Mach-O'
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

resolve_packaged_ffmpeg() {
  local candidate
  candidate="$(resolve_host_ffmpeg_candidate || true)"
  if [[ -n "$candidate" ]] && verify_host_ffmpeg "$candidate"; then
    packaged_ffmpeg_bin="$(real_file_path "$candidate")"
    echo "ffmpeg verified: $packaged_ffmpeg_bin" >&2
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
  packaged_ffmpeg_bin="$(real_file_path "$candidate")"
  echo "ffmpeg verified: $packaged_ffmpeg_bin" >&2
}

copy_packaged_ffmpeg() {
  local resources="$1"
  if [[ -z "$packaged_ffmpeg_bin" ]]; then
    echo "internal error: packaged_ffmpeg_bin is empty; resolve_packaged_ffmpeg must run before copy_packaged_ffmpeg" >&2
    exit 1
  fi
  if ! is_macho "$packaged_ffmpeg_bin"; then
    echo "ffmpeg binary is not a Mach-O executable: $packaged_ffmpeg_bin" >&2
    exit 1
  fi
  mkdir -p "$resources/bin"
  cp -f "$packaged_ffmpeg_bin" "$resources/bin/ffmpeg"
  chmod 755 "$resources/bin/ffmpeg"
  run_packaged_smoke_check "ffmpeg" "$resources/bin/ffmpeg" -version
}

add_rpath_if_missing() {
  local file="$1"
  local rpath="$2"
  if otool -l "$file" 2>/dev/null | awk '/path /{print $2}' | grep -Fxq "$rpath"; then
    return
  fi
  install_name_tool -add_rpath "$rpath" "$file"
}

add_git_rpaths() {
  local resources="$1"
  local file="$2"
  case "$file" in
    "$resources/bin/"*)
      add_rpath_if_missing "$file" "@loader_path/../lib"
      ;;
    "$resources/libexec/git-core/"*)
      add_rpath_if_missing "$file" "@loader_path/../../lib"
      ;;
    "$resources/lib/"*)
      add_rpath_if_missing "$file" "@loader_path"
      ;;
  esac
}

add_lsp_rpaths() {
  local lsp_root="$1"
  local file="$2"
  case "$file" in
    "$lsp_root/bin/"*)
      add_rpath_if_missing "$file" "@loader_path/../lib"
      ;;
    "$lsp_root/lib/"*)
      add_rpath_if_missing "$file" "@loader_path"
      ;;
    "$lsp_root/jdk/lib/server/"*)
      add_rpath_if_missing "$file" "@loader_path"
      add_rpath_if_missing "$file" "@loader_path/.."
      add_rpath_if_missing "$file" "@loader_path/../../../lib"
      ;;
    "$lsp_root/jdk/lib/"*)
      add_rpath_if_missing "$file" "@loader_path"
      add_rpath_if_missing "$file" "@loader_path/server"
      add_rpath_if_missing "$file" "@loader_path/../../lib"
      ;;
    "$lsp_root/jdtls/"*)
      add_rpath_if_missing "$file" "@loader_path"
      add_rpath_if_missing "$file" "@loader_path/../lib"
      add_rpath_if_missing "$file" "@loader_path/../../lib"
      add_rpath_if_missing "$file" "@loader_path/../../../lib"
      add_rpath_if_missing "$file" "@loader_path/../../../../lib"
      ;;
  esac
}

add_bundle_rpaths() {
  local kind="$1"
  local bundle_root="$2"
  local file="$3"
  case "$kind" in
    git)
      add_git_rpaths "$bundle_root" "$file"
      ;;
    lsp)
      add_lsp_rpaths "$bundle_root" "$file"
      ;;
  esac
}

resolve_git_bin() {
  if [[ -n "${SUPER_DOLPHIN_GIT_BIN:-}" ]]; then
    printf '%s\n' "$SUPER_DOLPHIN_GIT_BIN"
    return
  fi
  for candidate in \
    /Library/Developer/CommandLineTools/usr/bin/git \
    /Applications/Xcode.app/Contents/Developer/usr/bin/git; do
    if [[ -x "$candidate" ]]; then
      printf '%s\n' "$candidate"
      return
    fi
  done
  if brew_prefix="$(brew --prefix git 2>/dev/null)" && [[ -x "$brew_prefix/bin/git" ]]; then
    printf '%s\n' "$brew_prefix/bin/git"
    return
  fi
  if candidate="$(command -v git 2>/dev/null)" && [[ -x "$candidate" && "$candidate" != "/usr/bin/git" ]]; then
    printf '%s\n' "$candidate"
  fi
}

write_git_core_hardlink_manifest() {
  local git_exec_path="$1"
  local resources="$2"
  local dest_root="$resources/libexec/git-core"
  local manifest="$resources/.git-core-hardlinks.tsv"
  : > "$manifest"
  while IFS= read -r -d '' src; do
    local rel inode
    rel="${src#"$git_exec_path"/}"
    [[ -f "$dest_root/$rel" ]] || continue
    inode="$(stat -f '%i' "$src")"
    printf '%s\t%s\n' "$inode" "$rel"
  done < <(find "$git_exec_path" -type f -links +1 -print0) | sort -k1,1 -k2,2 > "$manifest"
}

restore_git_core_hardlinks() {
  local resources="$1"
  local manifest="$resources/.git-core-hardlinks.tsv"
  local dest_root="$resources/libexec/git-core"
  [[ -s "$manifest" ]] || return 0

  local current_group="" canonical="" group rel path
  while IFS=$'\t' read -r group rel; do
    path="$dest_root/$rel"
    [[ -f "$path" ]] || continue
    if [[ "$group" != "$current_group" ]]; then
      current_group="$group"
      canonical=""
    fi
    if [[ -z "$canonical" ]]; then
      canonical="$path"
      continue
    fi
    [[ "$path" == "$canonical" ]] && continue
    rm -f "$path"
    ln "$canonical" "$path"
  done < "$manifest"
  rm -f "$manifest"
}

copy_packaged_git() {
  local resources="$1"
  local git_bin
  git_bin="$(resolve_git_bin)"
  if [[ -z "$git_bin" || ! -x "$git_bin" ]]; then
    echo "missing portable Git; install Xcode Command Line Tools, Homebrew git, or set SUPER_DOLPHIN_GIT_BIN" >&2
    exit 1
  fi

  local git_exec_path git_prefix git_share
  git_exec_path="$("$git_bin" --exec-path)"
  if [[ -z "$git_exec_path" || ! -d "$git_exec_path" ]]; then
    echo "unable to resolve Git exec path from $git_bin" >&2
    exit 1
  fi
  git_prefix="$(cd "$git_exec_path/../.." && pwd)"
  git_share="$git_prefix/share/git-core"
  if [[ ! -d "$git_share" ]]; then
    echo "missing Git share directory: $git_share" >&2
    exit 1
  fi

  mkdir -p "$resources/bin" "$resources/libexec" "$resources/share"
  cp -f "$git_bin" "$resources/bin/git"
  chmod 755 "$resources/bin/git"
  rsync -aH --delete "$git_exec_path"/ "$resources/libexec/git-core"/
  rsync -aH --delete "$git_share"/ "$resources/share/git-core"/
  rm -f "$resources/libexec/git-core/git-p4"
  write_git_core_hardlink_manifest "$git_exec_path" "$resources"

  while IFS= read -r -d '' link; do
    local target helper
    target="$(readlink "$link")"
    case "$target" in
      ../../bin/*)
        helper="${target#../../bin/}"
        if [[ ! -x "$git_prefix/bin/$helper" ]]; then
          echo "missing Git symlink target: $git_prefix/bin/$helper" >&2
          exit 1
        fi
        cp -f "$git_prefix/bin/$helper" "$resources/bin/$helper"
        chmod 755 "$resources/bin/$helper"
        ;;
    esac
  done < <(find "$resources/libexec/git-core" -type l -print0)
}

copy_model_registry() {
  local resources="$1"
  local src="$root/cmd/mcp-orch/tools/modelregistry/models.yaml"
  if [[ ! -f "$src" ]]; then
    echo "missing model registry: $src" >&2
    exit 1
  fi
  cp -f "$src" "$resources/models.yaml"
}

copy_sqlite_migrations() {
  local bundle_root="$1"
  local src="$root/internal/platform/db/sqlite/migrations"
  local dest="$bundle_root/internal/platform/db/sqlite/migrations"
  if [[ ! -d "$src" ]]; then
    echo "missing SQLite migrations directory: $src" >&2
    exit 1
  fi
  if [[ -z "$(find "$src" -type f -print -quit)" ]]; then
    echo "missing SQLite migration files under $src" >&2
    exit 1
  fi
  rm -rf "$dest"
  mkdir -p "$(dirname "$dest")"
  cp -R "$src" "$dest"
}

check_loader_path_deps() {
  local pg_bundle="$1"
  local missing=0
  while IFS= read -r -d '' file; do
    [[ -f "$file" ]] || continue
    is_macho "$file" || continue
    while IFS= read -r ref; do
      [[ "$ref" == @loader_path/*.dylib ]] || continue
      local resolved
      resolved="$(dirname "$file")/${ref#@loader_path/}"
      if [[ ! -f "$resolved" ]]; then
        echo "missing @loader_path dependency: $ref referenced by $file" >&2
        missing=1
      fi
    done < <(dylib_refs_for "$file")
  done < <(find "$pg_bundle/bin" "$pg_bundle/lib" -type f -print0)
  return "$missing"
}

bundle_macho_dylibs() {
  local lib_dir="$1"
  local rpath_kind="$2"
  local bundle_root="$3"
  shift 3
  local roots=("$@")
  local seen_file
  seen_file="$(mktemp)"

  mkdir -p "$lib_dir"
  local queue=()
  local queue_index=0
  mark_seen_file() {
    local candidate="$1"
    grep -Fxq "$candidate" "$seen_file" 2>/dev/null && return 1
    printf '%s\n' "$candidate" >> "$seen_file"
  }
  enqueue_macho_candidate() {
    local candidate="$1"
    [[ -f "$candidate" ]] || return
    queue+=("$candidate")
  }
  while IFS= read -r -d '' file; do
    [[ -f "$file" ]] || continue
    is_macho "$file" || continue
    enqueue_macho_candidate "$file"
  done < <(find "${roots[@]}" -type f -print0)

  while ((queue_index < ${#queue[@]})); do
    local file="${queue[$queue_index]}"
    ((queue_index += 1))
    [[ -f "$file" ]] || continue
    is_macho "$file" || continue
    mark_seen_file "$file" || continue

    while IFS= read -r ref; do
      local dep
      dep="$(resolve_dylib_ref "$file" "$ref")"
      [[ -f "$dep" ]] || continue
      is_macho "$dep" || continue
      if [[ "$dep" != "$lib_dir"/* ]]; then
        local dest="$lib_dir/$(basename "$dep")"
        if [[ ! -f "$dest" ]]; then
          cp -fL "$dep" "$dest"
          chmod u+w "$dest"
        fi
      fi
      enqueue_macho_candidate "$dep"
    done < <(dylib_refs_for "$file")
  done

  while IFS= read -r dylib; do
    [[ -f "$dylib" ]] || continue
    is_macho "$dylib" || continue
    install_name_tool -id "@rpath/$(basename "$dylib")" "$dylib"
  done < <(find "${roots[@]}" "$lib_dir" -type f -name '*.dylib' | sort)

  while IFS= read -r file; do
    [[ -f "$file" ]] || continue
    is_macho "$file" || continue
    local rpaths_added=0
    while IFS= read -r ref; do
      local dep base
      dep="$(resolve_dylib_ref "$file" "$ref")"
      [[ -f "$dep" ]] || continue
      base="$(basename "$dep")"
      if [[ -f "$lib_dir/$base" || "$dep" == "$lib_dir"/* ]]; then
        if [[ "$rpaths_added" == "0" ]]; then
          add_bundle_rpaths "$rpath_kind" "$bundle_root" "$file"
          rpaths_added=1
        fi
        if ! install_name_tool -change "$ref" "@rpath/$base" "$file"; then
          echo "failed to rewrite dylib reference in $file: $ref -> @rpath/$base" >&2
          exit 1
        fi
      fi
    done < <(dylib_refs_for "$file")
  done < <(find "${roots[@]}" "$lib_dir" -type f | sort)

  rm -f "$seen_file"
}

verify_no_homebrew_dylib_refs() {
  local label="$1"
  shift
  if find "$@" -type f -print0 \
    | while IFS= read -r -d '' file; do is_macho "$file" && otool -L "$file" 2>/dev/null; done \
    | grep -Eq '/(opt/homebrew|usr/local)/(opt|Cellar)/'; then
    echo "packaged $label still references Homebrew dylibs" >&2
    find "$@" -type f -print0 \
      | while IFS= read -r -d '' file; do
          is_macho "$file" || continue
          if otool -L "$file" 2>/dev/null | grep -Eq '/(opt/homebrew|usr/local)/(opt|Cellar)/'; then
            echo "  $file" >&2
            otool -L "$file" 2>/dev/null | grep -E '/(opt/homebrew|usr/local)/(opt|Cellar)/' >&2
          fi
        done
    exit 1
  fi
}

bundle_git_dylibs() {
  local resources="$1"
  bundle_macho_dylibs "$resources/lib" git "$resources" "$resources/bin" "$resources/libexec/git-core"
  verify_no_homebrew_dylib_refs "Git" "$resources/bin" "$resources/libexec/git-core" "$resources/lib"
}

bundle_lsp_dylibs() {
  local resources="$1"
  local lsp_root="$resources/lsp"
  [[ -d "$lsp_root" ]] || return
  local macho_roots=()
  local root_dir
  for root_dir in \
    "$lsp_root/bin" \
    "$lsp_root/node/bin" \
    "$lsp_root/jdk/bin" \
    "$lsp_root/jdk/lib" \
    "$lsp_root/jdtls"; do
    [[ -e "$root_dir" ]] && macho_roots+=("$root_dir")
  done
  bundle_macho_dylibs "$lsp_root/lib" lsp "$lsp_root" "${macho_roots[@]}"
  verify_no_homebrew_dylib_refs "LSP" "${macho_roots[@]}" "$lsp_root/lib"
}

verify_packaged_git() {
  local resources="$1"
  run_packaged_smoke_check "Git CLI" \
    env \
    GIT_EXEC_PATH="$resources/libexec/git-core" \
    GIT_TEMPLATE_DIR="$resources/share/git-core/templates" \
    "$resources/bin/git" --version
}

verify_no_broken_symlinks() {
  local root_dir="$1"
  local broken
  broken="$(find -L "$root_dir" -type l -print 2>/dev/null || true)"
  if [[ -n "$broken" ]]; then
    echo "packaged app contains broken symlinks:" >&2
    printf '%s\n' "$broken" >&2
    exit 1
  fi
}

sign_macho_tree() {
  local identity="$1"
  shift
  local sign_args=(--force --sign "$identity")
  if [[ "$identity" != "-" ]]; then
    sign_args=(--force --options runtime --sign "$identity" --timestamp)
  fi
  while (($#)); do
    local root_dir="$1"
    shift
    [[ -e "$root_dir" ]] || continue
    while IFS= read -r -d '' file; do
      [[ -f "$file" ]] || continue
      is_macho "$file" || continue
      codesign "${sign_args[@]}" "$file"
    done < <(find "$root_dir" -type f -print0)
  done
}

resolve_release_profile
resolve_update_config
resolve_packaged_relay_env
resolve_packaged_video_env
resolve_macos_min_version
resolve_packaged_codex_artifact
resolve_packaged_lsp_bundle
resolve_packaged_ffmpeg

dist="$root/dist/package/macos"
app="$dist/$app_name.app"
dmg_path="$dist/$app_name.dmg"
contents="$app/Contents"
macos="$contents/MacOS"
resources="$contents/Resources"

rm -rf "$app" "$dmg_path" "$dmg_path.sha256"
mkdir -p "$macos" "$resources/bin"

phase_start "frontend build"
if [[ "${SUPER_DOLPHIN_SKIP_FRONTEND_BUILD:-}" != "1" ]]; then
  frontend_cache_inputs=(
    "input:NODE_VERSION=$(frontend_node_version_input)"
    "input:NPM_VERSION=$(frontend_npm_version_input)"
  )
  frontend_cache_paths=(
    "$root/frontend-app/package.json"
    "$root/frontend-app/package-lock.json"
    "$root/frontend-app/vite.config.js"
    "$root/frontend-app/index.html"
    "$root/frontend-app/public"
    "$root/frontend-app/src"
  )
  if ! phase_cache_check "frontend" "${frontend_cache_inputs[@]}" "${frontend_cache_paths[@]}"; then
    (
      cd "$root/frontend-app"
      npm ci
      npm run build
    )
    phase_cache_save
  fi
  rsync -a --delete --exclude .gitkeep "$root/frontend-app/dist"/ "$root/cmd/agent-terminal/web-dist"/
elif [[ ! -f "$root/frontend-app/dist/index.html" ]]; then
  echo "frontend dist missing; unset SUPER_DOLPHIN_SKIP_FRONTEND_BUILD or run npm run build first" >&2
  exit 1
else
  rsync -a --delete --exclude .gitkeep "$root/frontend-app/dist"/ "$root/cmd/agent-terminal/web-dist"/
fi
phase_end

phase_start "go binaries"
if ! phase_cache_check "go-binaries" "$root/cmd" "$root/internal" "$root/pkg" "$root/go.sum"; then
  (
    cd "$root"
    export MACOSX_DEPLOYMENT_TARGET="$macos_min_version"
    export CGO_CFLAGS="${CGO_CFLAGS:+$CGO_CFLAGS }-mmacosx-version-min=$macos_min_version"
    export CGO_CXXFLAGS="${CGO_CXXFLAGS:+$CGO_CXXFLAGS }-mmacosx-version-min=$macos_min_version"
    export CGO_LDFLAGS="${CGO_LDFLAGS:+$CGO_LDFLAGS }-mmacosx-version-min=$macos_min_version"
    make build-peer-binaries
    go build -o bin/agent-terminal ./cmd/agent-terminal
    go build -o bin/mcp-ida ./cmd/mcp-ida
    go build -o bin/super-dolphin-updater ./cmd/super-dolphin-updater
  )
  phase_cache_save
fi
phase_end

phase_start "copy app resources"
cp "$root/bin/agent-terminal" "$macos/agent-terminal"
cp "$root/bin/mcp-orch" "$resources/bin/mcp-orch"
cp "$root/bin/mcp-lsp" "$resources/bin/mcp-lsp"
cp "$root/bin/mcp-ida" "$resources/bin/mcp-ida"
copy_sqlite_migrations "$resources"
copy_model_registry "$resources"
write_packaged_relay_env "$resources"
write_packaged_video_env "$resources"
write_packaged_update_env "$resources"
copy_packaged_git "$resources"
copy_packaged_lsp_bundle "$resources"
copy_packaged_codex "$resources" "$resources/bin/codex"
copy_packaged_ffmpeg "$resources"
cp "$root/bin/super-dolphin-updater" "$resources/bin/super-dolphin-updater"
phase_end

phase_start "bundle git dylibs"
bundle_git_dylibs "$resources"
phase_end

phase_start "bundle lsp dylibs"
bundle_lsp_dylibs "$resources"
phase_end

phase_start "macos startup compatibility"
verify_startup_macos_compatibility "$macos_min_version"
phase_end

phase_start "write plist"
plist_bundle_id="$(xml_escape "$bundle_id")"
plist_app_name="$(xml_escape "$app_name")"
plist_version="$(xml_escape "$version")"
plist_macos_min_version="$(xml_escape "$macos_min_version")"

cat > "$contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleExecutable</key>
  <string>agent-terminal</string>
  <key>CFBundleIdentifier</key>
  <string>$plist_bundle_id</string>
  <key>CFBundleName</key>
  <string>$plist_app_name</string>
  <key>CFBundleDisplayName</key>
  <string>$plist_app_name</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleShortVersionString</key>
  <string>$plist_version</string>
  <key>CFBundleVersion</key>
  <string>$plist_version</string>
  <key>LSMinimumSystemVersion</key>
  <string>$plist_macos_min_version</string>
</dict>
</plist>
PLIST
phase_end

phase_start "codesign macho tree"
sign_macho_tree "$codesign_identity" "$macos" "$resources/bin" "$resources/lib" "$resources/libexec" "$resources/lsp"
restore_git_core_hardlinks "$resources"
phase_end

phase_start "runtime smoke checks"
verify_packaged_git "$resources"
write_codex_manifest "$resources"
write_lsp_manifest "$resources"
write_runtime_manifest "$resources" "$platform"
phase_end

codesign_args=(--force --sign "$codesign_identity")
if [[ "$codesign_identity" != "-" ]]; then
  codesign_args=(--force --options runtime --sign "$codesign_identity" --timestamp)
fi
phase_start "codesign app"
codesign "${codesign_args[@]}" "$app"
phase_end

phase_start "verify packaged app"
"$root/scripts/verify_packaged_app_macos.sh" "$app"
phase_end

phase_start "stage dmg contents"
staging="$dist/dmg-staging"
rm -rf "$staging"
mkdir -p "$staging"
ditto "$app" "$staging/$app_name.app"
install_script="$staging/安装 $app_name.command"
cat > "$install_script" <<'INSTALL_SH'
#!/bin/bash
set -e

APP_NAME="Super Dolphin"
SRC_DIR="$(cd "$(dirname "$0")" && pwd)"
SRC_APP="$SRC_DIR/$APP_NAME.app"
DEST_APP="/Applications/$APP_NAME.app"
STAGED_APP="/Applications/.$APP_NAME.installing.$$"
BACKUP_APP="/Applications/.$APP_NAME.backup.$$"

rollback_install() {
  local status=$?
  rm -rf "$STAGED_APP"
  if [[ -d "$BACKUP_APP" && ! -d "$DEST_APP" ]]; then
    mv "$BACKUP_APP" "$DEST_APP"
  elif [[ -d "$BACKUP_APP" ]]; then
    rm -rf "$BACKUP_APP"
  fi
  return "$status"
}
trap rollback_install ERR

clear
printf '\n'
printf '===============================================\n'
printf '   %s 安装程序\n' "$APP_NAME"
printf '===============================================\n\n'

if [[ ! -d "$SRC_APP" ]]; then
  printf '错误：未在 %s 找到 %s.app\n' "$SRC_DIR" "$APP_NAME" >&2
  printf '请确认本脚本与 %s.app 位于同一目录。\n' "$APP_NAME" >&2
  printf '\n按回车键退出...'
  read -r _ || true
  exit 1
fi

printf '正在安装到 /Applications ...\n'
rm -rf "$STAGED_APP" "$BACKUP_APP"
ditto "$SRC_APP" "$STAGED_APP"
if [[ -d "$DEST_APP" ]]; then
  printf '检测到已安装的旧版本，正在替换...\n'
  mv "$DEST_APP" "$BACKUP_APP"
fi

mv "$STAGED_APP" "$DEST_APP"
rm -rf "$BACKUP_APP"
trap - ERR

printf '正在解除下载隔离标签...\n'
xattr -dr com.apple.quarantine "$DEST_APP" 2>/dev/null || true

printf '\n===============================================\n'
printf '   安装完成！\n'
printf '===============================================\n\n'
printf '已安装至：%s\n' "$DEST_APP"
printf '可在「应用程序」文件夹或启动台中找到 %s。\n\n' "$APP_NAME"
printf '正在为您打开应用...\n'
open "$DEST_APP" || true

sleep 2
exit 0
INSTALL_SH
chmod 755 "$install_script"
ln -s /Applications "$staging/Applications"
phase_end

phase_start "create dmg"
hdiutil create -volname "$app_name" -srcfolder "$staging" -ov -format UDZO "$dmg_path"
rm -rf "$staging"
phase_end

if [[ "$release_profile" == "gray" ]]; then
  phase_start "notarize dmg"
  xcrun notarytool submit "$dmg_path" --keychain-profile "$NOTARY_PROFILE" --wait
  xcrun stapler staple "$dmg_path"
  spctl -a -t open --context context:primary-signature -v "$dmg_path"
  phase_end
fi

write_dmg_checksum "$dmg_path"

echo "macOS package ready: $dmg_path"
