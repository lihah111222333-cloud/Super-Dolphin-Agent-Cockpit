#!/usr/bin/env bash

# The hook and CI entrypoints share this fail-closed launcher contract. The
# launcher path may be selected by repository-local config, but the install
# root is always derived from the operating-system account and cannot be
# redirected by Git config.

trusted_launcher_stat() {
  local format=$1 target_path=$2
  if [[ "$(uname -s)" == "Darwin" ]]; then
    stat -f "$format" "$target_path"
  else
    case "$format" in
      '%u') stat -c '%u' "$target_path" ;;
      '%Lp') stat -c '%a' "$target_path" ;;
      *) return 2 ;;
    esac
  fi
}

trusted_launcher_sha256() {
  local target_path=${1:?path is required}
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$target_path" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$target_path" | awk '{print $1}'
  else
    printf '%s\n' 'super-dolphin gate blocked: system SHA-256 utility is unavailable.' >&2
    return 1
  fi
}

trusted_launcher_root() {
  local repo_root=${1:?repository root is required} root physical_root os_home
  os_home=$(trusted_launcher_os_home) || return 1
  root="$os_home/.super-dolphin-gate-launchers"
  [[ -d "$root" ]] || {
    printf '%s\n' 'super-dolphin gate blocked: no canonical launcher install root is provisioned; run make install-hooks.' >&2
    return 1
  }
  physical_root=$(cd "$root" && pwd -P) || return 1
  if [[ "$physical_root" != "$root" ]]; then
    printf '%s\n' 'super-dolphin gate blocked: configured launcher install root is not canonical.' >&2
    return 1
  fi
  printf '%s\n' "$root"
}

