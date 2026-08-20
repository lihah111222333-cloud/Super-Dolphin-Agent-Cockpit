#!/usr/bin/env bash
set -Eeuo pipefail

export LOCAL_NON_AUTHORITATIVE=1
export NOT_VERIFIED=1
export SUPER_DOLPHIN_AUTHORITY=LOCAL_NON_AUTHORITATIVE
export SUPER_DOLPHIN_RESULT_STATUS=NOT_VERIFIED

die() { printf 'linux_runner_daemon: %s LOCAL_NON_AUTHORITATIVE=1 NOT_VERIFIED=1\n' "$*" >&2; exit 2; }

repo_path=''
runner_id='linux-host'
mode='native'
timeout_seconds=120
alternates=''
command_args=()
docker_image=''
docker_image_seen=0
runtime=''
runtime_seen=0
docker_runtime_marker=''
readonly max_int64=9223372036854775807
require_bounded_positive_integer() {
  local value=$1 label=$2
  [[ "$value" =~ ^[1-9][0-9]*$ ]] || die "$label must be a positive decimal integer"
  if (( ${#value} > ${#max_int64} )); then
    die "$label overflows signed integer"
  fi
  if (( ${#value} == ${#max_int64} )) && [[ "$value" > "$max_int64" ]]; then
    die "$label overflows signed integer"
  fi
}

while (($#)); do
  case "$1" in
    --repo)
      (($# >= 2)) || die '--repo requires a value'
      repo_path=$2
      shift 2
      ;;
    --runner-id)
      (($# >= 2)) || die '--runner-id requires a value'
      runner_id=$2
      shift 2
      ;;
    --mode)
      (($# >= 2)) || die '--mode requires a value'
      mode=$2
      shift 2
      ;;
    --runtime)
      (($# >= 2)) || die '--runtime requires a value'
      ((runtime_seen == 0)) || die '--runtime must be provided once'
      runtime=$2
      runtime_seen=1
      shift 2
      ;;
    --docker-image)
      (($# >= 2)) || die '--docker-image requires a value'
      ((docker_image_seen == 0)) || die '--docker-image must be provided once'
      docker_image=$2
      docker_image_seen=1
      shift 2
      ;;
    --timeout-seconds)
      (($# >= 2)) || die '--timeout-seconds requires a value'
      timeout_seconds=$2
      shift 2
      ;;
    --alternates)
      (($# >= 2)) || die '--alternates requires a value'
      alternates=$2
      shift 2
      ;;
    --)
      shift
      command_args=("$@")
      break
      ;;
    -h|--help)
      printf '%s\n' 'linux_runner_daemon.sh --repo PATH [--mode native|docker] [--runtime ubuntu|darling-compat] [--docker-image REPOSITORY@sha256:DIGEST] [-- COMMAND...]'
      exit 0
      ;;
    *)
      die "unknown option: $1"
      ;;
  esac
done

[[ -n "$repo_path" && "$repo_path" = /* && -d "$repo_path" ]] || die 'an absolute existing --repo is required'
[[ "$mode" == native || "$mode" == docker ]] || die '--mode must be native or docker'
if [[ "$mode" == docker ]]; then
  ((runtime_seen == 1)) || die '--runtime is required for docker mode'
  ((docker_image_seen == 1)) || die '--docker-image is required for docker mode'
  case "$runtime" in
	ubuntu)
	  [[ "$docker_image" =~ ^super-dolphin-ci-runner:ubuntu@sha256:[a-f0-9]{64}$ ]] || die 'ubuntu runtime requires --docker-image super-dolphin-ci-runner:ubuntu@sha256:<64 lowercase hex>'
	  docker_runtime_marker=ubuntu_container
      ;;
    darling-compat)
      [[ "$docker_image" =~ ^super-dolphin-ci-runner:darwin-compat@sha256:[a-f0-9]{64}$ ]] || die 'darling-compat runtime requires --docker-image super-dolphin-ci-runner:darwin-compat@sha256:<64 lowercase hex>'
      docker_runtime_marker=darling_compat_runtime
      ;;
    *)
	  die '--runtime must be ubuntu or darling-compat'
	  ;;
	esac
	export SUPER_DOLPHIN_RUNTIME="$docker_runtime_marker"
else
	((runtime_seen == 0)) || die '--runtime is forbidden in native mode'
	((docker_image_seen == 0)) || die '--docker-image is forbidden in native mode'
	unset SUPER_DOLPHIN_RUNTIME
fi
require_bounded_positive_integer "$timeout_seconds" '--timeout-seconds'
[[ "$timeout_seconds" -le 120 ]] || die '--timeout-seconds must be in 1..120'
[[ "$(uname -s)" == Linux ]] || die 'daemon only runs on Linux'
[[ ${#command_args[@]} -gt 0 ]] || die 'a command is required after --'
command -v git >/dev/null 2>&1 || die 'git is required'
cgroup_root='/sys/fs/cgroup'
[[ -r "$cgroup_root/cgroup.controllers" ]] || die 'cgroup v2 is required'
git -C "$repo_path" rev-parse --git-dir >/dev/null 2>&1 || die 'unable to materialize Git repository identity'
read_cgroup_file() {
  local path=$1 label=$2 status
  [[ -r "$path" ]] || die "$label is missing or unreadable"
  cgroup_file_value=''
  if IFS= read -r -d '' cgroup_file_value < "$path"; then
    die "$label contains a NUL byte"
  else
    status=$?
    (( status == 1 )) || die "$label cannot be read"
  fi
}
read_cgroup_memory_bytes() {
  local value
  read_cgroup_file "$cgroup_root/memory.max" 'cgroup memory.max'
  value=$cgroup_file_value
  if [[ "$value" == *$'\n' ]]; then
    value=${value%$'\n'}
  fi
  [[ "$value" != *$'\n'* && "$value" != *$'\r'* ]] || die 'cgroup memory.max has extra lines'
  require_bounded_positive_integer "$value" 'cgroup memory.max'
  printf '%s\n' "$value"
}
read_cgroup_cpu_milli() {
	local raw quota period milli
	read_cgroup_file "$cgroup_root/cpu.max" 'cgroup cpu.max'
  raw=$cgroup_file_value
  if [[ "$raw" == *$'\n' ]]; then
    raw=${raw%$'\n'}
  fi
  [[ "$raw" != *$'\n'* && "$raw" != *$'\r'* ]] || die 'cgroup cpu.max has extra lines'
  [[ "$raw" =~ ^([1-9][0-9]*)[[:blank:]]+([1-9][0-9]*)$ ]] || die 'cgroup cpu.max must contain exactly positive quota and period'
  quota=${BASH_REMATCH[1]}
  period=${BASH_REMATCH[2]}
	require_bounded_positive_integer "$quota" 'cgroup cpu.max quota'
	require_bounded_positive_integer "$period" 'cgroup cpu.max period'
	((quota <= max_int64 / 1000)) || die 'cgroup cpu.max quota overflows millicore precision'
	milli=$(( quota * 1000 / period ))
	[[ "$milli" =~ ^[1-9][0-9]*$ ]] || die 'cgroup CPU probe returned less than one millicore'
	printf '%s\n' "$milli"
}

format_cpu_milli() {
	local value=$1 whole fraction fraction_text
	whole=$((value / 1000))
	fraction=$((value % 1000))
	if ((fraction == 0)); then
	  printf '%s\n' "$whole"
	  return 0
	fi
	fraction_text=$(printf '%03d' "$fraction")
	while [[ "$fraction_text" == *0 ]]; do
	  fraction_text=${fraction_text%0}
	done
	printf '%s.%s\n' "$whole" "$fraction_text"
}

cpu_milli=$(read_cgroup_cpu_milli)
docker_cpu=$(format_cpu_milli "$cpu_milli")
logical_cpu=$(( cpu_milli / 1000 ))
if ((cpu_milli % 1000 != 0)); then
  logical_cpu=$(( logical_cpu + 1 ))
fi
memory_bytes=$(read_cgroup_memory_bytes)
[[ "$memory_bytes" =~ ^[1-9][0-9]*$ ]] || die 'dynamic cgroup memory probe returned zero'

if [[ -n "$alternates" ]]; then
  [[ -d "$alternates" ]] || die '--alternates must name an existing directory'
  export GIT_ALTERNATE_OBJECT_DIRECTORIES="$alternates"
fi

if [[ "$mode" == docker ]]; then
  command -v docker >/dev/null 2>&1 || die 'docker is required for docker mode'
  docker info >/dev/null 2>&1 || die 'docker daemon is unavailable'
fi

if [[ "$mode" == docker ]]; then
  printf 'runner_id=%s mode=%s runtime=%s cgroup_v2=1 cpu_milli=%s logical_cpu_ceiling=%s memory_bytes=%s authority=LOCAL_NON_AUTHORITATIVE status=NOT_VERIFIED LOCAL_NON_AUTHORITATIVE=1 NOT_VERIFIED=1\n' \
    "$runner_id" "$mode" "$docker_runtime_marker" "$cpu_milli" "$logical_cpu" "$memory_bytes"
else
  printf 'runner_id=%s mode=%s cgroup_v2=1 cpu_milli=%s logical_cpu_ceiling=%s memory_bytes=%s authority=LOCAL_NON_AUTHORITATIVE status=NOT_VERIFIED LOCAL_NON_AUTHORITATIVE=1 NOT_VERIFIED=1\n' \
    "$runner_id" "$mode" "$cpu_milli" "$logical_cpu" "$memory_bytes"
fi

linux_process_group_exists() {
  local group_id=$1
  kill -0 -- "-$group_id" 2>/dev/null
}

signal_linux_process_group() {
  local group_id=$1 signal_name=$2
  if kill "-$signal_name" -- "-$group_id" 2>/dev/null; then
    return 0
  fi
  ! linux_process_group_exists "$group_id"
}

wait_linux_process_group_gone() {
  local group_id=$1 limit=$2 elapsed=0
  while linux_process_group_exists "$group_id"; do
    ((elapsed < limit)) || return 1
    sleep 1
    elapsed=$((elapsed + 1))
  done
}

terminate_linux_process_group() {
  local group_id=$1
  signal_linux_process_group "$group_id" TERM || return 1
  if wait_linux_process_group_gone "$group_id" 2; then
    return 0
  fi
  signal_linux_process_group "$group_id" KILL || return 1
  wait_linux_process_group_gone "$group_id" 2
}

run_command() {
	local child_pid status elapsed timed_out leader_exited ownership_probe_elapsed cleanup_failed
	local -a argv
  if [[ "$mode" == native ]]; then
    argv=("${command_args[@]}")
  else
	argv=(docker run --rm --platform linux/amd64 --cpus "$docker_cpu" --memory "$memory_bytes" -v "$repo_path:/workspace:rw" -w /workspace \
	  -e LOCAL_NON_AUTHORITATIVE=1 -e NOT_VERIFIED=1 \
	  -e SUPER_DOLPHIN_AUTHORITY=LOCAL_NON_AUTHORITATIVE -e SUPER_DOLPHIN_RESULT_STATUS=NOT_VERIFIED \
	  -e "SUPER_DOLPHIN_RUNTIME=$docker_runtime_marker" \
      "$docker_image" "${command_args[@]}")
  fi
  command -v setsid >/dev/null 2>&1 || die 'setsid is required for process-group ownership'
  setsid "${argv[@]}" &
  child_pid=$!
  ownership_probe_elapsed=0
  while kill -0 "$child_pid" 2>/dev/null && ! linux_process_group_exists "$child_pid"; do
    if ((ownership_probe_elapsed >= 1)); then
      kill -KILL "$child_pid" 2>/dev/null || true
      wait "$child_pid" 2>/dev/null || true
      die 'Capacity/ownership blocker: command did not acquire an independent process group'
    fi
    sleep 1
    ownership_probe_elapsed=$((ownership_probe_elapsed + 1))
  done

  elapsed=0
  timed_out=0
  leader_exited=0
  cleanup_failed=0
  while linux_process_group_exists "$child_pid"; do
    if ! kill -0 "$child_pid" 2>/dev/null; then
      leader_exited=1
      break
    fi
    if ((elapsed >= timeout_seconds)); then
      timed_out=1
      break
    fi
    sleep 1
    elapsed=$((elapsed + 1))
  done
  if ((timed_out != 0 || leader_exited != 0)); then
    terminate_linux_process_group "$child_pid" || cleanup_failed=1
  fi
  if wait "$child_pid"; then
    status=0
  else
    status=$?
  fi
  if ((cleanup_failed != 0)); then
    die 'Capacity/ownership blocker: process-group cleanup failed'
  fi
  if linux_process_group_exists "$child_pid"; then
    die 'Capacity/ownership blocker: process group remains after cleanup'
  fi
  if ((leader_exited != 0)); then
    die 'Capacity/ownership blocker: command exited with process-group descendants'
  fi
  return "$status"
}

run_command
