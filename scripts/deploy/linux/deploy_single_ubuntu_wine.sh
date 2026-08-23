#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  cat >&2 <<'EOF'
usage: deploy_single_ubuntu_wine.sh --host SSH_ALIAS --ssh-executable ABS --scp-executable ABS --ssh-connect-timeout-seconds 1..300 --ssh-control-persist-seconds CONNECT..3600 --known-hosts-file ABS --node-id ID --machine-id-sha256 SHA256 --remote-docker-host unix://ABS --docker-server-id ID --remote-docker-executable ABS --object-store-root ABS --worktree-root ABS --cache-root ABS --private-overlay-root ABS --temp-root ABS --wine-prefix-root ABS --install-root ABS --log-dir ABS --yes
EOF
}

die() {
  printf 'deploy_single_ubuntu_wine: %s\n' "$*" >&2
  exit 2
}

validate_absolute_clean_path() {
  local label=$1 value=$2 remainder component current=/
  [[ -n "$value" && "$value" = /* ]] || die "$label must be an absolute path"
  [[ "$value" =~ ^/[A-Za-z0-9._/+:-]+$ ]] || die "$label contains unsupported JSON/path characters"
  [[ ! "$value" =~ [[:cntrl:][:space:]] ]] || die "$label must not contain whitespace or control characters"
  [[ "$value" != / ]] || die "$label must not be /"
  case "$value" in
    *'//'*) die "$label must be clean" ;;
    */./*|*/.|*/../*|*/..) die "$label must be clean" ;;
    */) die "$label must not have a trailing slash" ;;
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
    [[ -n "$component" ]] || die "$label contains an empty path component"
    if [[ "$current" == / ]]; then
      current=/$component
    else
      current=$current/$component
    fi
    [[ ! -L "$current" ]] || die "$label contains a symlink component"
  done
}

validate_remote_receipt_path() {
  local label=$1 value=$2
  [[ -n "$value" && "$value" = /* ]] || die "$label must be an absolute remote path"
  [[ "$value" =~ ^/[A-Za-z0-9._/+:-]+$ ]] || die "$label contains unsupported JSON/path characters"
  [[ ! "$value" =~ [[:cntrl:][:space:]] ]] || die "$label contains whitespace or control characters"
  [[ "$value" != / ]] || die "$label must not be /"
  case "$value" in
    *'//'*) die "$label must be clean" ;;
    */./*|*/.|*/../*|*/..) die "$label must be clean" ;;
    */) die "$label must not have a trailing slash" ;;
  esac
}

validate_executable_path() {
  local label=$1 value=$2
  validate_absolute_clean_path "$label" "$value"
  [[ -f "$value" && -x "$value" && ! -L "$value" ]] || die "$label must be a non-symlink executable file"
}

hash_tool=''
hash_tool_kind=''
select_hash_tool() {
  if hash_tool=$(command -v sha256sum 2>/dev/null); then
    hash_tool_kind=sha256sum
  elif hash_tool=$(command -v shasum 2>/dev/null); then
    hash_tool_kind=shasum
  else
    die 'sha256sum or shasum is required'
  fi
}

sha256_file() {
  local path=$1 raw
  if [[ "$hash_tool_kind" == sha256sum ]]; then
    raw=$($hash_tool "$path") || die "unable to hash $path"
  else
    raw=$($hash_tool -a 256 "$path") || die "unable to hash $path"
  fi
  raw=${raw%% *}
  [[ "$raw" =~ ^[a-f0-9]{64}$ ]] || die "invalid SHA-256 for $path"
  printf 'sha256:%s\n' "$raw"
}

sha256_text() {
  local value=$1 raw
  if [[ "$hash_tool_kind" == sha256sum ]]; then
    raw=$(printf '%s' "$value" | $hash_tool) || die 'unable to hash deployment identity'
  else
    raw=$(printf '%s' "$value" | $hash_tool -a 256) || die 'unable to hash deployment identity'
  fi
  raw=${raw%% *}
  [[ "$raw" =~ ^[a-f0-9]{64}$ ]] || die 'invalid deployment identity SHA-256'
  printf 'sha256:%s\n' "$raw"
}

run_local_logged() {
  local label=$1 log_file status pipe_statuses
  shift
  log_file="$log_prefix.$label.log"
  : >"$log_file" || die "cannot create log file: $log_file"
  printf '[%s] local step log=%s\n' "$label" "$log_file" >&2
  set +e
  "$@" 2>&1 | tee "$log_file" >&2
  pipe_statuses=("${PIPESTATUS[@]}")
  set -e
  status=${pipe_statuses[0]:-1}
  [[ "$status" == 0 && "${pipe_statuses[1]:-1}" == 0 ]] || die "$label failed; see $log_file"
}

run_scp_logged() {
  local label=$1 source=$2 destination=$3 log_file status pipe_statuses
  log_file="$log_prefix.$label.log"
  : >"$log_file" || die "cannot create log file: $log_file"
  printf '[%s] scp step log=%s\n' "$label" "$log_file" >&2
  assert_known_hosts_current
  control_master_check "$log_file" || die "$label control master check failed; see $log_file"
  set +e
  "$scp_executable" -q "${ssh_common_options[@]}" "$source" "$host:$destination" 2>&1 | tee -a "$log_file" >&2
  pipe_statuses=("${PIPESTATUS[@]}")
  set -e
  status=${pipe_statuses[0]:-1}
  [[ "$status" == 0 && "${pipe_statuses[1]:-1}" == 0 ]] || die "$label failed; see $log_file"
}

run_remote_logged() {
  local label=$1 script=$2 log_file status pipe_statuses
  shift 2
  last_remote_log="$log_prefix.$label.log"
  log_file=$last_remote_log
  : >"$log_file" || die "cannot create log file: $log_file"
  printf '[%s] remote step log=%s\n' "$label" "$log_file" >&2
  assert_known_hosts_current
  control_master_check "$log_file" || die "$label control master check failed; see $log_file"
  if [[ "$label" == install ]]; then
    last_remote_stdout="$log_prefix.$label.stdout"
    last_remote_stderr="$log_prefix.$label.stderr"
    stderr_fifo="$log_prefix.$label.stderr.fifo"
    : >"$last_remote_stdout" || die "cannot create log file: $last_remote_stdout"
    rm -f "$stderr_fifo"
    mkfifo "$stderr_fifo" || die "cannot create log fifo: $stderr_fifo"
    tee -a "$last_remote_stderr" "$log_file" <"$stderr_fifo" >&2 &
    tee_pid=$!
    set +e
    "$ssh_executable" "${ssh_common_options[@]}" "$host" bash -s -- "$@" <"$script" >"$last_remote_stdout" 2>"$stderr_fifo"
    status=$?
    set -e
    set +e
    wait "$tee_pid"; tee_status=$?
    set -e
    rm -f "$stderr_fifo"
    cat "$last_remote_stdout" >>"$log_file"
    [[ "$status" == 0 && "$tee_status" == 0 ]] || die "$label failed; see $log_file"
    return 0
  fi
  set +e
  "$ssh_executable" "${ssh_common_options[@]}" "$host" bash -s -- "$@" <"$script" 2>&1 | tee -a "$log_file" >&2
  pipe_statuses=("${PIPESTATUS[@]}")
  set -e
  status=${pipe_statuses[0]:-1}
  [[ "$status" == 0 && "${pipe_statuses[1]:-1}" == 0 ]] || die "$label failed; see $log_file"
}

best_effort_remote_cleanup() {
  local log_file status pipe_statuses
  ((remote_stage_owned == 1)) || return 0
  [[ -n "$cleanup_script" && -f "$cleanup_script" ]] || return 1
  log_file="$log_prefix.cleanup.log"
  : >"$log_file" || return 1
  printf '[cleanup] remote staging cleanup log=%s\n' "$log_file" >&2
  assert_known_hosts_current || return 1
  control_master_check "$log_file" || return 1
  set +e
  "$ssh_executable" "${ssh_common_options[@]}" "$host" bash -s -- \
    "$install_root" "$remote_stage" "$bundle_id" <"$cleanup_script" 2>&1 | tee "$log_file" >&2
  pipe_statuses=("${PIPESTATUS[@]}")
  set -e
  status=${pipe_statuses[0]:-1}
  [[ "$status" == 0 && "${pipe_statuses[1]:-1}" == 0 ]]
}

host=''
ssh_executable=''
scp_executable=''
ssh_connect_timeout_seconds=''
ssh_control_persist_seconds=''
remote_docker_executable=''
known_hosts_file=''
node_id=''
machine_id_sha256=''
remote_docker_host=''
docker_server_id=''
object_store_root=''
worktree_root=''
cache_root=''
private_overlay_root=''
temp_root=''
wine_prefix_root=''
install_root=''
log_dir=''
yes_seen=0
host_seen=0
ssh_seen=0
scp_seen=0
ssh_connect_timeout_seen=0
ssh_control_persist_seen=0
remote_docker_seen=0
known_hosts_seen=0
node_seen=0
machine_seen=0
remote_host_seen=0
docker_server_seen=0
object_store_seen=0
worktree_seen=0
cache_seen=0
overlay_seen=0
temp_seen=0
prefix_seen=0
install_root_seen=0
log_dir_seen=0

