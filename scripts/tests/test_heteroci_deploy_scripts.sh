#!/usr/bin/env bash
# shellcheck disable=SC2016 # PowerShell 与 fake-script 字面源码必须由目标解释器展开。
set -Eeuo pipefail

root=$(cd -- "$(dirname -- "$0")/../.." && pwd -P)
die() { printf 'heteroci deploy script test: %s\n' "$*" >&2; exit 1; }
linux_daemon="$root/scripts/deploy/linux/linux_runner_daemon.sh"
mac_daemon="$root/scripts/deploy/mac/mac_runner_daemon.sh"
windows_runner="$root/scripts/deploy/windows/windows_runner.ps1"
linux_digest=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
real_git=$(command -v git) || die 'git is required for Linux shared source tests'

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
canonical_status=$(go_string_constant "$root/internal/devtools/heteroci/manifest.go" ResultStatusNotVerified) || die 'unable to derive canonical local result status'

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

run_linux_shared_cache_case() {
  local shared_root="$linux_fixture/shared-source" cache_root="$linux_fixture/shared-cache"
  local target_repo="$linux_fixture/target-repository" source_commit source_tree object_format object_manifest host_goos host_goarch cas_file
  local union_a_pid union_b_pid conflict_a_pid conflict_b_pid conflict_a_status conflict_b_status
  local first_output="$linux_fixture/out-shared-first.log" second_output="$linux_fixture/out-shared-second.log"
  local identity="sha256:$linux_digest" other_workload="sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" repository_id=fixture-repository
  mkdir -p "$target_repo" "$shared_root/$repository_id/objects" "$cache_root"
  "$real_git" -C "$target_repo" init -q
  host_goos=$(go env GOOS) || die 'unable to resolve host GOOS for shared-cache fixture'
  host_goarch=$(go env GOARCH) || die 'unable to resolve host GOARCH for shared-cache fixture'
  printf '%s\n' 'module example.invalid/cachefixture' 'go 1.26.5' >"$target_repo/go.mod"
  printf '%s\n' 'package cachefixture' 'func Value() int { return 42 }' >"$target_repo/cachefixture.go"
  printf '%s\n' ignored.go >"$target_repo/.gitignore"
  "$real_git" -C "$target_repo" -c user.name=fixture -c user.email=fixture@example.invalid add .gitignore go.mod cachefixture.go
  "$real_git" -C "$target_repo" -c user.name=fixture -c user.email=fixture@example.invalid commit -qm fixture
  source_commit=$("$real_git" -C "$target_repo" rev-parse HEAD) || die 'unable to resolve Linux shared-source fixture commit'
  source_tree=$("$real_git" -C "$target_repo" rev-parse 'HEAD^{tree}') || die 'unable to resolve Linux shared-source fixture tree'
  object_format=$("$real_git" -C "$target_repo" rev-parse --show-object-format) || die 'unable to resolve Linux shared-source object format'
  cp -a "$target_repo/.git/objects/." "$shared_root/$repository_id/objects/" || die 'unable to stage Linux shared-source objects'
  chmod -R a-w "$shared_root/$repository_id/objects" || die 'unable to make Linux shared-source objects read-only'
  object_manifest="$shared_root/$repository_id/object-store.identity"
  printf '%s\n' 'schema_version=ubuntu-shared-git-object-store/v1' "repository_id=$repository_id" "object_format=$object_format" >"$object_manifest"
  REAL_GIT="$real_git" PATH="$linux_fixture/bin:$PATH" bash "$linux_fixture/linux_runner_daemon.sh" \
    --repo "$target_repo" --mode native \
    --shared-root "$shared_root" --cache-root "$cache_root" --repository-id "$repository_id" --node-id ubuntu-a \
    --object-format "$object_format" --source-commit "$source_commit" --source-tree "$source_tree" \
    --toolchain-identity "$identity" --goos "$host_goos" --goarch "$host_goarch" --cgo-enabled 0 \
    --module-identity "$identity" --workload-identity "$other_workload" -- \
    /bin/bash -Eeuo pipefail -c \
    'go build ./...; mkdir -p "$GOMODCACHE/cache/download/example" "$GOCACHE/ab"; printf module >"$GOMODCACHE/cache/download/example/module.zip"; printf build >"$GOCACHE/ab/build-entry"' \
    >"$first_output" 2>"$linux_fixture/err-shared-first.log" || die "Linux first shared-cache publication failed: $(<"$linux_fixture/err-shared-first.log")"
  grep -Fq 'cache_seed_state=cold' "$first_output" || die "Linux first shared-cache run did not report a cold seed: stdout=$(<"$first_output") stderr=$(<"$linux_fixture/err-shared-first.log")"
  grep -Fq 'cache_publish=complete' "$first_output" || die 'Linux first shared-cache run did not publish its incremental cache'

  REAL_GIT="$real_git" PATH="$linux_fixture/bin:$PATH" bash "$linux_fixture/linux_runner_daemon.sh" \
    --repo "$target_repo" --mode native \
    --shared-root "$shared_root" --cache-root "$cache_root" --repository-id "$repository_id" --node-id ubuntu-b \
    --object-format "$object_format" --source-commit "$source_commit" --source-tree "$source_tree" \
    --toolchain-identity "$identity" --goos "$host_goos" --goarch "$host_goarch" --cgo-enabled 0 \
    --module-identity "$identity" --workload-identity "$identity" -- \
    /bin/bash -Eeuo pipefail -c \
    'test "$(cat "$GOMODCACHE/cache/download/example/module.zip")" = module; test "$(cat "$GOCACHE/ab/build-entry")" = build; go build -x ./... 2>&1' \
    >"$second_output" 2>"$linux_fixture/err-shared-second.log" || die 'Linux second node did not consume the shared incremental cache'
  grep -Fq 'cache_seed_state=ready' "$second_output" || die 'Linux second node did not report a ready shared seed'
  ! grep -Fq '/compile ' "$second_output" || die 'Linux second node recompiled despite the shared Go build cache'
  [[ -d "$cache_root/$repository_id" ]] || die 'Linux cache lost its repository namespace'
  [[ $(find "$cache_root/$repository_id" -path '*/nodes/ubuntu-a' -type d | wc -l | tr -d ' ') == 1 ]] || die 'Linux first node overlay is missing'
  [[ $(find "$cache_root/$repository_id" -path '*/nodes/ubuntu-b' -type d | wc -l | tr -d ' ') == 1 ]] || die 'Linux second node overlay is missing'

  REAL_GIT="$real_git" PATH="$linux_fixture/bin:$PATH" bash "$linux_fixture/linux_runner_daemon.sh" \
    --repo "$target_repo" --mode native --shared-root "$shared_root" --cache-root "$cache_root" \
    --repository-id "$repository_id" --node-id ubuntu-union-a --object-format "$object_format" \
    --source-commit "$source_commit" --source-tree "$source_tree" --toolchain-identity "$identity" \
    --goos "$host_goos" --goarch "$host_goarch" --cgo-enabled 0 --module-identity "$identity" --workload-identity "$identity" -- \
    /bin/bash -Eeuo pipefail -c 'mkdir -p "$GOCACHE/concurrent"; printf union-a >"$GOCACHE/concurrent/a"; sleep 1' \
    >"$linux_fixture/out-union-a.log" 2>"$linux_fixture/err-union-a.log" &
  union_a_pid=$!
  REAL_GIT="$real_git" PATH="$linux_fixture/bin:$PATH" bash "$linux_fixture/linux_runner_daemon.sh" \
    --repo "$target_repo" --mode native --shared-root "$shared_root" --cache-root "$cache_root" \
    --repository-id "$repository_id" --node-id ubuntu-union-b --object-format "$object_format" \
    --source-commit "$source_commit" --source-tree "$source_tree" --toolchain-identity "$identity" \
    --goos "$host_goos" --goarch "$host_goarch" --cgo-enabled 0 --module-identity "$identity" --workload-identity "$identity" -- \
    /bin/bash -Eeuo pipefail -c 'mkdir -p "$GOCACHE/concurrent"; printf union-b >"$GOCACHE/concurrent/b"; sleep 1' \
    >"$linux_fixture/out-union-b.log" 2>"$linux_fixture/err-union-b.log" &
  union_b_pid=$!
  wait "$union_a_pid" || die "Linux concurrent publisher A failed: $(<"$linux_fixture/err-union-a.log")"
  wait "$union_b_pid" || die "Linux concurrent publisher B failed: $(<"$linux_fixture/err-union-b.log")"
  REAL_GIT="$real_git" PATH="$linux_fixture/bin:$PATH" bash "$linux_fixture/linux_runner_daemon.sh" \
    --repo "$target_repo" --mode native --shared-root "$shared_root" --cache-root "$cache_root" \
    --repository-id "$repository_id" --node-id ubuntu-union-reader --object-format "$object_format" \
    --source-commit "$source_commit" --source-tree "$source_tree" --toolchain-identity "$identity" \
    --goos "$host_goos" --goarch "$host_goarch" --cgo-enabled 0 --module-identity "$identity" --workload-identity "$identity" -- \
    /bin/bash -Eeuo pipefail -c 'test "$(cat "$GOCACHE/concurrent/a")" = union-a; test "$(cat "$GOCACHE/concurrent/b")" = union-b' \
    >"$linux_fixture/out-union-reader.log" 2>"$linux_fixture/err-union-reader.log" || die 'Linux concurrent cache promotions did not produce a union generation'

  set +e
  REAL_GIT="$real_git" PATH="$linux_fixture/bin:$PATH" bash "$linux_fixture/linux_runner_daemon.sh" \
    --repo "$target_repo" --mode native --shared-root "$shared_root" --cache-root "$cache_root" \
    --repository-id "$repository_id" --node-id ubuntu-conflict-a --object-format "$object_format" \
    --source-commit "$source_commit" --source-tree "$source_tree" --toolchain-identity "$identity" \
    --goos "$host_goos" --goarch "$host_goarch" --cgo-enabled 0 --module-identity "$identity" --workload-identity "$identity" -- \
    /bin/bash -Eeuo pipefail -c 'mkdir -p "$GOCACHE/concurrent"; printf conflict-a >"$GOCACHE/concurrent/conflict"; sleep 1' \
    >"$linux_fixture/out-conflict-a.log" 2>"$linux_fixture/err-conflict-a.log" &
  conflict_a_pid=$!
  REAL_GIT="$real_git" PATH="$linux_fixture/bin:$PATH" bash "$linux_fixture/linux_runner_daemon.sh" \
    --repo "$target_repo" --mode native --shared-root "$shared_root" --cache-root "$cache_root" \
    --repository-id "$repository_id" --node-id ubuntu-conflict-b --object-format "$object_format" \
    --source-commit "$source_commit" --source-tree "$source_tree" --toolchain-identity "$identity" \
    --goos "$host_goos" --goarch "$host_goarch" --cgo-enabled 0 --module-identity "$identity" --workload-identity "$identity" -- \
    /bin/bash -Eeuo pipefail -c 'mkdir -p "$GOCACHE/concurrent"; printf conflict-b >"$GOCACHE/concurrent/conflict"; sleep 1' \
    >"$linux_fixture/out-conflict-b.log" 2>"$linux_fixture/err-conflict-b.log" &
  conflict_b_pid=$!
  wait "$conflict_a_pid"; conflict_a_status=$?
  wait "$conflict_b_pid"; conflict_b_status=$?
  set -e
  if ! { [[ "$conflict_a_status" == 0 && "$conflict_b_status" != 0 ]] || [[ "$conflict_a_status" != 0 && "$conflict_b_status" == 0 ]]; }; then
    die "Linux concurrent collision did not yield one winner: a=$conflict_a_status b=$conflict_b_status"
  fi
  grep -Fq 'shared cache generation path collision detected' "$linux_fixture/err-conflict-a.log" "$linux_fixture/err-conflict-b.log" || die 'Linux concurrent collision returned the wrong blocker'

  cas_file=$(find "$cache_root/$repository_id" -path '*/shared/cas/*/*' -type f -print -quit) || die 'Linux shared-cache CAS cannot be inspected'
  [[ -n "$cas_file" ]] || die 'Linux shared-cache CAS contains no blobs'
  chmod u+w "$cas_file" || die 'Linux CAS corruption fixture cannot make a blob writable'
  printf corrupt >"$cas_file"
  if REAL_GIT="$real_git" PATH="$linux_fixture/bin:$PATH" bash "$linux_fixture/linux_runner_daemon.sh" \
    --repo "$target_repo" --mode native \
    --shared-root "$shared_root" --cache-root "$cache_root" --repository-id "$repository_id" --node-id ubuntu-c \
    --object-format "$object_format" --source-commit "$source_commit" --source-tree "$source_tree" \
    --toolchain-identity "$identity" --goos "$host_goos" --goarch "$host_goarch" --cgo-enabled 0 \
    --module-identity "$identity" --workload-identity "$identity" -- /usr/bin/true \
    >"$linux_fixture/out-shared-corrupt.log" 2>"$linux_fixture/err-shared-corrupt.log"; then
    die 'Linux shared-cache reader accepted a corrupted CAS blob'
  fi
  grep -Fq 'shared cache generation blob' "$linux_fixture/err-shared-corrupt.log" || die 'Linux shared-cache corruption returned the wrong blocker'

  printf '%s\n' 'package cachefixture' >"$target_repo/ignored.go"
  if REAL_GIT="$real_git" PATH="$linux_fixture/bin:$PATH" bash "$linux_fixture/linux_runner_daemon.sh" \
    --repo "$target_repo" --mode native \
    --shared-root "$shared_root" --cache-root "$cache_root" --repository-id "$repository_id" --node-id ubuntu-d \
    --object-format "$object_format" --source-commit "$source_commit" --source-tree "$source_tree" \
    --toolchain-identity "$identity" --goos "$host_goos" --goarch "$host_goarch" --cgo-enabled 0 \
    --module-identity "$identity" --workload-identity "$identity" -- /usr/bin/true \
    >"$linux_fixture/out-shared-ignored.log" 2>"$linux_fixture/err-shared-ignored.log"; then
    die 'Linux shared-source runner accepted an ignored source file'
  fi
  grep -Fq 'target worktree must exactly match' "$linux_fixture/err-shared-ignored.log" || die 'Linux ignored source returned the wrong blocker'
  rm -f -- "$target_repo/ignored.go"

  repository_id=fixture-repository-alternate
  mkdir -p "$shared_root/$repository_id/objects/info" "$shared_root/$repository_id/objects/pack"
  printf '%s\n' "$shared_root/fixture-repository/objects" >"$shared_root/$repository_id/objects/info/alternates"
  chmod -R a-w "$shared_root/$repository_id/objects"
  printf '%s\n' 'schema_version=ubuntu-shared-git-object-store/v1' "repository_id=$repository_id" "object_format=$object_format" >"$shared_root/$repository_id/object-store.identity"
  if REAL_GIT="$real_git" PATH="$linux_fixture/bin:$PATH" bash "$linux_fixture/linux_runner_daemon.sh" \
    --repo "$target_repo" --mode native \
    --shared-root "$shared_root" --cache-root "$cache_root" --repository-id "$repository_id" --node-id ubuntu-a \
    --object-format "$object_format" --source-commit "$source_commit" --source-tree "$source_tree" \
    --toolchain-identity "$identity" --goos "$host_goos" --goarch "$host_goarch" --cgo-enabled 0 \
    --module-identity "$identity" --workload-identity "$identity" -- /usr/bin/true \
    >"$linux_fixture/out-shared-alternate.log" 2>"$linux_fixture/err-shared-alternate.log"; then
    die 'Linux shared-source runner accepted an alternate object source'
  fi
  grep -Fq 'must not delegate to another object source' "$linux_fixture/err-shared-alternate.log" || die 'Linux alternate object source returned the wrong blocker'

  repository_id=fixture-repository-writable
  mkdir -p "$shared_root/$repository_id/objects"
  cp -a "$target_repo/.git/objects/." "$shared_root/$repository_id/objects/"
  chmod -R a-w "$shared_root/$repository_id/objects"
  object_manifest=$(find "$shared_root/$repository_id/objects" -type f -print -quit)
  [[ -n "$object_manifest" ]] || die 'Linux writable-object fixture contains no object'
  chmod u+w "$object_manifest"
  printf '%s\n' 'schema_version=ubuntu-shared-git-object-store/v1' "repository_id=$repository_id" "object_format=$object_format" >"$shared_root/$repository_id/object-store.identity"
  if REAL_GIT="$real_git" PATH="$linux_fixture/bin:$PATH" bash "$linux_fixture/linux_runner_daemon.sh" \
    --repo "$target_repo" --mode native \
    --shared-root "$shared_root" --cache-root "$cache_root" --repository-id "$repository_id" --node-id ubuntu-a \
    --object-format "$object_format" --source-commit "$source_commit" --source-tree "$source_tree" \
    --toolchain-identity "$identity" --goos "$host_goos" --goarch "$host_goarch" --cgo-enabled 0 \
    --module-identity "$identity" --workload-identity "$identity" -- /usr/bin/true \
    >"$linux_fixture/out-shared-writable.log" 2>"$linux_fixture/err-shared-writable.log"; then
    die 'Linux shared-source runner accepted a writable nested object'
  fi
  grep -Fq 'closure must be read-only' "$linux_fixture/err-shared-writable.log" || die 'Linux writable nested object returned the wrong blocker'

  repository_id=fixture-repository-b
  mkdir -p "$shared_root/$repository_id/objects"
  cp -a "$target_repo/.git/objects/." "$shared_root/$repository_id/objects/" || die 'unable to stage second Linux repository objects'
  chmod -R a-w "$shared_root/$repository_id/objects" || die 'unable to make second Linux repository objects read-only'
  printf '%s\n' 'schema_version=ubuntu-shared-git-object-store/v1' "repository_id=$repository_id" "object_format=$object_format" >"$shared_root/$repository_id/object-store.identity"
  REAL_GIT="$real_git" PATH="$linux_fixture/bin:$PATH" bash "$linux_fixture/linux_runner_daemon.sh" \
    --repo "$target_repo" --mode native \
    --shared-root "$shared_root" --cache-root "$cache_root" --repository-id "$repository_id" --node-id ubuntu-a \
    --object-format "$object_format" --source-commit "$source_commit" --source-tree "$source_tree" \
    --toolchain-identity "$identity" --goos "$host_goos" --goarch "$host_goarch" --cgo-enabled 0 \
    --module-identity "$identity" --workload-identity "$identity" -- \
    /bin/bash -Eeuo pipefail -c \
    'test ! -e "$GOMODCACHE/cache/download/example/module.zip"; test ! -e "$GOCACHE/ab/build-entry"' \
    >"$linux_fixture/out-shared-other-repo.log" 2>"$linux_fixture/err-shared-other-repo.log" || die 'Linux repository cache namespaces were not isolated'
  grep -Fq 'cache_seed_state=cold' "$linux_fixture/out-shared-other-repo.log" || die 'Linux second repository reused the first repository cache'
  grep -Fq 'cache_publish=empty' "$linux_fixture/out-shared-other-repo.log" || die 'Linux empty cache publication was incorrectly marked ready'
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

if command -v pwsh >/dev/null 2>&1; then
  WINDOWS_SCRIPT_ROOT="$root" pwsh -NoLogo -NoProfile -NonInteractive -Command '
    $root = $env:WINDOWS_SCRIPT_ROOT
    $asts = @{}
    foreach ($relative in @("scripts/deploy/windows/windows_runner.ps1", "scripts/deploy/windows/deploy_windows_host.ps1")) {
      $path = Join-Path $root $relative
      $tokens = $null
      $errors = $null
      $asts[$relative] = [System.Management.Automation.Language.Parser]::ParseFile($path, [ref]$tokens, [ref]$errors)
      if ($errors.Count -ne 0) {
        $errors | ForEach-Object { Write-Error ("PowerShell parser: " + $_.Message) }
        exit 1
      }
    }
    $runnerRelative = "scripts/deploy/windows/windows_runner.ps1"
    $deployRelative = "scripts/deploy/windows/deploy_windows_host.ps1"
    $runnerParameters = @($asts[$runnerRelative].ParamBlock.Parameters | ForEach-Object { $_.Name.VariablePath.UserPath })
    $runnerHandoff = @($runnerParameters | Where-Object { $_ -cnotin @("Command", "CommandBase64", "RepositoryRoot", "TimeoutSeconds") })
    $deployParameters = @($asts[$deployRelative].ParamBlock.Parameters | ForEach-Object { $_.Name.VariablePath.UserPath })
    $deployProducer = @($deployParameters | Where-Object { $_ -cnotin @("Yes", "InstallPath", "Command") } | ForEach-Object { if ($_ -ceq "RunnerId") { "NodeId" } else { $_ } })
    $missing = @($runnerHandoff | Where-Object { $_ -cnotin $deployProducer })
    $stale = @($deployProducer | Where-Object { $_ -cnotin $runnerHandoff })
    if ($missing.Count -ne 0 -or $stale.Count -ne 0) {
      throw "runner/deploy mandatory handoff drift: missing=$($missing -join ",") stale=$($stale -join ",")"
    }
    $deployText = [System.IO.File]::ReadAllText((Join-Path $root $deployRelative))
    foreach ($name in $runnerHandoff) {
      if ([regex]::Matches($deployText, " -$name ").Count -ne 1) { throw "scheduled-task handoff is not exact: $name" }
    }
  ' || die 'Windows PowerShell AST/parser rejected a deploy script'
fi
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
grep -Fq '[string]$NodeId' "$windows_runner" || die 'Windows node identity parameter missing'
grep -Fq '[string]$RepositoryId' "$windows_runner" || die 'Windows repository identity parameter missing'
grep -Fq '$env:SUPER_DOLPHIN_CACHE_SCOPE' "$windows_runner" || die 'Windows cache scope marker missing'
grep -Fq "cache_scope = 'windows_node_local'" "$windows_runner" || die 'Windows structured cache scope output missing'
grep -Fq 'cache_seed_state' "$windows_runner" || die 'Windows cache seed state output missing'
grep -Fq 'cache_identity' "$windows_runner" || die 'Windows cache identity output missing'
grep -Fq '$env:GOMODCACHE' "$windows_runner" || die 'Windows GOMODCACHE isolation missing'
grep -Fq '$env:GOCACHE' "$windows_runner" || die 'Windows GOCACHE isolation missing'
grep -Fq 'Get-NodeScopedCacheRoot' "$windows_runner" || die 'Windows node-scoped cache root derivation missing'
grep -Fq 'WorktreeRoot must equal the uniquely verified Git worktree root' "$windows_runner" || die 'Windows worktree root is not bound to the actual Git root'
grep -Fq 'ObjectStoreRoot must equal the uniquely verified Git common directory' "$windows_runner" || die 'Windows object-store root is not bound to the actual Git common directory'
grep -Fq 'Assert-NodeLocalFixedPath' "$windows_runner" || die 'Windows runner does not reject shared/network source and cache roots'
grep -Fq 'Assert-DeploymentNodeLocalPath' "$root/scripts/deploy/windows/deploy_windows_host.ps1" || die 'Windows deploy does not reject shared/network source and cache roots'
grep -Fq '$env:SUPER_DOLPHIN_WORKLOAD_IDENTITY = $WorkloadIdentity' "$windows_runner" || die 'Windows workload identity binding missing'
grep -Fq 'RepositoryId' "$root/scripts/deploy/windows/deploy_windows_host.ps1" || die 'Windows deploy repository identity parameter missing'
grep -Fq 'NodeId' "$root/scripts/deploy/windows/deploy_windows_host.ps1" || die 'Windows deploy node identity handoff missing'
grep -Fq 'CacheRoot' "$root/scripts/deploy/windows/deploy_windows_host.ps1" || die 'Windows deploy cache root handoff missing'
! grep -Eiq 'HOME|Get-Location|Get-CurrentDirectory|\$PWD' "$windows_runner" || die 'Windows runner retained implicit HOME/cwd cache discovery'
! grep -Eiq 'super-agent-v3|wjboot-v2' "$windows_runner" || die 'Windows runner hardcoded a repository identity'
grep -Fq 'Add-MpPreference' "$root/scripts/deploy/windows/deploy_windows_host.ps1" || die 'Defender exclusion is not explicit'
grep -Fq 'CreateJobObject' "$root/scripts/deploy/windows/windows_runner.ps1" || die 'Windows Job Object control missing'
grep -Fq 'JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE' "$root/scripts/deploy/windows/windows_runner.ps1" || die 'Windows Job Object close-kill boundary missing'
grep -Fq 'WaitForJobEmpty' "$root/scripts/deploy/windows/windows_runner.ps1" || die 'Windows successful-exit Job Object empty proof missing'
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
linux_fixture=$(cd -P -- "$linux_fixture" && pwd) || die 'unable to canonicalize Linux runtime fixture'
cleanup_linux_fixture() {
  if [[ -n "${linux_fixture:-}" && -d "$linux_fixture" ]]; then
    chmod -R u+w "$linux_fixture" 2>/dev/null || true
    rm -rf -- "$linux_fixture"
  fi
}
trap cleanup_linux_fixture EXIT
mkdir -p "$linux_fixture/bin" "$linux_fixture/cgroup"
printf '%s\n' cpu memory >"$linux_fixture/cgroup/cgroup.controllers"
printf '%s\n' '200000 100000' >"$linux_fixture/cgroup/cpu.max"
printf '%s\n' 1073741824 >"$linux_fixture/cgroup/memory.max"
printf '%s\n' '#!/usr/bin/env bash' '[[ "${1:-}" == -s ]] || exit 97' 'printf "Linux\n"' >"$linux_fixture/bin/uname"
printf '%s\n' '#!/usr/bin/env bash' \
  'if [[ -n "${REAL_GIT:-}" ]]; then exec "$REAL_GIT" "$@"; fi' \
  'if [[ "${*: -1}" == --show-toplevel ]]; then printf "%s\n" "${FAKE_GIT_TOP:?}"; fi' \
  'exit 0' >"$linux_fixture/bin/git"
printf '%s\n' '#!/usr/bin/env bash' \
  'exec python3 -c '\''import os,sys; os.setsid(); os.execvp(sys.argv[1], sys.argv[1:])'\'' "$@"' >"$linux_fixture/bin/setsid"
printf '%s\n' '#!/usr/bin/env python3' \
  'import fcntl, sys, time' \
  'args = sys.argv[1:]' \
  'if args[0] == "-u":' \
  '    fcntl.flock(int(args[1]), fcntl.LOCK_UN)' \
  '    raise SystemExit(0)' \
  'deadline = time.monotonic() + float(args[1])' \
  'fd = int(args[2])' \
  'while True:' \
  '    try:' \
  '        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)' \
  '        break' \
  '    except BlockingIOError:' \
  '        if time.monotonic() >= deadline:' \
  '            raise SystemExit(1)' \
  '        time.sleep(0.02)' >"$linux_fixture/bin/flock"
printf '%s\n' '#!/usr/bin/env bash' 'set -Eeuo pipefail' \
  'if [[ "${1:-}" == info ]]; then exit 0; fi' \
  '[[ "${1:-}" == run ]] || exit 97' \
  'printf "%s\n" "$@" >"${DOCKER_ARGV_LOG:?}"' >"$linux_fixture/bin/docker"
chmod 700 "$linux_fixture/bin/uname" "$linux_fixture/bin/git" "$linux_fixture/bin/setsid" "$linux_fixture/bin/flock" "$linux_fixture/bin/docker"
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
run_linux_shared_cache_case
run_linux_large_cpu_case
run_linux_process_group_cases
printf '%s\n' 'heteroci deploy script contract: PASS'
