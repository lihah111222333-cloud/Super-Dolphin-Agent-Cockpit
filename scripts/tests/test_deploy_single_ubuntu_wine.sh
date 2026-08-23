#!/usr/bin/env bash
set -Eeuo pipefail

root=$(cd -- "$(dirname -- "$0")/../.." && pwd -P)
deploy="$root/scripts/deploy/linux/deploy_single_ubuntu_wine.sh"
[[ -x "$deploy" ]] || { printf 'deployment script is not executable\n' >&2; exit 1; }

tmp=$(mktemp -d "${TMPDIR:-/tmp}/single-ubuntu-wine-contract.XXXXXX")
tmp=$(cd -- "$tmp" && pwd -P)
trap 'rm -rf -- "$tmp"' EXIT
bin="$tmp/bin"
remote_root="$tmp/remote"
log_dir="$tmp/logs"
state="$tmp/docker-state"
known_hosts="$tmp/known_hosts"
docker_socket="$tmp/docker.sock"
fake_os_root="$tmp/fake-os"
fake_machine_id_path="$tmp/fake-machine-id"
fake_machine_hash_file="$tmp/fake-machine-id.hash"
fake_machine_override_file="$tmp/fake-machine-id.override"
machine_identity="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
docker_server_identity="fake-docker-server"
object_store_root="$remote_root/object-store"
worktree_root="$remote_root/worktree"
cache_root="$remote_root/cache"
private_overlay_root="$remote_root/overlay"
temp_root="$remote_root/temp"
wine_prefix_root="$remote_root/wine-prefix"
mkdir -p "$bin" "$remote_root" "$log_dir" "$state" "$fake_os_root"
printf 'ID=ubuntu\n' >"$fake_os_root/etc-os-release"
printf 'ID=ubuntu\n' >"$fake_os_root/usr-lib-os-release"
printf 'machine-id-fixture\n' >"$fake_machine_id_path"
printf '%s\n' aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa >"$fake_machine_hash_file"

: >"$known_hosts"
python3 - "$docker_socket" <<'PY'
import socket, sys
s = socket.socket(socket.AF_UNIX)
s.bind(sys.argv[1])
s.close()
PY

cat >"$bin/go" <<'FAKE_GO'
#!/usr/bin/env bash
set -Eeuo pipefail
out=''
while (($#)); do
  if [[ "$1" == -o ]]; then out=$2; shift 2; else shift; fi
done
[[ -n "$out" ]] || exit 2
printf 'fake-linux-amd64-worker\n' >"$out"
chmod 0755 "$out"
FAKE_GO

cat >"$bin/ssh" <<'FAKE_SSH'
#!/usr/bin/env bash
set -Eeuo pipefail
log=${FAKE_SSH_LOG:?}
printf '%s\n' "$*" >>"$log"
control_path=''
control_master=''
operation=''
master_start=0
host=''
while (($#)); do
  case "$1" in
    -o)
      option=${2:?}
      case "$option" in
        ControlPath=*) control_path=${option#ControlPath=} ;;
        ControlMaster=*) control_master=${option#ControlMaster=} ;;
      esac
      shift 2
      ;;
    -MNf|-M|-N|-f) master_start=1; shift ;;
    -O) operation=${2:?}; shift 2 ;;
    -*) shift ;;
    *) host=$1; shift; break ;;
  esac
done
[[ "$host" == fake-host ]] || exit 64
[[ -n "$control_path" ]] || exit 66
if [[ "$operation" == check ]]; then
  [[ -S "$control_path" ]] || { printf 'control master is absent\n' >&2; exit 255; }
  if [[ -f "$control_path.owner" ]]; then
    printf 'Master running (pid=%s)\n' "$(cat -- "$control_path.owner")"
  else
    printf 'Master running (%s)\n' "$control_path"
  fi
  printf 'master-check\n' >>"$log"
  exit 0
fi
if [[ "$operation" == exit ]]; then
  if [[ -f "$control_path.owner" ]]; then
    owner_pid=$(cat -- "$control_path.owner")
  else
    owner_pid=''
  fi
  if [[ "${FAKE_SSH_OWNER_IDENTITY_MISMATCH:-0}" == 1 && -n "$owner_pid" ]]; then
    kill -USR1 "$owner_pid" 2>/dev/null || true
    sleep 0.1
    printf 'Master refused termination request: Permission denied\n' >&2
    exit 255
  fi
  if [[ "${FAKE_SSH_REFUSE_EXIT:-0}" == 1 ]]; then
    printf 'Master refused termination request: Permission denied\n' >&2
    exit 255
  fi
  if [[ "${FAKE_SSH_EXIT_SOCKET_ONLY:-0}" == 1 ]]; then
    socket_path=$(cat -- "$control_path.real")
    rm -f -- "$control_path.real" "$control_path"
    if [[ -n "$socket_path" ]]; then rm -f -- "$socket_path"; fi
    printf 'master-exit socket_absent=true owner_alive=true\n' >>"$log"
    exit 255
  fi
  if [[ -n "$owner_pid" ]]; then kill -TERM "$owner_pid" 2>/dev/null || true; fi
  socket_path=$(cat -- "$control_path.real")
  rm -f -- "$control_path.owner" "$control_path.real" "$control_path"
  if [[ -n "$socket_path" ]]; then rm -f -- "$socket_path"; fi
  if [[ -e "$control_path" ]]; then
    printf 'master-exit socket_absent=false\n' >>"$log"
    exit 255
  fi
  printf 'master-exit socket_absent=true\n' >>"$log"
  exit 0