while (($#)); do
  case "$1" in
    --host)
      (($# >= 2)) || die '--host requires a value'
      ((host_seen == 0)) || die '--host must be provided once'
      host_seen=1
      host=$2
      shift 2
      ;;
    --ssh-executable)
      (($# >= 2)) || die '--ssh-executable requires a value'
      ((ssh_seen == 0)) || die '--ssh-executable must be provided once'
      ssh_seen=1
      ssh_executable=$2
      shift 2
      ;;
    --scp-executable)
      (($# >= 2)) || die '--scp-executable requires a value'
      ((scp_seen == 0)) || die '--scp-executable must be provided once'
      scp_seen=1
      scp_executable=$2
      shift 2
      ;;
    --ssh-connect-timeout-seconds)
      (($# >= 2)) || die '--ssh-connect-timeout-seconds requires a value'
      ((ssh_connect_timeout_seen == 0)) || die '--ssh-connect-timeout-seconds must be provided once'
      ssh_connect_timeout_seen=1
      ssh_connect_timeout_seconds=$2
      shift 2
      ;;
    --ssh-control-persist-seconds)
      (($# >= 2)) || die '--ssh-control-persist-seconds requires a value'
      ((ssh_control_persist_seen == 0)) || die '--ssh-control-persist-seconds must be provided once'
      ssh_control_persist_seen=1
      ssh_control_persist_seconds=$2
      shift 2
      ;;
    --remote-docker-executable)
      (($# >= 2)) || die '--remote-docker-executable requires a value'
      ((remote_docker_seen == 0)) || die '--remote-docker-executable must be provided once'
      remote_docker_seen=1
      remote_docker_executable=$2
      shift 2
      ;;
    --known-hosts-file)
      (($# >= 2)) || die '--known-hosts-file requires a value'
      ((known_hosts_seen == 0)) || die '--known-hosts-file must be provided once'
      known_hosts_seen=1; known_hosts_file=$2; shift 2
      ;;
    --node-id)
      (($# >= 2)) || die '--node-id requires a value'
      ((node_seen == 0)) || die '--node-id must be provided once'
      node_seen=1; node_id=$2; shift 2
      ;;
    --machine-id-sha256)
      (($# >= 2)) || die '--machine-id-sha256 requires a value'
      ((machine_seen == 0)) || die '--machine-id-sha256 must be provided once'
      machine_seen=1; machine_id_sha256=$2; shift 2
      ;;
    --remote-docker-host)
      (($# >= 2)) || die '--remote-docker-host requires a value'
      ((remote_host_seen == 0)) || die '--remote-docker-host must be provided once'
      remote_host_seen=1; remote_docker_host=$2; shift 2
      ;;
    --docker-server-id)
      (($# >= 2)) || die '--docker-server-id requires a value'
      ((docker_server_seen == 0)) || die '--docker-server-id must be provided once'
      docker_server_seen=1; docker_server_id=$2; shift 2
      ;;
    --object-store-root|--worktree-root|--cache-root|--private-overlay-root|--temp-root|--wine-prefix-root)
      (($# >= 2)) || die "$1 requires a value"
      case "$1" in
        --object-store-root) ((object_store_seen == 0)) || die '--object-store-root must be provided once'; object_store_seen=1; object_store_root=$2 ;;
        --worktree-root) ((worktree_seen == 0)) || die '--worktree-root must be provided once'; worktree_seen=1; worktree_root=$2 ;;
        --cache-root) ((cache_seen == 0)) || die '--cache-root must be provided once'; cache_seen=1; cache_root=$2 ;;
        --private-overlay-root) ((overlay_seen == 0)) || die '--private-overlay-root must be provided once'; overlay_seen=1; private_overlay_root=$2 ;;
        --temp-root) ((temp_seen == 0)) || die '--temp-root must be provided once'; temp_seen=1; temp_root=$2 ;;
        --wine-prefix-root) ((prefix_seen == 0)) || die '--wine-prefix-root must be provided once'; prefix_seen=1; wine_prefix_root=$2 ;;
      esac
      shift 2
      ;;
    --install-root)
      (($# >= 2)) || die '--install-root requires a value'
      ((install_root_seen == 0)) || die '--install-root must be provided once'
      install_root_seen=1
      install_root=$2
      shift 2
      ;;
    --log-dir)
      (($# >= 2)) || die '--log-dir requires a value'
      ((log_dir_seen == 0)) || die '--log-dir must be provided once'
      log_dir_seen=1
      log_dir=$2
      shift 2
      ;;
    --yes)
      ((yes_seen == 0)) || die '--yes must be provided once'
      yes_seen=1
      shift
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

((host_seen == 1)) || die '--host is required'
((ssh_seen == 1)) || die '--ssh-executable is required'
((scp_seen == 1)) || die '--scp-executable is required'
((ssh_connect_timeout_seen == 1)) || die '--ssh-connect-timeout-seconds is required'
((ssh_control_persist_seen == 1)) || die '--ssh-control-persist-seconds is required'
((remote_docker_seen == 1)) || die '--remote-docker-executable is required'
((known_hosts_seen == 1)) || die '--known-hosts-file is required'
((node_seen == 1)) || die '--node-id is required'
((machine_seen == 1)) || die '--machine-id-sha256 is required'
((remote_host_seen == 1)) || die '--remote-docker-host is required'
((docker_server_seen == 1)) || die '--docker-server-id is required'
((object_store_seen == 1 && worktree_seen == 1 && cache_seen == 1 && overlay_seen == 1 && temp_seen == 1 && prefix_seen == 1)) || die 'all runtime roots are required'
((install_root_seen == 1)) || die '--install-root is required'
((log_dir_seen == 1)) || die '--log-dir is required'
((yes_seen == 1)) || die 'deployment is non-interactive; pass --yes'
[[ "$host" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]] || die '--host must be a safe SSH alias'
[[ "$node_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]] || die '--node-id must be a safe identity'
[[ "$ssh_connect_timeout_seconds" =~ ^[1-9][0-9]*$ ]] || die '--ssh-connect-timeout-seconds must be an integer from 1 to 300'
[[ "$ssh_control_persist_seconds" =~ ^[1-9][0-9]*$ ]] || die '--ssh-control-persist-seconds must be an integer from connect timeout to 3600'
((10#$ssh_connect_timeout_seconds <= 300)) || die '--ssh-connect-timeout-seconds must be an integer from 1 to 300'
((10#$ssh_control_persist_seconds >= 10#$ssh_connect_timeout_seconds && 10#$ssh_control_persist_seconds <= 3600)) || die '--ssh-control-persist-seconds must be at least the connect timeout and no more than 3600'
[[ "$machine_id_sha256" =~ ^sha256:[a-f0-9]{64}$ ]] || die '--machine-id-sha256 must be canonical sha256'
[[ "$docker_server_id" =~ ^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$ ]] || die '--docker-server-id must be a safe opaque ID'
[[ "$remote_docker_host" =~ ^unix:///.+ ]] || die '--remote-docker-host must be unix://<absolute path>'
docker_socket_path=${remote_docker_host#unix://}
validate_absolute_clean_path '--remote-docker-host path' "$docker_socket_path"
validate_executable_path '--ssh-executable' "$ssh_executable"
validate_executable_path '--scp-executable' "$scp_executable"
validate_absolute_clean_path '--install-root' "$install_root"
validate_absolute_clean_path '--remote-docker-executable' "$remote_docker_executable"
validate_absolute_clean_path '--known-hosts-file' "$known_hosts_file"
[[ -f "$known_hosts_file" && ! -L "$known_hosts_file" && -r "$known_hosts_file" ]] || die '--known-hosts-file must be a readable regular file'
for runtime_root in "$object_store_root" "$worktree_root" "$cache_root" "$private_overlay_root" "$temp_root" "$wine_prefix_root"; do validate_absolute_clean_path 'runtime root' "$runtime_root"; done
for runtime_root in "$object_store_root" "$worktree_root" "$cache_root" "$private_overlay_root" "$temp_root" "$wine_prefix_root"; do
  for other_root in "$object_store_root" "$worktree_root" "$cache_root" "$private_overlay_root" "$temp_root" "$wine_prefix_root"; do
    [[ "$runtime_root" == "$other_root" ]] || [[ "$runtime_root" != "$other_root"/* && "$other_root" != "$runtime_root"/* ]] || die 'runtime roots must be disjoint'
  done
done
for runtime_root in "$object_store_root" "$worktree_root" "$cache_root" "$private_overlay_root" "$temp_root" "$wine_prefix_root"; do
  [[ "$runtime_root" != "$install_root/.staging" && "$runtime_root" != "$install_root/.locks" && "$runtime_root" != "$install_root/releases" ]] || die 'runtime root overlaps deployment state'
  [[ "$runtime_root" != "$install_root/.staging"/* && "$runtime_root" != "$install_root/.locks"/* && "$runtime_root" != "$install_root/releases"/* ]] || die 'runtime root is inside deployment state'
done
validate_absolute_clean_path '--log-dir' "$log_dir"
mkdir -p -- "$log_dir" || die 'unable to create --log-dir'
validate_absolute_clean_path '--log-dir' "$log_dir"

script_path=$(cd -- "$(dirname -- "$0")" && pwd -P) || die 'unable to resolve deployment script directory'
root=$(cd -- "$script_path/../../.." && pwd -P) || die 'unable to resolve repository root'
git_root=$(git -C "$root" rev-parse --show-toplevel 2>/dev/null) || die 'deployment script is not inside a Git worktree'
[[ "$git_root" == "$root" ]] || die 'deployment script Git root drifted'

dockerfile_source="$root/Dockerfile.ci-runner-ubuntu-wine"
ubuntu_dockerfile_source="$root/Dockerfile.ci-runner-ubuntu"
runner_source="$root/scripts/deploy/linux/linux_runner_daemon.sh"
worker_source="$root/cmd/super-dolphin-heteroci-node"
for source in "$dockerfile_source" "$ubuntu_dockerfile_source" "$runner_source"; do
  [[ -f "$source" && ! -L "$source" && -r "$source" ]] || die "required source is missing or unsafe: $source"
done
[[ -d "$worker_source" && ! -L "$worker_source" && -r "$worker_source" ]] || die "worker source directory is missing or unsafe: $worker_source"

select_hash_tool
known_hosts_identity=$(sha256_file "$known_hosts_file")
assert_known_hosts_current() {
  [[ "$(sha256_file "$known_hosts_file")" == "$known_hosts_identity" ]] || die 'known_hosts file changed during deployment'
}
ssh_common_options=()
control_path=''
control_master_started=0
control_master_pid=''
control_master_owner_uid=''
control_master_owner_start=''
control_master_owner_command=''
control_master_check() {
  local log_file=$1 pipe_statuses status pid_line
  assert_known_hosts_current
  set +e
  "$ssh_executable" "${ssh_common_options[@]}" -O check "$host" 2>&1 | tee -a "$log_file" >&2
  pipe_statuses=("${PIPESTATUS[@]}")
  set -e
  status=${pipe_statuses[0]:-1}
  if [[ "$status" == 0 && "${pipe_statuses[1]:-1}" == 0 ]]; then
    pid_line=$(sed -n 's/^Master running (pid=\([0-9][0-9]*\)).*$/\1/p' "$log_file" | tail -1)
    if [[ "$pid_line" =~ ^[0-9]+$ ]]; then
      if [[ -n "$control_master_pid" && "$pid_line" != "$control_master_pid" ]]; then
        printf 'control_master_pid_changed=true previous=%s current=%s\n' "$control_master_pid" "$pid_line" >>"$log_file"
        return 1
      fi
      control_master_pid=$pid_line
      printf 'control_master_pid=%s\n' "$control_master_pid" >>"$log_file"
    else
      printf 'control_master_pid_missing=true\n' >>"$log_file"
      return 1
    fi
    return 0
  fi
  return 1
}
capture_control_master_owner() {
  local log_file=$1
  [[ "$control_master_pid" =~ ^[1-9][0-9]*$ ]] || return 1
  control_master_owner_uid=$(ps -p "$control_master_pid" -o uid= 2>/dev/null | tr -d '[:space:]')
  control_master_owner_start=$(ps -p "$control_master_pid" -o lstart= 2>/dev/null | sed 's/^[[:space:]]*//')
  control_master_owner_command=$(ps -p "$control_master_pid" -o command= 2>/dev/null | sed 's/^[[:space:]]*//')
  [[ "$control_master_owner_uid" =~ ^[0-9]+$ ]] || return 1
  [[ -n "$control_master_owner_start" && -n "$control_master_owner_command" ]] || return 1
  [[ "$control_master_owner_uid" == "$(id -u)" ]] || return 1
  printf 'control_master_owner_captured=true pid=%s uid=%s\n' "$control_master_pid" "$control_master_owner_uid" >>"$log_file"
}
control_master_start() {
  local log_file="$log_prefix.control-master.log" status
  : >"$log_file" || die "cannot create log file: $log_file"
  printf '[control-master] start log=%s\n' "$log_file" >&2
  assert_known_hosts_current
  [[ ! -e "$control_path" ]] || die 'ControlPath already exists'
  set +e
  "$ssh_executable" "${ssh_common_options[@]}" -o ControlMaster=yes -o ControlPersist="$ssh_control_persist_seconds" -MNf "$host" >"$log_file" 2>&1
  status=$?
  set -e
  if [[ -S "$control_path" ]]; then
    control_master_started=1
  fi
  if [[ "$status" != 0 || "$control_master_started" != 1 ]]; then
    cat -- "$log_file" >&2 || true
    die 'SSH ControlMaster failed or did not create a socket; see control-master.log'
  fi
  control_master_check "$log_file" || die 'SSH ControlMaster check failed or did not report an owner PID; see control-master.log'
  capture_control_master_owner "$log_file" || die 'SSH ControlMaster owner identity could not be captured; see control-master.log'
}
wait_for_control_socket_absent() {
  local log_file=$1 iteration
  for iteration in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31 32 33 34 35 36 37 38 39 40 41 42 43 44 45 46 47 48 49 50; do
    if [[ ! -e "$control_path" ]]; then
      printf 'control_socket_absent=true iteration=%s\n' "$iteration" >>"$log_file"
      return 0
    fi
    sleep 0.1
  done
  printf 'control_socket_absent=false\n' >>"$log_file"
  return 1
}
wait_for_control_master_gone() {
  local log_file=$1 iteration
  for iteration in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31 32 33 34 35 36 37 38 39 40 41 42 43 44 45 46 47 48 49 50; do
    if ! kill -0 "$control_master_pid" 2>/dev/null; then
      printf 'control_master_gone=true iteration=%s\n' "$iteration" >>"$log_file"
      return 0
    fi
    sleep 0.1
  done
  printf 'control_master_gone=false\n' >>"$log_file"
  return 1
}
terminate_owned_control_master() {
  local log_file=$1 owner_uid owner_start owner_command
  [[ "$control_master_pid" =~ ^[1-9][0-9]*$ ]] || return 2
  owner_uid=$(ps -p "$control_master_pid" -o uid= 2>/dev/null | tr -d '[:space:]')
  owner_start=$(ps -p "$control_master_pid" -o lstart= 2>/dev/null | sed 's/^[[:space:]]*//')
  owner_command=$(ps -p "$control_master_pid" -o command= 2>/dev/null | sed 's/^[[:space:]]*//')
  if [[ "$owner_uid" != "$control_master_owner_uid" || "$owner_start" != "$control_master_owner_start" || "$owner_command" != "$control_master_owner_command" || "$owner_uid" != "$(id -u)" ]]; then
    printf 'control_master_owner_verified=false pid=%s uid=%s\n' "$control_master_pid" "$owner_uid" >>"$log_file"
    return 2
  fi
  printf 'control_master_owner_verified=true pid=%s uid=%s\n' "$control_master_pid" "$owner_uid" >>"$log_file"
  if kill -TERM "$control_master_pid" 2>>"$log_file"; then
    printf 'control_master_term_sent=true pid=%s\n' "$control_master_pid" >>"$log_file"
  else
    printf 'control_master_term_sent=false pid=%s\n' "$control_master_pid" >>"$log_file"
    return 1
  fi
  wait_for_control_master_gone "$log_file" || return 1
  if [[ -e "$control_path" ]]; then
    [[ -S "$control_path" ]] || return 1
    rm -f -- "$control_path" || return 1
    printf 'control_socket_unlinked_after_owner_term=true\n' >>"$log_file"
  fi
  wait_for_control_socket_absent "$log_file"
}
control_master_close() {
  local log_file="$log_prefix.control-master-exit.log" status result=0 socket_absent=0 pid_alive=0 identity_failure=0 term_status
  ((control_master_started == 1)) || return 0
  : >"$log_file" || result=1
  printf '[control-master] exit log=%s\n' "$log_file" >&2
  if [[ "$(sha256_file "$known_hosts_file")" != "$known_hosts_identity" ]]; then
    printf 'known_hosts file changed before ControlMaster exit\n' >>"$log_file"
    result=1
  fi
  set +e
  "$ssh_executable" "${ssh_common_options[@]}" -O exit "$host" >>"$log_file" 2>&1
  status=$?
  set -e
  printf 'control_master_exit_status=%s\n' "$status" >>"$log_file"
  if [[ "$status" == 0 ]] && wait_for_control_socket_absent "$log_file"; then
    socket_absent=1
  fi
  if [[ "$status" == 0 ]] && wait_for_control_master_gone "$log_file"; then
    pid_alive=0
  elif kill -0 "$control_master_pid" 2>/dev/null; then
    pid_alive=1
  fi
  if [[ ! -e "$control_path" ]]; then
    printf 'control_socket_absent=true\n' >>"$log_file"
    socket_absent=1
  else
    socket_absent=0
  fi
  if ((pid_alive == 1)); then
    if terminate_owned_control_master "$log_file"; then
      pid_alive=0
      socket_absent=1
    else
      term_status=$?
      [[ "$term_status" == 2 ]] && identity_failure=1
      result=1
    fi
  elif ((socket_absent == 0)); then
    if [[ -S "$control_path" ]]; then
      rm -f -- "$control_path" || result=1
      printf 'control_socket_unlinked_after_owner_gone=true\n' >>"$log_file"
      socket_absent=1
    else
      result=1
    fi
  fi
  if ((identity_failure == 0)); then
    wait_for_control_master_gone "$log_file" || result=1
    wait_for_control_socket_absent "$log_file" || result=1
  else
    printf 'control_master_cleanup_skipped_after_identity_mismatch=true\n' >>"$log_file"
  fi
  [[ "$pid_alive" == 0 && "$socket_absent" == 1 && ! -e "$control_path" ]] || result=1
  if ((result != 0)); then
    cat -- "$log_file" >&2 || true
  fi
  control_master_started=0
  return "$result"
}
run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$" || die 'unable to create deployment log identity'
log_prefix="$log_dir/ubuntu-wine-$run_id"
local_tmp=''
remote_stage=''
remote_stage_owned=0
cleanup_script=''
last_remote_log=''
bundle_id=''

cleanup_local() {
  local status=$? cleanup_status=0
  set +e
  if ((remote_stage_owned == 1)); then
    best_effort_remote_cleanup || cleanup_status=1
  fi
  control_master_close || cleanup_status=1
  if [[ -n "$local_tmp" && -d "$local_tmp" && ! -L "$local_tmp" ]]; then
    rm -rf -- "$local_tmp" || cleanup_status=1
  fi
  if ((status == 0 && cleanup_status != 0)); then
    printf 'deploy_single_ubuntu_wine: cleanup failed; inspect %s.cleanup.log\n' "$log_prefix" >&2
    status=1
  fi
  exit "$status"
}
trap cleanup_local EXIT

local_tmp=$(mktemp -d "$log_dir/.u.XXXXXX") || die 'unable to create local staging directory'
[[ "$local_tmp" == "$log_dir"/* && ! -L "$local_tmp" ]] || die 'local staging directory escaped --log-dir'
control_path="$local_tmp/c"
ssh_common_options=(
  -o BatchMode=yes
  -o StrictHostKeyChecking=yes
  -o UserKnownHostsFile="$known_hosts_file"
  -o GlobalKnownHostsFile=/dev/null
  -o ConnectTimeout="$ssh_connect_timeout_seconds"
  -o ControlPath="$control_path"
)
go_executable=$(command -v go 2>/dev/null || true)
[[ -n "$go_executable" && -x "$go_executable" ]] || die 'go executable is required for cross-build'
worker_target="$local_tmp/super-dolphin-heteroci-node"
run_local_logged cross-build env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOTOOLCHAIN=local \
  "$go_executable" -C "$root" build -buildvcs=false -trimpath -o "$worker_target" ./cmd/super-dolphin-heteroci-node
[[ -f "$worker_target" && -x "$worker_target" ]] || die 'cross-build did not produce an executable worker'
install -m 0644 "$dockerfile_source" "$local_tmp/Dockerfile.ci-runner-ubuntu-wine" || die 'unable to stage Dockerfile'
install -m 0644 "$ubuntu_dockerfile_source" "$local_tmp/Dockerfile.ci-runner-ubuntu" || die 'unable to stage Ubuntu Dockerfile'
install -m 0755 "$runner_source" "$local_tmp/linux_runner_daemon.sh" || die 'unable to stage Linux runner'

dockerfile_identity=$(sha256_file "$local_tmp/Dockerfile.ci-runner-ubuntu-wine")
ubuntu_dockerfile_identity=$(sha256_file "$local_tmp/Dockerfile.ci-runner-ubuntu")
runner_identity=$(sha256_file "$local_tmp/linux_runner_daemon.sh")
worker_identity=$(sha256_file "$worker_target")
bundle_material=$(printf '%s\n%s\n%s\n%s\n%s\n' \
  'single-ubuntu-wine-deployment/v3' "$ubuntu_dockerfile_identity" "$dockerfile_identity" "$runner_identity" "$worker_identity")
bundle_identity=$(sha256_text "$bundle_material")
bundle_id=${bundle_identity#sha256:}
remote_stage="$install_root/.staging/ubuntu-wine-$bundle_id-$run_id"
remote_release="$install_root/releases/ubuntu-wine-$bundle_id"
image_ref="ci-runner-ubuntu-wine:$bundle_id"

printf '%s\n' \
  'schema_version=single-ubuntu-wine-deployment/v3' \
  "deployment_id=$bundle_id" \
  "ubuntu_dockerfile_identity=$ubuntu_dockerfile_identity" \
  "dockerfile_identity=$dockerfile_identity" \
  "runner_identity=$runner_identity" \
  "worker_identity=$worker_identity" >"$local_tmp/deployment.identity"
manifest_identity=$(sha256_file "$local_tmp/deployment.identity")

cat >"$local_tmp/remote_preflight.sh" <<'REMOTE_PREFLIGHT'
#!/usr/bin/env bash
set -Eeuo pipefail
export DOCKER_HOST=unix:///run/docker.sock
die() { printf 'remote preflight: %s\n' "$*" >&2; exit 2; }
sha256_file() {
  local path=$1 actual
  actual=$(sha256sum -- "$path") || die "unable to hash $path"
  printf 'sha256:%s\n' "${actual%% *}"
}
validate_host_tool_path() {
  local label=$1 path=$2
  [[ "$path" = /* && "$path" =~ ^/[A-Za-z0-9._/+:-]+$ && "$path" != *'..'* ]] || die "$label path is unsafe"
}
detect_remote_host_tools() {
  local os_entry=/etc/os-release os_path id_lines uname_entry uname_path uname_os uname_machine
  [[ -f "$os_entry" && -r "$os_entry" ]] || die 'remote /etc/os-release is not a readable file'
  os_path=$(realpath -e -- "$os_entry") || die 'remote /etc/os-release cannot be resolved'
  [[ -f "$os_path" && ! -L "$os_path" && -r "$os_path" ]] || die 'remote canonical os-release is not a readable regular non-symlink file'
  validate_host_tool_path os_release "$os_path"
  id_lines=$(grep -E '^ID=' "$os_path" || true)
  [[ "$(printf '%s\n' "$id_lines" | sed '/^$/d' | wc -l | tr -d ' ')" == 1 && "$id_lines" == ID=ubuntu ]] || die 'remote OS must contain exactly one ID=ubuntu'
  uname_entry=$(command -v uname) || die 'remote uname executable cannot be located'
  [[ "$uname_entry" = /* ]] || die 'remote uname command path is not absolute'
  uname_path=$(realpath -e -- "$uname_entry") || die 'remote uname executable cannot be resolved'
  [[ -f "$uname_path" && ! -L "$uname_path" && -x "$uname_path" ]] || die 'remote canonical uname is not a regular non-symlink executable'
  validate_host_tool_path uname "$uname_path"
  uname_os=$("$uname_path" -s) || die 'remote uname -s failed'
  uname_machine=$("$uname_path" -m) || die 'remote uname -m failed'
  [[ "$uname_os" == Linux && "$uname_machine" == x86_64 ]] || die 'remote host is not linux/x86_64'
  os_release_path=$os_path
  os_release_identity=$(sha256_file "$os_path")
  uname_executable=$uname_path
  uname_identity=$(sha256_file "$uname_path")
}
detect_remote_host_tools
detect_remote_machine_identity() {
  local machine_entry=/etc/machine-id machine_path machine_actual
  [[ -f "$machine_entry" && -r "$machine_entry" ]] || die 'remote /etc/machine-id is not a readable file'
  machine_path=$(realpath -e -- "$machine_entry") || die 'remote /etc/machine-id cannot be resolved'
  [[ -f "$machine_path" && ! -L "$machine_path" && -r "$machine_path" ]] || die 'remote canonical machine-id is not a readable regular non-symlink file'
  validate_host_tool_path machine_id "$machine_path"
  machine_actual=$(sha256_file "$machine_path")
  [[ "$machine_actual" == "$machine_id_sha256" ]] || die 'remote machine-id identity differs'
  machine_id_path=$machine_path
}
install_root=$1
stage=$2
release=$3
image_ref=$4
remote_docker=$5
bundle_id=$6
owner="single-ubuntu-wine-deployment/v1:$bundle_id"
ubuntu_dockerfile_identity=$7
dockerfile_identity=$8
runner_identity=$9
worker_identity=${10}
manifest_identity=${11}
ubuntu_image_ref="ci-runner-ubuntu:$bundle_id"
node_id=${12}
machine_id_sha256=${13}
detect_remote_machine_identity
docker_server_id=${14}
remote_docker_host=${15}
object_store_root=${16}
worktree_root=${17}
cache_root=${18}
private_overlay_root=${19}
temp_root=${20}
wine_prefix_root=${21}
known_hosts_identity=${22}
ssh_connect_timeout_seconds=${23}
ssh_control_persist_seconds=${24}
[[ "$ssh_connect_timeout_seconds" =~ ^[1-9][0-9]*$ ]] || die 'SSH connect timeout identity is invalid'
[[ "$ssh_control_persist_seconds" =~ ^[1-9][0-9]*$ ]] || die 'SSH ControlPersist identity is invalid'
((10#$ssh_connect_timeout_seconds <= 300)) || die 'SSH connect timeout identity is out of range'
((10#$ssh_control_persist_seconds >= 10#$ssh_connect_timeout_seconds && 10#$ssh_control_persist_seconds <= 3600)) || die 'SSH ControlPersist identity is out of range'
unset DOCKER_CONTEXT
export DOCKER_HOST="$remote_docker_host"
docker_socket_path=${remote_docker_host#unix://}
[[ "$docker_socket_path" = /* && ! "$docker_socket_path" =~ [[:cntrl:][:space:]] && "$docker_socket_path" != *'..'* ]] || die 'remote Docker socket path is unsafe'
docker_socket_type=$(stat -c %F "$docker_socket_path" 2>/dev/null || stat -f %HT "$docker_socket_path" 2>/dev/null) || die 'remote Docker endpoint cannot be inspected'
[[ "$docker_socket_type" == socket || "$docker_socket_type" == Socket ]] || die 'remote Docker endpoint is not a Unix socket'
[[ ! -L "$docker_socket_path" ]] || die 'remote Docker endpoint is a symlink'
for runtime_root in "$object_store_root" "$worktree_root" "$cache_root" "$private_overlay_root" "$temp_root" "$wine_prefix_root"; do
  [[ "$runtime_root" = /* && ! "$runtime_root" =~ [[:cntrl:][:space:]] && "$runtime_root" != *'..'* ]] || die 'runtime root is unsafe'
  mkdir -p "$runtime_root"
  [[ "$(realpath -e -- "$runtime_root")" == "$runtime_root" && ! -L "$runtime_root" ]] || die 'runtime root resolves through a symlink'
done
[[ "$install_root" = /* && "$install_root" != / && ! "$install_root" =~ [[:cntrl:][:space:]] ]] || die 'install root is unsafe'
[[ "$stage" == "$install_root/.staging/ubuntu-wine-$bundle_id-"* ]] || die 'staging path is not bound to install root'
[[ "$release" == "$install_root/releases/ubuntu-wine-$bundle_id" ]] || die 'release path is not bound to install root'
[[ "$image_ref" == ci-runner-ubuntu-wine:* ]] || die 'image reference is not deployment scoped'
[[ "$bundle_id" =~ ^[a-f0-9]{64}$ ]] || die 'deployment identity is invalid'
mkdir -p -- "$install_root"
[[ "$(realpath -e -- "$install_root")" == "$install_root" ]] || die 'install root resolves through a symlink'
for child in .staging releases; do
  path="$install_root/$child"
  [[ ! -L "$path" ]] || die "install root child is a symlink: $child"
  mkdir -p -- "$path"
  [[ "$(realpath -e -- "$path")" == "$path" ]] || die "install root child resolves through a symlink: $child"
done
[[ -x "$remote_docker" && -f "$remote_docker" && ! -L "$remote_docker" ]] || die 'remote Docker executable is missing or unsafe'
[[ "$node_id" =~ ^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$ ]] || die 'remote node identity is invalid'
[[ "$remote_docker_host" =~ ^unix:///.+ ]] || die 'remote Docker host is invalid'
"$remote_docker" info >/dev/null 2>&1 || die 'remote Docker daemon is unavailable'
docker_actual_id=$("$remote_docker" info --format '{{.ID}}') || die 'remote Docker server ID cannot be read'
[[ "$docker_actual_id" == "$docker_server_id" ]] || die 'remote Docker server ID differs'
docker_os_arch=$("$remote_docker" info --format '{{.OSType}} {{.Architecture}}') || die 'remote Docker OS/architecture cannot be read'
[[ "$docker_os_arch" == 'linux x86_64' || "$docker_os_arch" == 'linux amd64' ]] || die 'remote Docker is not linux/amd64'
"$remote_docker" buildx version >/dev/null 2>&1 || die 'remote Docker buildx is unavailable'
release_exists=0
if [[ -e "$release" ]]; then
  [[ -d "$release" && ! -L "$release" ]] || die 'existing release identity is not a directory'
  [[ -f "$release/deployment.identity" && ! -L "$release/deployment.identity" ]] || die 'existing release identity has no manifest'
  grep -Fxq "deployment_id=$bundle_id" "$release/deployment.identity" || die 'existing release has a different identity'
  grep -Fxq "ubuntu_dockerfile_identity=$ubuntu_dockerfile_identity" "$release/deployment.identity" || die 'existing release Ubuntu Dockerfile identity differs'
  grep -Fxq "dockerfile_identity=$dockerfile_identity" "$release/deployment.identity" || die 'existing release Dockerfile identity differs'
  grep -Fxq "runner_identity=$runner_identity" "$release/deployment.identity" || die 'existing release runner identity differs'
  grep -Fxq "worker_identity=$worker_identity" "$release/deployment.identity" || die 'existing release worker identity differs'
  [[ "$(sha256_file "$release/deployment.identity")" == "$manifest_identity" ]] || die 'existing release manifest identity differs'
  [[ -f "$release/image.identity" && ! -L "$release/image.identity" ]] || die 'existing release image identity is missing'
  release_image_id=${release_image_id:-}
  [[ "$(cat "$release/image.identity")" == image_id=sha256:* ]] || die 'existing release image identity is not exact'
  release_image_id=${release_image_id:-$(sed -n 's/^image_id=//p' "$release/image.identity")}
  [[ "$release_image_id" =~ ^sha256:[a-f0-9]{64}$ ]] || die 'existing release image identity is invalid'
  [[ -f "$release/ubuntu-image.identity" && ! -L "$release/ubuntu-image.identity" ]] || die 'existing release Ubuntu image identity is missing'
  release_ubuntu_image_id=$(sed -n 's/^image_id=//p' "$release/ubuntu-image.identity")
  [[ "$release_ubuntu_image_id" =~ ^sha256:[a-f0-9]{64}$ ]] || die 'existing release Ubuntu image identity is invalid'
  release_exists=1
fi
image_exists=0
if "$remote_docker" image inspect "$image_ref" >/dev/null 2>&1; then image_exists=1; fi
ubuntu_image_exists=0
if "$remote_docker" image inspect "$ubuntu_image_ref" >/dev/null 2>&1; then ubuntu_image_exists=1; fi
if ((release_exists != image_exists || image_exists != ubuntu_image_exists)); then
  die 'release and image identity must exist together for idempotent deployment'
fi
if ((image_exists == 1)); then
  current_image_id=$("$remote_docker" image inspect --format '{{.Id}}' "$image_ref") || die 'existing image ID cannot be inspected'
  [[ "$current_image_id" == "$release_image_id" ]] || die 'existing image ID differs from release identity'
  current_image_label=$("$remote_docker" image inspect --format '{{index .Config.Labels "com.super-dolphin.deployment-id"}}' "$image_ref") || die 'existing image deployment label cannot be inspected'
  [[ "$current_image_label" == "$bundle_id" ]] || die 'existing image deployment label differs'
  current_ubuntu_image_id=$("$remote_docker" image inspect --format '{{.Id}}' "$ubuntu_image_ref") || die 'existing Ubuntu image ID cannot be inspected'
  [[ "$current_ubuntu_image_id" == "$release_ubuntu_image_id" ]] || die 'existing Ubuntu image ID differs from release identity'
  current_ubuntu_image_label=$("$remote_docker" image inspect --format '{{index .Config.Labels "com.super-dolphin.deployment-id"}}' "$ubuntu_image_ref") || die 'existing Ubuntu image deployment label cannot be inspected'
  [[ "$current_ubuntu_image_label" == "$bundle_id" ]] || die 'existing Ubuntu image deployment label differs'
fi
if [[ -e "$stage" ]]; then
  [[ -d "$stage" && ! -L "$stage" ]] || die 'existing staging identity is not a directory'
  [[ -f "$stage/.owner" && ! -L "$stage/.owner" ]] || die 'existing staging identity has no owner marker'
  [[ "$(cat -- "$stage/.owner")" == "$owner" ]] || die 'staging identity belongs to another deployment'
  rm -rf -- "$stage"
fi
mkdir -- "$stage"
chmod 700 "$stage"
printf '%s\n' "$owner" >"$stage/.owner"
chmod 600 "$stage/.owner"
printf 'os_release_path=%s\nos_release_identity=%s\nmachine_id_path=%s\nmachine_id_sha256=%s\nuname_executable=%s\nuname_identity=%s\n' \
  "$os_release_path" "$os_release_identity" "$machine_id_path" "$machine_id_sha256" "$uname_executable" "$uname_identity" >"$stage/.host-tools.identity"
chmod 600 "$stage/.host-tools.identity"
printf 'stage_ready=true deployment_id=%s release_exists=%s image_exists=%s\n' "$bundle_id" "$release_exists" "$image_exists"
REMOTE_PREFLIGHT
chmod 700 "$local_tmp/remote_preflight.sh"

cat >"$local_tmp/remote_cleanup.sh" <<'REMOTE_CLEANUP'
#!/usr/bin/env bash
set -Eeuo pipefail
install_root=$1
stage=$2
bundle_id=$3
owner="single-ubuntu-wine-deployment/v1:$bundle_id"
[[ "$install_root" = /* && "$install_root" != / && ! "$install_root" =~ [[:cntrl:][:space:]] ]] || exit 2
[[ "$stage" == "$install_root/.staging/ubuntu-wine-$bundle_id-"* ]] || exit 2
if [[ -e "$stage" ]]; then
  [[ -d "$stage" && ! -L "$stage" && -f "$stage/.owner" && ! -L "$stage/.owner" ]] || exit 3
  [[ "$(cat -- "$stage/.owner")" == "$owner" ]] || exit 3
  rm -rf -- "$stage"
fi
[[ ! -e "$stage" ]] || exit 4
printf 'staging_cleanup=verified deployment_id=%s\n' "$bundle_id"
REMOTE_CLEANUP
chmod 700 "$local_tmp/remote_cleanup.sh"
cleanup_script="$local_tmp/remote_cleanup.sh"

control_master_start
remote_stage_owned=1
run_remote_logged preflight "$local_tmp/remote_preflight.sh" \
  "$install_root" "$remote_stage" "$remote_release" "$image_ref" "$remote_docker_executable" "$bundle_id" \
  "$ubuntu_dockerfile_identity" "$dockerfile_identity" "$runner_identity" "$worker_identity" "$manifest_identity" \
  "$node_id" "$machine_id_sha256" "$docker_server_id" "$remote_docker_host" \
  "$object_store_root" "$worktree_root" "$cache_root" "$private_overlay_root" "$temp_root" "$wine_prefix_root" "$known_hosts_identity" \
  "$ssh_connect_timeout_seconds" "$ssh_control_persist_seconds"
run_scp_logged stage-ubuntu-dockerfile "$local_tmp/Dockerfile.ci-runner-ubuntu" "$remote_stage/Dockerfile.ci-runner-ubuntu"
run_scp_logged stage-dockerfile "$local_tmp/Dockerfile.ci-runner-ubuntu-wine" "$remote_stage/Dockerfile.ci-runner-ubuntu-wine"
run_scp_logged stage-runner "$local_tmp/linux_runner_daemon.sh" "$remote_stage/linux_runner_daemon.sh"
run_scp_logged stage-worker "$worker_target" "$remote_stage/super-dolphin-heteroci-node"
run_scp_logged stage-manifest "$local_tmp/deployment.identity" "$remote_stage/deployment.identity"

cat >"$local_tmp/remote_install.sh" <<'REMOTE_INSTALL'
#!/usr/bin/env bash
set -Eeuo pipefail
export DOCKER_HOST=unix:///run/docker.sock
die() { printf 'remote install: %s\n' "$*" >&2; exit 2; }
install_root=$1
stage=$2
release=$3
image_ref=$4
remote_docker=$5
bundle_id=$6
ubuntu_image_ref="ci-runner-ubuntu:$bundle_id"
ubuntu_dockerfile_identity=$7
dockerfile_identity=$8
runner_identity=$9
worker_identity=${10}
manifest_identity=${11}
node_id=${12}
machine_id_sha256=${13}
docker_server_id=${14}
remote_docker_host=${15}
object_store_root=${16}
worktree_root=${17}
cache_root=${18}
private_overlay_root=${19}
temp_root=${20}
wine_prefix_root=${21}
known_hosts_identity=${22}
ssh_connect_timeout_seconds=${23}
ssh_control_persist_seconds=${24}
[[ "$ssh_connect_timeout_seconds" =~ ^[1-9][0-9]*$ ]] || die 'SSH connect timeout identity is invalid'
[[ "$ssh_control_persist_seconds" =~ ^[1-9][0-9]*$ ]] || die 'SSH ControlPersist identity is invalid'
((10#$ssh_connect_timeout_seconds <= 300)) || die 'SSH connect timeout identity is out of range'
((10#$ssh_control_persist_seconds >= 10#$ssh_connect_timeout_seconds && 10#$ssh_control_persist_seconds <= 3600)) || die 'SSH ControlPersist identity is out of range'
unset DOCKER_CONTEXT
export DOCKER_HOST="$remote_docker_host"
owner="single-ubuntu-wine-deployment/v1:$bundle_id"
docker_socket_path=${remote_docker_host#unix://}
[[ "$docker_socket_path" = /* && ! "$docker_socket_path" =~ [[:cntrl:][:space:]] && "$docker_socket_path" != *'..'* ]] || die 'remote Docker socket path is unsafe'
docker_socket_type=$(stat -c %F "$docker_socket_path" 2>/dev/null || stat -f %HT "$docker_socket_path" 2>/dev/null) || die 'remote Docker endpoint cannot be inspected'
[[ "$docker_socket_type" == socket || "$docker_socket_type" == Socket ]] || die 'remote Docker endpoint is not a Unix socket'
[[ ! -L "$docker_socket_path" ]] || die 'remote Docker endpoint is a symlink'
for runtime_root in "$object_store_root" "$worktree_root" "$cache_root" "$private_overlay_root" "$temp_root" "$wine_prefix_root"; do
  [[ "$runtime_root" = /* && ! "$runtime_root" =~ [[:cntrl:][:space:]] && "$runtime_root" != *'..'* ]] || die 'runtime root is unsafe'
  mkdir -p "$runtime_root"
  [[ "$(realpath -e -- "$runtime_root")" == "$runtime_root" && ! -L "$runtime_root" ]] || die 'runtime root resolves through a symlink'
done
release_tmp=''
stage_owned=1
smoke_name="ubuntu-wine-smoke-$bundle_id"
ubuntu_smoke_name="ubuntu-smoke-$bundle_id"
ubuntu_tool_name="ubuntu-tool-$bundle_id"
wine_tool_name="wine-tool-$bundle_id"
smoke_started=0
ubuntu_smoke_started=0
ubuntu_tool_started=0
wine_tool_started=0
cleanup_failed=0
image_build_attempted=0
ubuntu_image_build_attempted=0

sha256_file() {
  local path=$1 actual
  actual=$(sha256sum -- "$path") || die "unable to hash $path"
  printf 'sha256:%s\n' "${actual%% *}"
}
validate_host_tool_path() {
  local label=$1 path=$2
  [[ "$path" = /* && "$path" =~ ^/[A-Za-z0-9._/+:-]+$ && "$path" != *'..'* ]] || die "$label path is unsafe"
}
detect_remote_host_tools() {
  local os_entry=/etc/os-release os_path id_lines uname_entry uname_path uname_os uname_machine
  [[ -f "$os_entry" && -r "$os_entry" ]] || die 'remote /etc/os-release is not a readable file'
  os_path=$(realpath -e -- "$os_entry") || die 'remote /etc/os-release cannot be resolved'
  [[ -f "$os_path" && ! -L "$os_path" && -r "$os_path" ]] || die 'remote canonical os-release is not a readable regular non-symlink file'
  validate_host_tool_path os_release "$os_path"
  id_lines=$(grep -E '^ID=' "$os_path" || true)
  [[ "$(printf '%s\n' "$id_lines" | sed '/^$/d' | wc -l | tr -d ' ')" == 1 && "$id_lines" == ID=ubuntu ]] || die 'remote OS must contain exactly one ID=ubuntu'
  uname_entry=$(command -v uname) || die 'remote uname executable cannot be located'
  [[ "$uname_entry" = /* ]] || die 'remote uname command path is not absolute'
  uname_path=$(realpath -e -- "$uname_entry") || die 'remote uname executable cannot be resolved'
  [[ -f "$uname_path" && ! -L "$uname_path" && -x "$uname_path" ]] || die 'remote canonical uname is not a regular non-symlink executable'
  validate_host_tool_path uname "$uname_path"
  uname_os=$("$uname_path" -s) || die 'remote uname -s failed'
  uname_machine=$("$uname_path" -m) || die 'remote uname -m failed'
  [[ "$uname_os" == Linux && "$uname_machine" == x86_64 ]] || die 'remote host is not linux/x86_64'
  os_release_path=$os_path
  os_release_identity=$(sha256_file "$os_path")
  uname_executable=$uname_path
  uname_identity=$(sha256_file "$uname_path")
}
detect_remote_host_tools
detect_remote_machine_identity() {
  local machine_entry=/etc/machine-id machine_path machine_actual
  [[ -f "$machine_entry" && -r "$machine_entry" ]] || die 'remote /etc/machine-id is not a readable file'
  machine_path=$(realpath -e -- "$machine_entry") || die 'remote /etc/machine-id cannot be resolved'
  [[ -f "$machine_path" && ! -L "$machine_path" && -r "$machine_path" ]] || die 'remote canonical machine-id is not a readable regular non-symlink file'
  validate_host_tool_path machine_id "$machine_path"
  machine_actual=$(sha256_file "$machine_path")
  [[ "$machine_actual" == "$machine_id_sha256" ]] || die 'remote machine-id identity differs'
  machine_id_path=$machine_path
}
detect_remote_machine_identity
host_tools_identity_file="$stage/.host-tools.identity"
[[ -f "$host_tools_identity_file" && ! -L "$host_tools_identity_file" ]] || die 'preflight host tool identity is missing'
expected_host_tools_identity=$(printf 'os_release_path=%s\nos_release_identity=%s\nmachine_id_path=%s\nmachine_id_sha256=%s\nuname_executable=%s\nuname_identity=%s' \
  "$os_release_path" "$os_release_identity" "$machine_id_path" "$machine_id_sha256" "$uname_executable" "$uname_identity")
[[ "$(cat -- "$host_tools_identity_file")" == "$expected_host_tools_identity" ]] || die 'remote host tool identity changed before install'

cleanup() {
  local status=$? own remaining
  set +e
  if ((smoke_started == 1)) && [[ -x "$remote_docker" ]]; then
    own=$("$remote_docker" ps -aq --filter "name=^${smoke_name}$" --filter "label=com.super-dolphin.deployment-id=$bundle_id" 2>/dev/null || true)
    if [[ -n "$own" ]]; then
      "$remote_docker" rm -f -- "$smoke_name" >/dev/null 2>&1 || cleanup_failed=1
    fi
    remaining=$("$remote_docker" ps -aq --filter "name=^${smoke_name}$" --filter "label=com.super-dolphin.deployment-id=$bundle_id" 2>/dev/null || true)
    [[ -z "$remaining" ]] || cleanup_failed=1
  fi
  if ((ubuntu_smoke_started == 1)) && [[ -x "$remote_docker" ]]; then
    ubuntu_own=$("$remote_docker" ps -aq --filter "name=^${ubuntu_smoke_name}$" --filter "label=com.super-dolphin.deployment-id=$bundle_id" 2>/dev/null || true)
    if [[ -n "$ubuntu_own" ]]; then
      "$remote_docker" rm -f -- "$ubuntu_smoke_name" >/dev/null 2>&1 || cleanup_failed=1
    fi
    ubuntu_remaining=$("$remote_docker" ps -aq --filter "name=^${ubuntu_smoke_name}$" --filter "label=com.super-dolphin.deployment-id=$bundle_id" 2>/dev/null || true)
    [[ -z "$ubuntu_remaining" ]] || cleanup_failed=1
  fi
  if ((ubuntu_tool_started == 1)); then
    tool_id=$("$remote_docker" ps -aq --filter "name=^${ubuntu_tool_name}$" --filter "label=com.super-dolphin.deployment-id=$bundle_id" 2>/dev/null || true)
    if [[ -n "$tool_id" ]]; then "$remote_docker" rm -f -- "$ubuntu_tool_name" >/dev/null 2>&1 || cleanup_failed=1; fi
    [[ -z "$($remote_docker ps -aq --filter "name=^${ubuntu_tool_name}$" --filter "label=com.super-dolphin.deployment-id=$bundle_id" 2>/dev/null || true)" ]] || cleanup_failed=1
  fi
  if ((wine_tool_started == 1)); then
    tool_id=$("$remote_docker" ps -aq --filter "name=^${wine_tool_name}$" --filter "label=com.super-dolphin.deployment-id=$bundle_id" 2>/dev/null || true)
    if [[ -n "$tool_id" ]]; then "$remote_docker" rm -f -- "$wine_tool_name" >/dev/null 2>&1 || cleanup_failed=1; fi
    [[ -z "$($remote_docker ps -aq --filter "name=^${wine_tool_name}$" --filter "label=com.super-dolphin.deployment-id=$bundle_id" 2>/dev/null || true)" ]] || cleanup_failed=1
  fi
  if ((image_build_attempted == 1)) && [[ -x "$remote_docker" ]]; then
    image_label=$("$remote_docker" image inspect --format '{{index .Config.Labels "com.super-dolphin.deployment-id"}}' "$image_ref" 2>/dev/null || true)
    if [[ "$image_label" == "$bundle_id" ]]; then
      "$remote_docker" image rm -f -- "$image_ref" >/dev/null 2>&1 || cleanup_failed=1
      "$remote_docker" image inspect "$image_ref" >/dev/null 2>&1 && cleanup_failed=1 || true
    elif "$remote_docker" image inspect "$image_ref" >/dev/null 2>&1; then
      cleanup_failed=1
    fi
  fi
  if ((ubuntu_image_build_attempted == 1)) && [[ -x "$remote_docker" ]]; then
    ubuntu_image_label=$("$remote_docker" image inspect --format '{{index .Config.Labels "com.super-dolphin.deployment-id"}}' "$ubuntu_image_ref" 2>/dev/null || true)
    if [[ "$ubuntu_image_label" == "$bundle_id" ]]; then
      "$remote_docker" image rm -f -- "$ubuntu_image_ref" >/dev/null 2>&1 || cleanup_failed=1
      "$remote_docker" image inspect "$ubuntu_image_ref" >/dev/null 2>&1 && cleanup_failed=1 || true
    elif "$remote_docker" image inspect "$ubuntu_image_ref" >/dev/null 2>&1; then
      cleanup_failed=1
    fi
  fi
  if [[ -n "$release_tmp" && -e "$release_tmp" ]]; then
    [[ -d "$release_tmp" && ! -L "$release_tmp" ]] && rm -rf -- "$release_tmp" || cleanup_failed=1
  fi
  if ((stage_owned == 1)) && [[ -e "$stage" ]]; then
    if [[ -d "$stage" && ! -L "$stage" && -f "$stage/.owner" && ! -L "$stage/.owner" ]] && \
      [[ "$(cat -- "$stage/.owner" 2>/dev/null)" == "$owner" ]]; then
      rm -rf -- "$stage" || cleanup_failed=1
    else
      cleanup_failed=1
    fi
  fi
  if ((cleanup_failed != 0)); then
    printf 'cleanup_state=failed deployment_id=%s\n' "$bundle_id" >&2
    status=1
  fi
  exit "$status"
}
trap cleanup EXIT

[[ "$install_root" = /* && "$install_root" != / && ! "$install_root" =~ [[:cntrl:][:space:]] ]] || die 'install root is unsafe'
[[ "$stage" == "$install_root/.staging/ubuntu-wine-$bundle_id-"* ]] || die 'staging path drifted'
[[ "$release" == "$install_root/releases/ubuntu-wine-$bundle_id" ]] || die 'release path drifted'
[[ "$bundle_id" =~ ^[a-f0-9]{64}$ ]] || die 'deployment identity is invalid'
mkdir -p "$install_root/.locks"
[[ ! -L "$install_root/.locks" ]] || die 'deployment lock directory is a symlink'
exec 8>"$install_root/.locks/ubuntu-wine-$bundle_id.lock"
flock -n 8 || die 'deployment identity is already active'
[[ "$node_id" =~ ^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$ ]] || die 'remote node identity is invalid'
[[ "$remote_docker_host" =~ ^unix:///.+ ]] || die 'remote Docker host is invalid'
docker_actual_id=$("$remote_docker" info --format '{{.ID}}') || die 'remote Docker server ID cannot be read'
[[ "$docker_actual_id" == "$docker_server_id" ]] || die 'remote Docker server ID differs'
docker_os_arch=$("$remote_docker" info --format '{{.OSType}} {{.Architecture}}') || die 'remote Docker OS/architecture cannot be read'
[[ "$docker_os_arch" == 'linux x86_64' || "$docker_os_arch" == 'linux amd64' ]] || die 'remote Docker is not linux/amd64'
[[ -d "$stage" && ! -L "$stage" ]] || die 'staging directory is missing or unsafe'
[[ -f "$stage/.owner" && ! -L "$stage/.owner" ]] || die 'staging owner marker is missing'
[[ "$(cat -- "$stage/.owner")" == "$owner" ]] || die 'staging owner marker drifted'
for file in Dockerfile.ci-runner-ubuntu Dockerfile.ci-runner-ubuntu-wine linux_runner_daemon.sh super-dolphin-heteroci-node deployment.identity; do
  [[ -f "$stage/$file" && ! -L "$stage/$file" ]] || die "staged file is missing or unsafe: $file"
done
[[ "$(sha256_file "$stage/Dockerfile.ci-runner-ubuntu-wine")" == "$dockerfile_identity" ]] || die 'Dockerfile identity drifted during staging'
[[ "$(sha256_file "$stage/Dockerfile.ci-runner-ubuntu")" == "$ubuntu_dockerfile_identity" ]] || die 'Ubuntu Dockerfile identity drifted during staging'
[[ "$(sha256_file "$stage/linux_runner_daemon.sh")" == "$runner_identity" ]] || die 'runner identity drifted during staging'
[[ "$(sha256_file "$stage/super-dolphin-heteroci-node")" == "$worker_identity" ]] || die 'worker identity drifted during staging'
[[ "$(sha256_file "$stage/deployment.identity")" == "$manifest_identity" ]] || die 'deployment manifest identity drifted during staging'
grep -Fxq 'schema_version=single-ubuntu-wine-deployment/v3' "$stage/deployment.identity" || die 'deployment manifest schema drifted'
grep -Fxq "deployment_id=$bundle_id" "$stage/deployment.identity" || die 'deployment manifest deployment identity drifted'
grep -Fxq "ubuntu_dockerfile_identity=$ubuntu_dockerfile_identity" "$stage/deployment.identity" || die 'deployment manifest Ubuntu Dockerfile identity drifted'
grep -Fxq "dockerfile_identity=$dockerfile_identity" "$stage/deployment.identity" || die 'deployment manifest Dockerfile identity drifted'
grep -Fxq "runner_identity=$runner_identity" "$stage/deployment.identity" || die 'deployment manifest runner identity drifted'
grep -Fxq "worker_identity=$worker_identity" "$stage/deployment.identity" || die 'deployment manifest worker identity drifted'
ubuntu_image_exists=0
if "$remote_docker" image inspect "$ubuntu_image_ref" >/dev/null 2>&1; then ubuntu_image_exists=1; fi
if ((ubuntu_image_exists == 0)); then
  ubuntu_image_build_attempted=1
  "$remote_docker" buildx build --progress=plain --load --platform linux/amd64 \
    --label "com.super-dolphin.deployment-id=$bundle_id" \
    --file "$stage/Dockerfile.ci-runner-ubuntu" --tag "$ubuntu_image_ref" "$stage" >&2
fi
ubuntu_image_id=$("$remote_docker" image inspect --format '{{.Id}}' "$ubuntu_image_ref") || die 'Ubuntu image cannot be inspected'
[[ "$ubuntu_image_id" =~ ^sha256:[a-f0-9]{64}$ ]] || die 'Ubuntu image ID is not immutable'
ubuntu_image_env=$("$remote_docker" image inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$ubuntu_image_ref") || die 'Ubuntu image environment cannot be inspected'
grep -Fxq 'SUPER_DOLPHIN_RUNTIME=ubuntu_container' <<<"$ubuntu_image_env" || die 'Ubuntu image runtime marker drifted'
grep -Fxq 'SUPER_DOLPHIN_TARGET_PLATFORM=linux/amd64' <<<"$ubuntu_image_env" || die 'Ubuntu image target marker drifted'
grep -Fxq 'SUPER_DOLPHIN_AUTHORITY=LOCAL_NON_AUTHORITATIVE' <<<"$ubuntu_image_env" || die 'Ubuntu image authority marker drifted'
grep -Fxq 'SUPER_DOLPHIN_RESULT_STATUS=NOT_VERIFIED' <<<"$ubuntu_image_env" || die 'Ubuntu image result marker drifted'
ubuntu_image_label=$("$remote_docker" image inspect --format '{{index .Config.Labels "com.super-dolphin.deployment-id"}}' "$ubuntu_image_ref") || die 'Ubuntu image deployment label cannot be inspected'
[[ "$ubuntu_image_label" == "$bundle_id" ]] || die 'Ubuntu image deployment label drifted'
[[ -z "$($remote_docker ps -aq --filter "name=^${ubuntu_tool_name}$" 2>/dev/null || true)" ]] || die 'Ubuntu tool probe container name is occupied'
ubuntu_tool_started=1
ubuntu_tool_output=$("$remote_docker" run --rm --name "$ubuntu_tool_name" --label "com.super-dolphin.deployment-id=$bundle_id" --cpus 1 --memory 1073741824 --pids-limit 256 --network none --user 65532:65532 --cap-drop ALL --security-opt no-new-privileges --read-only --tmpfs /tmp:rw,noexec,nosuid,nodev --entrypoint /bin/sh "$ubuntu_image_id" -c 'set -eu; test -f /usr/local/go/bin/go; test ! -L /usr/local/go/bin/go; test -x /usr/local/go/bin/go; sha256sum /usr/local/go/bin/go') || die 'Ubuntu tool identity probe failed'
[[ "$(printf '%s\n' "$ubuntu_tool_output" | wc -l | tr -d ' ')" == 1 ]] || die 'Ubuntu Go tool identity output is not exactly one line'
[[ "$ubuntu_tool_output" =~ ^[a-f0-9]{64}[[:space:]][[:space:]]/usr/local/go/bin/go$ ]] || die 'Ubuntu Go tool identity output drifted'
ubuntu_go_identity=sha256:${ubuntu_tool_output%% *}
[[ -z "$($remote_docker ps -aq --filter "name=^${ubuntu_tool_name}$" --filter "label=com.super-dolphin.deployment-id=$bundle_id" 2>/dev/null || true)" ]] || die 'Ubuntu tool probe cleanup was not proven'
ubuntu_tool_started=0
image_exists=0
if "$remote_docker" image inspect "$image_ref" >/dev/null 2>&1; then image_exists=1; fi
if ((image_exists == 0)); then
  image_build_attempted=1
  "$remote_docker" buildx build --progress=plain --load --platform linux/amd64 \
    --label "com.super-dolphin.deployment-id=$bundle_id" \
    --file "$stage/Dockerfile.ci-runner-ubuntu-wine" --tag "$image_ref" "$stage" >&2
fi
image_id=$("$remote_docker" image inspect --format '{{.Id}}' "$image_ref") || die 'built image cannot be inspected'
[[ "$image_id" =~ ^sha256:[a-f0-9]{64}$ ]] || die 'built image ID is not immutable'
image_env=$("$remote_docker" image inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$image_ref") || die 'built image environment cannot be inspected'
grep -Fxq 'SUPER_DOLPHIN_RUNTIME=ubuntu_wine_container' <<<"$image_env" || die 'built image runtime marker drifted'
grep -Fxq 'SUPER_DOLPHIN_TARGET_PLATFORM=windows/amd64' <<<"$image_env" || die 'built image target marker drifted'
grep -Fxq 'SUPER_DOLPHIN_AUTHORITY=LOCAL_NON_AUTHORITATIVE' <<<"$image_env" || die 'built image authority marker drifted'
grep -Fxq 'SUPER_DOLPHIN_RESULT_STATUS=NOT_VERIFIED' <<<"$image_env" || die 'built image result status marker drifted'
if ((image_build_attempted == 1)); then
  image_label=$("$remote_docker" image inspect --format '{{index .Config.Labels "com.super-dolphin.deployment-id"}}' "$image_ref") || die 'built image deployment label cannot be inspected'
  [[ "$image_label" == "$bundle_id" ]] || die 'built image deployment label drifted'
fi
image_label=$("$remote_docker" image inspect --format '{{index .Config.Labels "com.super-dolphin.deployment-id"}}' "$image_ref") || die 'current image deployment label cannot be inspected'
[[ "$image_label" == "$bundle_id" ]] || die 'current image deployment label drifted'
[[ -z "$($remote_docker ps -aq --filter "name=^${wine_tool_name}$" 2>/dev/null || true)" ]] || die 'Wine tool probe container name is occupied'
wine_tool_started=1
wine_tool_output=$("$remote_docker" run --rm --name "$wine_tool_name" --label "com.super-dolphin.deployment-id=$bundle_id" --cpus 1 --memory 1073741824 --pids-limit 256 --network none --user 65532:65532 --cap-drop ALL --security-opt no-new-privileges --read-only --tmpfs /tmp:rw,noexec,nosuid,nodev --entrypoint /bin/sh "$image_id" -c 'set -eu; test -f /usr/local/go/bin/go; test ! -L /usr/local/go/bin/go; test -x /usr/local/go/bin/go; test -f /usr/lib/wine/wine64; test ! -L /usr/lib/wine/wine64; test -x /usr/lib/wine/wine64; test -f /usr/lib/wine/wineserver64; test ! -L /usr/lib/wine/wineserver64; test -x /usr/lib/wine/wineserver64; sha256sum /usr/local/go/bin/go /usr/lib/wine/wine64 /usr/lib/wine/wineserver64') || die 'Wine tool identity probe failed'
[[ "$(printf '%s\n' "$wine_tool_output" | wc -l | tr -d ' ')" == 3 ]] || die 'Wine tool identity output is not exactly three lines'
while IFS= read -r tool_line; do
  [[ "$tool_line" =~ ^[a-f0-9]{64}[[:space:]][[:space:]](/usr/local/go/bin/go|/usr/lib/wine/wine64|/usr/lib/wine/wineserver64)$ ]] || die 'Wine tool identity output drifted'
done <<<"$wine_tool_output"
wine_go_identity=sha256:$(printf '%s\n' "$wine_tool_output" | awk '$2=="/usr/local/go/bin/go" {print $1}')
wine_real_identity=sha256:$(printf '%s\n' "$wine_tool_output" | awk '$2=="/usr/lib/wine/wine64" {print $1}')
wine_server_identity=sha256:$(printf '%s\n' "$wine_tool_output" | awk '$2=="/usr/lib/wine/wineserver64" {print $1}')
[[ "$wine_go_identity" =~ ^sha256:[a-f0-9]{64}$ && "$wine_real_identity" =~ ^sha256:[a-f0-9]{64}$ && "$wine_server_identity" =~ ^sha256:[a-f0-9]{64}$ ]] || die 'Wine tool identities are invalid'
[[ "$wine_go_identity" == "$ubuntu_go_identity" ]] || die 'Linux/Wine Go tool identities differ'
[[ -z "$($remote_docker ps -aq --filter "name=^${wine_tool_name}$" --filter "label=com.super-dolphin.deployment-id=$bundle_id" 2>/dev/null || true)" ]] || die 'Wine tool probe cleanup was not proven'
wine_tool_started=0
if [[ -e "$release" ]]; then
  [[ -f "$release/ubuntu-go.identity" && ! -L "$release/ubuntu-go.identity" ]] || die 'existing Ubuntu Go identity is missing before smoke'
  expected_ubuntu_tool=$(printf 'path=/usr/local/go/bin/go\nidentity=%s' "$ubuntu_go_identity")
  [[ "$(cat "$release/ubuntu-go.identity")" == "$expected_ubuntu_tool" ]] || die 'existing Ubuntu Go identity changed before smoke'
  [[ -f "$release/wine-tools.identity" && ! -L "$release/wine-tools.identity" ]] || die 'existing Wine tool identity is missing before smoke'
  expected_wine_tools=$(printf 'go_path=/usr/local/go/bin/go\ngo_identity=%s\nwine_path=/usr/lib/wine/wine64\nwine_identity=%s\nwineserver_path=/usr/lib/wine/wineserver64\nwineserver_identity=%s' "$wine_go_identity" "$wine_real_identity" "$wine_server_identity")
  [[ "$(cat "$release/wine-tools.identity")" == "$expected_wine_tools" ]] || die 'existing Wine tool identity changed before smoke'
fi
ubuntu_smoke_started=1
ubuntu_smoke_output=$("$remote_docker" run --rm --name "$ubuntu_smoke_name" --label "com.super-dolphin.deployment-id=$bundle_id" \
  --cpus 1 --memory 1073741824 --pids-limit 256 --network none --user 65532:65532 --cap-drop ALL \
  --security-opt no-new-privileges --read-only --tmpfs /tmp:rw,noexec,nosuid,nodev --entrypoint /bin/bash "$ubuntu_image_id" \
  -Eeuo pipefail -c 'test "$(/usr/local/go/bin/go version)" = "go version go1.26.5 linux/amd64"; test "$(/usr/local/bin/node --version)" = "v22.13.0"; test "$SUPER_DOLPHIN_RUNTIME" = "ubuntu_container"; printf "%s\\n" "linux-amd64|smoke-ok"') || die 'Ubuntu runtime smoke failed'
[[ "$ubuntu_smoke_output" == 'linux-amd64|smoke-ok' ]] || die 'Ubuntu runtime smoke output drifted'
ubuntu_smoke_started=0
if [[ -e "$release" ]]; then
  [[ -d "$release" && ! -L "$release" ]] || die 'existing release is not a directory during install'
  [[ -f "$release/deployment.identity" && ! -L "$release/deployment.identity" ]] || die 'existing release manifest is missing during install'
  [[ "$(sha256_file "$release/deployment.identity")" == "$manifest_identity" ]] || die 'existing release manifest changed during install'
  [[ "$(sha256_file "$release/Dockerfile.ci-runner-ubuntu-wine")" == "$dockerfile_identity" ]] || die 'existing release Dockerfile changed during install'
  [[ "$(sha256_file "$release/Dockerfile.ci-runner-ubuntu")" == "$ubuntu_dockerfile_identity" ]] || die 'existing release Ubuntu Dockerfile changed during install'
  [[ "$(sha256_file "$release/linux_runner_daemon.sh")" == "$runner_identity" ]] || die 'existing release runner changed during install'
  [[ "$(sha256_file "$release/super-dolphin-heteroci-node")" == "$worker_identity" ]] || die 'existing release worker changed during install'
  [[ -f "$release/image.identity" && ! -L "$release/image.identity" ]] || die 'existing release image identity is missing during install'
  [[ "$(cat "$release/image.identity")" == "image_id=$image_id" ]] || die 'existing release image identity is not exact'
  [[ "$(cat "$release/ubuntu-image.identity")" == "image_id=$ubuntu_image_id" ]] || die 'existing release Ubuntu image identity is not exact'
fi
existing_smoke=$("$remote_docker" ps -aq --filter "name=^${smoke_name}$" 2>/dev/null || true)
[[ -z "$existing_smoke" ]] || die 'smoke container name is already occupied'
smoke_started=1
smoke_output=$("$remote_docker" run --rm --name "$smoke_name" \
  --label "com.super-dolphin.deployment-id=$bundle_id" \
  --cpus 1 --memory 1073741824 --pids-limit 256 \
  --network none --user 65532:65532 --cap-drop ALL \
  --security-opt no-new-privileges --read-only --tmpfs /tmp:rw,noexec,nosuid,nodev \
  --entrypoint /bin/sh "$image_id" -c 'set -eu; rm -rf /tmp/wine-prefix; mkdir -m 0700 /tmp/wine-prefix; cp -a /home/ci-runner/.wine64/. /tmp/wine-prefix/; printf "%s\\n" "wine-qualification:prefix_copy" >&2; export WINEARCH=win64 WINEPREFIX=/tmp/wine-prefix WINELOADER=/usr/lib/wine/wine64 WINESERVER=/usr/lib/wine/wineserver64; test "$(/usr/bin/dpkg-query -W -f='"'"'${Version}'"'"' wine64)" = "9.0~repack-4build3"; printf "%s\\n" "wine-qualification:version" >&2; test -x /opt/super-dolphin/smoke/wine-smoke.exe; /usr/bin/xvfb-run --auto-servernum --server-args='"'"'-screen 0 1024x768x24 -nolisten tcp'"'"' /usr/lib/wine/wine64 /opt/super-dolphin/smoke/wine-smoke.exe >&2; printf "%s\\n" "wine-qualification:pe" >&2; /usr/lib/wine/wineserver64 -k >&2; printf "%s\\n" "wine-qualification:kill" >&2; /usr/lib/wine/wineserver64 -w >&2; printf "%s\\n" "wine-qualification:wait" >&2; printf "%s\n" "wine-9.0|smoke-ok"') || die 'Wine qualification smoke failed'
[[ "$smoke_output" == 'wine-9.0|smoke-ok' ]] || die 'Wine qualification output drifted'
remaining_smoke=$("$remote_docker" ps -aq --filter "name=^${smoke_name}$" --filter "label=com.super-dolphin.deployment-id=$bundle_id" 2>/dev/null || true)
[[ -z "$remaining_smoke" ]] || die 'smoke container cleanup was not proven'
smoke_started=0

if [[ -e "$release" ]]; then
  [[ -d "$release" && ! -L "$release" ]] || die 'existing release is not a directory'
  [[ "$(sha256_file "$release/Dockerfile.ci-runner-ubuntu-wine")" == "$dockerfile_identity" ]] || die 'existing release Dockerfile identity drifted'
  [[ "$(sha256_file "$release/Dockerfile.ci-runner-ubuntu")" == "$ubuntu_dockerfile_identity" ]] || die 'existing release Ubuntu Dockerfile identity drifted'
  [[ "$(sha256_file "$release/linux_runner_daemon.sh")" == "$runner_identity" ]] || die 'existing release runner identity drifted'
  [[ "$(sha256_file "$release/super-dolphin-heteroci-node")" == "$worker_identity" ]] || die 'existing release worker identity drifted'
  [[ -f "$release/image.identity" && ! -L "$release/image.identity" ]] || die 'existing release image identity is missing'
  [[ "$(<"$release/image.identity")" == "image_id=$image_id" ]] || die 'existing release image identity drifted'
  [[ -f "$release/ubuntu-image.identity" && ! -L "$release/ubuntu-image.identity" ]] || die 'existing release Ubuntu image identity is missing'
  [[ "$(<"$release/ubuntu-image.identity")" == "image_id=$ubuntu_image_id" ]] || die 'existing release Ubuntu image identity drifted'
  [[ -f "$release/ubuntu-go.identity" && ! -L "$release/ubuntu-go.identity" ]] || die 'existing Ubuntu Go identity is missing'
  expected_ubuntu_go_identity=$(printf 'path=/usr/local/go/bin/go\nidentity=%s' "$ubuntu_go_identity")
  [[ "$(<"$release/ubuntu-go.identity")" == "$expected_ubuntu_go_identity" ]] || die 'existing Ubuntu Go identity drifted'
  [[ -f "$release/wine-tools.identity" && ! -L "$release/wine-tools.identity" ]] || die 'existing Wine tool identity is missing'
  expected_wine_tools_identity=$(printf 'go_path=/usr/local/go/bin/go\ngo_identity=%s\nwine_path=/usr/lib/wine/wine64\nwine_identity=%s\nwineserver_path=/usr/lib/wine/wineserver64\nwineserver_identity=%s' "$wine_go_identity" "$wine_real_identity" "$wine_server_identity")
  [[ "$(<"$release/wine-tools.identity")" == "$expected_wine_tools_identity" ]] || die 'existing Wine tool identity drifted'
else
  release_parent=${release%/*}
  [[ -d "$release_parent" && ! -L "$release_parent" ]] || die 'release parent is missing or unsafe'
  release_tmp="$release_parent/.ubuntu-wine-$bundle_id.$$.tmp"
  [[ ! -e "$release_tmp" ]] || die 'release staging path already exists'
  mkdir -- "$release_tmp"
  install -m 0644 "$stage/Dockerfile.ci-runner-ubuntu" "$release_tmp/Dockerfile.ci-runner-ubuntu"
  install -m 0644 "$stage/Dockerfile.ci-runner-ubuntu-wine" "$release_tmp/Dockerfile.ci-runner-ubuntu-wine"
  install -m 0755 "$stage/linux_runner_daemon.sh" "$release_tmp/linux_runner_daemon.sh"
  install -m 0755 "$stage/super-dolphin-heteroci-node" "$release_tmp/super-dolphin-heteroci-node"
  install -m 0644 "$stage/deployment.identity" "$release_tmp/deployment.identity"
  printf 'image_id=%s\n' "$image_id" >"$release_tmp/image.identity"
  printf 'image_id=%s\n' "$ubuntu_image_id" >"$release_tmp/ubuntu-image.identity"
  printf 'path=/usr/local/go/bin/go\nidentity=%s\n' "$ubuntu_go_identity" >"$release_tmp/ubuntu-go.identity"
  printf 'go_path=/usr/local/go/bin/go\ngo_identity=%s\nwine_path=/usr/lib/wine/wine64\nwine_identity=%s\nwineserver_path=/usr/lib/wine/wineserver64\nwineserver_identity=%s\n' "$wine_go_identity" "$wine_real_identity" "$wine_server_identity" >"$release_tmp/wine-tools.identity"
  [[ "$(sha256_file "$release_tmp/Dockerfile.ci-runner-ubuntu-wine")" == "$dockerfile_identity" ]] || die 'installed Dockerfile identity drifted'
  [[ "$(sha256_file "$release_tmp/linux_runner_daemon.sh")" == "$runner_identity" ]] || die 'installed runner identity drifted'
  [[ "$(sha256_file "$release_tmp/super-dolphin-heteroci-node")" == "$worker_identity" ]] || die 'installed worker identity drifted'
  mv -- "$release_tmp" "$release"
  release_tmp=''
fi
image_build_attempted=0
ubuntu_image_build_attempted=0
printf 'DEPLOYMENT_RECEIPT_JSON={"schema_version":"single-ubuntu-wine-deployment-receipt/v6","status":"deployed","deployment_id":"%s","node_id":"%s","ssh_connect_timeout_seconds":%s,"ssh_control_persist_seconds":%s,"known_hosts_identity":"%s","machine_id_sha256":"%s","machine_id_path":"%s","remote_docker_host":"%s","docker_server_id":"%s","host_os_id":"ubuntu","os_release_path":"%s","os_release_identity":"%s","uname_executable":"%s","uname_identity":"%s","linux_runtime":"ubuntu_container","linux_target_platform":"linux/amd64","linux_image_id":"%s","wine_runtime":"ubuntu_wine_container","wine_target_platform":"windows/amd64","wine_image_id":"%s","authority":"LOCAL_NON_AUTHORITATIVE","result_status":"NOT_VERIFIED","ubuntu_dockerfile_identity":"%s","wine_dockerfile_identity":"%s","runner_identity":"%s","worker_identity":"%s","probe_path":"%s/super-dolphin-heteroci-node","runner_path":"%s/linux_runner_daemon.sh","worker_path":"%s/super-dolphin-heteroci-node","go_path":"/usr/local/go/bin/go","go_identity":"%s","wine_path":"/usr/lib/wine/wine64","wine_identity":"%s","wineserver_path":"/usr/lib/wine/wineserver64","wineserver_identity":"%s","object_store_root":"%s","worktree_root":"%s","cache_root":"%s","private_overlay_root":"%s","temp_root":"%s","wine_prefix_root":"%s","cleanup_state":"verified"}\n' \
  "$bundle_id" "$node_id" "$ssh_connect_timeout_seconds" "$ssh_control_persist_seconds" "$known_hosts_identity" "$machine_id_sha256" "$machine_id_path" "$remote_docker_host" "$docker_server_id" "$os_release_path" "$os_release_identity" "$uname_executable" "$uname_identity" "$ubuntu_image_id" "$image_id" "$ubuntu_dockerfile_identity" "$dockerfile_identity" "$runner_identity" "$worker_identity" "$release" "$release" "$release" "$ubuntu_go_identity" "$wine_real_identity" "$wine_server_identity" "$object_store_root" "$worktree_root" "$cache_root" "$private_overlay_root" "$temp_root" "$wine_prefix_root"
REMOTE_INSTALL
chmod 700 "$local_tmp/remote_install.sh"

run_remote_logged install "$local_tmp/remote_install.sh" \
  "$install_root" "$remote_stage" "$remote_release" "$image_ref" "$remote_docker_executable" "$bundle_id" \
  "$ubuntu_dockerfile_identity" "$dockerfile_identity" "$runner_identity" "$worker_identity" "$manifest_identity" \
  "$node_id" "$machine_id_sha256" "$docker_server_id" "$remote_docker_host" \
  "$object_store_root" "$worktree_root" "$cache_root" "$private_overlay_root" "$temp_root" "$wine_prefix_root" "$known_hosts_identity" \
  "$ssh_connect_timeout_seconds" "$ssh_control_persist_seconds"

receipt_line_count=$(wc -l <"$last_remote_stdout" | tr -d ' ')
[[ "$receipt_line_count" == 1 ]] || die 'remote deployment did not return exactly one canonical receipt'
IFS= read -r receipt_line <"$last_remote_stdout" || die 'remote deployment receipt cannot be read'
[[ "$receipt_line" == DEPLOYMENT_RECEIPT_JSON=* ]] || die 'remote deployment stdout contains a non-receipt line'
receipt=${receipt_line#DEPLOYMENT_RECEIPT_JSON=}
json_field() { printf '%s\n' "$receipt" | sed -n "s/.*\"$1\":\"\\([^\"]*\\)\".*/\\1/p"; }
ubuntu_image_id=$(json_field linux_image_id)
image_id=$(json_field wine_image_id)
ubuntu_go_identity=$(json_field go_identity)
wine_go_identity=$(json_field go_identity)
wine_real_identity=$(json_field wine_identity)
wine_server_identity=$(json_field wineserver_identity)
for tool_identity in "$ubuntu_image_id" "$image_id" "$ubuntu_go_identity" "$wine_go_identity" "$wine_real_identity" "$wine_server_identity"; do
  [[ "$tool_identity" =~ ^sha256:[a-f0-9]{64}$ ]] || die 'receipt contains a non-canonical tool/image identity'
done
os_release_path=$(json_field os_release_path)
os_release_identity=$(json_field os_release_identity)
machine_id_path=$(json_field machine_id_path)
uname_executable=$(json_field uname_executable)
uname_identity=$(json_field uname_identity)
validate_remote_receipt_path 'receipt os_release_path' "$os_release_path"
validate_remote_receipt_path 'receipt machine_id_path' "$machine_id_path"
validate_remote_receipt_path 'receipt uname_executable' "$uname_executable"
for host_tool_identity in "$os_release_identity" "$uname_identity"; do
  [[ "$host_tool_identity" =~ ^sha256:[a-f0-9]{64}$ ]] || die 'receipt contains a non-canonical host tool identity'
done
expected_receipt=$(printf '{"schema_version":"single-ubuntu-wine-deployment-receipt/v6","status":"deployed","deployment_id":"%s","node_id":"%s","ssh_connect_timeout_seconds":%s,"ssh_control_persist_seconds":%s,"known_hosts_identity":"%s","machine_id_sha256":"%s","machine_id_path":"%s","remote_docker_host":"%s","docker_server_id":"%s","host_os_id":"ubuntu","os_release_path":"%s","os_release_identity":"%s","uname_executable":"%s","uname_identity":"%s","linux_runtime":"ubuntu_container","linux_target_platform":"linux/amd64","linux_image_id":"%s","wine_runtime":"ubuntu_wine_container","wine_target_platform":"windows/amd64","wine_image_id":"%s","authority":"LOCAL_NON_AUTHORITATIVE","result_status":"NOT_VERIFIED","ubuntu_dockerfile_identity":"%s","wine_dockerfile_identity":"%s","runner_identity":"%s","worker_identity":"%s","probe_path":"%s/super-dolphin-heteroci-node","runner_path":"%s/linux_runner_daemon.sh","worker_path":"%s/super-dolphin-heteroci-node","go_path":"/usr/local/go/bin/go","go_identity":"%s","wine_path":"/usr/lib/wine/wine64","wine_identity":"%s","wineserver_path":"/usr/lib/wine/wineserver64","wineserver_identity":"%s","object_store_root":"%s","worktree_root":"%s","cache_root":"%s","private_overlay_root":"%s","temp_root":"%s","wine_prefix_root":"%s","cleanup_state":"verified"}' \
  "$bundle_id" "$node_id" "$ssh_connect_timeout_seconds" "$ssh_control_persist_seconds" "$known_hosts_identity" "$machine_id_sha256" "$machine_id_path" "$remote_docker_host" "$docker_server_id" "$os_release_path" "$os_release_identity" "$uname_executable" "$uname_identity" "$ubuntu_image_id" "$image_id" "$ubuntu_dockerfile_identity" "$dockerfile_identity" "$runner_identity" "$worker_identity" "$remote_release" "$remote_release" "$remote_release" "$ubuntu_go_identity" "$wine_real_identity" "$wine_server_identity" "$object_store_root" "$worktree_root" "$cache_root" "$private_overlay_root" "$temp_root" "$wine_prefix_root")
[[ "$receipt" == "$expected_receipt" ]] || die 'remote deployment receipt is not canonical'
control_master_close || die 'ControlMaster cleanup failed before receipt publication'
remote_stage_owned=0
printf '%s' "$receipt"
