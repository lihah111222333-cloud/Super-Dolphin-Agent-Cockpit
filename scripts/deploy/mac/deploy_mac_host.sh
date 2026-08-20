#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  cat >&2 <<'EOF'
usage: deploy_mac_host.sh --yes --repo PATH --command-file PATH --install-dir PATH --runner-id ID --mode native|docker [--docker-image REPOSITORY@sha256:DIGEST]
EOF
}

die() { printf 'deploy_mac_host: %s\n' "$*" >&2; exit 2; }
validate_absolute_clean_path() {
  local kind=$1 value=$2 remainder component current=/
  [[ -n "$value" ]] || die "$kind path is required"
  [[ "$value" = /* ]] || die "$kind path must be absolute"
  [[ ! "$value" =~ [[:cntrl:]] ]] || die "$kind path contains control characters"
  case "$value" in
    *'//'*) die "$kind path must be clean" ;;
    */./*|*/.|*/../*|*/..) die "$kind path must be clean" ;;
    */) [[ "$value" == / ]] || die "$kind path must be clean" ;;
  esac
  remainder=${value#/}
  while [[ -n "$remainder" ]]; do
    if [[ "$remainder" == */* ]]; then
      component=${remainder%%/*}
      remainder=${remainder#*/}
    else
      component=$remainder
      remainder=''
    fi
    [[ -n "$component" ]] || die "$kind path must be clean"
    if [[ "$current" == / ]]; then
      current=/$component
    else
      current=$current/$component
    fi
    [[ ! -L "$current" ]] || die "$kind path contains a symlink"
  done
}

repo_path=''
install_dir=''
runner_id=''
command_file=''
mode=''
docker_image=''
confirmed=0
repo_seen=0
install_seen=0
runner_seen=0
command_seen=0
mode_seen=0
docker_image_seen=0

while (($#)); do
  case "$1" in
    --yes|-y)
      confirmed=1
      shift
      ;;
    --repo)
      (($# >= 2)) || die '--repo requires a value'
      ((repo_seen == 0)) || die '--repo must be provided once'
      repo_seen=1
      repo_path=$2
      shift 2
      ;;
    --install-dir)
      (($# >= 2)) || die '--install-dir requires a value'
      ((install_seen == 0)) || die '--install-dir must be provided once'
      install_seen=1
      install_dir=$2
      shift 2
      ;;
    --runner-id)
      (($# >= 2)) || die '--runner-id requires a value'
      ((runner_seen == 0)) || die '--runner-id must be provided once'
      runner_seen=1
      runner_id=$2
      shift 2
      ;;
    --command-file)
      (($# >= 2)) || die '--command-file requires a value'
      ((command_seen == 0)) || die '--command-file must be provided once'
      command_seen=1
      command_file=$2
      shift 2
      ;;
    --mode)
      (($# >= 2)) || die '--mode requires a value'
      ((mode_seen == 0)) || die '--mode must be provided once'
      mode_seen=1
      mode=$2
      shift 2
      ;;
    --docker-image)
      (($# >= 2)) || die '--docker-image requires a value'
      ((docker_image_seen == 0)) || die '--docker-image must be provided once'
      docker_image_seen=1
      docker_image=$2
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage
      die "unknown option: $1"
      ;;
  esac
done

((confirmed == 1)) || die 'deployment is non-interactive; pass --yes'
[[ -n "$repo_path" ]] || die '--repo is required'
[[ -n "$install_dir" ]] || die '--install-dir is required'
[[ -n "$runner_id" ]] || die '--runner-id is required'
[[ -n "$command_file" ]] || die '--command-file is required'
[[ -n "$mode" ]] || die '--mode is required'
validate_absolute_clean_path '--repo' "$repo_path"
validate_absolute_clean_path '--install-dir' "$install_dir"
validate_absolute_clean_path '--command-file' "$command_file"
home_path=${HOME-}
validate_absolute_clean_path 'HOME' "$home_path"
[[ -d "$repo_path" ]] || die "repository does not exist: $repo_path"
[[ -d "$home_path" ]] || die 'HOME must name an existing directory'
[[ ! -L "$command_file" && -f "$command_file" && -r "$command_file" ]] || die 'an absolute readable regular --command-file is required'
[[ "$runner_id" =~ ^[A-Za-z0-9._-]+$ ]] || die '--runner-id contains unsupported characters'
[[ "$mode" == native || "$mode" == docker ]] || die '--mode must be native or docker'
if [[ "$mode" == docker ]]; then
  ((docker_image_seen == 1)) || die '--docker-image is required for docker mode'
  [[ "$docker_image" =~ ^super-dolphin-ci-runner:darwin-compat@sha256:[a-f0-9]{64}$ ]] || die '--docker-image must be super-dolphin-ci-runner:darwin-compat@sha256:<64 lowercase hex>'
else
  ((docker_image_seen == 0)) || die '--docker-image is forbidden in native mode'
fi
[[ "$(uname -s)" == Darwin ]] || die 'this installer only runs on macOS'
command -v sysctl >/dev/null 2>&1 || die 'sysctl is required'
command -v git >/dev/null 2>&1 || die 'git is required'

logical_cpu=$(sysctl -n hw.logicalcpu 2>/dev/null || true)
memory_bytes=$(sysctl -n hw.memsize 2>/dev/null || true)
[[ "$logical_cpu" =~ ^[1-9][0-9]*$ ]] || die 'unable to read positive logical CPU count'
[[ "$memory_bytes" =~ ^[1-9][0-9]*$ ]] || die 'unable to read positive memory size'
git -C "$repo_path" rev-parse --show-toplevel >/dev/null 2>&1 || die 'repo is not a Git worktree'

script_dir=$(cd -- "$(dirname -- "$0")" && pwd -P)
daemon_source="$script_dir/mac_runner_daemon.sh"
[[ -r "$daemon_source" ]] || die "runner daemon is missing: $daemon_source"
mkdir -p "$install_dir"
validate_absolute_clean_path '--install-dir' "$install_dir"
[[ -d "$install_dir" ]] || die '--install-dir must name a directory'
daemon_target="$install_dir/mac_runner_daemon.sh"
command_target="$install_dir/runner-command.sh"
validate_absolute_clean_path 'daemon target' "$daemon_target"
validate_absolute_clean_path 'command target' "$command_target"
install -m 0755 "$daemon_source" "$daemon_target"
install -m 0755 "$command_file" "$command_target"

xml_escape() {
  local value=$1
  value=${value//&/&amp;}
  value=${value//</&lt;}
  value=${value//>/&gt;}
  value=${value//\"/&quot;}
  value=${value//\'/&apos;}
  printf '%s' "$value"
}

plist_daemon=$(xml_escape "$install_dir/mac_runner_daemon.sh")
plist_repo=$(xml_escape "$repo_path")
plist_runner=$(xml_escape "$runner_id")
plist_command=$(xml_escape "$install_dir/runner-command.sh")
plist_mode=$(xml_escape "$mode")
plist_docker_image_arg=''
if [[ "$mode" == docker ]]; then
  plist_docker_image=$(xml_escape "$docker_image")
  plist_docker_image_arg=$(printf '    <string>--docker-image</string><string>%s</string>' "$plist_docker_image")
fi

plist_dir="$home_path/Library/LaunchAgents"
validate_absolute_clean_path 'LaunchAgents' "$plist_dir"
mkdir -p "$plist_dir"
validate_absolute_clean_path 'LaunchAgents' "$plist_dir"
[[ -d "$plist_dir" ]] || die 'LaunchAgents directory was not created'
plist_path="$plist_dir/com.super-dolphin.heteroci.$runner_id.plist"
validate_absolute_clean_path 'LaunchAgent plist' "$plist_path"
[[ ! -L "$plist_path" ]] || die 'LaunchAgent plist must not be a symlink'
cat >"$plist_path" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>com.super-dolphin.heteroci.$plist_runner</string>
  <key>EnvironmentVariables</key><dict>
    <key>LOCAL_NON_AUTHORITATIVE</key><string>1</string>
    <key>NOT_VERIFIED</key><string>1</string>
  </dict>
  <key>ProgramArguments</key><array>
    <string>$plist_daemon</string>
    <string>--repo</string><string>$plist_repo</string>
    <string>--runner-id</string><string>$plist_runner</string>
    <string>--mode</string><string>$plist_mode</string>
${plist_docker_image_arg}
    <string>--</string><string>/bin/bash</string><string>$plist_command</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><false/>
  <key>ProcessType</key><string>Background</string>
</dict></plist>
EOF

printf 'runner_id=%s mode=%s repo=%s logical_cpu=%s memory_bytes=%s daemon=%s\n' \
  "$runner_id" "$mode" "$repo_path" "$logical_cpu" "$memory_bytes" "$install_dir/mac_runner_daemon.sh"