fi
if ((master_start == 1)); then
  [[ "$control_master" == yes ]] || exit 67
  socket_path="/tmp/fake-control-$RANDOM-$PPID.sock"
  python3 - "$socket_path" <<'PY'
import os
import socket
import sys

path = sys.argv[1]
try:
    os.unlink(path)
except FileNotFoundError:
    pass
server = socket.socket(socket.AF_UNIX)
server.bind(path)
server.close()
PY
  [[ -S "$socket_path" ]] || exit 68
  printf '%s\n' "$socket_path" >"$control_path.real"
  ln -s -- "$socket_path" "$control_path"
  [[ -S "$control_path" ]] || exit 68
  printf 'master-start\n' >>"$log"
  if [[ "${FAKE_SSH_OWNER_IDENTITY_MISMATCH:-0}" == 1 ]]; then
    python3 - <<'PY' &
import os
import signal
import time

def switch_identity(_signum, _frame):
    os.execl('/bin/sleep', 'sleep', '30')

signal.signal(signal.SIGUSR1, switch_identity)
while True:
    time.sleep(30)
PY
  else
    bash -c 'while :; do /bin/sleep 30; done' &
  fi
  owner_pid=$!
  printf '%s\n' "$owner_pid" >"$control_path.owner"
  printf '%s\n' "$owner_pid" >>"${FAKE_OWNER_LOG:?}"
  exit 0
fi
[[ "${1:-}" == bash && "${2:-}" == -s && "${3:-}" == -- ]] || exit 65
shift 3
rewritten=$(mktemp "${TMPDIR:-/tmp}/fake-ssh-script.XXXXXX")
trap 'rm -f -- "$rewritten"' EXIT
sed \
  -e "s#/etc/os-release#${FAKE_OS_ROOT:?}/etc-os-release#g" \
  -e "s#/usr/lib/os-release#${FAKE_OS_ROOT:?}/usr-lib-os-release#g" \
  -e "s#/etc/machine-id#${FAKE_MACHINE_ID_PATH:?}#g" \
  >"$rewritten"
if [[ "${FAKE_SSH_DRIFT_HOST_TOOLS:-0}" == 1 ]] && grep -Fq 'DEPLOYMENT_RECEIPT_JSON=' "$rewritten"; then
  printf 'ID=debian\n' >"${FAKE_OS_ROOT:?}/etc-os-release"
  printf 'ID=debian\n' >"${FAKE_OS_ROOT:?}/usr-lib-os-release"
fi
if [[ "${FAKE_SSH_DRIFT_MACHINE_ID:-0}" == 1 ]] && grep -Fq 'DEPLOYMENT_RECEIPT_JSON=' "$rewritten"; then
  printf '%s\n' bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb >"${FAKE_MACHINE_HASH_FILE:?}"
fi
if [[ "${FAKE_SSH_DRIFT_MACHINE_ID_PATH:-0}" == 1 ]] && grep -Fq 'DEPLOYMENT_RECEIPT_JSON=' "$rewritten"; then
  alternate_path="${FAKE_MACHINE_ID_PATH:?}.alternate"
  cp -- "${FAKE_MACHINE_ID_PATH:?}" "$alternate_path"
  printf '%s\n' "$alternate_path" >"${FAKE_MACHINE_ID_REALPATH_OVERRIDE_FILE:?}"
fi
if [[ "${FAKE_SSH_EXTRA_STDOUT:-0}" == 1 ]]; then
  /bin/bash -s -- "$@" <"$rewritten"
  status=$?
  printf 'EXTRA_STDOUT\n'
  exit "$status"
fi
if [[ "${FAKE_SSH_FAIL_AFTER_STAGE_READY:-0}" == 1 ]]; then
  stdout_tmp=$(mktemp "${TMPDIR:-/tmp}/fake-ssh-stdout.XXXXXX")
  stderr_tmp=$(mktemp "${TMPDIR:-/tmp}/fake-ssh-stderr.XXXXXX")
  /bin/bash -s -- "$@" <"$rewritten" >"$stdout_tmp" 2>"$stderr_tmp"
  status=$?
  cat -- "$stderr_tmp" >&2
  cat -- "$stdout_tmp"
  if grep -Fq 'stage_ready=' "$stdout_tmp"; then
    printf 'fake remote failure after stage_ready\n' >&2
    status=75
  fi
  rm -f -- "$stdout_tmp" "$stderr_tmp"
  exit "$status"
fi
/bin/bash -s -- "$@" <"$rewritten"
FAKE_SSH

