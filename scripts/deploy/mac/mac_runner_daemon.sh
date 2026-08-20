#!/usr/bin/env bash
set -Eeuo pipefail

export LOCAL_NON_AUTHORITATIVE=1
export NOT_VERIFIED=1
export SUPER_DOLPHIN_AUTHORITY=LOCAL_NON_AUTHORITATIVE
export SUPER_DOLPHIN_RESULT_STATUS=NOT_VERIFIED

die() { printf 'mac_runner_daemon: %s LOCAL_NON_AUTHORITATIVE=1 NOT_VERIFIED=1\n' "$*" >&2; exit 2; }

repo_path=''
runner_id='darwin-host'
mode='native'
timeout_seconds=120
alternates=''
alternates_seen=0
command_args=()
docker_image=''
docker_image_seen=0
docker_cpu=''
docker_memory=''
docker_cpu_info=''
docker_memory_info=''
cgroup_cpu_quota=''
cgroup_cpu_period=''
cgroup_memory=''
cgroup_cpu_milli=0
effective_cpu_milli=0
effective_memory=0
docker_cid_directory=''
docker_cidfile=''
docker_wait_file=''
docker_container_exit_code=''
docker_container_name=''
docker_client_exit_code=''
native_runtime_marker=darwin_native
docker_runtime_marker=darling_compat_runtime
active_runtime_marker=''
readonly docker_startup_timeout_seconds=5
readonly docker_control_timeout_seconds=5

readonly max_int64=9223372036854775807

is_canonical_docker_image() {
  local value=$1
  [[ "$value" =~ ^[a-z0-9][a-z0-9._/:-]*@sha256:[a-f0-9]{64}$ ]]
}

require_canonical_docker_image() {
  local value=$1
  is_canonical_docker_image "$value" || die '--docker-image must be a repository@sha256:<64 lowercase hex> digest reference'
}