trusted_launcher_os_home() {
  local uid username home physical_home
  uid=$(id -u) || return 1
  if [[ "$(uname -s)" == "Darwin" ]]; then
    username=$(id -un) || return 1
    home=$(dscl . -read "/Users/$username" NFSHomeDirectory 2>/dev/null | awk '$1 ~ /^NFSHomeDirectory/ { print $2; exit }')
  elif command -v getent >/dev/null 2>&1; then
    home=$(getent passwd "$uid" | awk -F: 'NF >= 6 { print $6; exit }')
  else
    home=
  fi
  if [[ -z "$home" || "$home" != /* || ! -d "$home" ]]; then
    printf '%s\n' 'super-dolphin gate blocked: operating-system home directory is unavailable.' >&2
    return 1
  fi
  physical_home=$(cd "$home" && pwd -P) || return 1
  if [[ "$physical_home" != "$home" ]]; then
    printf '%s\n' 'super-dolphin gate blocked: operating-system home directory is not canonical.' >&2
    return 1
  fi
  printf '%s\n' "$home"
}

validate_trusted_launcher_directory() {
  local directory_path=${1:?directory is required} owner mode
  [[ -d "$directory_path" && ! -L "$directory_path" ]] || return 1
  owner=$(trusted_launcher_stat '%u' "$directory_path") || return 1
  mode=$(trusted_launcher_stat '%Lp' "$directory_path") || return 1
  [[ "$owner" == "$(id -u)" && $((8#$mode & 8#022)) -eq 0 ]]
}

validate_trusted_launcher_root() {
  local repo_root=${1:?repository root is required} root
  root=$(trusted_launcher_root "$repo_root") || return 1
  validate_trusted_launcher_root_path "$root"
}

validate_trusted_launcher_root_path() {
  local root=${1:?launcher root is required} current owner mode
  validate_trusted_launcher_directory "$root" || {
    printf '%s\n' 'super-dolphin gate blocked: launcher install root is not a current-user non-writable directory.' >&2
    return 1
  }
  current=$root
  while [[ "$current" != / ]]; do
    current=$(dirname "$current")
    [[ ! -L "$current" ]] || {
      printf 'super-dolphin gate blocked: launcher install ancestor is a symlink: %s\n' "$current" >&2
      return 1
    }
    [[ -d "$current" ]] || {
      printf 'super-dolphin gate blocked: launcher install ancestor is not a directory: %s\n' "$current" >&2
      return 1
    }
    owner=$(trusted_launcher_stat '%u' "$current") || return 1
    if [[ "$owner" != "$(id -u)" && "$owner" != 0 ]]; then
      printf 'super-dolphin gate blocked: launcher install ancestor has unsafe owner %s: %s\n' "$owner" "$current" >&2
      return 1
    fi
    mode=$(trusted_launcher_stat '%Lp' "$current") || return 1
    if (( (8#$mode & 8#022) != 0 )); then
      printf 'super-dolphin gate blocked: launcher install ancestor permissions %s permit group or world writes: %s\n' "$mode" "$current" >&2
      return 1
    fi
  done
}

validate_trusted_gate_launcher() {
  local repo_root launcher root
  if [[ $# -eq 1 ]]; then
    repo_root=$(git rev-parse --show-toplevel) || return 1
    launcher=$1
    root=$(trusted_launcher_root "$repo_root") || return 1
  else
    repo_root=${1:?repository root is required}
    launcher=${2:-}
    root=$(trusted_launcher_root "$repo_root") || return 1
  fi
  local owner mode relative tree digest actual_digest

  if [[ -z "$launcher" || "$launcher" != /* ]]; then
    printf '%s\n' 'super-dolphin gate blocked: configured launcher must be an absolute path.' >&2
    return 1
  fi
  if [[ ! -f "$launcher" || ! -x "$launcher" ]]; then
    printf 'super-dolphin gate blocked: configured launcher is not an executable regular file: %s\n' "$launcher" >&2
    return 1
  fi

  [[ ! -L "$launcher" ]] || {
    printf '%s\n' 'super-dolphin gate blocked: launcher must not be a symlink.' >&2
    return 1
  }
  owner=$(trusted_launcher_stat '%u' "$launcher") || return 1
  mode=$(trusted_launcher_stat '%Lp' "$launcher") || return 1
  if [[ "$owner" != "$(id -u)" ]]; then
    printf 'super-dolphin gate blocked: launcher owner %s does not match current user.\n' "$owner" >&2
    return 1
  fi
  if (( (8#$mode & 8#022) != 0 )); then
    printf 'super-dolphin gate blocked: launcher permissions %s permit group or world writes.\n' "$mode" >&2
    return 1
  fi

  validate_trusted_launcher_root_path "$root" || return 1
  relative=${launcher#"$root"/}
  if [[ "$relative" == "$launcher" || ! "$relative" =~ ^v1/([0-9a-f]{40}|[0-9a-f]{64})/([0-9a-f]{64})/super-dolphin-gate$ ]]; then
    printf '%s\n' 'super-dolphin gate blocked: launcher path is not rooted in the canonical content-addressed install tree.' >&2
    return 1
  fi
  tree=$(basename "$(dirname "$(dirname "$launcher")")")
  digest=$(basename "$(dirname "$launcher")")
  [[ "$(dirname "$(dirname "$(dirname "$(dirname "$launcher")")")")" == "$root" ]] || {
    printf '%s\n' 'super-dolphin gate blocked: launcher path escapes the canonical install root.' >&2
    return 1
  }
  if ! validate_trusted_launcher_directory "$root/v1" \
    || ! validate_trusted_launcher_directory "$root/v1/$tree" \
    || ! validate_trusted_launcher_directory "$root/v1/$tree/$digest"; then
    printf '%s\n' 'super-dolphin gate blocked: launcher install path has unsafe ownership, mode, or symlink.' >&2
    return 1
  fi
  actual_digest=$(trusted_launcher_sha256 "$launcher") || return 1
  if [[ "$actual_digest" != "$digest" ]]; then
    printf '%s\n' 'super-dolphin gate blocked: launcher binary digest does not match its content-addressed directory.' >&2
    return 1
  fi
}

trusted_gate_launcher() {
  local repo_root=${1:?repository root is required}
  local launcher tree

  launcher=$(git -C "$repo_root" config --local --get superdolphin.gateLauncher 2>/dev/null || true)
  if [[ -z "$launcher" ]]; then
    printf '%s\n' 'super-dolphin gate blocked: no trusted launcher is provisioned; run make install-hooks.' >&2
    return 1
  fi
  validate_trusted_gate_launcher "$repo_root" "$launcher" || return 1
  tree=$(basename "$(dirname "$(dirname "$launcher")")")
  verify_trusted_gate_launcher_tree "$repo_root" "$launcher" "$tree" || return 1
  printf '%s\n' "$launcher"
}

trusted_gate_launcher_for_tree() {
  local repo_root=${1:?repository root is required}
  local tree=${2:?tree is required}
  local install_root configured candidate

  if [[ ! "$tree" =~ ^([0-9a-f]{40}|[0-9a-f]{64})$ ]]; then
    printf '%s\n' 'super-dolphin gate blocked: exact launcher tree is invalid.' >&2
    return 1
  fi
  install_root=$(trusted_launcher_root "$repo_root") || return 1
  validate_trusted_launcher_root "$repo_root" || return 1
  configured=$(git -C "$repo_root" config --local --get superdolphin.gateLauncher 2>/dev/null || true)
  if [[ -n "$configured" ]] \
    && validate_trusted_gate_launcher "$repo_root" "$configured" \
    && verify_trusted_gate_launcher_tree "$repo_root" "$configured" "$tree"; then
    printf '%s\n' "$configured"
    return 0
  fi
  for candidate in "$install_root/v1"/*/*/super-dolphin-gate; do
    if [[ ! -e "$candidate" ]]; then
      continue
    fi
    if [[ "$candidate" == "$configured" ]]; then
      continue
    fi
    if validate_trusted_gate_launcher "$repo_root" "$candidate" \
      && verify_trusted_gate_launcher_tree "$repo_root" "$candidate" "$tree"; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  printf 'super-dolphin gate blocked: no verified launcher version matches tree %s; run make install-hooks.\n' "$tree" >&2
  return 1
}

verify_trusted_gate_launcher_tree() {
  local repo_root=${1:?repository root is required}
  local launcher=${2:?launcher is required}
  local tree=${3:?tree is required}
  local receipt

  receipt=$(dirname "$launcher")/receipt.json
  "$launcher" launcher verify \
    --repository "$repo_root" \
    --tree "$tree" \
    --receipt "$receipt"
}