cat >"$bin/scp" <<'FAKE_SCP'
#!/usr/bin/env bash
set -Eeuo pipefail
log=${FAKE_SCP_LOG:?}
printf '%s\n' "$*" >>"$log"
control_path=''
[[ "${1:-}" == -q ]] || exit 64
shift
while (($#)); do
  if [[ "$1" == -o ]]; then
    option=${2:?}
    [[ "$option" == ControlPath=* ]] && control_path=${option#ControlPath=}
    shift 2
  else
    break
  fi
done
[[ -n "$control_path" && -S "$control_path" ]] || { printf 'control master is absent\n' >&2; exit 255; }
src=$1
target=$2
dest=${target#fake-host:}
[[ "$dest" != "$target" ]] || exit 65
mkdir -p -- "$(dirname -- "$dest")"
cp -- "$src" "$dest"
FAKE_SCP

cat >"$bin/docker" <<'FAKE_DOCKER'
#!/usr/bin/env bash
set -Eeuo pipefail
log=${FAKE_DOCKER_LOG:?}
state=${FAKE_DOCKER_STATE:?}
kind_for_args() {
  local ref="${@: -1}"
  if [[ "$ref" == ci-runner-ubuntu:* ]]; then printf 'linux\n'; else printf 'wine\n'; fi
}
printf '%q ' "$@" >>"$log"
printf '\n' >>"$log"
printf '%s\n' "$@" >>"$log.raw"
if [[ "${1:-}" == info && "${2:-}" == --format ]]; then
  case "$3" in
    *'.ID'*) printf 'fake-docker-server\n' ;;
    *) printf 'linux amd64\n' ;;
  esac
  exit 0
fi
if [[ "${1:-}" == info ]]; then exit 0; fi
if [[ "${1:-}" == buildx && "${2:-}" == version ]]; then printf 'github.com/docker/buildx v0.36.1\n'; exit 0; fi
if [[ "${1:-}" == buildx && "${2:-}" == build ]]; then
  kind=wine
  previous=''
  for arg in "$@"; do
    if [[ "$previous" == --tag && "$arg" == ci-runner-ubuntu:* ]]; then kind=linux; fi
    previous=$arg
  done
  label=''
  for arg in "$@"; do [[ "$arg" == com.super-dolphin.deployment-id=* ]] && label=${arg#*=}; done
  printf '%s\n' "$label" >"$state/$kind-image-label"
  printf '%s\n' "${FAKE_IMAGE_ID:-sha256:$(printf '%064d' 1)}" >"$state/$kind-image-id"
  : >"$state/$kind-image"
  printf 'build progress\n'
  exit 0
fi
if [[ "${1:-}" == image && "${2:-}" == inspect ]]; then
  kind=$(kind_for_args "$@")
  if [[ "${3:-}" == --format ]]; then
    format=$4
    [[ -f "$state/$kind-image" ]] || exit 1
    case "$format" in
      *'.Id'*) cat "$state/$kind-image-id" ;;
      *'Config.Labels'*) cat "$state/$kind-image-label" ;;
      *) if [[ "$kind" == linux ]]; then printf '%s\n' \
        'SUPER_DOLPHIN_RUNTIME=ubuntu_container' \
        'SUPER_DOLPHIN_TARGET_PLATFORM=linux/amd64' \
        'SUPER_DOLPHIN_AUTHORITY=LOCAL_NON_AUTHORITATIVE' \
        'SUPER_DOLPHIN_RESULT_STATUS=NOT_VERIFIED'; else printf '%s\n' \
        'SUPER_DOLPHIN_RUNTIME=ubuntu_wine_container' \
        'SUPER_DOLPHIN_TARGET_PLATFORM=windows/amd64' \
        'SUPER_DOLPHIN_AUTHORITY=LOCAL_NON_AUTHORITATIVE' \
        'SUPER_DOLPHIN_RESULT_STATUS=NOT_VERIFIED'; fi ;;
    esac
    exit 0
  fi
  kind=$(kind_for_args "$@")
  [[ -f "$state/$kind-image" ]] && exit 0
  exit 1
fi
if [[ "${1:-}" == image && "${2:-}" == rm ]]; then
  kind=$(kind_for_args "$@")
  rm -f -- "$state/$kind-image" "$state/$kind-image-label" "$state/$kind-image-id"
  exit 0
fi
if [[ "${1:-}" == ps ]]; then
  [[ -f "$state/container" ]] && printf 'container-id\n'
  exit 0
fi
if [[ "${1:-}" == run ]]; then
  if [[ "$*" == *'/usr/bin/wine /opt'* ]]; then
    printf 'wine wrapper qualification is forbidden\n' >&2
    exit 67
  fi
  if [[ "${FAKE_DOCKER_FAIL_SMOKE:-0}" == 1 && "$*" != *sha256sum* ]]; then
    : >"$state/container"
    printf 'smoke failed\n' >&2
    exit 19
  fi
  if [[ "$*" == *'wine-qualification:wait'* ]]; then
    command_script=''
    previous=''
    for arg in "$@"; do
      if [[ "$previous" == -c ]]; then command_script=$arg; fi
      previous=$arg
    done
    expected_printf='printf "%s\n" "wine-9.0|smoke-ok"'
    [[ "$command_script" == *"$expected_printf"* ]] || { printf 'qualification final printf drifted\n' >&2; exit 68; }
    [[ "$command_script" == *'wine-smoke.exe >&2'* ]] || { printf 'PE stdout was not routed to stderr\n' >&2; exit 71; }
    [[ "$command_script" == *'wineserver64 -k >&2'* && "$command_script" == *'wineserver64 -w >&2'* ]] || { printf 'wineserver stdout was not routed to stderr\n' >&2; exit 72; }
    printf 'fake-pe-stdout-routed-to-stderr\n' >&2
    printf 'fake-wineserver-kill-stdout-routed-to-stderr\n' >&2
    printf 'fake-wineserver-wait-stdout-routed-to-stderr\n' >&2
    qualification_output=$(/bin/sh -c "$expected_printf") || exit 69
    [[ "$qualification_output" == 'wine-9.0|smoke-ok' ]] || exit 70
    printf '%s\n' "$qualification_output"
    exit 0
  fi
  if [[ "$*" == *sha256sum* ]]; then
    if [[ "$*" == *wineserver64* ]]; then
      printf '%s  /usr/local/go/bin/go\n%s  /usr/lib/wine/wine64\n%s  /usr/lib/wine/wineserver64\n' \
        aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
        bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb \
        cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
    else
      printf '%s  /usr/local/go/bin/go\n' aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    fi
    exit 0
  fi
  if [[ "$*" == *'linux-amd64|smoke-ok'* ]]; then printf 'linux-amd64|smoke-ok\n'; else printf 'wine-9.0|smoke-ok\n'; fi
  exit 0
