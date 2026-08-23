#!/usr/bin/env bash
set -Eeuo pipefail
export PYTHONDONTWRITEBYTECODE=1

root=$(cd -- "$(dirname -- "$0")/../.." && pwd -P)
die() { printf 'heteroci deploy script test: %s\n' "$*" >&2; exit 1; }
linux_daemon="$root/scripts/deploy/linux/linux_runner_daemon.sh"
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
canonical_wine_runtime=$(go_string_constant "$root/internal/devtools/heteroci/manifest.go" RuntimeUbuntuWineContainer) || die 'unable to derive canonical Ubuntu Wine runtime'
canonical_status=$(go_string_constant "$root/internal/devtools/heteroci/manifest.go" ResultStatusNotVerified) || die 'unable to derive canonical local result status'

expect_linux_reject() {
  local label=$1 expected=$2 output
  shift 2
  if output=$(bash "$linux_daemon" "$@" 2>&1); then
    die "$label was unexpectedly accepted"
  fi
  [[ "$output" == *"$expected"* ]] || die "$label returned the wrong failure: $output"
}

run_linux_runner_resource_e2e() {
  local fixture docker_socket docker_host docker_server_id runner_identity commit tree object_format identity log output chown_log source_objects shared_objects object_manifest shared_log shared_chown_log
  local -a common valid missing_cpu missing_memory
	fixture=$(mktemp -d "/tmp/heteroci-linux-resource.XXXXXX") || die 'unable to create standalone Linux runner fixture'
  fixture=$(cd -P -- "$fixture" && pwd -P) || die 'unable to canonicalize standalone Linux runner fixture'
  cleanup_resource_fixture() {
    chmod -R u+w "$fixture" 2>/dev/null || true
    rm -rf -- "$fixture"
  }
  trap cleanup_resource_fixture RETURN
  mkdir -p "$fixture/bin" "$fixture/worktrees" "$fixture/overlay" "$fixture/temp" "$fixture/wine-prefix-root"
  docker_socket="$fixture/docker.sock"
  docker_host="unix://$docker_socket"
  docker_server_id='docker-server-fixture'
  python3 - "$docker_socket" <<'PY'
import socket
import sys

server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
server.bind(sys.argv[1])
server.close()
PY
  export EXPECTED_DOCKER_HOST="$docker_host"
  export EXPECTED_DOCKER_SERVER_ID="$docker_server_id"
  printf '%s\n' '#!/usr/bin/env bash' '[[ "${1:-}" == -s ]] || exit 97' 'printf "Linux\n"' >"$fixture/bin/uname"
  printf '%s\n' '#!/usr/bin/env bash' 'if [[ -n "${REAL_GIT:-}" ]]; then exec "$REAL_GIT" "$@"; fi' 'exit 97' >"$fixture/bin/git"
  printf '%s\n' '#!/usr/bin/env python3' 'import os,sys' 'os.setsid()' 'os.execvp(sys.argv[1],sys.argv[1:])' >"$fixture/bin/setsid"
  printf '%s\n' '#!/usr/bin/env bash' 'set -Eeuo pipefail' 'target="${@: -1}"' 'test -w "$target"' 'printf "%s\n" "$target" >>"${CHOWN_LOG:?}"' >"$fixture/bin/chown"
  printf '%s\n' '#!/usr/bin/env python3' 'import sys' 'raise SystemExit(0)' >"$fixture/bin/flock"
  printf '%s\n' '#!/usr/bin/env bash' 'set -Eeuo pipefail' \
    '[[ "${DOCKER_HOST:-}" == "${EXPECTED_DOCKER_HOST:?}" ]]' \
    '[[ -z "${DOCKER_CONTEXT+x}" ]]' \
    'if [[ "${1:-}" == info ]]; then if [[ "${2:-}" == --format ]]; then printf "%s\n" "${EXPECTED_DOCKER_SERVER_ID:?}"; fi; exit 0; fi' \
    'if [[ "${1:-}" == image && "${2:-}" == inspect ]]; then if [[ "${4:-}" == *Id* ]]; then printf "%s\n" "${EXPECTED_IMAGE_ID:?}"; else printf "SUPER_DOLPHIN_RUNTIME=%s\n" "${EXPECTED_IMAGE_RUNTIME:?}"; fi; exit 0; fi' \
    'if [[ "${1:-}" == container && "${2:-}" == inspect ]]; then [[ ! -e "${DOCKER_REMOVED:?}" ]] || exit 1; printf "%s\n" "${EXPECTED_CONTAINER_OWNER:?}"; exit 0; fi' \
    'if [[ "${1:-}" == rm ]]; then : >"${DOCKER_REMOVED:?}"; exit 0; fi' \
    '[[ "${1:-}" == run ]] || exit 97' \
    'printf "%s\n" "$@" >"${DOCKER_ARGV_LOG:?}"' >"$fixture/bin/docker"
  chmod 700 "$fixture/bin/uname" "$fixture/bin/git" "$fixture/bin/setsid" "$fixture/bin/chown" "$fixture/bin/flock" "$fixture/bin/docker"
  cp -- "$linux_daemon" "$fixture/runner.sh"
  chmod 700 "$fixture/runner.sh"
  runner_identity=$(sha256sum -- "$fixture/runner.sh") || die 'unable to hash standalone runner fixture'
  runner_identity=sha256:${runner_identity%% *}
  commit=$($real_git -C "$root" rev-parse HEAD) || die 'standalone fixture source commit unavailable'
  tree=$($real_git -C "$root" rev-parse 'HEAD^{tree}') || die 'standalone fixture source tree unavailable'
  object_format=$($real_git -C "$root" rev-parse --show-object-format) || die 'standalone fixture object format unavailable'
  identity="sha256:$linux_digest"
  common=(
    --repo "$root" --mode docker --runtime ubuntu_container
    --worktree-root "$fixture/worktrees" --private-overlay-root "$fixture/overlay" --temp-root "$fixture/temp"
    --docker-executable "$fixture/bin/docker" --docker-host "$docker_host" --docker-server-id "$docker_server_id" --runner-identity "$runner_identity" --docker-image "runner:test@$identity" --docker-image-identity "$identity"
    --repository-id standalone --node-id linux-test --object-format "$object_format" --source-commit "$commit" --source-tree "$tree"
    --toolchain-identity "$identity" --goos linux --goarch amd64 --cgo-enabled 0 --module-identity "$identity" --workload-identity "$identity" --timeout-seconds 5
  )
  valid=("${common[@]}" --cpu-milli 1500 --memory-bytes 1073741824)
  missing_cpu=("${common[@]}" --memory-bytes 1073741824)
  missing_memory=("${common[@]}" --cpu-milli 1000)
  missing_server=()
  skip_server=0
  for argument in "${common[@]}"; do
    if ((skip_server != 0)); then
      skip_server=0
    elif [[ "$argument" == --docker-server-id ]]; then
      skip_server=1
    else
      missing_server+=("$argument")
    fi
  done
  if output=$(DOCKER_REMOVED="$fixture/docker.removed" REAL_GIT="$real_git" EXPECTED_IMAGE_RUNTIME=ubuntu_container EXPECTED_IMAGE_ID="$identity" EXPECTED_CONTAINER_OWNER="$identity" PATH="$fixture/bin:$PATH" bash "$fixture/runner.sh" "${missing_server[@]}" --cpu-milli 1000 --memory-bytes 1073741824 -- /usr/bin/true 2>&1); then
    die 'missing Docker server identity was unexpectedly accepted'
  fi
  [[ "$output" == *'remote-ci-helper-log/v1'* ]] || die "missing Docker server identity did not return a helper-local log: $output"
  if output=$(DOCKER_REMOVED="$fixture/docker.removed" REAL_GIT="$real_git" EXPECTED_IMAGE_RUNTIME=ubuntu_container EXPECTED_IMAGE_ID="$identity" EXPECTED_CONTAINER_OWNER="$identity" PATH="$fixture/bin:$PATH" bash "$fixture/runner.sh" "${valid[@]}" --docker-server-id "$docker_server_id" -- /usr/bin/true 2>&1); then
    die 'duplicate Docker server identity was unexpectedly accepted'
  fi
  [[ "$output" == *'remote-ci-helper-log/v1'* ]] || die "duplicate Docker server identity did not return a helper-local log: $output"
  if output=$(DOCKER_REMOVED="$fixture/docker.removed" REAL_GIT="$real_git" EXPECTED_IMAGE_RUNTIME=ubuntu_container EXPECTED_IMAGE_ID="$identity" EXPECTED_CONTAINER_OWNER="$identity" PATH="$fixture/bin:$PATH" bash "$fixture/runner.sh" "${valid[@]}" --docker-host "$docker_host" -- /usr/bin/true 2>&1); then
    die 'duplicate Docker host was unexpectedly accepted'
  fi
  [[ "$output" == *'remote-ci-helper-log/v1'* ]] || die "duplicate Docker host did not return a helper-local log: $output"
  bad_runner=("${valid[@]}")
  for index in "${!bad_runner[@]}"; do
    if [[ "${bad_runner[$index]}" == --runner-identity ]]; then
      bad_runner[$((index + 1))]="sha256:$linux_digest"
      break
    fi
  done
  if output=$(DOCKER_REMOVED="$fixture/docker.removed" REAL_GIT="$real_git" EXPECTED_IMAGE_RUNTIME=ubuntu_container EXPECTED_IMAGE_ID="$identity" EXPECTED_CONTAINER_OWNER="$identity" PATH="$fixture/bin:$PATH" bash "$fixture/runner.sh" "${bad_runner[@]}" -- /usr/bin/true 2>&1); then
    die 'runner executable identity drift was unexpectedly accepted'
  fi
  [[ "$output" == *'remote-ci-helper-log/v1'* ]] || die "runner executable identity drift did not return a helper-local log: $output"
  expect_resource_reject() {
    local label=$1
    shift
    if output=$(DOCKER_REMOVED="$fixture/docker.removed" REAL_GIT="$real_git" EXPECTED_IMAGE_RUNTIME=ubuntu_container EXPECTED_IMAGE_ID="$identity" EXPECTED_CONTAINER_OWNER="$identity" PATH="$fixture/bin:$PATH" bash "$fixture/runner.sh" "$@" -- /usr/bin/true 2>&1); then
      die "$label was unexpectedly accepted"
    fi
    [[ "$output" == *'remote-ci-helper-log/v1'* ]] || die "$label did not emit helper-local log"
  }
  expect_resource_reject 'missing cpu' "${missing_cpu[@]}"
  expect_resource_reject 'duplicate cpu' "${valid[@]}" --cpu-milli 1000
  expect_resource_reject 'zero cpu' "${missing_memory[@]}" --memory-bytes 1073741824 --cpu-milli 0
  expect_resource_reject 'overflow cpu' "${missing_memory[@]}" --memory-bytes 1073741824 --cpu-milli 9223372036854775808
  expect_resource_reject 'missing memory' "${missing_memory[@]}"
  expect_resource_reject 'duplicate memory' "${valid[@]}" --memory-bytes 2
  expect_resource_reject 'zero memory' "${missing_memory[@]}" --memory-bytes 0
  expect_resource_reject 'overflow memory' "${missing_memory[@]}" --memory-bytes 9223372036854775808
  log="$fixture/docker.argv"
  chown_log="$fixture/chown.log"
	if ! DOCKER_ARGV_LOG="$log" DOCKER_REMOVED="$fixture/docker.removed" CHOWN_LOG="$chown_log" REAL_GIT="$real_git" EXPECTED_IMAGE_RUNTIME=ubuntu_container EXPECTED_IMAGE_ID="$identity" EXPECTED_CONTAINER_OWNER="$identity" PATH="$fixture/bin:$PATH" bash "$fixture/runner.sh" "${valid[@]}" -- /usr/bin/true >"$fixture/stdout" 2>"$fixture/stderr"; then
	    die "standalone explicit resource Docker run failed: $(<"$fixture/stderr")"
  fi
  awk 'previous == "--cpus" && $0 == "1.5" { found=1 } { previous=$0 } END { exit(found ? 0 : 1) }' "$log" || die 'standalone Docker argv lost 1500m CPU as 1.5'
  awk 'previous == "--memory" && $0 == "1073741824" { found=1 } { previous=$0 } END { exit(found ? 0 : 1) }' "$log" || die 'standalone Docker argv lost exact memory bytes'
  [[ -z "$(find "$fixture/worktrees" -mindepth 1 -print -quit)" ]] || die 'standalone private worktree was not cleaned'
  [[ $(grep -Fc -- "$fixture/temp/runner." "$log") == 1 ]] || die 'standalone Docker argv duplicated temp mount'
  [[ $(grep -Fc -- "$fixture/overlay/runner." "$log") == 1 ]] || die 'standalone Docker argv duplicated overlay mount'
  grep -Fq -- "$fixture/worktrees/runner." "$chown_log" || die 'private worktree was not chowned for container user'
  grep -Fq -- "$fixture/overlay/runner." "$chown_log" || die 'private overlay was not chowned for container user'
  grep -Fq -- "$fixture/temp/runner." "$chown_log" || die 'private temp was not chowned for container user'
  ! grep -Fq -- "$root" "$chown_log" || die 'seed repository was incorrectly chowned'

  source_objects=$($real_git -C "$root" rev-parse --git-path objects) || die 'standalone shared object source unavailable'
  shared_objects="$fixture/shared/standalone/objects"
  mkdir -p "$shared_objects" "$fixture/cache"
  cp -a "$source_objects/." "$shared_objects/" || die 'standalone shared objects could not be staged'
  chmod -R a-w "$fixture/shared/standalone/objects"
  object_manifest="$fixture/shared/standalone/object-store.identity"
  printf '%s\n' 'schema_version=ubuntu-shared-git-object-store/v1' 'repository_id=standalone' "object_format=$object_format" >"$object_manifest"
  shared_log="$fixture/docker-shared.argv"
  shared_chown_log="$fixture/chown-shared.log"
  if ! DOCKER_ARGV_LOG="$shared_log" DOCKER_REMOVED="$fixture/docker-shared.removed" CHOWN_LOG="$shared_chown_log" REAL_GIT="$real_git" EXPECTED_IMAGE_RUNTIME=ubuntu_container EXPECTED_IMAGE_ID="$identity" EXPECTED_CONTAINER_OWNER="$identity" PATH="$fixture/bin:$PATH" bash "$fixture/runner.sh" \
    "${valid[@]}" --shared-root "$fixture/shared" --cache-root "$fixture/cache" -- /usr/bin/true >"$fixture/stdout-shared" 2>"$fixture/stderr-shared"; then
    die "standalone shared-cache Docker run failed: $(<"$fixture/stderr-shared")"
  fi
  grep -Fq '/gomodcache' "$shared_chown_log" || die 'node GOMODCACHE overlay was not chowned for container user'
  grep -Fq '/gocache' "$shared_chown_log" || die 'node GOCACHE overlay was not chowned for container user'
  ! grep -Fq -- "$fixture/shared" "$shared_chown_log" || die 'shared object root was incorrectly chowned'
}

