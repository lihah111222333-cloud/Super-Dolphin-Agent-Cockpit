#!/usr/bin/env bash

# The hook and CI entrypoints share this fail-closed launcher contract. The
# launcher is provisioned into the repository's local Git config by install-hooks.

validate_trusted_gate_launcher() {
  local launcher=${1:-}
  local owner mode

  if [[ -z "$launcher" || "$launcher" != /* ]]; then
    printf '%s\n' 'super-dolphin gate blocked: configured launcher must be an absolute path.' >&2
    return 1
  fi
  if [[ ! -f "$launcher" || ! -x "$launcher" ]]; then
    printf 'super-dolphin gate blocked: configured launcher is not an executable regular file: %s\n' "$launcher" >&2
    return 1
  fi

  if [[ "$(uname -s)" == "Darwin" ]]; then
    owner=$(stat -f '%u' "$launcher") || return 1
    mode=$(stat -f '%Lp' "$launcher") || return 1
  else
    owner=$(stat -c '%u' "$launcher") || return 1
    mode=$(stat -c '%a' "$launcher") || return 1
  fi
  if [[ "$owner" != "$(id -u)" ]]; then
    printf 'super-dolphin gate blocked: launcher owner %s does not match current user.\n' "$owner" >&2
    return 1
  fi
  if (( (8#$mode & 8#022) != 0 )); then
    printf 'super-dolphin gate blocked: launcher permissions %s permit group or world writes.\n' "$mode" >&2
    return 1
  fi
}

trusted_gate_launcher() {
  local repo_root=${1:?repository root is required}
  local launcher

  launcher=$(git -C "$repo_root" config --local --get superdolphin.gateLauncher 2>/dev/null || true)
  if [[ -z "$launcher" ]]; then
    printf '%s\n' 'super-dolphin gate blocked: no trusted launcher is provisioned; run SUPER_DOLPHIN_GATE_LAUNCHER=/absolute/path make install-hooks.' >&2
    return 1
  fi
  validate_trusted_gate_launcher "$launcher" || return 1
  printf '%s\n' "$launcher"
}
