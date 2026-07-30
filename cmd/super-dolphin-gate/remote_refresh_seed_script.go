package main

const remoteBaselineSeedBootstrapScript = `#!/bin/sh
set -eu
seed=/input/seed.sh
test -f "$seed"
test "$(wc -c < "$seed" | tr -d ' ')" = "$BASELINE_SEED_SCRIPT_SIZE"
printf '%s  %s\n' "$BASELINE_SEED_SCRIPT_SHA256" "$seed" | sha256sum -c -
exec /bin/sh "$seed"
`

const remoteBaselineSeedScriptHead = `#!/bin/sh
set -eu
umask 022

export HOME=/tmp/home
export XDG_CACHE_HOME=/tmp/xdg-cache
export GOENV=off
export GOTELEMETRY=off
export DEBIAN_FRONTEND=noninteractive
mkdir -p "$HOME" "$XDG_CACHE_HOME"

required_env="BASELINE_MANIFEST_SCHEMA_VERSION BASELINE_GENERATION BASELINE_MAIN_COMMIT BASELINE_MAIN_TREE BASELINE_PLATFORM BASELINE_POLICY_DIGEST BASELINE_TOOLCHAIN_DIGEST BASELINE_GATE_SOURCE_SHA256 BASELINE_RUNTIME_IMAGE BASELINE_SOURCE_MODE BASELINE_SOURCE_BUNDLE_SIZE BASELINE_SOURCE_MANIFEST_SHA256 BASELINE_SQRUFF_SHA256 BASELINE_STORAGE_MODE"
for name in $required_env; do
  eval "value=\${$name:-}"
  test -n "$value"
done
case "$BASELINE_STORAGE_MODE" in anchor|delta) ;; *) echo "unsupported baseline storage mode" >&2; exit 1;; esac
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
  if test "$BASELINE_STORAGE_MODE" = delta && { test "$anchor_schema" != "$BASELINE_MANIFEST_SCHEMA_VERSION" || test "$anchor_mode" != anchor; }; then
    printf 'previous baseline is not a v%s Anchor; compacting full Anchor\n' "$BASELINE_MANIFEST_SCHEMA_VERSION"
    BASELINE_STORAGE_MODE=anchor
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
  mv "$go_build_cache" "$stage/anchor-go-build-cache"
  install -d -m 0700 "$go_build_cache"
  test -x /previous/bin/super-dolphin-gate
  test -n "$(find "$stage/anchor-go-build-cache" -type f -print -quit)"
  export GOCACHE="$go_build_cache"
  export GOCACHEPROG="/previous/bin/super-dolphin-gate worker go-cache-proxy --seed $stage/anchor-go-build-cache --private $go_build_cache"
  printf 'go build cache source: private delta\n'
fi

use_runtime() {
  SUPER_DOLPHIN_RUNTIME_ROOT=$1
  export SUPER_DOLPHIN_RUNTIME_ROOT
  PATH="$SUPER_DOLPHIN_RUNTIME_ROOT/bin:$SUPER_DOLPHIN_RUNTIME_ROOT/go/bin:$SUPER_DOLPHIN_RUNTIME_ROOT/node/bin:/usr/local/bin:/usr/bin:/bin"
  export PATH
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
  printf 'seed stage start: %s\n' "$label"
  if "$@" >"$log_file" 2>&1; then
    printf 'seed stage complete: %s\n' "$label"
    return 0
  else
    status=$?
    printf 'seed stage failed: %s (tail 160)\n' "$label" >&2
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

# refresh_go_build_cache 使用 worker 的实际编译参数，仅补齐普通与 race 编译缓存 miss。
refresh_go_build_cache() (
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
  env CGO_ENABLED=1 GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
	  GOMODCACHE="$private_go_mod_cache" GOCACHE="$go_build_cache" \
	  GOFLAGS="-p=2" GOMAXPROCS=2 GOMEMLIMIT=1GiB \
	  go test -mod=readonly -exec=true -run '^$' ./...
	env CGO_ENABLED=1 GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
	  GOMODCACHE="$private_go_mod_cache" GOCACHE="$go_build_cache" \
    GOFLAGS="-p=1" GOMAXPROCS=1 GOMEMLIMIT=1GiB \
    go test -mod=readonly -race -exec=true -run '^$' ./...
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
    printf '%s\n' "$BASELINE_MAIN_COMMIT" > "$source_root/.git/shallow"
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
  if cmp -s "$previous_source/build/gate/runtime-deps.lock" "$source_root/build/gate/runtime-deps.lock"; then
    reuse_runtime_rootfs=1
  fi
fi
if test -d "$previous_source" && \
   cmp -s "$stage/previous-go-module-locks" "$stage/current-go-module-locks" && \
   cmp -s "$previous_source/frontend-app/package-lock.json" "$source_root/frontend-app/package-lock.json" && \
   cmp -s "$previous_source/build/gate/runtime-lsp/package-lock.json" "$source_root/build/gate/runtime-lsp/package-lock.json" && \
   cmp -s "$previous_source/build/gate/runtime-deps.lock" "$source_root/build/gate/runtime-deps.lock" && \
   cmp -s "$previous_source/build/gate/toolchain.lock" "$source_root/build/gate/toolchain.lock"; then
  seeds_changed=0
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
    GOMODCACHE="$payload_root/runtime/go-mod-cache" go list -deps -test ./... >/dev/null
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
  validate_offline_module_cache
  test "$gate_cli_ready" = 1
  "$payload_root/bin/super-dolphin-gate" worker runtime-seed verify "$source_root" $payload_root/runtime
)

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
      build-essential ca-certificates curl git jq libbz2-dev libffi-dev libgtk-3-dev \
      liblzma-dev libncursesw5-dev libreadline-dev libsqlite3-dev libssl-dev \
      libwebkit2gtk-4.1-dev libsoup-3.0-dev pkg-config procps python3 ripgrep \
      rsync tk-dev uuid-dev x11-xkb-utils xauth xkb-data xvfb xz-utils zlib1g-dev
    tar -C / -cf - usr lib lib64 etc/ssl etc/ca-certificates.conf |
      tar -C $payload_root/runtime/rootfs -xf -
  fi

  case "$BASELINE_PLATFORM" in
    linux/amd64)
      runtime_multiarch=x86_64-linux-gnu
      go_url=https://mirrors.aliyun.com/golang/go1.26.5.linux-amd64.tar.gz
      go_sha256=5c2c3b16caefa1d968a94c1daca04a7ca301a496d9b086e17ad77bb81393f053
      node_url=https://mirrors.aliyun.com/nodejs-release/v24.18.0/node-v24.18.0-linux-x64.tar.xz
      node_sha256=55aa7153f9d88f28d765fcdad5ae6945b5c0f98a36881703817e4c450fa76742
      ;;
    linux/arm64)
      runtime_multiarch=aarch64-linux-gnu
      go_url=https://mirrors.aliyun.com/golang/go1.26.5.linux-arm64.tar.gz
      go_sha256=fe4789e92b1f33358680864bbe8704289e7bb5fc207d80623c308935bd696d49
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
     test "$(head -n 1 "$previous_runtime/go/VERSION" 2>/dev/null || true)" = go1.26.5; then
    mv "$previous_runtime/go" $payload_root/runtime/go
    go_reused=1
    printf 'runtime toolchain reused: go\n'
  fi
  if test "$go_reused" = 0; then
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
tool=$(basename "$0")
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
  test "$(go version | awk '{print $3}')" = "go1.26.5"
  test "$(node --version)" = "v24.18.0"
  test "$(npm --version)" = "11.16.0"
  test "$(python3 --version)" = "Python 3.11.2"
  test "$(rg --version | head -n 1)" = "ripgrep 13.0.0"

  download_go_module() (
    cd "$1"
    env GOFLAGS=-mod=readonly GOWORK=off GOTOOLCHAIN=local GOPROXY=https://goproxy.cn,direct \
      GOMODCACHE="$go_mod_cache" go list -deps -test ./... >/dev/null
    for target in \
      windows/amd64 windows/arm64 \
      darwin/amd64 darwin/arm64 \
      linux/amd64 linux/arm64 \
      freebsd/amd64 freebsd/arm64; do
      target_goos=${target%/*}
      target_goarch=${target#*/}
      env GOFLAGS=-mod=readonly GOWORK=off GOTOOLCHAIN=local GOPROXY=https://goproxy.cn,direct \
        GOMODCACHE="$go_mod_cache" GOOS="$target_goos" GOARCH="$target_goarch" CGO_ENABLED=0 \
        go list -deps -test ./... >/dev/null
    done
  )
  download_locked_module_proxy() (
    cd "$source_root/build/gate/runtime-proxy"
    env GOFLAGS=-mod=readonly GOWORK=off GOTOOLCHAIN=local GOPROXY=https://goproxy.cn,direct \
      GOMODCACHE="$go_mod_cache" go mod download
  )
  download_go_module "$source_root"
  download_locked_module_proxy
  git -C "$source_root" ls-files -- '*/go.mod' | LC_ALL=C sort | while IFS= read -r nested_go_mod; do
    case "$nested_go_mod" in
      */go.mod) ;;
      *) echo "invalid tracked nested Go module path: $nested_go_mod" >&2; exit 1 ;;
    esac
    nested_module_dir=${nested_go_mod%/go.mod}
    test -n "$nested_module_dir"
    test -f "$source_root/$nested_go_mod"
    download_go_module "$source_root/$nested_module_dir"
  done
  verify_source_tree_clean
  if test "$reuse_go_dependencies" = 1 && test -n "$previous_runtime" && test -d "$previous_runtime/go-proxy"; then
    mv "$previous_runtime/go-proxy" $payload_root/runtime/go-proxy
    printf 'runtime dependency cache reused: Go module proxy\n'
  else
    mkdir -p $payload_root/runtime/go-proxy
    cp -a "$go_mod_cache/cache/download/." $payload_root/runtime/go-proxy/
  fi

  if test "$reuse_frontend_dependencies" = 1 && test -n "$previous_runtime" && \
     test -d "$previous_runtime/frontend/node_modules"; then
    mv "$previous_runtime/frontend" $payload_root/runtime/frontend
    printf 'runtime dependency cache reused: frontend node_modules\n'
  else
    (
      cd "$source_root/frontend-app"
      env NPM_CONFIG_CACHE=$stage/npm-cache \
        npm ci --ignore-scripts --no-audit --no-fund
    )
    mkdir -p $payload_root/runtime/frontend
    mv "$source_root/frontend-app/node_modules" $payload_root/runtime/frontend/node_modules
  fi

  if test "$reuse_lsp_dependencies" = 1 && test -n "$previous_runtime" && \
     test -d "$previous_runtime/lsp/node_modules"; then
    mv "$previous_runtime/lsp" $payload_root/runtime/lsp
    printf 'runtime dependency cache reused: lsp node_modules\n'
  else
    (
      cd "$source_root/build/gate/runtime-lsp"
      npm ci --ignore-scripts --no-audit --no-fund
    )
    mkdir -p $payload_root/runtime/lsp
    mv "$source_root/build/gate/runtime-lsp/node_modules" $payload_root/runtime/lsp/node_modules
  fi
  for tool in bash-language-server pyright-langserver typescript-language-server vscode-css-language-server; do
    test -x "$payload_root/runtime/lsp/node_modules/.bin/$tool"
    ln -s "../lsp/node_modules/.bin/$tool" "$payload_root/runtime/bin/$tool"
  done

  runtime_tools_reused=0
  if test "$reuse_runtime_tools" = 1 && test -n "$previous_runtime"; then
    runtime_tools_reused=1
    for tool in actionlint gopls sqlc; do
      if test ! -x "$previous_runtime/bin/$tool"; then runtime_tools_reused=0; break; fi
    done
  fi
  if test "$runtime_tools_reused" = 1; then
    for tool in actionlint gopls sqlc; do mv "$previous_runtime/bin/$tool" "$payload_root/runtime/bin/$tool"; done
    printf 'runtime dependency cache reused: Go tools\n'
  else
    (
      cd "$source_root/build/gate/runtime-tools"
      env GOWORK=off GOTOOLCHAIN=local GOPROXY=https://goproxy.cn,direct GOMODCACHE="$tool_go_mod_cache" \
        go build -mod=readonly -trimpath -buildvcs=false -o $payload_root/runtime/bin/actionlint github.com/rhysd/actionlint/cmd/actionlint
      env GOWORK=off GOTOOLCHAIN=local GOPROXY=https://goproxy.cn,direct GOMODCACHE="$tool_go_mod_cache" \
        go build -mod=readonly -trimpath -buildvcs=false -o $payload_root/runtime/bin/gopls golang.org/x/tools/gopls
      env GOWORK=off GOTOOLCHAIN=local GOPROXY=https://goproxy.cn,direct GOMODCACHE="$tool_go_mod_cache" \
        go build -mod=readonly -trimpath -buildvcs=false -o $payload_root/runtime/bin/sqlc github.com/sqlc-dev/sqlc/cmd/sqlc
    )
  fi
  rm -rf $payload_root/runtime/go-mod-cache
  mv "$go_mod_cache" $payload_root/runtime/go-mod-cache
	if test -n "$previous_runtime" && test -x "$previous_runtime/bin/sqruff"; then
	  mv "$previous_runtime/bin/sqruff" "$payload_root/runtime/bin/sqruff"
	  printf 'runtime dependency cache reused: sqruff\n'
	else
	  test -f /input/sqruff.tar.gz
	  cp /input/sqruff.tar.gz "$stage/sqruff.tar.gz"
	  printf '%s  %s\n' "$BASELINE_SQRUFF_SHA256" "$stage/sqruff.tar.gz" | sha256sum -c -
	  tar -xzf "$stage/sqruff.tar.gz" -C $payload_root/runtime/bin
	fi
  test "$($payload_root/runtime/bin/sqruff --version)" = "sqruff 0.38.0"
  if test -n "$previous_runtime"; then rm -rf "$previous_runtime"; fi

fi

# The distro xvfb-run helper can report readiness before a portable-rootfs
# Xvfb has published its Unix socket. Keep the public command contract while
# waiting for the display that the desktop process will actually use.
cat > $payload_root/runtime/bin/xvfb-run <<'EOF'
#!/bin/sh
set -eu
runtime_root=${SUPER_DOLPHIN_RUNTIME_ROOT:-/opt/super-dolphin-gate/runtime}
runtime_bin=$runtime_root/bin
auto_servernum=0
server_args=
while test "$#" -gt 0; do
  case "$1" in
    -a|--auto-servernum)
      auto_servernum=1
      shift
      ;;
    --server-args=*)
      server_args=${1#*=}
      shift
      ;;
    --)
      shift
      break
      ;;
    -*)
      echo "unsupported portable xvfb-run option: $1" >&2
      exit 2
      ;;
    *)
      break
      ;;
  esac
done
test "$#" -gt 0
case "$server_args" in
  ''|'-screen 0 1280x1024x24') ;;
  *) echo "unsupported portable xvfb-run server args: $server_args" >&2; exit 2 ;;
esac
servernum=${XVFB_RUN_SERVERNUM:-99}
if test "$auto_servernum" = 1; then
  while test -e "/tmp/.X11-unix/X$servernum" || test -e "/tmp/.X$servernum-lock"; do
    servernum=$((servernum + 1))
    test "$servernum" -le 599 || { echo "no free Xvfb display" >&2; exit 1; }
  done
fi
display=:$servernum
log=${TMPDIR:-/tmp}/super-dolphin-xvfb-$$.log
mkdir -p /tmp/.X11-unix
chmod 1777 /tmp/.X11-unix
"$runtime_bin/Xvfb" "$display" -screen 0 1280x1024x24 -ac -nolisten tcp \
  -xkbdir "$runtime_root/rootfs/usr/share/X11/xkb" >"$log" 2>&1 &
xvfb_pid=$!
cleanup_xvfb() {
  kill "$xvfb_pid" 2>/dev/null || true
  wait "$xvfb_pid" 2>/dev/null || true
  rm -f "$log"
}
trap cleanup_xvfb EXIT HUP INT TERM
ready=0
attempt=0
while test "$attempt" -lt 100; do
  if ! kill -0 "$xvfb_pid" 2>/dev/null; then
    cat "$log" >&2
    echo "portable Xvfb exited before publishing $display" >&2
    exit 1
  fi
  if test -S "/tmp/.X11-unix/X$servernum"; then
    ready=1
    break
  fi
  attempt=$((attempt + 1))
  sleep 0.05
done
if test "$ready" != 1; then
  cat "$log" >&2
  echo "portable Xvfb did not publish $display" >&2
  exit 1
fi
export DISPLAY=$display
"$@"
EOF
chmod 0755 $payload_root/runtime/bin/xvfb-run
`

const remoteBaselineSeedScript = remoteBaselineSeedScriptHead + remoteBaselineSeedScriptRuntime