ensure_test_git_source() {
  if "$real_git" -C "$root" rev-parse HEAD >/dev/null 2>&1; then
    return 0
  fi
  "$real_git" -C "$root" init -q || die 'unable to initialize standalone Git source fixture'
  "$real_git" -C "$root" config user.name heteroci-fixture
  "$real_git" -C "$root" config user.email heteroci-fixture@example.invalid
  "$real_git" -C "$root" add -A || die 'unable to stage standalone Git source fixture'
  "$real_git" -C "$root" commit -qm heteroci-fixture || die 'unable to commit standalone Git source fixture'
}

ensure_test_git_source
run_linux_runner_resource_e2e

run_linux_runtime_case() {
  local runtime=$1 runtime_marker=$2 image=$3 expected_cpu=$4
  local argv_log="$linux_fixture/argv-$runtime.log"
  local expected actual runtime_output
	if ! DOCKER_ARGV_LOG="$argv_log" EXPECTED_IMAGE_RUNTIME="$runtime_marker" EXPECTED_IMAGE_ID="sha256:$linux_digest" EXPECTED_CONTAINER_OWNER="sha256:$linux_digest" PATH="$linux_fixture/bin:$PATH" \
	    bash "$linux_fixture/linux_runner_daemon.sh" --repo "$root" --mode docker --runtime "$runtime" --docker-host "$docker_host" --docker-server-id "$docker_server_id" --runner-identity "$runner_identity" --docker-image "$image" -- /bin/echo "$runtime" \
	    >"$linux_fixture/out-$runtime.log" 2>"$linux_fixture/err-$runtime.log"; then
	    die "Linux $runtime runtime fake Docker execution failed: $(<"$linux_fixture/err-$runtime.log")"
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

run_linux_runtime_marker_mismatch_case() {
  local output
  if output=$(EXPECTED_IMAGE_RUNTIME=unexpected_runtime PATH="$linux_fixture/bin:$PATH" \
    bash "$linux_fixture/linux_runner_daemon.sh" --repo "$root" --mode docker --runtime ubuntu --docker-host "$docker_host" --docker-server-id "$docker_server_id" --runner-identity "$runner_identity" --docker-image "$ubuntu_image" -- true 2>&1); then
    die 'Linux runtime marker mismatch was unexpectedly accepted'
  fi
  [[ "$output" == *'immutable Docker image runtime marker does not match --runtime'* ]] || die "Linux runtime marker mismatch returned the wrong failure: $output"
}

run_linux_worker_mount_case() {
  local worker="$linux_fixture/worker" digest argv_log="$linux_fixture/argv-worker.log" output
  printf '%s\n' '#!/usr/bin/env bash' 'exit 0' >"$worker"
  chmod 555 "$worker"
  digest=$(sha256sum -- "$worker") || die 'unable to hash Linux worker fixture'
  digest=sha256:${digest%% *}
  if ! DOCKER_ARGV_LOG="$argv_log" EXPECTED_IMAGE_RUNTIME="$canonical_ubuntu_runtime" PATH="$linux_fixture/bin:$PATH" \
    bash "$linux_fixture/linux_runner_daemon.sh" --repo "$root" --mode docker --runtime ubuntu --docker-host "$docker_host" --docker-server-id "$docker_server_id" --runner-identity "$runner_identity" --docker-image "$ubuntu_image" \
    --worker-binary "$worker" --worker-identity "$digest" -- /opt/super-dolphin/bin/super-dolphin-heteroci-node batch --request-base64 token \
    >"$linux_fixture/out-worker.log" 2>"$linux_fixture/err-worker.log"; then
    die 'Linux worker bind mount execution failed'
  fi
  grep -Fxq "$worker:/opt/super-dolphin/bin/super-dolphin-heteroci-node:ro" "$argv_log" || die 'Linux worker bind mount is missing'
  if output=$(EXPECTED_IMAGE_RUNTIME="$canonical_ubuntu_runtime" PATH="$linux_fixture/bin:$PATH" \
    bash "$linux_fixture/linux_runner_daemon.sh" --repo "$root" --mode docker --runtime ubuntu --docker-host "$docker_host" --docker-server-id "$docker_server_id" --runner-identity "$runner_identity" --docker-image "$ubuntu_image" \
    --worker-binary "$worker" --worker-identity "sha256:$linux_digest" -- true 2>&1); then
    die 'Linux worker identity mismatch was unexpectedly accepted'
  fi
  [[ "$output" == *'--worker-binary does not match --worker-identity'* ]] || die "Linux worker mismatch returned the wrong failure: $output"
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

	repository_id='fixture-repository-alternate'
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

	repository_id='fixture-repository-writable'
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

	repository_id='fixture-repository-b'
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

bash -n "$linux_daemon"
grep -Fq "SUPER_DOLPHIN_RUNTIME=$canonical_ubuntu_runtime" "$root/Dockerfile.ci-runner-ubuntu" || die 'Ubuntu image canonical runtime marker missing'
! grep -Eiq 'OCI_SOURCE|OCI_REVISION' "$root/Dockerfile.ci-runner-ubuntu" || die 'Ubuntu image retained repository/source build metadata'
! grep -Fq 'COPY scripts/deploy/linux/linux_runner_daemon.sh' "$root/Dockerfile.ci-runner-ubuntu" || die 'Ubuntu image retained host runner COPY'
ubuntu_wine_dockerfile="$root/Dockerfile.ci-runner-ubuntu-wine"
[[ -f "$ubuntu_wine_dockerfile" ]] || die 'Ubuntu Wine image Dockerfile is missing'
grep -Fq 'FROM --platform=linux/amd64 ubuntu@sha256:1e0a86e57d247923571b75e0aaf48a1449cf8c543d51fb3e07a4a7d7bfa79316' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image base digest drifted'
grep -Fq "SUPER_DOLPHIN_RUNTIME=$canonical_wine_runtime" "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image runtime marker missing'
grep -Fq 'SUPER_DOLPHIN_TARGET_PLATFORM=windows/amd64' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image target platform marker missing'
grep -Fq 'SUPER_DOLPHIN_AUTHORITY=LOCAL_NON_AUTHORITATIVE' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image authority marker missing'
grep -Fq 'SUPER_DOLPHIN_RESULT_STATUS=NOT_VERIFIED' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image result status marker missing'
grep -Fq 'sha256:5c2c3b16caefa1d968a94c1daca04a7ca301a496d9b086e17ad77bb81393f053' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image Go checksum missing'
grep -Fq 'sha256:3ff0d57063c33313d73d0bdcebc4c778ad6be948234584694a042c6fe57164f6' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image Node checksum missing'
grep -Fq 'wine64=9.0~repack-4build3' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image wine64 pin missing'
grep -Fq 'wine=9.0~repack-4build3' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image wine pin missing'
grep -Fq 'mingw-w64=11.0.1-3build1' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image mingw-w64 pin missing'
grep -Fq 'xvfb=2:21.1.12-1ubuntu1.6' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image xvfb pin missing'
! grep -Fqi 'wine-binfmt' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image must not install wine-binfmt'
! grep -Eiq -- '--privileged|SYS_ADMIN|CAP_ADD' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image requests privileged execution'
! grep -Eiq 'wjboot-v2|super-agent-v3|ssh' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image hardcodes a repository or SSH path'
grep -Fq 'WINEARCH=win64' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image does not pin a 64-bit Wine prefix'
grep -Fq 'WINEPREFIX=/home/ci-runner/.wine64' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image private prefix path missing'
grep -Fq '/usr/lib/wine/wine64 wineboot.exe --init' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image must seed the prefix through the regular 64-bit loader'
grep -Fq '/usr/lib/wine/wineserver64 -w' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image prefix seed must wait for the regular 64-bit wineserver flush'
grep -Fq 'test -f /home/ci-runner/.wine64/system.reg' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image prefix health check missing'
grep -Fq 'WINE_REAL_BIN=/usr/lib/wine/wine64' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image regular wine path missing'
grep -Fq 'WINE_SERVER_REAL_BIN=/usr/lib/wine/wineserver64' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image regular wineserver path missing'
grep -Fq 'test -x /usr/lib/wine/wine64' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image regular wine executable check missing'
grep -Fq 'test -x /usr/lib/wine/wineserver64' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image regular wineserver executable check missing'
grep -Fq 'GOOS=windows GOARCH=amd64' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image Windows amd64 cross-compile smoke missing'
grep -Fq '/usr/bin/x86_64-w64-mingw32-gcc' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image MinGW compiler path missing'
grep -Fq '/usr/bin/xvfb-run' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image headless X runner path missing'
grep -Fq '/usr/bin/wine /opt/super-dolphin/smoke/wine-smoke.exe' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image Wine smoke execution missing'
grep -Fq 'HEALTHCHECK' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image healthcheck missing'
grep -Fq 'USER 65532:65532' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image must run as non-root'
grep -Fq 'install -d -o 65532 -g 65532 -m 0755 /workspace' "$ubuntu_wine_dockerfile" || die 'Ubuntu Wine image workspace ownership is not explicit'
grep -Fq 'export SUPER_DOLPHIN_AUTHORITY=LOCAL_NON_AUTHORITATIVE' "$linux_daemon" || die 'Linux canonical authority marker missing'
grep -Fq 'export SUPER_DOLPHIN_RESULT_STATUS=NOT_VERIFIED' "$linux_daemon" || die 'Linux canonical result status marker missing'
! grep -Fq 'cgroup_root=' "$root/scripts/deploy/linux/linux_runner_daemon.sh" || die 'Linux runner retained self-cgroup resource derivation'
! grep -Fq 'read_cgroup_cpu_milli' "$root/scripts/deploy/linux/linux_runner_daemon.sh" || die 'Linux runner retained cgroup CPU derivation'
grep -Fq -- '--cpu-milli' "$root/scripts/deploy/linux/linux_runner_daemon.sh" || die 'Linux explicit CPU reservation flag missing'
grep -Fq -- '--memory-bytes' "$root/scripts/deploy/linux/linux_runner_daemon.sh" || die 'Linux explicit memory reservation flag missing'
grep -Fq 'terminate_linux_process_group "$child_pid"' "$root/scripts/deploy/linux/linux_runner_daemon.sh" || die 'Linux process-group cleanup missing'
grep -Fq -- '--runtime ubuntu_container|ubuntu_wine_container' "$linux_daemon" || die 'Linux explicit runtime help is missing'
grep -Fq -- '--docker-executable' "$linux_daemon" || die 'Linux Docker executable handoff missing'
grep -Fq -- '--docker-host' "$linux_daemon" || die 'Linux Docker host handoff missing'
grep -Fq -- '--docker-server-id' "$linux_daemon" || die 'Linux Docker server identity handoff missing'
grep -Fq -- '--runner-identity' "$linux_daemon" || die 'Linux runner identity handoff missing'
grep -Fq 'attest_runner_identity' "$linux_daemon" || die 'Linux runner self identity attestation missing'
grep -Fq 'unset DOCKER_CONTEXT' "$linux_daemon" || die 'Linux Docker context was not cleared'
grep -Fq 'export DOCKER_HOST="$docker_host"' "$linux_daemon" || die 'Linux Docker host environment was not exported'
grep -Fq 'non-symlink Unix socket' "$linux_daemon" || die 'Linux Docker socket validation missing'
grep -Fq "info --format '{{.ID}}'" "$linux_daemon" || die 'Linux Docker server identity probe missing'
grep -Fq -- '--docker-image-identity' "$linux_daemon" || die 'Linux Docker image identity handoff missing'
grep -Fq -- '--worktree-root' "$linux_daemon" || die 'Linux private worktree root handoff missing'
grep -Fq -- '--private-overlay-root' "$linux_daemon" || die 'Linux private overlay root handoff missing'
grep -Fq -- '--temp-root' "$linux_daemon" || die 'Linux temp root handoff missing'
grep -Fq -- '--wine-server-executable-identity' "$linux_daemon" || die 'Linux Wine server identity handoff missing'
grep -Fq 'git clone --quiet --shared --no-checkout' "$linux_daemon" || die 'Linux runner does not create a private source repository'
grep -Fq 'execution_repo_path' "$linux_daemon" || die 'Linux runner does not execute from a private source path'
grep -Fq 'fixed_repo_mutated' "$linux_daemon" || die 'Linux runner does not prove fixed seed immutability'
grep -Fq 'remote-ci-helper-log/v1' "$linux_daemon" || die 'Linux runner helper-local stderr schema missing'
! grep -Fq 'remote-ci-event/v1' "$linux_daemon" || die 'Linux runner must not own outer remote-ci-event/v1'
! grep -Fq 'command -v docker' "$linux_daemon" || die 'Linux runner retained PATH Docker discovery'
grep -Fq -- '--network none' "$linux_daemon" || die 'Linux Docker execution is not network isolated'
grep -Fq -- '--user 65532:65532' "$linux_daemon" || die 'Linux Docker execution user is not explicit'
grep -Fq -- '--cap-drop ALL' "$linux_daemon" || die 'Linux Docker execution does not drop capabilities'
grep -Fq -- '--security-opt no-new-privileges' "$linux_daemon" || die 'Linux Docker execution does not disable privilege escalation'
grep -Fq -- '--pids-limit 256' "$linux_daemon" || die 'Linux Docker PID limit is not explicit'
grep -Fq 'super-dolphin.owner' "$linux_daemon" || die 'Linux Docker owner label is missing'
grep -Fq 'cleanup_docker_container' "$linux_daemon" || die 'Linux Docker owned-container cleanup is missing'

ubuntu_image="super-dolphin-ci-runner:ubuntu@sha256:$linux_digest"
linux_fixture=$(mktemp -d "/tmp/heteroci-linux-runtime.XXXXXX") || die 'unable to create Linux runtime fixture'
linux_fixture=$(cd -P -- "$linux_fixture" && pwd) || die 'unable to canonicalize Linux runtime fixture'
cleanup_linux_fixture() {
  if [[ -n "${linux_fixture:-}" && -d "$linux_fixture" ]]; then
    chmod -R u+w "$linux_fixture" 2>/dev/null || true
    rm -rf -- "$linux_fixture"
  fi
}
trap cleanup_linux_fixture EXIT
mkdir -p "$linux_fixture/bin" "$linux_fixture/worktrees" "$linux_fixture/overlay" "$linux_fixture/temp" "$linux_fixture/wine-prefix-root"
docker_socket="$linux_fixture/docker.sock"
docker_host="unix://$docker_socket"
docker_server_id='docker-server-fixture'
python3 - "$docker_socket" <<'PY'
import socket
import sys

server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
server.bind(sys.argv[1])
server.close()
PY
export EXPECTED_DOCKER_HOST="$docker_host"
export EXPECTED_DOCKER_SERVER_ID="$docker_server_id"
printf '%s\n' '#!/usr/bin/env bash' '[[ "${1:-}" == -s ]] || exit 97' 'printf "Linux\n"' >"$linux_fixture/bin/uname"
printf '%s\n' '#!/usr/bin/env bash' \
  'if [[ -n "${REAL_GIT:-}" ]]; then exec "$REAL_GIT" "$@"; fi' \
  'if [[ "${*: -1}" == --show-toplevel ]]; then printf "%s\n" "${FAKE_GIT_TOP:?}"; fi' \
  'exit 0' >"$linux_fixture/bin/git"
printf '%s\n' '#!/usr/bin/env bash' \
  'exec python3 -c '\''import os,sys; os.setsid(); os.execvp(sys.argv[1], sys.argv[1:])'\'' dummy "$@"' >"$linux_fixture/bin/setsid"
printf '%s\n' '#!/usr/bin/env bash' 'exec "$@"' >"$linux_fixture/bin/setsid"
printf '%s\n' '#!/usr/bin/env bash' 'exit 0' >"$linux_fixture/bin/chown"
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
  '[[ "${DOCKER_HOST:-}" == "${EXPECTED_DOCKER_HOST:?}" ]]' \
  '[[ -z "${DOCKER_CONTEXT+x}" ]]' \
  'if [[ "${1:-}" == info ]]; then if [[ "${2:-}" == --format ]]; then printf "%s\n" "${EXPECTED_DOCKER_SERVER_ID:?}"; fi; exit 0; fi' \
  'if [[ "${1:-}" == image && "${2:-}" == inspect ]]; then if [[ "${*: -1}" == * ]]; then :; fi; if [[ "${4:-}" == *Id* || "${3:-}" == *Id* ]]; then printf "%s\n" "${EXPECTED_IMAGE_ID:?}"; else printf "SUPER_DOLPHIN_RUNTIME=%s\n" "${EXPECTED_IMAGE_RUNTIME:?}"; fi; exit 0; fi' \
	'if [[ "${1:-}" == container && "${2:-}" == inspect ]]; then [[ ! -e "${DOCKER_REMOVED:?}" ]] || exit 1; printf "%s\n" "${EXPECTED_CONTAINER_OWNER:?}"; exit 0; fi' \
	'if [[ "${1:-}" == rm ]]; then : >"${DOCKER_REMOVED:?}"; exit 0; fi' \
  '[[ "${1:-}" == run ]] || exit 97' \
  'printf "%s\n" "$@" >"${DOCKER_ARGV_LOG:?}"' >"$linux_fixture/bin/docker"
chmod 700 "$linux_fixture/bin/uname" "$linux_fixture/bin/git" "$linux_fixture/bin/setsid" "$linux_fixture/bin/chown" "$linux_fixture/bin/flock" "$linux_fixture/bin/docker"
cp -- "$linux_daemon" "$linux_fixture/linux_runner_daemon.sh" || die 'unable to copy Linux runner fixture'
chmod 700 "$linux_fixture/linux_runner_daemon.sh"
runner_identity=$(sha256sum -- "$linux_fixture/linux_runner_daemon.sh") || die 'unable to hash Linux runner fixture'
runner_identity=sha256:${runner_identity%% *}

run_linux_explicit_resource_cases() {
  local commit tree object_format identity output actual_log
  local -a common_args base_args valid_args no_memory_args
  commit=$($real_git -C "$root" rev-parse HEAD) || die 'unable to resolve Linux source commit'
  tree=$($real_git -C "$root" rev-parse 'HEAD^{tree}') || die 'unable to resolve Linux source tree'
  object_format=$($real_git -C "$root" rev-parse --show-object-format) || die 'unable to resolve Linux object format'
  identity="sha256:$linux_digest"
  common_args=(
    --repo "$root" --mode docker --runtime ubuntu_container
    --worktree-root "$linux_fixture/worktrees" --private-overlay-root "$linux_fixture/overlay" --temp-root "$linux_fixture/temp"
    --docker-executable "$linux_fixture/bin/docker" --docker-host "$docker_host" --docker-server-id "$docker_server_id" --runner-identity "$runner_identity" --docker-image "$ubuntu_image" --docker-image-identity "$identity"
    --repository-id explicit-resource --node-id linux-test --object-format "$object_format"
    --source-commit "$commit" --source-tree "$tree" --toolchain-identity "$identity"
    --goos linux --goarch amd64 --cgo-enabled 0 --module-identity "$identity" --workload-identity "$identity"
    --timeout-seconds 5
  )
  base_args=("${common_args[@]}" --memory-bytes 1073741824)
  valid_args=("${base_args[@]}" --cpu-milli 1000)
  no_memory_args=("${common_args[@]}" --cpu-milli 1000)
  expect_resource_reject() {
    local label=$1
    shift
	if output=$(DOCKER_REMOVED="$linux_fixture/docker.removed" REAL_GIT="$real_git" EXPECTED_IMAGE_RUNTIME=ubuntu_container EXPECTED_IMAGE_ID="$identity" EXPECTED_CONTAINER_OWNER="$identity" PATH="$linux_fixture/bin:$PATH" bash "$linux_fixture/linux_runner_daemon.sh" "$@" -- /usr/bin/true 2>&1); then
      die "$label was unexpectedly accepted"
    fi
    [[ "$output" == *'remote-ci-helper-log/v1'* ]] || die "$label did not return helper-local stderr log"
  }
  expect_resource_reject 'missing cpu reservation' "${base_args[@]}"
  expect_resource_reject 'duplicate cpu reservation' "${valid_args[@]}" --cpu-milli 1000
  expect_resource_reject 'zero cpu reservation' "${base_args[@]}" --cpu-milli 0
  expect_resource_reject 'overflow cpu reservation' "${base_args[@]}" --cpu-milli 9223372036854775808
  expect_resource_reject 'missing memory reservation' "${no_memory_args[@]}"
  expect_resource_reject 'duplicate memory reservation' "${valid_args[@]}" --memory-bytes 2
  expect_resource_reject 'zero memory reservation' "${no_memory_args[@]}" --memory-bytes 0
  expect_resource_reject 'overflow memory reservation' "${no_memory_args[@]}" --memory-bytes 9223372036854775808

  actual_log="$linux_fixture/argv-explicit.log"
	rm -f -- "$linux_fixture/docker.removed"
	if ! DOCKER_ARGV_LOG="$actual_log" DOCKER_REMOVED="$linux_fixture/docker.removed" REAL_GIT="$real_git" EXPECTED_IMAGE_RUNTIME=ubuntu_container EXPECTED_IMAGE_ID="$identity" EXPECTED_CONTAINER_OWNER="$identity" PATH="$linux_fixture/bin:$PATH" \
	    bash "$linux_fixture/linux_runner_daemon.sh" "${base_args[@]}" --cpu-milli 1500 -- /usr/bin/true >"$linux_fixture/out-explicit.log" 2>"$linux_fixture/err-explicit.log"; then
	    die "explicit resource Docker execution failed: $(<"$linux_fixture/err-explicit.log")"
  fi
  awk 'prev == "--cpus" && $0 == "1.5" { found=1 } { prev=$0 } END { exit(found ? 0 : 1) }' "$actual_log" || die 'Docker argv did not preserve exact 1500 milli CPU as 1.5'
  awk 'prev == "--memory" && $0 == "1073741824" { found=1 } { prev=$0 } END { exit(found ? 0 : 1) }' "$actual_log" || die 'Docker argv did not preserve exact memory bytes'
  [[ $(grep -Fc -- "$linux_fixture/temp/runner." "$actual_log") == 1 ]] || die 'Docker argv duplicated private temp mount'
  [[ $(grep -Fc -- "$linux_fixture/overlay/runner." "$actual_log") == 1 ]] || die 'Docker argv duplicated private overlay mount'
  [[ -z "$(find "$linux_fixture/worktrees" -mindepth 1 -print -quit)" ]] || die 'private worktree was not cleaned'

  actual_log="$linux_fixture/argv-wine.log"
	rm -f -- "$linux_fixture/docker.removed"
	if ! DOCKER_ARGV_LOG="$actual_log" DOCKER_REMOVED="$linux_fixture/docker.removed" REAL_GIT="$real_git" EXPECTED_IMAGE_RUNTIME=ubuntu_wine_container EXPECTED_IMAGE_ID="$identity" EXPECTED_CONTAINER_OWNER="$identity" PATH="$linux_fixture/bin:$PATH" \
    bash "$linux_fixture/linux_runner_daemon.sh" \
      --repo "$root" --mode docker --runtime ubuntu_wine_container --cpu-milli 1000 --memory-bytes 1073741824 \
      --worktree-root "$linux_fixture/worktrees" --private-overlay-root "$linux_fixture/overlay" --temp-root "$linux_fixture/temp" \
      --docker-executable "$linux_fixture/bin/docker" --docker-host "$docker_host" --docker-server-id "$docker_server_id" --runner-identity "$runner_identity" --docker-image "$ubuntu_image" --docker-image-identity "$identity" \
      --wine-executable /usr/lib/wine/wine64 --wine-executable-identity "$identity" --wine-server-executable /usr/lib/wine/wineserver64 --wine-server-executable-identity "$identity" --wine-prefix-root "$linux_fixture/wine-prefix-root" \
      --repository-id explicit-wine --node-id linux-wine-test --object-format "$object_format" --source-commit "$commit" --source-tree "$tree" \
      --toolchain-identity "$identity" --goos windows --goarch amd64 --cgo-enabled 0 --module-identity "$identity" --workload-identity "$identity" --timeout-seconds 5 -- /usr/bin/true \
      >"$linux_fixture/out-wine.log" 2>"$linux_fixture/err-wine.log"; then
    die "Wine image Docker execution failed: $(<"$linux_fixture/err-wine.log")"
  fi
  grep -Fq -- "$linux_fixture/wine-prefix-root/runner." "$actual_log" || die 'Wine Docker argv did not mount a private prefix child'
}

if [[ "${HETERO_CI_LINUX_ONLY:-0}" == 1 ]]; then
  run_linux_explicit_resource_cases
  printf '%s\n' 'heteroci Linux-only Ubuntu/Wine runner contract: PASS'
  exit 0
fi

run_linux_explicit_resource_cases
printf '%s\n' 'heteroci deploy script contract: PASS'
