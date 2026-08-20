#!/usr/bin/env bash
set -Eeuo pipefail

root=$(cd -- "$(dirname -- "$0")/../.." && pwd -P)
die() { printf 'heteroci deploy script test: %s\n' "$*" >&2; exit 1; }
linux_daemon="$root/scripts/deploy/linux/linux_runner_daemon.sh"
mac_daemon="$root/scripts/deploy/mac/mac_runner_daemon.sh"
windows_runner="$root/scripts/deploy/windows/windows_runner.ps1"
linux_digest=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa

go_string_constant() {
  local file=$1 name=$2
  awk -v name="$name" '
    $1 == name && $2 == "=" {
      value=$3
      sub(/^"/, "", value)
      sub(/"$/, "", value)
      print value
      found++
    }
    END { if (found != 1) exit 1 }
  ' "$file"
}

canonical_authority=$(go_string_constant "$root/internal/devtools/heteroci/manifest.go" AuthorityLocalNonAuthoritative) || die 'unable to derive canonical authority'
canonical_ubuntu_runtime=$(go_string_constant "$root/internal/devtools/heteroci/manifest.go" RuntimeUbuntuContainer) || die 'unable to derive canonical Ubuntu runtime'
canonical_darling_runtime=$(go_string_constant "$root/internal/devtools/heteroci/manifest.go" RuntimeDarlingCompatRuntime) || die 'unable to derive canonical Darling runtime'
canonical_windows_runtime=$(go_string_constant "$root/internal/devtools/heteroci/manifest.go" RuntimeWindowsNative) || die 'unable to derive canonical Windows runtime'
canonical_darwin_runtime=$(go_string_constant "$root/internal/devtools/heteroci/manifest.go" RuntimeDarwinNative) || die 'unable to derive canonical Darwin runtime'
canonical_status=$(go_string_constant "$root/internal/devtools/heterociworkflow/types.go" ResultStatusNotVerified) || die 'unable to derive canonical local result status'

expect_linux_reject() {
  local label=$1 expected=$2 output
  shift 2
  if output=$(bash "$linux_daemon" "$@" 2>&1); then
    die "$label was unexpectedly accepted"
  fi
  [[ "$output" == *"$expected"* ]] || die "$label returned the wrong failure: $output"
}

run_linux_runtime_case() {
  local runtime=$1 runtime_marker=$2 image=$3 expected_cpu=$4
  local argv_log="$linux_fixture/argv-$runtime.log"
  local expected actual runtime_output
  if ! DOCKER_ARGV_LOG="$argv_log" PATH="$linux_fixture/bin:$PATH" \
    bash "$linux_fixture/linux_runner_daemon.sh" --repo "$root" --mode docker --runtime "$runtime" --docker-image "$image" -- /bin/echo "$runtime" \
    >"$linux_fixture/out-$runtime.log" 2>"$linux_fixture/err-$runtime.log"; then
    die "Linux $runtime runtime fake Docker execution failed"
  fi
  [[ -f "$argv_log" ]] || die "Linux $runtime runtime did not invoke fake Docker"
  actual=$(<"$argv_log")
  runtime_output=$(<"$linux_fixture/out-$runtime.log")
  [[ "$runtime_output" == *"runtime=$runtime_marker"* ]] || die "Linux $runtime result envelope lost canonical runtime"
  expected=$(printf '%s\n' run --rm --platform linux/amd64 --cpus "$expected_cpu" --memory 1073741824 \
    -v "$root:/workspace:rw" -w /workspace \
    -e LOCAL_NON_AUTHORITATIVE=1 -e NOT_VERIFIED=1 \
    -e "SUPER_DOLPHIN_AUTHORITY=$canonical_authority" -e "SUPER_DOLPHIN_RESULT_STATUS=$canonical_status" \
    -e "SUPER_DOLPHIN_RUNTIME=$runtime_marker" \
    "$image" /bin/echo "$runtime")
  [[ "$actual" == "$expected" ]] || die "Linux $runtime runtime Docker argv drifted"
}

run_linux_native_case() {
  local output output_file="$linux_fixture/out-native.log"
  if ! EXPECTED_AUTHORITY="$canonical_authority" EXPECTED_STATUS="$canonical_status" \
    PATH="$linux_fixture/bin:$PATH" bash "$linux_fixture/linux_runner_daemon.sh" \
    --repo "$root" --mode native -- /bin/bash -Eeuo pipefail -c \
    'test "$SUPER_DOLPHIN_AUTHORITY" = "$EXPECTED_AUTHORITY"; test "$SUPER_DOLPHIN_RESULT_STATUS" = "$EXPECTED_STATUS"; test -z "${SUPER_DOLPHIN_RUNTIME+x}"' \
    >"$output_file" 2>"$linux_fixture/err-native.log"; then
    die 'Linux native canonical marker execution failed'
  fi
  output=$(<"$output_file")
  [[ "$output" == *'authority=LOCAL_NON_AUTHORITATIVE status=NOT_VERIFIED'* ]] || die 'Linux native result envelope lost canonical authority/status'
}

run_linux_large_cpu_case() {
  local output_file="$linux_fixture/out-large-cpu.log" output
  printf '%s\n' '9223372036854775 1' >"$linux_fixture/cgroup/cpu.max"
  if ! PATH="$linux_fixture/bin:$PATH" bash "$linux_fixture/linux_runner_daemon.sh" \
    --repo "$root" --mode native -- /usr/bin/true >"$output_file" 2>"$linux_fixture/err-large-cpu.log"; then
    die 'Linux large cgroup CPU fixture failed'
  fi
  output=$(<"$output_file")
  [[ "$output" == *'cpu_milli=9223372036854775000 logical_cpu_ceiling=9223372036854775'* ]] || die 'Linux logical CPU ceiling overflowed'
}

assert_linux_pid_gone() {
  local label=$1 pid_file=$2 pid
  [[ -s "$pid_file" ]] || die "$label did not record its descendant PID"
  pid=$(<"$pid_file")
  [[ "$pid" =~ ^[1-9][0-9]*$ ]] || die "$label recorded an invalid descendant PID"
  if kill -0 "$pid" 2>/dev/null; then
    kill -KILL "$pid" 2>/dev/null || true
    die "$label left descendant PID $pid running"
  fi
}

run_linux_process_group_cases() {
  [[ "$(uname -s)" == Linux ]] || return 0
  command -v setsid >/dev/null 2>&1 || die 'Linux process-group test requires setsid'
  local descendant_file="$linux_fixture/descendant.pid"
  local ignore_term_file="$linux_fixture/ignore-term.pid"
  local leak_error="$linux_fixture/err-descendant.log"

  if DESCENDANT_PID_FILE="$descendant_file" bash "$linux_fixture/linux_runner_daemon.sh" \
    --repo "$root" --mode native --timeout-seconds 3 -- /bin/bash -Eeuo pipefail -c \
    'sleep 30 & printf "%s\n" "$!" > "$DESCENDANT_PID_FILE"' \
    >"$linux_fixture/out-descendant.log" 2>"$leak_error"; then
    die 'Linux runner accepted a leader that left process-group descendants'
  fi
  grep -Fq 'command exited with process-group descendants' "$leak_error" || die 'Linux leader-exit cleanup returned the wrong blocker'
  assert_linux_pid_gone 'Linux leader-exit cleanup' "$descendant_file"

  if IGNORE_TERM_PID_FILE="$ignore_term_file" bash "$linux_fixture/linux_runner_daemon.sh" \
    --repo "$root" --mode native --timeout-seconds 1 -- /bin/bash -Eeuo pipefail -c \
    'trap "" TERM; sleep 30 & printf "%s\n" "$!" > "$IGNORE_TERM_PID_FILE"; wait' \
    >"$linux_fixture/out-ignore-term.log" 2>"$linux_fixture/err-ignore-term.log"; then
    die 'Linux runner timeout unexpectedly succeeded'
  fi
  assert_linux_pid_gone 'Linux timeout cleanup' "$ignore_term_file"
}

bash -n "$root/scripts/deploy/mac/deploy_mac_host.sh" "$root/scripts/deploy/mac/mac_runner_daemon.sh" "$root/scripts/deploy/linux/linux_runner_daemon.sh"
grep -Fq -- '--yes' "$root/scripts/deploy/mac/deploy_mac_host.sh" || die 'mac installer lost noninteractive flag'
grep -Fq -- '--command-file' "$root/scripts/deploy/mac/deploy_mac_host.sh" || die 'mac installer does not bind a command file'
grep -Fq '<string>--</string><string>/bin/bash</string>' "$root/scripts/deploy/mac/deploy_mac_host.sh" || die 'mac LaunchAgent command handoff is missing'
grep -Fq 'sysctl -n hw.logicalcpu' "$root/scripts/deploy/mac/mac_runner_daemon.sh" || die 'mac dynamic CPU probe missing'
grep -Fq 'timeout_seconds=120' "$root/scripts/deploy/mac/mac_runner_daemon.sh" || die 'mac 120 second watchdog default missing'
grep -Fq -- '--platform linux/amd64' "$root/scripts/deploy/mac/mac_runner_daemon.sh" || die 'Darwin compatibility container platform missing'
grep -Fq '/sys/fs/cgroup/cpu.max' "$root/scripts/deploy/mac/mac_runner_daemon.sh" || die 'Mac Docker cgroup v2 CPU probe missing'
grep -Fq '/sys/fs/cgroup/memory.max' "$root/scripts/deploy/mac/mac_runner_daemon.sh" || die 'Mac Docker cgroup v2 memory probe missing'
grep -Fq "SUPER_DOLPHIN_RUNTIME=$canonical_darling_runtime" "$root/Dockerfile.ci-runner-Darwin-compat" || die 'Darling compatibility image canonical runtime marker missing'
grep -Fq "SUPER_DOLPHIN_RUNTIME=$canonical_ubuntu_runtime" "$root/Dockerfile.ci-runner-ubuntu" || die 'Ubuntu image canonical runtime marker missing'
! grep -Fq 'SUPER_DOLPHIN_RUNTIME=Darwin-compat' "$root/Dockerfile.ci-runner-Darwin-compat" || die 'Darling compatibility image retained noncanonical runtime marker'
! grep -Fq 'SUPER_DOLPHIN_RUNTIME_KIND=' "$root/Dockerfile.ci-runner-Darwin-compat" || die 'Darling compatibility image retained duplicate runtime identity field'
grep -Fq "com.super-dolphin.qualification=\"$canonical_status\"" "$root/Dockerfile.ci-runner-Darwin-compat" || die 'Darling compatibility image lost explicit NOT_VERIFIED qualification'
! grep -Fq 'com.super-dolphin.qualification="VERIFIED"' "$root/Dockerfile.ci-runner-Darwin-compat" || die 'Darling compatibility image falsely advertises verified qualification'
grep -Fq 'export SUPER_DOLPHIN_AUTHORITY=LOCAL_NON_AUTHORITATIVE' "$linux_daemon" || die 'Linux canonical authority marker missing'
grep -Fq 'export SUPER_DOLPHIN_RESULT_STATUS=NOT_VERIFIED' "$linux_daemon" || die 'Linux canonical result status marker missing'
grep -Fq 'export SUPER_DOLPHIN_AUTHORITY=LOCAL_NON_AUTHORITATIVE' "$mac_daemon" || die 'Mac canonical authority marker missing'
grep -Fq 'export SUPER_DOLPHIN_RESULT_STATUS=NOT_VERIFIED' "$mac_daemon" || die 'Mac canonical result status marker missing'
grep -Fq "native_runtime_marker=$canonical_darwin_runtime" "$mac_daemon" || die 'Mac native canonical runtime marker missing'
grep -Fq "\$env:SUPER_DOLPHIN_RUNTIME = '$canonical_windows_runtime'" "$windows_runner" || die 'Windows canonical runtime marker missing'
grep -Fq "runtime = '$canonical_windows_runtime'" "$windows_runner" || die 'Windows result runtime field missing'
grep -Fq 'Add-MpPreference' "$root/scripts/deploy/windows/deploy_windows_host.ps1" || die 'Defender exclusion is not explicit'
grep -Fq 'CreateJobObject' "$root/scripts/deploy/windows/windows_runner.ps1" || die 'Windows Job Object control missing'
grep -Fq 'JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE' "$root/scripts/deploy/windows/windows_runner.ps1" || die 'Windows Job Object close-kill boundary missing'
grep -Fq 'TotalPhysicalMemory' "$root/scripts/deploy/windows/windows_runner.ps1" || die 'Windows dynamic memory probe missing'
grep -Fq 'TimeoutSeconds -gt 120' "$root/scripts/deploy/windows/windows_runner.ps1" || die 'Windows 120 second watchdog bound missing'
grep -Fq 'CommandBase64' "$root/scripts/deploy/windows/deploy_windows_host.ps1" || die 'scheduled task command handoff is not explicit'
grep -Fq '$cgroup_root/cgroup.controllers' "$root/scripts/deploy/linux/linux_runner_daemon.sh" || die 'Linux cgroup v2 probe missing'
grep -Fq 'cpu_milli=' "$root/scripts/deploy/linux/linux_runner_daemon.sh" || die 'Linux cgroup CPU probe lost millicore precision'
grep -Fq 'terminate_linux_process_group "$child_pid"' "$root/scripts/deploy/linux/linux_runner_daemon.sh" || die 'Linux process-group cleanup missing'
! grep -Fq 'watchdog_pid' "$root/scripts/deploy/linux/linux_runner_daemon.sh" || die 'Linux watchdog can orphan its sleep child'
grep -Fq -- '--runtime ubuntu|darling-compat' "$linux_daemon" || die 'Linux explicit runtime help is missing'

ubuntu_image="super-dolphin-ci-runner:ubuntu@sha256:$linux_digest"
darling_image="super-dolphin-ci-runner:darwin-compat@sha256:$linux_digest"
expect_linux_reject 'Linux missing runtime' '--runtime is required for docker mode' \
  --repo "$root" --mode docker --docker-image "$ubuntu_image" -- true
expect_linux_reject 'Linux duplicate runtime' '--runtime must be provided once' \
  --repo "$root" --mode docker --runtime ubuntu --runtime ubuntu --docker-image "$ubuntu_image" -- true
expect_linux_reject 'Linux unknown runtime' '--runtime must be ubuntu or darling-compat' \
  --repo "$root" --mode docker --runtime unknown --docker-image "$ubuntu_image" -- true
expect_linux_reject 'Linux native runtime' '--runtime is forbidden in native mode' \
  --repo "$root" --mode native --runtime ubuntu -- true
expect_linux_reject 'Linux Ubuntu runtime with Darling image' 'ubuntu runtime requires --docker-image super-dolphin-ci-runner:ubuntu@sha256:' \
  --repo "$root" --mode docker --runtime ubuntu --docker-image "$darling_image" -- true
expect_linux_reject 'Linux Darling runtime with Ubuntu image' 'darling-compat runtime requires --docker-image super-dolphin-ci-runner:darwin-compat@sha256:' \
  --repo "$root" --mode docker --runtime darling-compat --docker-image "$ubuntu_image" -- true

linux_fixture=$(mktemp -d "${TMPDIR:-/tmp}/heteroci-linux-runtime.XXXXXX") || die 'unable to create Linux runtime fixture'
cleanup_linux_fixture() {
  [[ -n "${linux_fixture:-}" ]] && rm -rf -- "$linux_fixture"
}
trap cleanup_linux_fixture EXIT
mkdir -p "$linux_fixture/bin" "$linux_fixture/cgroup"
printf '%s\n' cpu memory >"$linux_fixture/cgroup/cgroup.controllers"
printf '%s\n' '200000 100000' >"$linux_fixture/cgroup/cpu.max"
printf '%s\n' 1073741824 >"$linux_fixture/cgroup/memory.max"
printf '%s\n' '#!/usr/bin/env bash' '[[ "${1:-}" == -s ]] || exit 97' 'printf "Linux\n"' >"$linux_fixture/bin/uname"
printf '%s\n' '#!/usr/bin/env bash' 'exit 0' >"$linux_fixture/bin/git"
printf '%s\n' '#!/usr/bin/env bash' 'exec "$@"' >"$linux_fixture/bin/setsid"
printf '%s\n' '#!/usr/bin/env bash' 'set -Eeuo pipefail' \
  'if [[ "${1:-}" == info ]]; then exit 0; fi' \
  '[[ "${1:-}" == run ]] || exit 97' \
  'printf "%s\n" "$@" >"${DOCKER_ARGV_LOG:?}"' >"$linux_fixture/bin/docker"
chmod 700 "$linux_fixture/bin/uname" "$linux_fixture/bin/git" "$linux_fixture/bin/setsid" "$linux_fixture/bin/docker"
awk -v fixture_root="$linux_fixture/cgroup" '
  $0 == "cgroup_root=\047/sys/fs/cgroup\047" {
    printf "cgroup_root=%c%s%c\n", 39, fixture_root, 39
    replacement_count++
    next
  }
  { print }
  END { if (replacement_count != 1) exit 1 }
' "$linux_daemon" >"$linux_fixture/linux_runner_daemon.sh" || die 'unable to bind Linux cgroup fixture'

run_linux_runtime_case ubuntu "$canonical_ubuntu_runtime" "$ubuntu_image" 2
printf '%s\n' '150000 100000' >"$linux_fixture/cgroup/cpu.max"
run_linux_runtime_case darling-compat "$canonical_darling_runtime" "$darling_image" 1.5
run_linux_native_case
run_linux_large_cpu_case
run_linux_process_group_cases
printf '%s\n' 'heteroci deploy script contract: PASS'
