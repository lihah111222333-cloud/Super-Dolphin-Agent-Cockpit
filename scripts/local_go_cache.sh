#!/usr/bin/env bash

case "$(uname -s 2>/dev/null || true)" in
  MINGW*|MSYS*|CYGWIN*)
    # shellcheck disable=SC1091
    source "${BASH_SOURCE[0]%/*}/platform/windows/local_go_cache_path.sh"
    ;;
  *)
    # shellcheck disable=SC1091
    source "${BASH_SOURCE[0]%/*}/platform/posix/local_go_cache_path.sh"
    ;;
esac

local_go_cache_digest_file() {
  local file_path="$1"
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file_path" | awk '{print $1}'
    return
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file_path" | awk '{print $1}'
    return
  fi
  echo "SHA-256 tool is unavailable" >&2
  return 1
}

local_go_cache_resolve_binary() {
  local name="$1" resolved target directory
  resolved="$(command -v "$name" 2>/dev/null || true)"
  if [[ -z "$resolved" || ! -f "$resolved" ]]; then
    echo "local Go cache cannot resolve compiler binary: $name" >&2
    return 1
  fi
  directory="${resolved%/*}"
  [[ "$directory" != "$resolved" ]] || directory="."
  resolved="$(cd -P -- "$directory" && printf '%s/%s\n' "$PWD" "${resolved##*/}")"
  while [[ -L "$resolved" ]]; do
    target="$(/usr/bin/readlink "$resolved")" || return 1
    if [[ "$target" == /* ]]; then
      resolved="$target"
    else
      resolved="${resolved%/*}/$target"
    fi
    directory="${resolved%/*}"
    resolved="$(cd -P -- "$directory" && printf '%s/%s\n' "$PWD" "${resolved##*/}")"
  done
  if [[ ! -f "$resolved" || -L "$resolved" ]]; then
    echo "local Go cache cannot canonicalize compiler binary: $name" >&2
    return 1
  fi
  printf '%s\n' "$resolved"
}

local_go_cache_write_file_identity() {
  local label="$1" file_path="$2" identity_file="$3" digest
  if [[ ! -f "$file_path" || -L "$file_path" ]]; then
    echo "local Go cache identity requires a regular non-symlink file: $file_path" >&2
    return 1
  fi
  digest="$(local_go_cache_digest_file "$file_path")"
  printf '%s=%s\n' "$label" "$digest" >>"$identity_file"
}

local_go_cache_write_command_identity() {
  local label="$1" identity_file="$2"; shift 2
  local output_file digest
  output_file="$(mktemp "${TMPDIR:-/tmp}/local-go-cache-command.XXXXXX")"
  if ! "$@" >"$output_file" 2>&1; then
    rm -f -- "$output_file"
    echo "local Go cache cannot read $label identity" >&2
    return 1
  fi
  digest="$(local_go_cache_digest_file "$output_file")"
  rm -f -- "$output_file"
  printf '%s=%s\n' "$label" "$digest" >>"$identity_file"
}

local_go_cache_write_tool_identity() {
  local tool_dir="$1" identity_file="$2" tool_name
  for tool_name in asm cgo compile link; do
    local_go_cache_write_file_identity "tool:$tool_name" "$tool_dir/$tool_name" "$identity_file"
  done
}

local_go_cache_write_c_identity() {
  local real_go="$1" identity_file="$2" cgo_enabled cc cxx cc_path cxx_path
  cgo_enabled="$(GOTOOLCHAIN=local "$real_go" env CGO_ENABLED)"
  printf 'CGO_ENABLED=%s\n' "$cgo_enabled" >>"$identity_file"
  if [[ "$cgo_enabled" == "0" ]]; then
    printf '%s\n' 'C_TOOLCHAIN=not_applicable' >>"$identity_file"
    return
  fi
  if [[ "$cgo_enabled" != "1" ]]; then
    echo "local Go cache identity rejects CGO_ENABLED=$cgo_enabled" >&2
    return 1
  fi
  cc="$(GOTOOLCHAIN=local "$real_go" env CC)"
  cxx="$(GOTOOLCHAIN=local "$real_go" env CXX)"
  cc_path="$(local_go_cache_resolve_binary "$cc")"
  cxx_path="$(local_go_cache_resolve_binary "$cxx")"
  local_go_cache_write_file_identity CC "$cc_path" "$identity_file" || return 1
  local_go_cache_write_file_identity CXX "$cxx_path" "$identity_file" || return 1
  local_go_cache_write_command_identity CC_VERSION "$identity_file" "$cc_path" --version || return 1
  local_go_cache_write_command_identity CC_TARGET "$identity_file" "$cc_path" -dumpmachine || return 1
  if command -v xcrun >/dev/null 2>&1; then
    local_go_cache_write_command_identity APPLE_SDK_PATH "$identity_file" xcrun --show-sdk-path || return 1
    local_go_cache_write_command_identity APPLE_SDK_VERSION "$identity_file" xcrun --show-sdk-version || return 1
  else
    local_go_cache_write_command_identity CC_SYSROOT "$identity_file" "$cc_path" -print-sysroot || return 1
  fi
}

local_go_cache_identity() {
  local repository_root="$1" real_go="$2" identity_file="$3" tool_dir key value
  : >"$identity_file"
  chmod 600 "$identity_file"
  for key in GOVERSION GOOS GOARCH GOAMD64 GOARM64 GOARM GOEXPERIMENT GOTOOLCHAIN; do
    value="$(GOTOOLCHAIN=local "$real_go" env "$key")"
    printf '%s=%s\n' "$key" "$value" >>"$identity_file"
  done
  printf 'GOFLAGS=%s\n' "${GOFLAGS:-}" >>"$identity_file"
  local_go_cache_write_file_identity go "$real_go" "$identity_file"
  tool_dir="$(GOTOOLCHAIN=local "$real_go" env GOTOOLDIR)"
  tool_dir="$(local_go_cache_platform_tool_dir "$tool_dir")" || return 1
  if [[ "$tool_dir" != /* || ! -d "$tool_dir" || -L "$tool_dir" ]]; then
    echo "local Go cache identity requires a canonical absolute GOTOOLDIR" >&2
    return 1
  fi
  local_go_cache_write_tool_identity "$tool_dir" "$identity_file"
  local_go_cache_write_c_identity "$real_go" "$identity_file"
}

local_go_cache_hex() {
  LC_ALL=C od -An -tx1 | tr -d ' \n'
}

local_go_cache_write_state() {
  local cache_base="$1" cache_root="$2" identity="$3" state_dir state_file temporary path_hex updated_at
  state_dir="$cache_base/state"
  state_file="$state_dir/$identity.json"
  mkdir -p "$state_dir"
  chmod 700 "$state_dir"
  path_hex="$(printf '%s' "$cache_root" | local_go_cache_hex)"
  updated_at="$(date +%s)"
  if [[ ! "$path_hex" =~ ^[0-9a-f]+$ || ! "$updated_at" =~ ^[0-9]+$ ]]; then
    echo "local Go cache state identity is invalid" >&2
    return 1
  fi
  temporary="$(mktemp "$state_dir/.state.XXXXXX")"
  printf '{"schema_version":"local-go-cache-state/v1","identity_sha256":"%s","cache_path_hex":"%s","updated_at_unix_sec":%s}\n' \
    "$identity" "$path_hex" "$updated_at" >"$temporary"
  chmod 600 "$temporary"
  mv -f -- "$temporary" "$state_file"
}

local_go_cache_prepare() {
  local repository_root="$1" real_go="$2" common_git_directory cache_base identity_file identity cache_root temp_root
  common_git_directory="$(git -C "$repository_root" rev-parse --path-format=absolute --git-common-dir)"
  cache_base="$(dirname "$common_git_directory")/.build-cache/local-go-cache-v1"
  umask 077
  mkdir -p "$cache_base"
  chmod 700 "$cache_base"
  identity_file="$(mktemp "$cache_base/.identity.XXXXXX")"
  if ! local_go_cache_identity "$repository_root" "$real_go" "$identity_file"; then
    rm -f -- "$identity_file"
    return 1
  fi
  identity="$(local_go_cache_digest_file "$identity_file")"
  rm -f -- "$identity_file"
  cache_root="$cache_base/objects/$identity"
  mkdir -p "$cache_root"
  chmod 700 "$cache_base/objects" "$cache_root"
  local_go_cache_write_state "$cache_base" "$cache_root" "$identity"
  temp_root="$(mktemp -d "$cache_base/tmp.XXXXXX")"
  chmod 700 "$temp_root"
  printf '%s\n%s\n%s\n' "$cache_root" "$temp_root" "$identity"
}

local_go_cache_cleanup_temp() {
  local temp_root="$1" parent base
  parent="$(dirname "$temp_root")"
  base="$(basename "$temp_root")"
  if [[ "$(basename "$parent")" != "local-go-cache-v1" || "$base" != tmp.* || ! -d "$temp_root" || -L "$temp_root" ]]; then
    echo "refusing to clean untrusted local Go temporary directory: $temp_root" >&2
    return 1
  fi
  chmod -R u+w "$temp_root"
  rm -rf -- "$temp_root"
}