is_bounded_positive_integer() {
  local value=$1
  local digits
  [[ "$value" =~ ^[1-9][0-9]*$ ]] || return 1
  digits=${#value}
  ((digits <= ${#max_int64})) || return 1
  if ((digits == ${#max_int64})) && [[ "$value" > "$max_int64" ]]; then
    return 1
  fi
}

require_bounded_positive_integer() {
  local value=$1
  local label=$2
  is_bounded_positive_integer "$value" || die "$label must be a bounded positive integer"
}

format_cpu_milli() {
  local value=$1
  local whole=$((value / 1000))
  local fraction=$((value % 1000))
  local fraction_text
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

resolve_git_object_directory() {
  local git_dir=$1 object_dir
  object_dir=$(git --git-dir="$git_dir" rev-parse --path-format=absolute --git-path objects 2>/dev/null) || return 1
  [[ "$object_dir" = /* && "$object_dir" != *$'\n'* && "$object_dir" != *$'\r'* ]] || return 1
  printf '%s\n' "$object_dir"
}

readback_alternates_file() {
  local file=$1 expected=$2 readback
  [[ ! -L "$file" ]] || die 'Git alternates file must not be a symlink'
  [[ -e "$file" ]] || die 'Git alternates file disappeared before readback'
  [[ -f "$file" && -r "$file" ]] || die 'Git alternates file must be a readable regular file'
  if ! readback=$(<"$file"); then
    die 'Git alternates file readback failed'
  fi
  [[ "$readback" == "$expected" ]] || die 'Git alternates file conflicts with the requested path'
}

alternates_temp=''
cleanup_alternates_temp() {
  if [[ -n "$alternates_temp" ]]; then
    rm -f "$alternates_temp"
  fi
}

materialize_git_alternates() {
  local alternate_git_dir alternate_objects canonical_alternates
  local repo_git_dir repo_objects info_dir alternates_file
  validate_absolute_clean_path '--alternates' "$alternates"
  [[ -d "$alternates" && -r "$alternates" && -x "$alternates" && ! -L "$alternates" ]] || die '--alternates must name a readable Git object directory'
  canonical_alternates=$(cd -- "$alternates" && pwd -P) || die 'unable to canonicalize --alternates'
  [[ "$canonical_alternates" == "$alternates" ]] || die '--alternates must be canonical and contain no symlink'

  alternate_git_dir=${alternates%/*}
  [[ -n "$alternate_git_dir" && "$alternate_git_dir" != "$alternates" ]] || die '--alternates must be a Git objects directory'
  alternate_objects=$(resolve_git_object_directory "$alternate_git_dir") || die '--alternates parent is not a Git directory'
  [[ "$alternate_objects" == "$alternates" ]] || die '--alternates is not the Git object directory resolved by its parent repository'

  repo_git_dir=$(git -C "$repo_path" rev-parse --path-format=absolute --git-dir 2>/dev/null) || die 'unable to resolve target Git directory'
  [[ "$repo_git_dir" = /* && "$repo_git_dir" != *$'\n'* && "$repo_git_dir" != *$'\r'* ]] || die 'target Git directory resolution returned invalid data'
  repo_objects=$(resolve_git_object_directory "$repo_git_dir") || die 'unable to resolve target Git object directory'
  [[ -d "$repo_objects" && -r "$repo_objects" && -x "$repo_objects" && ! -L "$repo_objects" ]] || die 'target Git object directory is not readable'
  info_dir=$repo_objects/info
  [[ -d "$info_dir" && -r "$info_dir" && -x "$info_dir" && ! -L "$info_dir" ]] || die 'target Git object info directory is invalid'
  alternates_file=$info_dir/alternates

  if [[ -L "$alternates_file" || -e "$alternates_file" ]]; then
    readback_alternates_file "$alternates_file" "$canonical_alternates"
    return 0
  fi

  [[ -w "$info_dir" ]] || die 'target Git object info directory is not writable'
  alternates_temp=$(mktemp "$info_dir/.alternates.tmp.XXXXXX") || die 'unable to create atomic Git alternates temporary file'
  printf '%s\n' "$canonical_alternates" >"$alternates_temp" || die 'unable to write Git alternates temporary file'
  mv -n "$alternates_temp" "$alternates_file" || die 'unable to atomically install Git alternates file'
  readback_alternates_file "$alternates_file" "$canonical_alternates"
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
      ((alternates_seen == 0)) || die '--alternates must be provided once'
      alternates_seen=1
      alternates=$2
      shift 2
      ;;
    --)
      shift
      command_args=("$@")
      break
      ;;
    -h|--help)
      printf '%s\n' 'mac_runner_daemon.sh --repo PATH [--mode native|docker] [--docker-image REPOSITORY@sha256:DIGEST] [--alternates OBJECTS_DIR] [-- COMMAND...]'
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
	((docker_image_seen == 1)) || die '--docker-image is required for docker mode'
	require_canonical_docker_image "$docker_image"
	active_runtime_marker=$docker_runtime_marker
else
	((docker_image_seen == 0)) || die '--docker-image is forbidden in native mode'
	active_runtime_marker=$native_runtime_marker
fi
export SUPER_DOLPHIN_RUNTIME="$active_runtime_marker"
require_bounded_positive_integer "$timeout_seconds" '--timeout-seconds'
[[ "$timeout_seconds" -le 120 ]] || die 'watchdog timeout cannot exceed 120 seconds'
[[ "$(uname -s)" == Darwin ]] || die 'daemon only runs on macOS host'
[[ ${#command_args[@]} -gt 0 ]] || die 'a command is required after --'
git -C "$repo_path" rev-parse --show-toplevel >/dev/null 2>&1 || die 'repo is not a Git worktree'

logical_cpu=$(sysctl -n hw.logicalcpu 2>/dev/null || true)
memory_bytes=$(sysctl -n hw.memsize 2>/dev/null || true)
require_bounded_positive_integer "$logical_cpu" 'dynamic host logical CPU probe'
require_bounded_positive_integer "$memory_bytes" 'dynamic host memory probe'

if ((alternates_seen == 1)); then
  trap cleanup_alternates_temp EXIT
  materialize_git_alternates
fi
git -C "$repo_path" rev-parse --git-dir >/dev/null || die 'unable to materialize Git repository identity'

if [[ "$mode" == docker ]]; then
  command -v docker >/dev/null 2>&1 || die 'docker is required for docker mode'
  docker info >/dev/null 2>&1 || die 'docker daemon is unavailable'
  docker_info_output=''
  if ! docker_info_output=$(docker info --format '{{.NCPU}} {{.MemTotal}}'); then
    die 'Docker resource probe failed'
  fi
  [[ "$docker_info_output" != *$'\n'* && "$docker_info_output" != *$'\r'* ]] || die 'Docker resource probe has extra fields'
  if [[ "$docker_info_output" =~ ^[[:space:]]*([1-9][0-9]*)[[:space:]]+([1-9][0-9]*)[[:space:]]*$ ]]; then
    docker_cpu_info=${BASH_REMATCH[1]}
    docker_memory_info=${BASH_REMATCH[2]}
  else
    die 'Docker resource probe must contain exactly NCPU and MemTotal'
  fi
  require_bounded_positive_integer "$docker_cpu_info" 'Docker NCPU probe'
  require_bounded_positive_integer "$docker_memory_info" 'Docker MemTotal probe'
  ((docker_cpu_info <= max_int64 / 1000)) || die 'Docker NCPU probe overflows CPU intersection'
  cgroup_limits=''
	if ! cgroup_limits=$(docker run --rm --platform linux/amd64 --cpus "$docker_cpu_info" --memory "$docker_memory_info" \
	  -e LOCAL_NON_AUTHORITATIVE=1 -e NOT_VERIFIED=1 \
	  -e SUPER_DOLPHIN_AUTHORITY=LOCAL_NON_AUTHORITATIVE -e SUPER_DOLPHIN_RESULT_STATUS=NOT_VERIFIED \
	  -e SUPER_DOLPHIN_RUNTIME=darling_compat_runtime \
    "$docker_image" /bin/bash -Eeuo pipefail -c \
    'set -Eeuo pipefail; test -r /sys/fs/cgroup/cpu.max; test -r /sys/fs/cgroup/memory.max; cpu_raw=$(< /sys/fs/cgroup/cpu.max); memory_raw=$(< /sys/fs/cgroup/memory.max); newline=$(printf "x\\ny"); newline=${newline#x}; newline=${newline%y}; [[ "$cpu_raw" != *"$newline"* && "$memory_raw" != *"$newline"* ]] || exit 2; cpu_extra=""; memory_extra=""; read -r quota period cpu_extra <<< "$cpu_raw"; read -r memory memory_extra <<< "$memory_raw"; [[ -n "$quota" && -n "$period" && -z "$cpu_extra" && -n "$memory" && -z "$memory_extra" && "$quota" =~ ^[1-9][0-9]*$ && "$period" =~ ^[1-9][0-9]*$ && "$memory" =~ ^[1-9][0-9]*$ ]] || exit 2; printf "%s %s\\n%s\\n" "$quota" "$period" "$memory"'); then
    die 'Docker cgroup v2 probe failed'
  fi
  [[ "$cgroup_limits" == *$'\n'* && "$cgroup_limits" != *$'\r'* ]] || die 'Docker cgroup v2 probe must contain cpu.max and memory.max'
  cgroup_cpu_line=${cgroup_limits%%$'\n'*}
  cgroup_memory_line=${cgroup_limits#*$'\n'}
  [[ -n "$cgroup_cpu_line" && -n "$cgroup_memory_line" && "$cgroup_memory_line" != *$'\n'* ]] || die 'Docker cgroup v2 probe has extra fields'
  cgroup_cpu_extra=''
  cgroup_memory_extra=''
  read -r cgroup_cpu_quota cgroup_cpu_period cgroup_cpu_extra <<< "$cgroup_cpu_line"
  read -r cgroup_memory cgroup_memory_extra <<< "$cgroup_memory_line"
  [[ -n "$cgroup_cpu_quota" && -n "$cgroup_cpu_period" && -z "$cgroup_cpu_extra" && -n "$cgroup_memory" && -z "$cgroup_memory_extra" ]] || die 'Docker cgroup v2 probe has invalid fields'
  require_bounded_positive_integer "$cgroup_cpu_quota" 'Docker cgroup cpu.max quota'
  require_bounded_positive_integer "$cgroup_cpu_period" 'Docker cgroup cpu.max period'
  require_bounded_positive_integer "$cgroup_memory" 'Docker cgroup memory.max'
  ((cgroup_cpu_quota <= max_int64 / 1000)) || die 'Docker cgroup cpu.max quota overflows CPU intersection'
  cgroup_cpu_milli=$((cgroup_cpu_quota * 1000 / cgroup_cpu_period))
  ((cgroup_cpu_milli > 0)) || die 'Docker cgroup cpu.max produces no positive CPU capacity'
  effective_cpu_milli=$((docker_cpu_info * 1000))
  if ((cgroup_cpu_milli < effective_cpu_milli)); then
    effective_cpu_milli=$cgroup_cpu_milli
  fi
  ((effective_cpu_milli > 0)) || die 'Docker CPU intersection is not positive'
  docker_cpu=$(format_cpu_milli "$effective_cpu_milli")
  if ((cgroup_memory < docker_memory_info)); then
    effective_memory=$cgroup_memory
  else
    effective_memory=$docker_memory_info
  fi
  ((effective_memory > 0)) || die 'Docker memory intersection is not positive'
  docker_memory=$effective_memory
fi

printf 'runner_id=%s mode=%s runtime=%s logical_cpu=%s memory_bytes=%s docker_cpu=%s docker_memory=%s authority=LOCAL_NON_AUTHORITATIVE status=NOT_VERIFIED LOCAL_NON_AUTHORITATIVE=1 NOT_VERIFIED=1\n' \
	"$runner_id" "$mode" "$active_runtime_marker" "$logical_cpu" "$memory_bytes" "${docker_cpu:-0}" "${docker_memory:-0}"

is_docker_container_id() {
  local value=$1
  [[ "$value" =~ ^[0-9a-f]{64}$ ]]
}
is_docker_container_name() {
  local value=$1
  [[ "$value" =~ ^mac-runner-[A-Za-z0-9]{6,32}$ ]]
}
is_docker_container_reference() {
  local value=$1
  is_docker_container_id "$value" || is_docker_container_name "$value"
}

read_docker_cidfile() {
  local file=$1 value='' line='' line_count=0
  [[ -f "$file" && ! -L "$file" && -r "$file" ]] || return 1
  while IFS= read -r line || [[ -n "$line" ]]; do
    ((line_count += 1))
    if ((line_count > 1)); then
      return 1
    fi
    value=$line
  done <"$file"
  ((line_count == 1)) || return 1
  is_docker_container_id "$value" || return 1
  printf '%s\n' "$value"
}

docker_control_bounded() {
  local control_pid elapsed=0 control_status
  "$@" &
  control_pid=$!
  while kill -0 "$control_pid" 2>/dev/null; do
    if ((elapsed >= docker_control_timeout_seconds)); then
      if kill -TERM "$control_pid" 2>/dev/null; then
        :
      elif kill -0 "$control_pid" 2>/dev/null; then
        return 125
      fi
      if ! sleep 1; then
        if kill -KILL "$control_pid" 2>/dev/null; then
          :
        elif kill -0 "$control_pid" 2>/dev/null; then
          return 125
        fi
        wait "$control_pid" 2>/dev/null || true
        return 125
      fi
      if kill -0 "$control_pid" 2>/dev/null; then
        if kill -KILL "$control_pid" 2>/dev/null; then
          :
        elif kill -0 "$control_pid" 2>/dev/null; then
          return 125
        fi
      fi
      wait "$control_pid" 2>/dev/null || true
      return 124
    fi
    if ! sleep 1; then
      if kill -KILL "$control_pid" 2>/dev/null; then
        :
      elif kill -0 "$control_pid" 2>/dev/null; then
        return 125
      fi
      wait "$control_pid" 2>/dev/null || true
      return 125
    fi
    elapsed=$((elapsed + 1))
  done
  if wait "$control_pid"; then
    return 0
  else
    control_status=$?
  fi
  return "$control_status"
}

inspect_docker_container() {
  local container_ref=$1 inspection
  is_docker_container_reference "$container_ref" || return 1
  if ! inspection=$(docker_control_bounded docker inspect --format '{{.State.Running}}' "$container_ref"); then
    return 1
  fi
  [[ "$inspection" != *$'\r'* && "$inspection" != *$'\n'* ]] || return 1
  [[ "$inspection" == true || "$inspection" == false ]] || return 1
  printf '%s\n' "$inspection"
}

inspect_docker_container_binding() {
  local container_ref=$1 expected_name=$2 inspection
  is_docker_container_id "$container_ref" || return 1
  is_docker_container_name "$expected_name" || return 1
  if ! inspection=$(docker_control_bounded docker inspect --format '{{.Name}}|{{index .Config.Labels "com.super-dolphin.runner-name"}}' "$container_ref"); then
    return 1
  fi
  [[ "$inspection" != *$'\r'* && "$inspection" != *$'\n'* ]] || return 1
  if [[ "$inspection" == "/$expected_name|$expected_name" ]]; then
    return 0
  fi
  return 2
}

inspect_docker_container_startup() {
  local container_ref=$1 expected_name=$2 elapsed=0 binding_status
  while :; do
    if inspect_docker_container "$container_ref" >/dev/null; then
      if inspect_docker_container_binding "$container_ref" "$expected_name"; then
        return 0
      else
        binding_status=$?
        if ((binding_status == 2)); then
          return 2
        fi
      fi
    fi
    if ((elapsed >= docker_startup_timeout_seconds)); then
      return 1
    fi
    sleep 1 || return 1
    elapsed=$((elapsed + 1))
  done
}

wait_docker_container() {
  local container_ref=$1 allow_termination_status=${2:-0}
  local wait_output='' line='' line_count=0 wait_status=0
  docker_container_exit_code=''
  is_docker_container_reference "$container_ref" || return 1
  if ! : >"$docker_wait_file"; then
    return 1
  fi
  if docker_control_bounded docker wait "$container_ref" >"$docker_wait_file"; then
    wait_status=0
  else
    wait_status=$?
  fi
  while IFS= read -r line || [[ -n "$line" ]]; do
    ((line_count += 1))
    if ((line_count > 1)); then
      return 1
    fi
    wait_output=$line
  done <"$docker_wait_file"
  ((line_count == 1)) || return 1
  case "$wait_output" in
    0|[1-9]|[1-9][0-9]|1[0-9][0-9]|2[0-4][0-9]|25[0-5]) ;;
    *) return 1 ;;
  esac
  if ((wait_status != 0)); then
    if ((allow_termination_status == 0)) || [[ "$wait_status" != "$wait_output" ]] || [[ "$wait_output" != 137 && "$wait_output" != 143 ]]; then
      return 1
    fi
  fi
  docker_container_exit_code=$wait_output
}

cleanup_docker_container() {
  local container_ref=$1 allow_termination_status=${2:-0} state='' cleanup_status=0
  is_docker_container_reference "$container_ref" || return 2
  if state=$(inspect_docker_container "$container_ref"); then
    :
  else
    cleanup_status=2
    state=true
  fi
  if [[ "$state" == true ]]; then
    if ! docker_control_bounded docker stop --time 2 "$container_ref"; then
      cleanup_status=2
    fi
    if state=$(inspect_docker_container "$container_ref"); then
      :
    else
      cleanup_status=2
      state=true
    fi
  fi
  if [[ "$state" == true ]]; then
    if ! docker_control_bounded docker kill "$container_ref"; then
      cleanup_status=2
    fi
  fi
  if ! wait_docker_container "$container_ref" "$allow_termination_status"; then
    cleanup_status=2
  fi
  if ! docker_control_bounded docker rm "$container_ref"; then
    cleanup_status=2
  fi
  return "$cleanup_status"
}
wait_docker_client() {
  local child_pid=$1
  if wait "$child_pid"; then
    docker_client_exit_code=0
  else
    docker_client_exit_code=$?
  fi
}

terminate_docker_client() {
  local child_pid=$1
  if kill -0 "$child_pid" 2>/dev/null; then
    if kill -TERM "$child_pid" 2>/dev/null; then
      :
    elif kill -0 "$child_pid" 2>/dev/null; then
      return 1
    fi
    if ! sleep 1; then
      return 1
    fi
    if kill -0 "$child_pid" 2>/dev/null; then
      if kill -KILL "$child_pid" 2>/dev/null; then
        :
      elif kill -0 "$child_pid" 2>/dev/null; then
        return 1
      fi
    fi
  fi
  wait_docker_client "$child_pid"
}

cleanup_docker_temp_files() {
  local cleanup_status=0
  if [[ -n "$docker_cidfile" && ( -e "$docker_cidfile" || -L "$docker_cidfile" ) ]]; then
    if [[ -L "$docker_cidfile" ]] || ! rm -f "$docker_cidfile"; then
      cleanup_status=2
    fi
  fi
  if [[ -n "$docker_wait_file" && ( -e "$docker_wait_file" || -L "$docker_wait_file" ) ]]; then
    if [[ -L "$docker_wait_file" ]] || ! rm -f "$docker_wait_file"; then
      cleanup_status=2
    fi
  fi
  if [[ -n "$docker_cid_directory" && -e "$docker_cid_directory" ]]; then
    if [[ -L "$docker_cid_directory" ]] || ! rmdir "$docker_cid_directory"; then
      cleanup_status=2
    fi
  fi
  return "$cleanup_status"
}

run_docker_command() {
  local child_pid startup_elapsed=0 watchdog_elapsed=0 startup_ready=0 timed_out=0
  local startup_error='' container_id='' container_ref='' cleanup_status=0 startup_cleanup_status=0 binding_status=0
  local docker_cid_basename='' docker_name_suffix=''
  local -a argv
  if ! docker_cid_directory=$(mktemp -d -t mac-runner-docker-cid); then
    die 'unable to create Docker cidfile temporary directory'
  fi
  if [[ "$docker_cid_directory" != /* || "$docker_cid_directory" == *$'\n'* || "$docker_cid_directory" == *$'\r'* || ! -d "$docker_cid_directory" || -L "$docker_cid_directory" ]]; then
    if ! cleanup_docker_temp_files; then
      die 'Docker cidfile temporary directory cleanup failed'
    fi
    die 'Docker cidfile temporary directory is invalid'
  fi
  if ! chmod 700 "$docker_cid_directory"; then
    if ! cleanup_docker_temp_files; then
      die 'Docker cidfile temporary directory cleanup failed'
    fi
    die 'unable to restrict Docker cidfile temporary directory'
  fi
  docker_cid_basename=${docker_cid_directory##*/}
  if [[ ! "$docker_cid_basename" =~ ^mac-runner-docker-cid\.([A-Za-z0-9]{6,32})$ ]]; then
    cleanup_docker_temp_files || die 'Docker cidfile temporary directory cleanup failed'
    die 'Docker cidfile temporary directory name is invalid'
  fi
  docker_name_suffix=${docker_cid_basename#*.}
  docker_container_name="mac-runner-$docker_name_suffix"
  if ! is_docker_container_name "$docker_container_name"; then
    cleanup_docker_temp_files || die 'Docker cidfile temporary directory cleanup failed'
    die 'Docker container name generation failed'
  fi
  container_ref=$docker_container_name
  docker_cidfile=$docker_cid_directory/container.cid
  docker_wait_file=$docker_cid_directory/wait.exit
	argv=(docker run --platform linux/amd64 --name "$docker_container_name" --label "com.super-dolphin.runner-name=$docker_container_name" --cidfile "$docker_cidfile" --cpus "$docker_cpu" --memory "$docker_memory" -v "$repo_path:/workspace:rw" -w /workspace \
	  -e LOCAL_NON_AUTHORITATIVE=1 -e NOT_VERIFIED=1 \
	  -e SUPER_DOLPHIN_AUTHORITY=LOCAL_NON_AUTHORITATIVE -e SUPER_DOLPHIN_RESULT_STATUS=NOT_VERIFIED \
	  -e SUPER_DOLPHIN_RUNTIME=darling_compat_runtime \
    "$docker_image" "${command_args[@]}")
  "${argv[@]}" &
  child_pid=$!
  while ((startup_ready == 0)); do
    if [[ -e "$docker_cidfile" ]]; then
      if container_id=$(read_docker_cidfile "$docker_cidfile"); then
        if inspect_docker_container_startup "$container_id" "$docker_container_name"; then
          container_ref=$container_id
          startup_ready=1
          break
        fi
        binding_status=$?
        if ((binding_status == 2)); then
          startup_error='Docker cidfile container name binding mismatch'
        else
          startup_error='Docker container ID failed strict startup inspection'
        fi
        break
      fi
      startup_error='Docker cidfile contains an invalid container ID'
      break
    fi
    if ! kill -0 "$child_pid" 2>/dev/null; then
      startup_error='Docker run exited before writing a container ID'
      break
    fi
    if ((startup_elapsed >= docker_startup_timeout_seconds)); then
      startup_error='Docker cidfile was not written before startup deadline'
      break
    fi
    if ! sleep 1; then
      startup_error='Docker cidfile startup wait failed'
      break
    fi
    startup_elapsed=$((startup_elapsed + 1))
  done
  if ((startup_ready == 0)); then
    startup_cleanup_status=0
    terminate_docker_client "$child_pid" || startup_cleanup_status=2
    cleanup_docker_container "$container_ref" 1 || startup_cleanup_status=2
    cleanup_docker_temp_files || startup_cleanup_status=2
    if ((startup_cleanup_status != 0)); then
      die 'Docker startup cleanup failed'
    fi
    die "$startup_error"
  fi
  while kill -0 "$child_pid" 2>/dev/null; do
    if ((watchdog_elapsed >= timeout_seconds)); then
      timed_out=1
      break
    fi
    if ! sleep 1; then
      startup_error='Docker watchdog wait failed'
      break
    fi
    watchdog_elapsed=$((watchdog_elapsed + 1))
  done
  if ((timed_out == 1)) || [[ -n "$startup_error" ]]; then
    if ! cleanup_docker_container "$container_ref" 1; then
      cleanup_status=2
    fi
  fi
  if kill -0 "$child_pid" 2>/dev/null; then
    terminate_docker_client "$child_pid" || cleanup_status=2
  else
    wait_docker_client "$child_pid"
  fi
  if ((timed_out == 0)) && [[ -z "$startup_error" ]]; then
    if ! cleanup_docker_container "$container_ref" 1; then
      cleanup_status=2
    fi
  fi
  if ! cleanup_docker_temp_files; then
    cleanup_status=2
  fi
  if [[ -n "$startup_error" ]]; then
    die "$startup_error"
  fi
  if ((cleanup_status != 0)); then
    die 'Docker container lifecycle cleanup failed'
  fi
  if [[ -z "$docker_container_exit_code" ]]; then
    die 'Docker container exit code was not observed'
  fi
  if ((timed_out == 1)); then
    return 124
  fi
  if [[ -z "$docker_client_exit_code" ]]; then
    die 'Docker client exit code was not observed'
  fi
  if [[ "$docker_client_exit_code" != "$docker_container_exit_code" ]]; then
    die 'Docker client and container exit statuses differ'
  fi
  return "$docker_container_exit_code"
}

native_abort_unowned_root() {
  local root_pid=$1
  if kill -0 "$root_pid" 2>/dev/null; then
    if kill -TERM "$root_pid" 2>/dev/null; then
      :
    elif kill -0 "$root_pid" 2>/dev/null; then
      return 2
    fi
    if ! sleep 1; then
      return 2
    fi
    if kill -0 "$root_pid" 2>/dev/null; then
      if kill -KILL "$root_pid" 2>/dev/null; then
        :
      elif kill -0 "$root_pid" 2>/dev/null; then
        return 2
      fi
    fi
  fi
  if wait "$root_pid" 2>/dev/null; then
    :
  else
    :
  fi
}

native_cleanup_group() {
  local group_pid=$1 elapsed=0
  kill -0 -- "-$group_pid" 2>/dev/null || return 2
  if kill -TERM -- "-$group_pid" 2>/dev/null; then
    :
  elif kill -0 -- "-$group_pid" 2>/dev/null; then
    return 2
  fi
  while kill -0 -- "-$group_pid" 2>/dev/null; do
    if ((elapsed >= 2)); then
      break
    fi
    if ! sleep 1; then
      return 2
    fi
    elapsed=$((elapsed + 1))
  done
  if kill -0 -- "-$group_pid" 2>/dev/null; then
    if kill -KILL -- "-$group_pid" 2>/dev/null; then
      :
    elif kill -0 -- "-$group_pid" 2>/dev/null; then
      return 2
    fi
  fi
  elapsed=0
  while kill -0 -- "-$group_pid" 2>/dev/null; do
    if ((elapsed >= 5)); then
      return 2
    fi
    if ! sleep 1; then
      return 2
    fi
    elapsed=$((elapsed + 1))
  done
}

run_command() {
  local child_pid status native_monitor_was_enabled native_probe_elapsed native_elapsed native_timed_out
  local -a argv
  if [[ "$mode" == native ]]; then
    argv=("${command_args[@]}")
    native_monitor_was_enabled=0
    native_probe_elapsed=0
    case "$-" in
      *m*) native_monitor_was_enabled=1 ;;
    esac
    set -m
    ( exec "${argv[@]}" ) &
    child_pid=$!
    if ((native_monitor_was_enabled == 0)); then
      set +m
    fi
    while kill -0 "$child_pid" 2>/dev/null; do
      if kill -0 -- "-$child_pid" 2>/dev/null; then
        break
      fi
      if ((native_probe_elapsed >= 1)); then
        native_abort_unowned_root "$child_pid" || die 'Capacity/ownership blocker: native root cleanup failed after process-group verification'
        die 'Capacity/ownership blocker: native command did not acquire an independent process group'
      fi
      if ! sleep 1; then
        native_abort_unowned_root "$child_pid" || die 'Capacity/ownership blocker: native root cleanup failed after watchdog wait failure'
        die 'Capacity/ownership blocker: native process-group ownership probe failed'
      fi
      native_probe_elapsed=$((native_probe_elapsed + 1))
    done
    native_elapsed=0
    native_timed_out=0
    while kill -0 "$child_pid" 2>/dev/null; do
      if ((native_elapsed >= timeout_seconds)); then
        native_timed_out=1
        break
      fi
      if ! sleep 1; then
        die 'Capacity/ownership blocker: native watchdog wait failed'
      fi
      native_elapsed=$((native_elapsed + 1))
    done
    if ((native_timed_out != 0)); then
      native_cleanup_group "$child_pid" || die 'Capacity/ownership blocker: native process-group timeout cleanup failed'
      if wait "$child_pid"; then
        status=0
      else
        status=$?
      fi
      return "$status"
    fi
    if wait "$child_pid"; then
      status=0
    else
      status=$?
    fi
    if kill -0 -- "-$child_pid" 2>/dev/null; then
      native_cleanup_group "$child_pid" || die 'Capacity/ownership blocker: native descendant process-group cleanup failed'
      die 'Capacity/ownership blocker: native command exited with process-group descendants'
    fi
    return "$status"
  fi
  run_docker_command
}

run_command