fi
if [[ "${1:-}" == rm ]]; then
  rm -f -- "$state/container"
  exit 0
fi
exit 66
FAKE_DOCKER
cat >"$bin/realpath" <<'FAKE_REALPATH'
#!/usr/bin/env bash
set -Eeuo pipefail
[[ "${1:-}" == -e ]] && shift
[[ "${1:-}" == -- ]] && shift
path=${1:?}
if [[ "$path" == "${FAKE_OS_ROOT:?}/etc-os-release" ]]; then
  printf '%s/usr-lib-os-release\n' "$FAKE_OS_ROOT"
elif [[ "$path" == "${FAKE_MACHINE_ID_PATH:?}" && -s "${FAKE_MACHINE_ID_REALPATH_OVERRIDE_FILE:-}" ]]; then
  cat -- "$FAKE_MACHINE_ID_REALPATH_OVERRIDE_FILE"
elif [[ -n "${FAKE_MACHINE_ID_REALPATH_OVERRIDE_FILE:-}" && -s "$FAKE_MACHINE_ID_REALPATH_OVERRIDE_FILE" && "$path" == "$(cat -- "$FAKE_MACHINE_ID_REALPATH_OVERRIDE_FILE")" ]]; then
  printf '%s\n' "$path"
else
  printf '%s\n' "$path"
