package main

const remoteBaselineSeedScriptHead = `#!/bin/sh
set -eu
umask 022

export HOME=/tmp/home
export XDG_CACHE_HOME=/tmp/xdg-cache
export GOENV=off
export GOTELEMETRY=off
export DEBIAN_FRONTEND=noninteractive
mkdir -p "$HOME" "$XDG_CACHE_HOME"

required_env="BASELINE_MANIFEST_SCHEMA_VERSION BASELINE_MANIFEST_MIN_COMPATIBLE_VERSION BASELINE_GENERATION BASELINE_MAIN_COMMIT BASELINE_MAIN_TREE BASELINE_PLATFORM BASELINE_POLICY_DIGEST BASELINE_TOOLCHAIN_DIGEST BASELINE_GATE_SOURCE_SHA256 BASELINE_GO_TOOLCHAIN BASELINE_TOOLCHAIN_CHANGED BASELINE_RUNTIME_IMAGE BASELINE_SOURCE_MODE BASELINE_SOURCE_BUNDLE_SIZE BASELINE_SOURCE_MANIFEST_SHA256 BASELINE_SQRUFF_SHA256 BASELINE_STORAGE_MODE BASELINE_FORCE_RUNTIME_REFRESH BASELINE_RUNTIME_DEPENDENCY_DIGEST"
for name in $required_env; do
  eval "value=\${$name:-}"
  test -n "$value"
done
case "$BASELINE_STORAGE_MODE" in anchor|delta) ;; *) echo "unsupported baseline storage mode" >&2; exit 1;; esac
case "$BASELINE_TOOLCHAIN_CHANGED" in true|false) ;; *) echo "invalid toolchain change marker" >&2; exit 1;; esac
case "$BASELINE_FORCE_RUNTIME_REFRESH" in true|false) ;; *) echo "unsupported runtime refresh mode" >&2; exit 1;; esac
oss_output=/output
test -d "$oss_output"
test -z "$(find "$oss_output" -mindepth 1 -maxdepth 1 -print -quit)"
test -d /input

stage=/tmp/super-dolphin-baseline
source_root=$stage/source
go_mod_cache=$stage/go-mod-cache
tool_go_mod_cache=$stage/tool-go-mod-cache
payload_root=$stage/payload
go_build_cache=$payload_root/cache-seed/go-build
previous_source=$payload_root/source
mkdir -p "$payload_root" "$go_mod_cache" "$tool_go_mod_cache"

digest_file() { printf 'sha256:%s' "$(sha256sum "$1" | awk '{print $1}')"; }

previous_gate_source_sha256=
previous_gate_platform=
previous_gate_toolchain_digest=
load_verified_gate() {
  manifest=$1
  binary=$2
  test -f "$manifest"
  test -x "$binary"
  candidate_gate_source=$(sed -n 's/.*"gate_source_sha256":"\([^"]*\)".*/\1/p' "$manifest")
  candidate_gate_digest=$(sed -n 's/.*"gate_binary_sha256":"\([^"]*\)".*/\1/p' "$manifest")
  candidate_gate_size=$(sed -n 's/.*"gate_binary_size":\([0-9][0-9]*\).*/\1/p' "$manifest")
  candidate_gate_platform=$(sed -n 's/.*"platform":"\([^"]*\)".*/\1/p' "$manifest")
  candidate_gate_toolchain=$(sed -n 's/.*"toolchain_digest":"\([^"]*\)".*/\1/p' "$manifest")
  test -n "$candidate_gate_digest"
  test -n "$candidate_gate_size"
  test -n "$candidate_gate_platform"
  test -n "$candidate_gate_toolchain"
  test "$(digest_file "$binary")" = "$candidate_gate_digest"
  test "$(stat -c '%s' "$binary")" = "$candidate_gate_size"
  install -d -m 0755 "$payload_root/bin"
  cp "$binary" "$payload_root/bin/super-dolphin-gate"
  chmod 0755 "$payload_root/bin/super-dolphin-gate"
  previous_gate_source_sha256=$candidate_gate_source
  previous_gate_platform=$candidate_gate_platform
  previous_gate_toolchain_digest=$candidate_gate_toolchain
}

verify_gate_cli_identity() (
  binary=$1
  if ! grep -Fq 'case "cli-identity":' "$source_root/cmd/super-dolphin-gate/main.go"; then
    "$binary" plan local-fast >/dev/null
    printf 'gate CLI identity mode: source-bound legacy probe\n'
    exit 0
  fi
  identity=$("$binary" worker cli-identity)
  expected=$(printf 'gate_source_sha256=%s\nplatform=%s\ntoolchain_digest=%s' \
    "$BASELINE_GATE_SOURCE_SHA256" "$BASELINE_PLATFORM" "$BASELINE_TOOLCHAIN_DIGEST")
  test "$identity" = "$expected"
)

command -v tar >/dev/null
command -v gzip >/dev/null
command -v sha256sum >/dev/null
previous_baseline=0
previous_layered=0
previous_runtime_manifest=$stage/previous-runtime-manifest.json
if test -n "${BASELINE_ANCHOR_MANIFEST_DIGEST:-}"; then
  test -f /previous/baseline-manifest.json
  test "$(digest_file /previous/baseline-manifest.json)" = "$BASELINE_ANCHOR_MANIFEST_DIGEST"
fi
if test -f /previous/runtime-deps.tar.gz || test -f /previous/source.tar.gz || test -f /previous/go-build-cache.tar.gz; then
  for archive in runtime-deps.tar.gz source.tar.gz go-build-cache.tar.gz; do
    test -f "/previous/$archive"
    tar -xzf "/previous/$archive" -C "$payload_root"
  done
  anchor_manifest=/previous/baseline-manifest.json
  test -f "$anchor_manifest"
  anchor_schema=$(sed -n 's/.*"schema_version":\([0-9][0-9]*\).*/\1/p' "$anchor_manifest")
  anchor_mode=$(sed -n 's/.*"storage_mode":"\([^"]*\)".*/\1/p' "$anchor_manifest")
  if test "$BASELINE_STORAGE_MODE" = delta && { test -z "$anchor_schema" || test "$anchor_schema" -lt "$BASELINE_MANIFEST_MIN_COMPATIBLE_VERSION" || test "$anchor_schema" -gt "$BASELINE_MANIFEST_SCHEMA_VERSION" || test "$anchor_mode" != anchor; }; then
    echo 'previous baseline schema or storage mode is incompatible; full Anchor rebuild is forbidden' >&2
    exit 1
  fi
  expected_commit=$(sed -n 's/.*"main_commit":"\([0-9a-f]*\)".*/\1/p' "$anchor_manifest")
  expected_tree=$(sed -n 's/.*"main_tree":"\([0-9a-f]*\)".*/\1/p' "$anchor_manifest")
  test -n "$expected_commit"; test -n "$expected_tree"
  load_verified_gate "$anchor_manifest" /previous/bin/super-dolphin-gate
  if test -n "${BASELINE_DELTA_MANIFEST_1:-}${BASELINE_DELTA_MANIFEST_2:-}${BASELINE_DELTA_MANIFEST_3:-}${BASELINE_DELTA_MANIFEST_4:-}"; then
    test -d /layers
    delta_gap=0
    for name in BASELINE_DELTA_MANIFEST_1 BASELINE_DELTA_MANIFEST_2 BASELINE_DELTA_MANIFEST_3 BASELINE_DELTA_MANIFEST_4; do
      eval "entry=\${$name:-}"
      if test -z "$entry"; then delta_gap=1; continue; fi
      test "$delta_gap" = 0
      generation=${entry%%@*}; manifest_digest=${entry#*@}
      case "$generation" in ''|*[!0-9]*) echo "invalid baseline delta generation" >&2; exit 1;; esac
      layer_root=/layers/$generation/output
      manifest=$layer_root/baseline-manifest.json
      test "$generation" != "$entry"; test -f "$manifest"
      test "$(digest_file "$manifest")" = "$manifest_digest"
      manifest_generation=$(sed -n 's/.*"generation":\([0-9][0-9]*\).*/\1/p' "$manifest")
      manifest_mode=$(sed -n 's/.*"storage_mode":"\([^"]*\)".*/\1/p' "$manifest")
      base_commit=$(sed -n 's/.*"base_commit":"\([0-9a-f]*\)".*/\1/p' "$manifest")
      base_tree=$(sed -n 's/.*"base_tree":"\([0-9a-f]*\)".*/\1/p' "$manifest")
      target_commit=$(sed -n 's/.*"target_commit":"\([0-9a-f]*\)".*/\1/p' "$manifest")
      target_tree=$(sed -n 's/.*"target_tree":"\([0-9a-f]*\)".*/\1/p' "$manifest")
      test "$manifest_generation" = "$generation"; test "$manifest_mode" = delta
      test "$base_commit" = "$expected_commit"; test "$base_tree" = "$expected_tree"
      source_delta=$layer_root/source.delta.bundle
      cache_delta=$layer_root/go-build-cache.delta.tar.gz
      source_digest=$(sed -n 's/.*"name":"source","archive":"source.delta.bundle","sha256":"\([^"]*\)".*/\1/p' "$manifest")
      source_size=$(sed -n 's/.*"name":"source","archive":"source.delta.bundle","sha256":"[^"]*","size":\([0-9][0-9]*\).*/\1/p' "$manifest")
      cache_digest=$(sed -n 's/.*"name":"go-build-cache","archive":"go-build-cache.delta.tar.gz","sha256":"\([^"]*\)".*/\1/p' "$manifest")
      cache_size=$(sed -n 's/.*"name":"go-build-cache","archive":"go-build-cache.delta.tar.gz","sha256":"[^"]*","size":\([0-9][0-9]*\).*/\1/p' "$manifest")
      test -n "$source_digest"; test -n "$cache_digest"; test -n "$source_size"; test -n "$cache_size"
      test "$(digest_file "$source_delta")" = "$source_digest"
      test "$(digest_file "$cache_delta")" = "$cache_digest"
      test "$(stat -c '%s' "$source_delta")" = "$source_size"
      test "$(stat -c '%s' "$cache_delta")" = "$cache_size"
      runtime_go_count=$(grep -o '"name":"runtime-go"' "$manifest" | wc -l | tr -d ' ')
      case "$runtime_go_count" in 0) ;; 1)
        runtime_go_delta=$layer_root/runtime-go.delta.tar.gz
        runtime_go_digest=$(sed -n 's/.*"name":"runtime-go","archive":"runtime-go.delta.tar.gz","sha256":"\([^"]*\)".*/\1/p' "$manifest")
        runtime_go_size=$(sed -n 's/.*"name":"runtime-go","archive":"runtime-go.delta.tar.gz","sha256":"[^"]*","size":\([0-9][0-9]*\).*/\1/p' "$manifest")
        test -n "$runtime_go_digest"; test -n "$runtime_go_size"; test -f "$runtime_go_delta"
        test "$(digest_file "$runtime_go_delta")" = "$runtime_go_digest"
        test "$(stat -c '%s' "$runtime_go_delta")" = "$runtime_go_size"
        "$payload_root/runtime/python/bin/python3" - "$runtime_go_delta" <<'PY'
import posixpath
import sys
import tarfile

archive_path = sys.argv[1]
seen = set()
has_go = False
has_manifest = False
expanded = 0
with tarfile.open(archive_path, "r:gz") as archive:
    for member in archive:
        name = member.name.rstrip("/")
        clean = posixpath.normpath(name)
        if not name or name.startswith("/") or clean != name or clean == ".." or clean.startswith("../"):
            raise SystemExit("runtime-go delta contains an unsafe path")
        if clean in seen:
            raise SystemExit("runtime-go delta contains a duplicate path")
        seen.add(clean)
        if member.issym() or member.islnk() or not (member.isdir() or member.isfile()):
            raise SystemExit("runtime-go delta contains an unsupported entry type")
        if clean == "runtime/go" or clean.startswith("runtime/go/"):
            has_go = True
        elif clean == "runtime/manifest.json" and member.isfile():
            has_manifest = True
        elif clean != "runtime":
            raise SystemExit("runtime-go delta contains a forbidden path")
        expanded += member.size
        if expanded > 20 << 30:
            raise SystemExit("runtime-go delta expanded size is too large")
if not has_go or not has_manifest:
    raise SystemExit("runtime-go delta is incomplete")
PY
        runtime_go_stage=$stage/runtime-go-$generation
        mkdir -p "$runtime_go_stage"
        tar -xzf "$runtime_go_delta" -C "$runtime_go_stage"
        test -d "$runtime_go_stage/runtime/go"; test -f "$runtime_go_stage/runtime/manifest.json"
        previous_go=$runtime_go_stage/previous-go
        previous_manifest=$runtime_go_stage/previous-manifest.json
        mv "$payload_root/runtime/go" "$previous_go"
        if ! mv "$payload_root/runtime/manifest.json" "$previous_manifest"; then
          mv "$previous_go" "$payload_root/runtime/go"
          exit 1
        fi
        if ! mv "$runtime_go_stage/runtime/go" "$payload_root/runtime/go"; then
          mv "$previous_manifest" "$payload_root/runtime/manifest.json"
          mv "$previous_go" "$payload_root/runtime/go"
          exit 1
        fi
        if ! mv "$runtime_go_stage/runtime/manifest.json" "$payload_root/runtime/manifest.json"; then
          mv "$payload_root/runtime/go" "$runtime_go_stage/runtime/go"
          mv "$previous_manifest" "$payload_root/runtime/manifest.json"
          mv "$previous_go" "$payload_root/runtime/go"
          exit 1
        fi
        rm -rf "$previous_go" "$previous_manifest" "$go_build_cache"
        install -d -m 0700 "$go_build_cache"
        ;; *) echo "baseline delta contains duplicate runtime-go layers" >&2; exit 1;; esac
      load_verified_gate "$manifest" "$layer_root/bin/super-dolphin-gate"
      test -x "$payload_root/runtime/bin/git"
      SUPER_DOLPHIN_RUNTIME_ROOT=$payload_root/runtime "$payload_root/runtime/bin/git" -C "$payload_root/source" fetch --quiet "$source_delta" "$target_commit"
      SUPER_DOLPHIN_RUNTIME_ROOT=$payload_root/runtime "$payload_root/runtime/bin/git" -C "$payload_root/source" checkout --quiet --detach FETCH_HEAD
      test "$(SUPER_DOLPHIN_RUNTIME_ROOT=$payload_root/runtime "$payload_root/runtime/bin/git" -C "$payload_root/source" rev-parse 'HEAD^{tree}')" = "$target_tree"
      test -z "$(SUPER_DOLPHIN_RUNTIME_ROOT=$payload_root/runtime "$payload_root/runtime/bin/git" -C "$payload_root/source" status --porcelain=v1 --untracked-files=all)"
      tar -xzf "$cache_delta" -C "$payload_root"
      expected_commit=$target_commit
      expected_tree=$target_tree
    done
  fi
  previous_baseline=1
  previous_layered=1
elif test -f /previous/baseline.tar.gz; then
	tar -xzf /previous/baseline.tar.gz -C "$payload_root"
	previous_baseline=1
elif test -f /previous/baseline.tar; then
  tar -xf /previous/baseline.tar -C "$payload_root"
  previous_baseline=1
fi
if test "$previous_baseline" = 1; then
  test -d "$go_build_cache"
  test -n "$(find "$go_build_cache" -type f -print -quit)"
  test -f "$payload_root/runtime/manifest.json"
  cp "$payload_root/runtime/manifest.json" "$previous_runtime_manifest"
  printf 'go build cache source: previous DataCache\n'
else
  install -d -m 0700 "$go_build_cache"
  printf 'go build cache source: empty bootstrap\n'
fi
if test "$BASELINE_STORAGE_MODE" = delta; then
  if test "$BASELINE_TOOLCHAIN_CHANGED" = true; then
    rm -rf "$go_build_cache"
    install -d -m 0700 "$go_build_cache"
    unset GOCACHEPROG
    export GOCACHE="$go_build_cache"
    printf 'go build cache source: empty toolchain-scoped delta\n'
  else
    mv "$go_build_cache" "$stage/anchor-go-build-cache"
    install -d -m 0700 "$go_build_cache"
    test -x /previous/bin/super-dolphin-gate
    test -n "$(find "$stage/anchor-go-build-cache" -type f -print -quit)"
    export GOCACHE="$go_build_cache"
    export GOCACHEPROG="/previous/bin/super-dolphin-gate worker go-cache-proxy --seed $stage/anchor-go-build-cache --private $go_build_cache"
    printf 'go build cache source: private delta\n'
  fi
fi

use_runtime() {
  SUPER_DOLPHIN_RUNTIME_ROOT=$1
  export SUPER_DOLPHIN_RUNTIME_ROOT
  PATH="$SUPER_DOLPHIN_RUNTIME_ROOT/bin:$SUPER_DOLPHIN_RUNTIME_ROOT/go/bin:$SUPER_DOLPHIN_RUNTIME_ROOT/node/bin:/usr/local/bin:/usr/bin:/bin"
  export PATH
  if test -f "$SUPER_DOLPHIN_RUNTIME_ROOT/multiarch"; then
    IFS= read -r runtime_multiarch < "$SUPER_DOLPHIN_RUNTIME_ROOT/multiarch"
    case "$runtime_multiarch" in
      x86_64-linux-gnu|aarch64-linux-gnu) ;;
      *) echo "unsupported runtime multiarch: $runtime_multiarch" >&2; exit 1 ;;
    esac
    runtime_system_root=$SUPER_DOLPHIN_RUNTIME_ROOT/rootfs
    LD_LIBRARY_PATH=$runtime_system_root/usr/lib/$runtime_multiarch:$runtime_system_root/lib/$runtime_multiarch:$runtime_system_root/usr/lib:$runtime_system_root/lib
    export LD_LIBRARY_PATH
  fi
}

apt_ready=0
apt_update() {
  if test "$apt_ready" = 0; then
    apt-get update -qq
    apt_ready=1
  fi
}

if test -x "$payload_root/runtime/bin/git"; then
  use_runtime "$payload_root/runtime"
  git --version >/dev/null
  curl --version >/dev/null
else
  apt_update
  apt-get install -y -qq --no-install-recommends ca-certificates curl git xz-utils
fi
for tool in git tar sha256sum curl; do
  command -v "$tool" >/dev/null
done

# download_file 对暂态 TLS EOF 使用有限且可观测的 curl 重试；调用方仍须校验固定 SHA-256。
download_file() {
  destination=$1
  source_url=$2
  curl -fsSL --retry 5 --retry-all-errors --retry-delay 2 --connect-timeout 15 --max-time 600 -o "$destination" "$source_url"
}

# run_logged 将冗长阶段输出写入独立日志；失败时仅回放末尾并保留原始退出码。
run_logged() {
  label=$1
  shift
  log_file=$stage/$label.log
  slow_threshold_ms=100000
  started_at_ms=$(($(date +%s) * 1000))
  printf 'seed stage start: %s elapsed_ms=0\n' "$label"
  if "$@" >"$log_file" 2>&1; then
    elapsed_ms=$(($(date +%s) * 1000 - started_at_ms))
    printf 'seed stage complete: %s elapsed_ms=%s\n' "$label" "$elapsed_ms"
    if test "$elapsed_ms" -gt "$slow_threshold_ms"; then
      printf 'seed stage slow: %s elapsed_ms=%s threshold_ms=%s\n' "$label" "$elapsed_ms" "$slow_threshold_ms"
    fi
    return 0
  else
    status=$?
    elapsed_ms=$(($(date +%s) * 1000 - started_at_ms))
    printf 'seed stage failed: %s elapsed_ms=%s (tail 160)\n' "$label" "$elapsed_ms" >&2
    if test "$elapsed_ms" -gt "$slow_threshold_ms"; then
      printf 'seed stage slow: %s elapsed_ms=%s threshold_ms=%s\n' "$label" "$elapsed_ms" "$slow_threshold_ms" >&2
    fi
    tail -n 160 "$log_file" >&2 || true
    return "$status"
  fi
}

# build_python_runtime 在受控日志边界内构建固定版本的 Python runtime。
build_python_runtime() (
  cd "$stage/Python-3.11.2"
  ./configure --quiet --prefix=$payload_root/runtime/python --without-ensurepip
  make -s -j"$(getconf _NPROCESSORS_ONLN)"
  make -s install
)

# refresh_go_build_cache 使用 worker 的实际编译参数，并按编译模式分别报告耗时。
refresh_go_build_cache() (
	test "$BASELINE_SEED_GO_PARALLELISM" -ge 1
	case "$BASELINE_SEED_GO_MEMORY_LIMIT" in
	  *GiB) ;;
	  *) echo 'baseline seed Go memory limit is invalid' >&2; exit 1 ;;
	esac
	go_cache_compile() {
	  phase=$1
	  shift
	  printf 'go cache compile start: phase=%s\n' "$phase"
	  "$@"
	}
	private_go_mod_cache=$stage/go-mod-cache-refresh
	rm -rf "$private_go_mod_cache"
	install -d -m 0700 "$private_go_mod_cache"
	"$payload_root/bin/super-dolphin-gate" worker go-module-overlay "$go_mod_cache" "$private_go_mod_cache"
	printf 'go module cache source: immutable runtime overlay\n'
  worker_source=/workspace/work/lanes/lane-0/run/source
  rm -rf /workspace/work/lanes/lane-0
  mkdir -p "$(dirname "$worker_source")"
  cp -a "$source_root" "$worker_source"
  trap 'rm -rf /workspace/work/lanes/lane-0' EXIT
  cd "$worker_source"
  mkdir -p cmd/agent-terminal/web-dist
  cp "$payload_root/frontend-embed/index.html" cmd/agent-terminal/web-dist/index.html
  compile_go_cache_normal() {
	go_cache_compile normal env CGO_ENABLED=1 GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
	    GOMODCACHE="$private_go_mod_cache" GOCACHE="$go_build_cache" \
	    GOFLAGS="-p=$BASELINE_SEED_GO_PARALLELISM" GOMAXPROCS="$BASELINE_SEED_GO_PARALLELISM" GOMEMLIMIT="$BASELINE_SEED_GO_MEMORY_LIMIT" \
	    go test -mod=readonly -exec=true -run '^$' ./...
  }
  compile_go_cache_e2e() {
	go_cache_compile e2e env CGO_ENABLED=1 GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
	    GOMODCACHE="$private_go_mod_cache" GOCACHE="$go_build_cache" \
	    GOFLAGS="-p=$BASELINE_SEED_GO_PARALLELISM" GOMAXPROCS="$BASELINE_SEED_GO_PARALLELISM" GOMEMLIMIT="$BASELINE_SEED_GO_MEMORY_LIMIT" \
	    go test -mod=readonly -tags=e2e -exec=true -run '^$' ./cmd/mcp-lsp
  }
  compile_go_cache_race() {
    race_packages=$("$payload_root/bin/super-dolphin-gate" worker race-package-patterns)
    test -n "$race_packages"
    # registry patterns do not contain whitespace; deliberate splitting preserves the go CLI argv.
    set -- $race_packages
	  go_cache_compile race env CGO_ENABLED=1 GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
	    GOMODCACHE="$private_go_mod_cache" GOCACHE="$go_build_cache" \
	    GOFLAGS="-p=$BASELINE_SEED_GO_PARALLELISM" GOMAXPROCS="$BASELINE_SEED_GO_PARALLELISM" GOMEMLIMIT="$BASELINE_SEED_GO_MEMORY_LIMIT" \
      go test -mod=readonly -race -exec=true -run '^$' "$@"
  }
  run_logged go-cache-normal-compile compile_go_cache_normal
  run_logged go-cache-e2e-compile compile_go_cache_e2e
  run_logged go-cache-race-compile compile_go_cache_race
)

source_manifest=/input/source-manifest.json
test -f "$source_manifest"
printf '%s  %s\n' "$BASELINE_SOURCE_MANIFEST_SHA256" "$source_manifest" | sha256sum -c -

verify_source_bundle() {
  bundle=/input/source.bundle
  test -f "$bundle"
  test -n "${BASELINE_SOURCE_BUNDLE_SHA256:-}"
  test "$BASELINE_SOURCE_BUNDLE_SIZE" -gt 0
  test "$(wc -c < "$bundle" | tr -d ' ')" = "$BASELINE_SOURCE_BUNDLE_SIZE"
  printf '%s  %s\n' "$BASELINE_SOURCE_BUNDLE_SHA256" "$bundle" | sha256sum -c -
}

verify_source_tree_clean() {
  git -C "$source_root" fsck --connectivity-only
  test "$(git -C "$source_root" rev-parse HEAD)" = "$BASELINE_MAIN_COMMIT"
  test "$(git -C "$source_root" rev-parse 'HEAD^{tree}')" = "$BASELINE_MAIN_TREE"
  source_status=$(git -C "$source_root" status --porcelain=v1 --untracked-files=all)
  if test -n "$source_status"; then
    printf 'baseline source tree mutated:\n%s\n' "$source_status" >&2
    exit 1
  fi
}

case "$BASELINE_SOURCE_MODE" in
  full)
    verify_source_bundle
    git clone --quiet --no-checkout /input/source.bundle "$source_root"
    git -C "$source_root" checkout --quiet --detach "$BASELINE_MAIN_COMMIT"
    git -C "$source_root" remote remove origin
    ;;
  delta)
    test -d "$previous_source"
    test -n "${BASELINE_SOURCE_BASE_COMMIT:-}"
    test -n "${BASELINE_SOURCE_BASE_TREE:-}"
    cp -a "$previous_source" "$source_root"
    test "$(git -C "$source_root" rev-parse HEAD)" = "$BASELINE_SOURCE_BASE_COMMIT"
    test "$(git -C "$source_root" rev-parse 'HEAD^{tree}')" = "$BASELINE_SOURCE_BASE_TREE"
    verify_source_bundle
    git -C "$source_root" fetch --quiet /input/source.bundle "$BASELINE_MAIN_COMMIT"
    git -C "$source_root" checkout --quiet --detach FETCH_HEAD
    ;;
  reuse)
    test -d "$previous_source"
    test "$BASELINE_SOURCE_BUNDLE_SIZE" = 0
    test -z "${BASELINE_SOURCE_BUNDLE_SHA256:-}"
    cp -a "$previous_source" "$source_root"
    ;;
  *)
    echo "unsupported baseline source mode: $BASELINE_SOURCE_MODE" >&2
    exit 1
    ;;
esac
verify_source_tree_clean

module_lock_manifest() {
  git -C "$1" ls-files -s -- go.mod go.sum '*/go.mod' '*/go.sum'
}

runtime_dependency_manifest() {
  jq -S -e '
    if .schema_version == "11" then .inputs
    elif .schema_version == "10" then .inputs
    elif .schema_version == "9" then
      .inputs | del(.runtime_seed_recipe_sha256)
    else error("unsupported runtime dependency lock schema")
    end
  ' "$1"
}

seeds_changed=1
reuse_go_dependencies=0
reuse_fixed_toolchains=0
reuse_runtime_rootfs=0
reuse_frontend_dependencies=0
reuse_lsp_dependencies=0
reuse_runtime_tools=0
if test -d "$previous_source"; then
  module_lock_manifest "$previous_source" > "$stage/previous-go-module-locks"
  module_lock_manifest "$source_root" > "$stage/current-go-module-locks"
  if cmp -s "$stage/previous-go-module-locks" "$stage/current-go-module-locks"; then
    reuse_go_dependencies=1
  fi
  if cmp -s "$previous_source/frontend-app/package-lock.json" "$source_root/frontend-app/package-lock.json"; then
    reuse_frontend_dependencies=1
  fi
  if cmp -s "$previous_source/build/gate/runtime-lsp/package-lock.json" "$source_root/build/gate/runtime-lsp/package-lock.json"; then
    reuse_lsp_dependencies=1
  fi
  if cmp -s "$previous_source/build/gate/runtime-tools/go.mod" "$source_root/build/gate/runtime-tools/go.mod" && \
     cmp -s "$previous_source/build/gate/runtime-tools/go.sum" "$source_root/build/gate/runtime-tools/go.sum" && \
     cmp -s "$previous_source/build/gate/toolchain.lock" "$source_root/build/gate/toolchain.lock"; then
    reuse_runtime_tools=1
  fi
  if cmp -s "$previous_source/build/gate/toolchain.lock" "$source_root/build/gate/toolchain.lock"; then
    reuse_fixed_toolchains=1
  fi
  if test "$BASELINE_FORCE_RUNTIME_REFRESH" != true; then
    runtime_dependency_manifest "$previous_source/build/gate/runtime-deps.lock" > "$stage/previous-runtime-dependencies"
    runtime_dependency_manifest "$source_root/build/gate/runtime-deps.lock" > "$stage/current-runtime-dependencies"
    if cmp -s "$stage/previous-runtime-dependencies" "$stage/current-runtime-dependencies"; then
      reuse_runtime_rootfs=1
    fi
  fi
fi
if test "$BASELINE_FORCE_RUNTIME_REFRESH" != true && test -d "$previous_source" && \
   cmp -s "$stage/previous-go-module-locks" "$stage/current-go-module-locks" && \
   cmp -s "$previous_source/frontend-app/package-lock.json" "$source_root/frontend-app/package-lock.json" && \
   cmp -s "$previous_source/build/gate/runtime-lsp/package-lock.json" "$source_root/build/gate/runtime-lsp/package-lock.json" && \
   cmp -s "$stage/previous-runtime-dependencies" "$stage/current-runtime-dependencies" && \
   cmp -s "$previous_source/build/gate/toolchain.lock" "$source_root/build/gate/toolchain.lock"; then
  seeds_changed=0
fi
if test "$BASELINE_FORCE_RUNTIME_REFRESH" = true; then
  printf 'historical runtime dependency schema requires seed refresh\n'
  seeds_changed=1
  reuse_runtime_rootfs=0
fi

rm -rf "$payload_root/source" "$payload_root/baseline-manifest.json"
mv "$source_root" "$payload_root/source"
source_root=$payload_root/source
mkdir -p "$payload_root/bin" "$payload_root/runtime/bin" "$go_build_cache" "$payload_root/frontend-embed"
chmod 0700 "$go_build_cache"
printf '<!doctype html><title>gate compile seed</title>\n' > "$payload_root/frontend-embed/index.html"
mkdir -p "$source_root/cmd/agent-terminal/web-dist"
cp "$payload_root/frontend-embed/index.html" "$source_root/cmd/agent-terminal/web-dist/index.html"

gate_cli_ready=0
gate_cli_mode=compile
if test -x "$payload_root/bin/super-dolphin-gate" && \
   test "$previous_gate_source_sha256" = "$BASELINE_GATE_SOURCE_SHA256" && \
   test "$previous_gate_platform" = "$BASELINE_PLATFORM" && \
   test "$previous_gate_toolchain_digest" = "$BASELINE_TOOLCHAIN_DIGEST" && \
   verify_gate_cli_identity "$payload_root/bin/super-dolphin-gate"; then
  gate_cli_ready=1
  gate_cli_mode=reuse
  printf 'gate CLI mode: reuse; source=%s; elapsed_seconds=0\n' "$BASELINE_GATE_SOURCE_SHA256"
else
  rm -f "$payload_root/bin/super-dolphin-gate"
fi

build_gate_cli() (
  use_runtime "$payload_root/runtime"
  cd "$source_root"
  env CGO_ENABLED=0 GOWORK=off GOTOOLCHAIN=local \
    GOPROXY=off GOSUMDB=off GOMODCACHE="$payload_root/runtime/go-mod-cache" GOCACHE="$go_build_cache" \
    go build -mod=readonly -trimpath -buildvcs=false \
      -ldflags="-X main.gateSourceDigest=$BASELINE_GATE_SOURCE_SHA256 -X main.gateToolchainDigest=$BASELINE_TOOLCHAIN_DIGEST" \
      -o "$payload_root/bin/super-dolphin-gate" ./cmd/super-dolphin-gate
)

compile_gate_cli() {
  started_at=$(date +%s)
	if run_logged gate-cli-compile build_gate_cli && \
	   run_logged gate-cli-identity verify_gate_cli_identity "$payload_root/bin/super-dolphin-gate"; then
    gate_cli_ready=1
    gate_cli_mode=compile
    elapsed_seconds=$(($(date +%s) - started_at))
    printf 'gate CLI mode: compile; source=%s; elapsed_seconds=%s\n' "$BASELINE_GATE_SOURCE_SHA256" "$elapsed_seconds"
    return 0
  fi
  rm -f "$payload_root/bin/super-dolphin-gate"
  gate_cli_ready=0
  return 1
}

if test "$gate_cli_ready" = 0 && test "$seeds_changed" = 0 && \
   test "$reuse_fixed_toolchains" = 1 && test "$reuse_go_dependencies" = 1; then
  if ! compile_gate_cli; then
    printf 'gate CLI compile needs refreshed runtime dependencies\n'
    seeds_changed=1
  fi
fi

validate_offline_go_module() (
  cd "$1"
  env GOFLAGS=-mod=readonly GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
    GOMODCACHE="$runtime_validation_go_mod_cache" go list -deps -test ./... >/dev/null
)

validate_offline_module_cache() {
  validate_offline_go_module "$source_root"
  git -C "$source_root" ls-files -- '*/go.mod' | LC_ALL=C sort | while IFS= read -r nested_go_mod; do
    case "$nested_go_mod" in
      */go.mod) ;;
      *) echo "invalid tracked nested Go module path: $nested_go_mod" >&2; exit 1 ;;
    esac
    nested_module_dir=${nested_go_mod%/go.mod}
    test -n "$nested_module_dir"
    test -f "$source_root/$nested_go_mod"
    validate_offline_go_module "$source_root/$nested_module_dir"
  done
}

validate_reusable_runtime() (
  use_runtime $payload_root/runtime
  runtime_validation_go_mod_cache=$stage/runtime-reuse-go-mod-cache
  mkdir -p "$runtime_validation_go_mod_cache"
  "$payload_root/bin/super-dolphin-gate" worker go-module-overlay \
    "$payload_root/runtime/go-mod-cache" "$runtime_validation_go_mod_cache"
  validate_offline_module_cache
  test "$gate_cli_ready" = 1
  "$payload_root/bin/super-dolphin-gate" worker runtime-seed verify "$source_root" $payload_root/runtime
)

if test "$seeds_changed" = 0 && \
   test ! -x "$payload_root/runtime/frontend/node_modules/.bin/playwright"; then
  printf 'runtime frontend cache is missing Playwright CLI; rebuilding frontend dependencies\n' >&2
  reuse_frontend_dependencies=0
  seeds_changed=1
fi

if test "$seeds_changed" = 0; then
  reuse_log=$stage/runtime-reuse-verify.log
  if validate_reusable_runtime >"$reuse_log" 2>&1; then
    printf 'runtime seed cache verified; reusing\n'
  else
    printf 'runtime seed cache is stale; rebuilding\n' >&2
    tail -n 40 "$reuse_log" >&2 || true
    seeds_changed=1
  fi
fi

if test "$seeds_changed" = 1; then
  previous_runtime=
  if test -d $payload_root/runtime; then
    previous_runtime=$stage/previous-runtime
    mv $payload_root/runtime "$previous_runtime"
  fi
  mkdir -p $payload_root/runtime/bin $payload_root/runtime/rootfs

  if test "$reuse_runtime_rootfs" = 1 && test -n "$previous_runtime" && test -d "$previous_runtime/rootfs"; then
    rm -rf $payload_root/runtime/rootfs
    mv "$previous_runtime/rootfs" $payload_root/runtime/rootfs
    printf 'runtime rootfs reused\n'
  else
    apt_update
    run_logged runtime-apt apt-get install -y -qq --no-install-recommends \
      build-essential ca-certificates curl fontconfig fonts-liberation git jq libbz2-dev libffi-dev libgtk-3-dev \
      libasound2 libatk-bridge2.0-0 libatk1.0-0 libatspi2.0-0 libcairo2 libcups2 \
      libdbus-1-3 libdrm2 libgbm1 libglib2.0-0 liblzma-dev libncursesw5-dev \
      libnspr4 libnss3 libpango-1.0-0 libreadline-dev libsqlite3-dev libssl-dev \
      libwebkit2gtk-4.1-dev libsoup-3.0-dev libx11-6 libxcb1 libxcomposite1 \
      libxdamage1 libxext6 libxfixes3 libxkbcommon0 libxrandr2 pkg-config procps \
      python3 ripgrep rsync tk-dev uuid-dev x11-xkb-utils xauth xkb-data xvfb \
      xz-utils zlib1g-dev
    test -f /etc/fonts/fonts.conf
    test -d /usr/share/fonts
    test -n "$(find /usr/share/fonts -type f -print -quit)"
    tar -C / -cf - usr lib lib64 etc/fonts etc/ssl etc/ca-certificates.conf |
      tar -C $payload_root/runtime/rootfs -xf -
    test -f "$payload_root/runtime/rootfs/etc/fonts/fonts.conf"
    test -d "$payload_root/runtime/rootfs/usr/share/fonts"
    test -n "$(find "$payload_root/runtime/rootfs/usr/share/fonts" -type f -print -quit)"
  fi

  case "$BASELINE_PLATFORM" in
    linux/amd64)
      runtime_multiarch=x86_64-linux-gnu
      go_arch=amd64
      node_url=https://mirrors.aliyun.com/nodejs-release/v24.18.0/node-v24.18.0-linux-x64.tar.xz
      node_sha256=55aa7153f9d88f28d765fcdad5ae6945b5c0f98a36881703817e4c450fa76742
      ;;
    linux/arm64)
      runtime_multiarch=aarch64-linux-gnu
      go_arch=arm64
      node_url=https://mirrors.aliyun.com/nodejs-release/v24.18.0/node-v24.18.0-linux-arm64.tar.xz
      node_sha256=58c9520501f6ae2b52d5b210444e24b9d0c029a58c5011b797bc1fe7105886f6
      ;;
    *)
      echo "unsupported baseline platform: $BASELINE_PLATFORM" >&2
      exit 1
      ;;
  esac
  go_reused=0
  if test "$reuse_fixed_toolchains" = 1 && test -n "$previous_runtime" && \
     test "$(head -n 1 "$previous_runtime/go/VERSION" 2>/dev/null || true)" = "$BASELINE_GO_TOOLCHAIN"; then
    mv "$previous_runtime/go" $payload_root/runtime/go
    go_reused=1
    printf 'runtime toolchain reused: go\n'
  fi
  if test "$go_reused" = 0; then
    case "$BASELINE_GO_TOOLCHAIN/$go_arch" in
      go1.26.5/amd64) go_sha256=5c2c3b16caefa1d968a94c1daca04a7ca301a496d9b086e17ad77bb81393f053 ;;
      go1.26.5/arm64) go_sha256=fe4789e92b1f33358680864bbe8704289e7bb5fc207d80623c308935bd696d49 ;;
      *) echo "unsupported locked Go archive: $BASELINE_GO_TOOLCHAIN/$go_arch" >&2; exit 1 ;;
    esac
    go_filename=${BASELINE_GO_TOOLCHAIN}.linux-${go_arch}.tar.gz
    test "$go_filename" = "${BASELINE_GO_TOOLCHAIN}.linux-${go_arch}.tar.gz"
    test "${#go_sha256}" = 64
    case "$go_sha256" in *[!0-9a-f]*) echo "invalid Go archive SHA-256" >&2; exit 1;; esac
    go_url="https://mirrors.aliyun.com/golang/$go_filename"
    download_file "$stage/go.tar.gz" "$go_url"
    printf '%s  %s\n' "$go_sha256" "$stage/go.tar.gz" | sha256sum -c -
    mkdir -p $payload_root/runtime/go
    tar -xzf "$stage/go.tar.gz" -C $payload_root/runtime/go --strip-components=1
  fi

  node_reused=0
  if test "$reuse_fixed_toolchains" = 1 && test -n "$previous_runtime" && \
     test "$($previous_runtime/node/bin/node --version 2>/dev/null || true)" = v24.18.0; then
    mv "$previous_runtime/node" $payload_root/runtime/node
    node_reused=1
    printf 'runtime toolchain reused: node\n'
  fi
  if test "$node_reused" = 0; then
    download_file "$stage/node.tar.xz" "$node_url"
    printf '%s  %s\n' "$node_sha256" "$stage/node.tar.xz" | sha256sum -c -
    mkdir -p $payload_root/runtime/node
    tar -xJf "$stage/node.tar.xz" -C $payload_root/runtime/node --strip-components=1
  fi

  python_reused=0
  if test "$reuse_fixed_toolchains" = 1 && test -n "$previous_runtime" && \
     test -d "$previous_runtime/python"; then
    mv "$previous_runtime/python" $payload_root/runtime/python
    python_reused=1
    printf 'runtime toolchain reused: python\n'
  fi
  if test "$python_reused" = 0; then
    download_file "$stage/python.tar.xz" https://mirrors.aliyun.com/python-release/source/Python-3.11.2.tar.xz
    printf '%s  %s\n' 29e4b8f5f1658542a8c13e2dd277358c9c48f2b2f7318652ef1675e402b9d2af \
      "$stage/python.tar.xz" | sha256sum -c -
    tar -xJf "$stage/python.tar.xz" -C "$stage"
    run_logged python-runtime build_python_runtime
  fi
  printf '%s\n' "$runtime_multiarch" > $payload_root/runtime/multiarch

  cat > $payload_root/runtime/bin/portable-tool <<'EOF'
#!/bin/sh
set -eu
runtime_root=${SUPER_DOLPHIN_RUNTIME_ROOT:-/opt/super-dolphin-gate/runtime}
system_root=$runtime_root/rootfs
test -f "$runtime_root/multiarch"
IFS= read -r multiarch < "$runtime_root/multiarch"
case "$multiarch" in
  x86_64-linux-gnu|aarch64-linux-gnu) ;;
  *) echo "unsupported runtime multiarch: $multiarch" >&2; exit 1 ;;
esac
library_path=$system_root/usr/lib/$multiarch:$system_root/lib/$multiarch:$system_root/usr/lib:$system_root/lib
export LD_LIBRARY_PATH=$library_path
tool=${0##*/}
case "$tool" in
  git)
    export GIT_EXEC_PATH=$system_root/usr/lib/git-core
    export GIT_TEMPLATE_DIR=$system_root/usr/share/git-core/templates
    export SSL_CERT_FILE=$system_root/etc/ssl/certs/ca-certificates.crt
    exec "$system_root/usr/bin/git" "$@"
    ;;
  gcc|cc|g++|c++)
    compiler=$tool
    case "$tool" in cc) compiler=gcc ;; c++) compiler=g++ ;; esac
    compiler_root=$system_root/usr/lib/gcc/$multiarch
    compiler_version=$(find "$compiler_root" -mindepth 1 -maxdepth 1 -type d -print -quit)
    test -n "$compiler_version"
    export GCC_EXEC_PREFIX=$compiler_version/
    export COMPILER_PATH=$compiler_version:$system_root/usr/bin
    export LIBRARY_PATH=$compiler_version:$system_root/usr/lib/$multiarch:$system_root/lib/$multiarch
    exec "$system_root/usr/bin/$compiler" --sysroot="$system_root" "$@"
    ;;
  pkg-config)
    export PKG_CONFIG_SYSROOT_DIR=$system_root
    export PKG_CONFIG_LIBDIR=$system_root/usr/lib/$multiarch/pkgconfig:$system_root/usr/lib/pkgconfig:$system_root/usr/share/pkgconfig
    exec "$system_root/usr/bin/pkg-config" "$@"
    ;;
  python|python3)
    export PYTHONHOME=$runtime_root/python
    exec "$runtime_root/python/bin/python3" "$@"
    ;;
  Xvfb|curl|jq|make|ps|rg|rsync|xauth|xkbcomp|xvfb-run)
    exec "$system_root/usr/bin/$tool" "$@"
    ;;
  *)
    echo "unsupported portable tool: $tool" >&2
    exit 1
    ;;
esac
EOF
  chmod 0755 $payload_root/runtime/bin/portable-tool
  for tool in git gcc cc g++ c++ pkg-config python python3 Xvfb curl jq make ps rg rsync xauth xkbcomp xvfb-run; do
    cp $payload_root/runtime/bin/portable-tool "$payload_root/runtime/bin/$tool"
  done

  if test -n "$previous_runtime" && test -d "$previous_runtime/go-mod-cache"; then
    rm -rf "$go_mod_cache"
    mv "$previous_runtime/go-mod-cache" "$go_mod_cache"
    printf 'runtime dependency cache reused: go modules\n'
  fi

  use_runtime $payload_root/runtime
  test "$(go version | awk '{print $3}')" = "$BASELINE_GO_TOOLCHAIN"
  test "$(node --version)" = "v24.18.0"
  test "$(npm --version)" = "11.16.0"
  test "$(python3 --version)" = "Python 3.11.2"
  test "$(rg --version | head -n 1)" = "ripgrep 13.0.0"

  module_download_root=$stage/module-download-source
  rm -rf "$module_download_root"
  cp -a "$source_root" "$module_download_root"
  runtime_dependency_goproxy=https://goproxy.cn,direct
  if test -n "$previous_runtime"; then
    runtime_dependency_goproxy=off
  fi
  download_go_module() (
    cd "$1"
    env GOFLAGS=-mod=readonly GOWORK=off GOTOOLCHAIN=local GOPROXY="$runtime_dependency_goproxy" GOSUMDB=off \
      GOMODCACHE="$go_mod_cache" go mod download all
    cd "$2"
    env GOFLAGS=-mod=readonly GOWORK=off GOTOOLCHAIN=local GOPROXY="$runtime_dependency_goproxy" GOSUMDB=off \
      GOMODCACHE="$go_mod_cache" go list -deps -test ./... >/dev/null
    for target in \
      windows/amd64 windows/arm64 \
      darwin/amd64 darwin/arm64 \
      linux/amd64 linux/arm64 \
      freebsd/amd64 freebsd/arm64; do
      target_goos=${target%/*}
      target_goarch=${target#*/}
      env GOFLAGS=-mod=readonly GOWORK=off GOTOOLCHAIN=local GOPROXY="$runtime_dependency_goproxy" GOSUMDB=off \
        GOMODCACHE="$go_mod_cache" GOOS="$target_goos" GOARCH="$target_goarch" CGO_ENABLED=0 \
        go list -deps -test ./... >/dev/null
    done
  )
  download_locked_module_proxy() (
    cd "$source_root/build/gate/runtime-proxy"
    env GOFLAGS=-mod=readonly GOWORK=off GOTOOLCHAIN=local GOPROXY="$runtime_dependency_goproxy" GOSUMDB=off \
      GOMODCACHE="$go_mod_cache" go mod download
  )
  download_go_module "$module_download_root" "$source_root"
  download_locked_module_proxy
  git -C "$source_root" ls-files -- '*/go.mod' | LC_ALL=C sort | while IFS= read -r nested_go_mod; do
    case "$nested_go_mod" in
      */go.mod) ;;
      *) echo "invalid tracked nested Go module path: $nested_go_mod" >&2; exit 1 ;;
    esac
    nested_module_dir=${nested_go_mod%/go.mod}
    test -n "$nested_module_dir"
    test -f "$source_root/$nested_go_mod"
    download_go_module "$module_download_root/$nested_module_dir" "$source_root/$nested_module_dir"
  done
  rm -rf "$module_download_root"
  verify_source_tree_clean
  if test "$reuse_go_dependencies" = 1 && test -n "$previous_runtime" && test -d "$previous_runtime/go-proxy"; then
    mv "$previous_runtime/go-proxy" $payload_root/runtime/go-proxy
    printf 'runtime dependency cache reused: Go module proxy\n'
  else
    mkdir -p $payload_root/runtime/go-proxy
    cp -a "$go_mod_cache/cache/download/." $payload_root/runtime/go-proxy/
  fi

  if test "$reuse_frontend_dependencies" = 1 && test -n "$previous_runtime" && \
     test -d "$previous_runtime/frontend/node_modules" && \
     test -d "$previous_runtime/frontend/npm-cache"; then
    mv "$previous_runtime/frontend" $payload_root/runtime/frontend
    printf 'runtime dependency cache reused: frontend node_modules and npm cache\n'
  else
    (
      cd "$source_root/frontend-app"
      env NPM_CONFIG_CACHE=$stage/npm-cache \
        npm ci --ignore-scripts --no-audit --no-fund
    )
    rm -rf "$stage/npm-cache/_logs" "$stage/npm-cache/_cacache/tmp"
    rm -f "$stage/npm-cache/_update-notifier-last-checked"
    test -d "$stage/npm-cache/_cacache/content-v2"
    test -d "$stage/npm-cache/_cacache/index-v5"
    mkdir -p $payload_root/runtime/frontend
    mv "$source_root/frontend-app/node_modules" $payload_root/runtime/frontend/node_modules
    mv "$stage/npm-cache" $payload_root/runtime/frontend/npm-cache
  fi
`

const remoteBaselineSeedScript = remoteBaselineSeedScriptHead + remoteBaselineSeedScriptBrowser + remoteBaselineSeedScriptLSP + remoteBaselineSeedScriptTail + remoteBaselineSeedScriptRuntime
