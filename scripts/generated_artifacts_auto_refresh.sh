#!/usr/bin/env bash
set -euo pipefail

label="com.super-agent-v3.generated-artifacts-refresh"
interval_seconds=300
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
super_repo="$(cd "${script_dir}/.." && pwd -P)"
script_path="${script_dir}/$(basename "${BASH_SOURCE[0]}")"
wjboot_repo=""
lock_dir=""
lock_held=0

usage() {
  printf '%s\n' \
    "Usage:" \
    "  scripts/generated_artifacts_auto_refresh.sh install --wjboot-repo PATH" \
    "  scripts/generated_artifacts_auto_refresh.sh uninstall" \
    "  scripts/generated_artifacts_auto_refresh.sh status" \
    "  scripts/generated_artifacts_auto_refresh.sh run-once --wjboot-repo PATH"
}

fail() {
  echo "generated-artifacts-auto-refresh: $*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

canonical_dir() {
  local path=$1
  [ -d "$path" ] || fail "directory not found: $path"
  (cd "$path" && pwd -P)
}

require_wjboot_repo() {
  [ -n "$wjboot_repo" ] || fail "--wjboot-repo is required"
  wjboot_repo="$(canonical_dir "$wjboot_repo")"
  [ -f "${wjboot_repo}/scripts/refresh_generated_artifacts.sh" ] ||
    fail "wjboot-v2 refresh entrypoint not found: ${wjboot_repo}/scripts/refresh_generated_artifacts.sh"
}

release_lock() {
  if [ "$lock_held" -eq 1 ] && [ -n "$lock_dir" ]; then
    rm -rf "$lock_dir"
    lock_held=0
  fi
}

acquire_lock() {
  local git_common_dir existing_pid
  git_common_dir="$(git -C "$super_repo" rev-parse --path-format=absolute --git-common-dir)" ||
    fail "cannot resolve super-agent-v3 Git common directory"
  lock_dir="${git_common_dir}/generated-artifacts-auto-refresh.lock"

  if ! mkdir "$lock_dir" 2>/dev/null; then
    existing_pid=""
    if [ -f "${lock_dir}/pid" ]; then
      existing_pid="$(tr -d '[:space:]' <"${lock_dir}/pid")"
    fi
    if [[ "$existing_pid" =~ ^[0-9]+$ ]] && kill -0 "$existing_pid" 2>/dev/null; then
      fail "refresh is already running with pid ${existing_pid}"
    fi
    rm -rf "$lock_dir"
    mkdir "$lock_dir" || fail "cannot replace stale lock: $lock_dir"
  fi

  printf '%s\n' "$$" >"${lock_dir}/pid"
  lock_held=1
  trap release_lock EXIT
  trap 'release_lock; exit 129' HUP
  trap 'release_lock; exit 130' INT
  trap 'release_lock; exit 143' TERM
}

run_once() {
  local super_refresh wjboot_refresh
  require_command git
  require_wjboot_repo
  super_refresh="${super_repo}/scripts/refresh_generated_artifacts.sh"
  wjboot_refresh="${wjboot_repo}/scripts/refresh_generated_artifacts.sh"
  [ -f "$super_refresh" ] || fail "super-agent-v3 refresh entrypoint not found: $super_refresh"

  acquire_lock
  echo "[auto-refresh] refresh super-agent-v3 capability manifest"
  bash "$super_refresh" capcontract --repo "$super_repo"
  echo "[auto-refresh] refresh wjboot-v2 AI project map"
  bash "$wjboot_refresh" project-map --repo "$wjboot_repo"
  echo "[auto-refresh] refresh completed"
}

xml_escape() {
  printf '%s' "$1" | sed \
    -e 's/&/\&amp;/g' \
    -e 's/</\&lt;/g' \
    -e 's/>/\&gt;/g' \
    -e 's/"/\&quot;/g' \
    -e "s/'/\&apos;/g"
}

launch_context() {
  [ -n "${HOME:-}" ] || fail "HOME is required"
  require_command launchctl
  require_command id
  user_uid="$(id -u)"
  launch_domain="gui/${user_uid}"
  launch_service="${launch_domain}/${label}"
  launch_agents_dir="${HOME}/Library/LaunchAgents"
  log_dir="${HOME}/Library/Logs/super-agent-v3"
  plist_path="${launch_agents_dir}/${label}.plist"
  stdout_path="${log_dir}/generated-artifacts-refresh.log"
  stderr_path="${log_dir}/generated-artifacts-refresh.error.log"
}

is_loaded() {
  launchctl print "$launch_service" >/dev/null 2>&1
}

write_plist() {
  local temporary_plist escaped_label escaped_bash escaped_script escaped_wjboot
  local escaped_super escaped_stdout escaped_stderr escaped_path escaped_home
  temporary_plist="${plist_path}.tmp.$$"
  escaped_label="$(xml_escape "$label")"
  escaped_bash="$(xml_escape "$(command -v bash)")"
  escaped_script="$(xml_escape "$script_path")"
  escaped_wjboot="$(xml_escape "$wjboot_repo")"
  escaped_super="$(xml_escape "$super_repo")"
  escaped_stdout="$(xml_escape "$stdout_path")"
  escaped_stderr="$(xml_escape "$stderr_path")"
  escaped_path="$(xml_escape "$PATH")"
  escaped_home="$(xml_escape "$HOME")"

  {
    printf '%s\n' '<?xml version="1.0" encoding="UTF-8"?>'
    printf '%s\n' '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">'
    printf '%s\n' '<plist version="1.0">' '<dict>'
    printf '  <key>Label</key>\n  <string>%s</string>\n' "$escaped_label"
    printf '%s\n' '  <key>ProgramArguments</key>' '  <array>'
    printf '    <string>%s</string>\n' "$escaped_bash" "$escaped_script"
    printf '%s\n' '    <string>run-once</string>' '    <string>--wjboot-repo</string>'
    printf '    <string>%s</string>\n' "$escaped_wjboot"
    printf '%s\n' '  </array>'
    printf '  <key>WorkingDirectory</key>\n  <string>%s</string>\n' "$escaped_super"
    printf '%s\n' '  <key>EnvironmentVariables</key>' '  <dict>'
    printf '    <key>HOME</key>\n    <string>%s</string>\n' "$escaped_home"
    printf '    <key>PATH</key>\n    <string>%s</string>\n' "$escaped_path"
    printf '%s\n' '  </dict>'
    printf '  <key>StartInterval</key>\n  <integer>%s</integer>\n' "$interval_seconds"
    printf '%s\n' '  <key>RunAtLoad</key>' '  <true/>'
    printf '  <key>StandardOutPath</key>\n  <string>%s</string>\n' "$escaped_stdout"
    printf '  <key>StandardErrorPath</key>\n  <string>%s</string>\n' "$escaped_stderr"
    printf '%s\n' '</dict>' '</plist>'
  } >"$temporary_plist"
  chmod 0644 "$temporary_plist"
  if command -v plutil >/dev/null 2>&1; then
    plutil -lint "$temporary_plist" >/dev/null
  fi
  mv "$temporary_plist" "$plist_path"
}

install_agent() {
  require_wjboot_repo
  for command_name in bash git go make node rg; do
    require_command "$command_name"
  done
  launch_context
  mkdir -p "$launch_agents_dir" "$log_dir"

  if is_loaded; then
    launchctl bootout "$launch_service"
  fi
  write_plist
  launchctl bootstrap "$launch_domain" "$plist_path"
  launchctl kickstart -k "$launch_service"
  echo "installed ${label} (${interval_seconds}s interval)"
  echo "plist: ${plist_path}"
  echo "logs: ${stdout_path} / ${stderr_path}"
}

status_agent() {
  launch_context
  echo "plist: ${plist_path}"
  echo "logs: ${stdout_path} / ${stderr_path}"
  if ! launchctl print "$launch_service"; then
    fail "LaunchAgent is not loaded: $launch_service"
  fi
}

uninstall_agent() {
  launch_context
  if is_loaded; then
    launchctl bootout "$launch_service"
  fi
  rm -f "$plist_path"
  echo "uninstalled ${label}; logs retained in ${log_dir}"
}

command_name="${1:-}"
if [ -z "$command_name" ]; then
  usage >&2
  exit 2
fi
shift

while [ $# -gt 0 ]; do
  case "$1" in
    --wjboot-repo)
      [ $# -ge 2 ] && [ -n "$2" ] || fail "--wjboot-repo requires a path"
      wjboot_repo=$2
      shift 2
      ;;
    -h|--help|help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

case "$command_name" in
  install) install_agent ;;
  uninstall) uninstall_agent ;;
  status) status_agent ;;
  run-once) run_once ;;
  -h|--help|help) usage ;;
  *)
    echo "unknown command: $command_name" >&2
    usage >&2
    exit 2
    ;;
esac