fi
FAKE_REALPATH
cat >"$bin/uname" <<'FAKE_UNAME'
#!/usr/bin/env bash
set -Eeuo pipefail
if [[ "${1:-}" == -s ]]; then printf 'Linux\n'; elif [[ "${1:-}" == -m ]]; then printf 'x86_64\n'; else printf 'Linux\n'; fi
FAKE_UNAME
cat >"$bin/stat" <<'FAKE_STAT'
#!/usr/bin/env bash
set -Eeuo pipefail
printf 'socket\n'
FAKE_STAT
real_sha256sum=$(command -v sha256sum)
cat >"$bin/sha256sum" <<'FAKE_SHA256SUM'
#!/usr/bin/env bash
set -Eeuo pipefail
if (($# == 0)); then exec "${REAL_SHA256SUM:?}"; fi
path_arg=${1:?}
if [[ "$path_arg" == -- ]]; then shift; path_arg=${1:?}; fi
if [[ "$path_arg" == "${FAKE_MACHINE_ID_PATH:?}" || ( -n "${FAKE_MACHINE_ID_REALPATH_OVERRIDE_FILE:-}" && -s "$FAKE_MACHINE_ID_REALPATH_OVERRIDE_FILE" && "$path_arg" == "$(cat -- "$FAKE_MACHINE_ID_REALPATH_OVERRIDE_FILE")" ) ]]; then
  printf '%s  %s\n' "$(cat -- "${FAKE_MACHINE_HASH_FILE:?}")" "$path_arg"
else
  exec "${REAL_SHA256SUM:?}" "$@"
fi
FAKE_SHA256SUM
cat >"$bin/flock" <<'FAKE_FLOCK'
#!/usr/bin/env bash
set -Eeuo pipefail
exit 0
FAKE_FLOCK
chmod 0755 "$bin"/*

run_deploy() {
  local output=$1 errors=$2 timeout=${3:-120} persist=${4:-600} omit_timeout_args=${5:-0}
  local -a deploy_args=(
    --host fake-host --ssh-executable "$bin/ssh" --scp-executable "$bin/scp"
    --ssh-connect-timeout-seconds "$timeout" --ssh-control-persist-seconds "$persist"
    --known-hosts-file "$known_hosts" --node-id fake-node --machine-id-sha256 "$machine_identity"
    --remote-docker-host "unix://$docker_socket" --docker-server-id "$docker_server_identity"
    --remote-docker-executable "$bin/docker" --install-root "$remote_root" --log-dir "$log_dir" --yes
    --object-store-root "$object_store_root" --worktree-root "$worktree_root" --cache-root "$cache_root"
    --private-overlay-root "$private_overlay_root" --temp-root "$temp_root" --wine-prefix-root "$wine_prefix_root"
  )
  if [[ "$omit_timeout_args" == 1 ]]; then
    deploy_args=(
      --host fake-host --ssh-executable "$bin/ssh" --scp-executable "$bin/scp"
      --known-hosts-file "$known_hosts" --node-id fake-node --machine-id-sha256 "$machine_identity"
      --remote-docker-host "unix://$docker_socket" --docker-server-id "$docker_server_identity"
      --remote-docker-executable "$bin/docker" --install-root "$remote_root" --log-dir "$log_dir" --yes
      --object-store-root "$object_store_root" --worktree-root "$worktree_root" --cache-root "$cache_root"
      --private-overlay-root "$private_overlay_root" --temp-root "$temp_root" --wine-prefix-root "$wine_prefix_root"
    )
  fi
  PATH="$bin:$PATH" REAL_SHA256SUM="$real_sha256sum" FAKE_SSH_LOG="$tmp/ssh.log" FAKE_SCP_LOG="$tmp/scp.log" \
    FAKE_SSH_EXTRA_STDOUT="${FAKE_SSH_EXTRA_STDOUT:-0}" FAKE_SSH_FAIL_AFTER_STAGE_READY="${FAKE_SSH_FAIL_AFTER_STAGE_READY:-0}" FAKE_SSH_REFUSE_EXIT="${FAKE_SSH_REFUSE_EXIT:-0}" FAKE_SSH_EXIT_SOCKET_ONLY="${FAKE_SSH_EXIT_SOCKET_ONLY:-0}" FAKE_SSH_OWNER_IDENTITY_MISMATCH="${FAKE_SSH_OWNER_IDENTITY_MISMATCH:-0}" FAKE_SSH_DRIFT_HOST_TOOLS="${FAKE_SSH_DRIFT_HOST_TOOLS:-0}" FAKE_SSH_DRIFT_MACHINE_ID="${FAKE_SSH_DRIFT_MACHINE_ID:-0}" FAKE_SSH_DRIFT_MACHINE_ID_PATH="${FAKE_SSH_DRIFT_MACHINE_ID_PATH:-0}" FAKE_OWNER_LOG="$tmp/fake-owner.log" FAKE_OS_ROOT="$fake_os_root" FAKE_MACHINE_ID_PATH="$fake_machine_id_path" FAKE_MACHINE_HASH_FILE="$fake_machine_hash_file" FAKE_MACHINE_ID_REALPATH_OVERRIDE_FILE="$fake_machine_override_file" \
    FAKE_DOCKER_LOG="$tmp/docker.log" FAKE_DOCKER_STATE="$state" \
    "$deploy" "${deploy_args[@]}" \
    >"$output" 2>"$errors" || { cat -- "$errors" >&2; return 1; }
}

set +e
PATH="$bin:$PATH" "$deploy" --host fake-host --ssh-executable "$bin/ssh" --scp-executable "$bin/scp" \
  --ssh-connect-timeout-seconds 120 --ssh-control-persist-seconds 600 \
  --install-root "$remote_root" --log-dir "$log_dir" --yes >"$tmp/missing.out" 2>"$tmp/missing.err"
missing_status=$?
set -e
[[ "$missing_status" != 0 && ! -s "$tmp/missing.out" ]] || { printf 'missing remote docker was accepted\n' >&2; exit 1; }
grep -Fq -- '--remote-docker-executable is required' "$tmp/missing.err"
set +e
run_deploy "$tmp/omitted-timeout.out" "$tmp/omitted-timeout.err" 120 600 1
omitted_timeout_status=$?
set -e
[[ "$omitted_timeout_status" != 0 && ! -s "$tmp/omitted-timeout.out" ]] || { printf 'omitted SSH timeout arguments were accepted\n' >&2; exit 1; }
grep -Fq -- '--ssh-connect-timeout-seconds is required' "$tmp/omitted-timeout.err"
set +e
run_deploy "$tmp/invalid-timeout.out" "$tmp/invalid-timeout.err" 0 600
invalid_timeout_status=$?
set -e
[[ "$invalid_timeout_status" != 0 && ! -s "$tmp/invalid-timeout.out" ]] || { printf 'invalid SSH timeout was accepted\n' >&2; exit 1; }
grep -Fq -- 'must be an integer from 1 to 300' "$tmp/invalid-timeout.err"
set +e
run_deploy "$tmp/invalid-persist.out" "$tmp/invalid-persist.err" 300 299
invalid_persist_status=$?
set -e
[[ "$invalid_persist_status" != 0 && ! -s "$tmp/invalid-persist.out" ]] || { printf 'invalid SSH ControlPersist was accepted\n' >&2; exit 1; }
grep -Fq -- 'must be at least the connect timeout' "$tmp/invalid-persist.err"
set +e
printf 'ID=debian\n' >"$fake_os_root/etc-os-release"
printf 'ID=debian\n' >"$fake_os_root/usr-lib-os-release"
run_deploy "$tmp/nonubuntu.out" "$tmp/nonubuntu.err"
nonubuntu_status=$?
set -e
printf 'ID=ubuntu\n' >"$fake_os_root/etc-os-release"
printf 'ID=ubuntu\n' >"$fake_os_root/usr-lib-os-release"
[[ "$nonubuntu_status" != 0 && ! -s "$tmp/nonubuntu.out" ]] || { printf 'non-Ubuntu fake host was accepted\n' >&2; exit 1; }
grep -Fq -- 'remote OS must contain exactly one ID=ubuntu' "$tmp/nonubuntu.err"

(cd /tmp && run_deploy "$tmp/happy.out" "$tmp/happy.err")
[[ -s "$tmp/happy.out" ]] || { printf 'happy receipt is empty\n' >&2; exit 1; }
[[ "$(wc -l <"$tmp/happy.out" | tr -d ' ')" == 0 ]] || { printf 'happy stdout has trailing newline\n' >&2; exit 1; }
python3 - "$tmp/happy.out" "$fake_machine_id_path" <<'PY'
import json, sys
raw = open(sys.argv[1], 'rb').read()
obj = json.loads(raw)
assert obj['schema_version'] == 'single-ubuntu-wine-deployment-receipt/v6'
assert obj['status'] == 'deployed'
assert obj['node_id'] == 'fake-node'
assert obj['ssh_connect_timeout_seconds'] == 120
assert obj['ssh_control_persist_seconds'] == 600
assert obj['machine_id_path'] == sys.argv[2]
assert obj['machine_id_sha256'] == 'sha256:' + 'a' * 64
assert obj['host_os_id'] == 'ubuntu'
assert obj['os_release_path'].endswith('/fake-os/usr-lib-os-release')
assert obj['uname_executable'].endswith('/bin/uname')
assert obj['os_release_identity'].startswith('sha256:')
assert obj['uname_identity'].startswith('sha256:')
assert obj['linux_runtime'] == 'ubuntu_container'
assert obj['linux_target_platform'] == 'linux/amd64'
assert obj['wine_runtime'] == 'ubuntu_wine_container'
assert obj['wine_target_platform'] == 'windows/amd64'
assert obj['cleanup_state'] == 'verified'
assert obj['linux_image_id'].startswith('sha256:')
assert obj['wine_image_id'].startswith('sha256:')
assert obj['go_path'] == '/usr/local/go/bin/go'
assert obj['wine_path'] == '/usr/lib/wine/wine64'
assert obj['wineserver_path'] == '/usr/lib/wine/wineserver64'
assert obj['probe_path'].endswith('/super-dolphin-heteroci-node')
assert obj['runner_path'].endswith('/linux_runner_daemon.sh')
assert obj['worker_path'].endswith('/super-dolphin-heteroci-node')
assert obj['object_store_root'].endswith('/object-store')
assert obj['worktree_root'].endswith('/worktree')
assert obj['cache_root'].endswith('/cache')
assert obj['private_overlay_root'].endswith('/overlay')
assert obj['temp_root'].endswith('/temp')
assert obj['wine_prefix_root'].endswith('/wine-prefix')
assert raw == json.dumps(obj, separators=(',', ':')).encode()
PY
grep -Fq -- 'fake-pe-stdout-routed-to-stderr' "$tmp/happy.err" || { printf 'fake PE stdout noise was not routed to stderr\n' >&2; exit 1; }
grep -Fq -- 'fake-wineserver-kill-stdout-routed-to-stderr' "$tmp/happy.err" || { printf 'fake wineserver kill noise was not routed to stderr\n' >&2; exit 1; }
grep -Fq -- 'fake-wineserver-wait-stdout-routed-to-stderr' "$tmp/happy.err" || { printf 'fake wineserver wait noise was not routed to stderr\n' >&2; exit 1; }
grep -Fq -- 'master-start' "$tmp/ssh.log" || { printf 'ControlMaster was not started\n' >&2; exit 1; }
grep -Fq -- 'master-check' "$tmp/ssh.log" || { printf 'ControlMaster was not checked\n' >&2; exit 1; }
grep -Fq -- 'master-exit socket_absent=true' "$tmp/ssh.log" || { printf 'ControlMaster socket absence was not proven\n' >&2; exit 1; }
grep -Fq -- 'ConnectTimeout=120' "$tmp/ssh.log" || { printf 'caller SSH connect timeout was not passed\n' >&2; exit 1; }
grep -Fq -- 'ControlPersist=600' "$tmp/ssh.log" || { printf 'caller SSH ControlPersist was not passed\n' >&2; exit 1; }
if find "$log_dir" -type s -print -quit 2>/dev/null | grep -q .; then
  printf 'ControlMaster socket leaked from local staging\n' >&2
  exit 1
fi
deployment_id=$(python3 - "$tmp/happy.out" <<'PY'
import json, sys
print(json.load(open(sys.argv[1]))['deployment_id'])
PY
)
release="$remote_root/releases/ubuntu-wine-$deployment_id"
[[ -f "$release/deployment.identity" && -f "$release/image.identity" && -f "$release/ubuntu-image.identity" ]] || { printf 'atomic release is incomplete\n' >&2; exit 1; }
[[ ! -e "$remote_root/.staging/ubuntu-wine-$deployment_id" ]] || { printf 'staging was not cleaned\n' >&2; exit 1; }
set +e
FAKE_SSH_DRIFT_HOST_TOOLS=1 run_deploy "$tmp/host-tools-drift.out" "$tmp/host-tools-drift.err"
host_tools_drift_status=$?
set -e
printf 'ID=ubuntu\n' >"$fake_os_root/etc-os-release"
printf 'ID=ubuntu\n' >"$fake_os_root/usr-lib-os-release"
[[ "$host_tools_drift_status" != 0 && ! -s "$tmp/host-tools-drift.out" ]] || { printf 'host tool drift was accepted\n' >&2; exit 1; }
grep -Fq -- 'remote OS must contain exactly one ID=ubuntu' "$tmp/host-tools-drift.err" || { printf 'host tool drift was not rechecked during install\n' >&2; exit 1; }
set +e
FAKE_SSH_DRIFT_MACHINE_ID=1 run_deploy "$tmp/machine-drift.out" "$tmp/machine-drift.err"
machine_drift_status=$?
set -e
printf '%s\n' aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa >"$fake_machine_hash_file"
[[ "$machine_drift_status" != 0 && ! -s "$tmp/machine-drift.out" ]] || { printf 'machine-id hash drift was accepted\n' >&2; exit 1; }
grep -Fq -- 'remote machine-id identity differs' "$tmp/machine-drift.err" || { printf 'machine-id hash drift was not rejected\n' >&2; exit 1; }
set +e
FAKE_SSH_DRIFT_MACHINE_ID_PATH=1 run_deploy "$tmp/machine-path-drift.out" "$tmp/machine-path-drift.err"
machine_path_drift_status=$?
set -e
: >"$fake_machine_override_file"
[[ "$machine_path_drift_status" != 0 && ! -s "$tmp/machine-path-drift.out" ]] || { printf 'machine-id path drift was accepted\n' >&2; exit 1; }
grep -Fq -- 'remote host tool identity changed before install' "$tmp/machine-path-drift.err" || { printf 'machine-id path drift was not rejected\n' >&2; exit 1; }
set +e
FAKE_SSH_REFUSE_EXIT=1 run_deploy "$tmp/refused-exit.out" "$tmp/refused-exit.err"
refused_exit_status=$?
set -e
[[ "$refused_exit_status" == 0 && -s "$tmp/refused-exit.out" ]] || { printf 'owned ControlMaster refusal was not recovered\n' >&2; exit 1; }
find "$log_dir" -type f -name '*control-master-exit.log' -exec grep -Fq 'control_master_term_sent=true' {} \; -print -quit | grep -q . || {
  printf 'exact ControlMaster owner termination was not recorded\n' >&2
  exit 1
}
set +e
FAKE_SSH_EXIT_SOCKET_ONLY=1 run_deploy "$tmp/socket-only.out" "$tmp/socket-only.err"
socket_only_status=$?
set -e
[[ "$socket_only_status" == 0 && -s "$tmp/socket-only.out" ]] || { printf 'socket-only ControlMaster refusal was not recovered\n' >&2; exit 1; }
find "$log_dir" -type f -name '*control-master-exit.log' -exec grep -Fq 'control_master_term_sent=true' {} \; -print -quit | grep -q . || {
  printf 'socket-only owner termination was not recorded\n' >&2
  exit 1
}
set +e
FAKE_SSH_OWNER_IDENTITY_MISMATCH=1 run_deploy "$tmp/owner-mismatch.out" "$tmp/owner-mismatch.err"
owner_mismatch_status=$?
set -e
[[ "$owner_mismatch_status" != 0 && ! -s "$tmp/owner-mismatch.out" ]] || { printf 'ControlMaster owner identity mismatch was accepted\n' >&2; exit 1; }
grep -Fq -- 'control_master_owner_verified=false' "$tmp/owner-mismatch.err" || { printf 'owner mismatch was not fail-closed\n' >&2; exit 1; }
mismatch_pid=$(tail -1 "$tmp/fake-owner.log")
if kill -0 "$mismatch_pid" 2>/dev/null; then kill -TERM "$mismatch_pid" 2>/dev/null || true; fi
sleep 0.1
if kill -0 "$mismatch_pid" 2>/dev/null; then printf 'mismatch fake owner cleanup failed\n' >&2; exit 1; fi
set +e
FAKE_SSH_EXTRA_STDOUT=1 run_deploy "$tmp/extra.out" "$tmp/extra.err"
extra_status=$?
set -e
[[ "$extra_status" != 0 && ! -s "$tmp/extra.out" ]] || { printf 'extra remote stdout was accepted\n' >&2; exit 1; }
grep -Fq -- '--progress=plain' "$tmp/docker.log"
grep -Fq -- '--load' "$tmp/docker.log"
grep -Fq -- '--rm' "$tmp/docker.log"
grep -Fq -- '/opt/super-dolphin/smoke/wine-smoke.exe' "$tmp/docker.log"
grep -Fq -- '/usr/lib/wine/wine64' "$tmp/docker.log.raw" || { printf 'real Wine loader qualification path missing\n' >&2; exit 1; }
grep -Fq -- '/usr/lib/wine/wineserver64' "$tmp/docker.log.raw" || { printf 'real wineserver qualification path missing\n' >&2; exit 1; }
grep -Fq -- '/tmp/wine-prefix' "$tmp/docker.log.raw" || { printf 'writable qualification prefix copy missing\n' >&2; exit 1; }
grep -Fq -- 'wineserver64 -w' "$tmp/docker.log.raw" || { printf 'wineserver wait missing\n' >&2; exit 1; }
grep -Fq -- 'wineserver64 -k' "$tmp/docker.log.raw" || { printf 'wineserver kill missing\n' >&2; exit 1; }
grep -Fq -- "/usr/bin/dpkg-query -W -f='\${Version}' wine64" "$tmp/docker.log.raw" || { printf 'exact wine64 package version query missing\n' >&2; exit 1; }
qualification_line=$(grep -F 'wine-qualification:prefix_copy' "$tmp/docker.log.raw" | tail -1)
case "$qualification_line" in
  *wine-qualification:prefix_copy*wine-qualification:version*wine-qualification:pe*wine-qualification:kill*wine-qualification:wait*) ;;
  *) printf 'qualification markers are missing or out of order\n' >&2; exit 1 ;;
esac
python3 - "$tmp/docker.log.raw" <<'PY'
import subprocess
import sys

raw = open(sys.argv[1], encoding='utf-8').read().splitlines()
scripts = [line for line in raw if 'wine-qualification:wait' in line]
assert scripts
expected = 'printf "%s\\n" "wine-9.0|smoke-ok"'
assert expected in scripts[-1]
assert 'printf "%s\\\\n" "wine-9.0|smoke-ok"' not in scripts[-1]
result = subprocess.run(['/bin/sh', '-c', expected], check=True, capture_output=True).stdout
assert result == b'wine-9.0|smoke-ok\n'
PY
if grep -Fq -- '/usr/bin/wine /opt' "$tmp/docker.log.raw"; then
  printf 'Wine wrapper qualification path was used\n' >&2
  exit 1
fi
grep -Fq -- '--network none' "$tmp/docker.log"
grep -Fq -- '--cap-drop ALL' "$tmp/docker.log"
grep -Fq -- '--security-opt no-new-privileges' "$tmp/docker.log"
grep -Fq -- '--read-only' "$tmp/docker.log"
grep -Fq -- '--tmpfs' "$tmp/docker.log"
grep -Fq -- 'noexec' "$tmp/docker.log"
grep -Fq -- '--entrypoint /bin/sh' "$tmp/docker.log"
if grep -Fq -- '--privileged' "$tmp/docker.log"; then
  printf 'privileged Docker execution was requested\n' >&2
  exit 1
fi

build_count_before=$(grep -c 'buildx build' "$tmp/docker.log" || true)
run_deploy "$tmp/reused.out" "$tmp/reused.err"
build_count_after=$(grep -c 'buildx build' "$tmp/docker.log" || true)
[[ "$build_count_before" == "$build_count_after" ]] || { printf 'same identity rebuilt image\n' >&2; exit 1; }
cmp -s "$tmp/happy.out" "$tmp/reused.out" || { printf 'same identity receipt drifted\n' >&2; exit 1; }

run_count_before=$(grep -c 'run --rm' "$tmp/docker.log" || true)
printf '%s\n' replaced-label >"$state/wine-image-label"
set +e
run_deploy "$tmp/replaced-label.out" "$tmp/replaced-label.err"
replaced_label_status=$?
set -e
[[ "$replaced_label_status" != 0 && ! -s "$tmp/replaced-label.out" ]] || { printf 'replaced image label was accepted\n' >&2; exit 1; }
[[ "$(grep -c 'run --rm' "$tmp/docker.log" || true)" == "$run_count_before" ]] || { printf 'replaced image label reached smoke\n' >&2; exit 1; }
printf '%s\n' "$(sed -n 's/^image_id=//p' "$release/image.identity")" >"$state/wine-image-id"
printf '%s\n' restored-label >"$state/wine-image-label"
printf '%s\n' image >"$state/wine-image"
printf '%s\n' sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa >"$state/wine-image-id"
set +e
run_deploy "$tmp/replaced-id.out" "$tmp/replaced-id.err"
replaced_id_status=$?
set -e
[[ "$replaced_id_status" != 0 && ! -s "$tmp/replaced-id.out" ]] || { printf 'replaced image ID was accepted\n' >&2; exit 1; }
[[ "$(grep -c 'run --rm' "$tmp/docker.log" || true)" == "$run_count_before" ]] || { printf 'replaced image ID reached smoke\n' >&2; exit 1; }

rm -rf -- "$remote_root/releases" "$remote_root/.staging"
rm -f -- "$state"/*-image "$state"/*-image-label "$state"/*-image-id
set +e
FAKE_SSH_FAIL_AFTER_STAGE_READY=1 run_deploy "$tmp/stage-fail.out" "$tmp/stage-fail.err"
stage_fail_status=$?
set -e
[[ "$stage_fail_status" != 0 && ! -s "$tmp/stage-fail.out" ]] || { printf 'stage_ready transport failure was accepted\n' >&2; exit 1; }
grep -Fq -- 'fake remote failure after stage_ready' "$tmp/stage-fail.err"
grep -Fq -- 'staging_cleanup=verified' "$tmp/stage-fail.err" || { printf 'uncertain preflight did not invoke staging cleanup\n' >&2; exit 1; }
if find "$remote_root/.staging" -mindepth 1 -print -quit 2>/dev/null | grep -q .; then
  printf 'stage_ready transport failure leaked staging\n' >&2
  exit 1
fi
set +e
FAKE_DOCKER_FAIL_SMOKE=1 run_deploy "$tmp/fail.out" "$tmp/fail.err"
fail_status=$?
set -e
[[ "$fail_status" != 0 && ! -s "$tmp/fail.out" ]] || { printf 'smoke failure was accepted\n' >&2; exit 1; }
[[ ! -e "$state/container" && ! -e "$state/image" ]] || { printf 'failed deployment leaked owned Docker state\n' >&2; exit 1; }
if find "$remote_root/.staging" -mindepth 1 -print -quit 2>/dev/null | grep -q .; then
  printf 'failed deployment leaked staging\n' >&2
  exit 1
fi
find "$log_dir" -type f -print -quit | grep -q .
if find "$log_dir" -type s -print -quit 2>/dev/null | grep -q .; then
  printf 'ControlMaster socket leaked after final deployment\n' >&2
  exit 1
fi
if [[ -f "$tmp/fake-owner.log" ]]; then
  while IFS= read -r owner_pid; do
    [[ "$owner_pid" =~ ^[0-9]+$ ]] || { printf 'fake owner pid format drifted\n' >&2; exit 1; }
    if kill -0 "$owner_pid" 2>/dev/null; then
      printf 'fake ControlMaster owner process leaked: %s\n' "$owner_pid" >&2
      exit 1
    fi
  done <"$tmp/fake-owner.log"
fi
printf 'deploy_single_ubuntu_wine contract: PASS\n'
