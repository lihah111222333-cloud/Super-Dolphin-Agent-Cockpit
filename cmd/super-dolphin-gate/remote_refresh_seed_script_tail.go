package main

const remoteBaselineSeedScriptTail = `  runtime_tools_reused=0
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
      env GOWORK=off GOTOOLCHAIN=local GOPROXY="$runtime_dependency_goproxy" GOSUMDB=off GOMODCACHE="$tool_go_mod_cache" \
        go build -mod=readonly -trimpath -buildvcs=false -o $payload_root/runtime/bin/actionlint github.com/rhysd/actionlint/cmd/actionlint
      env GOWORK=off GOTOOLCHAIN=local GOPROXY="$runtime_dependency_goproxy" GOSUMDB=off GOMODCACHE="$tool_go_mod_cache" \
        go build -mod=readonly -trimpath -buildvcs=false -o $payload_root/runtime/bin/gopls golang.org/x/tools/gopls
      env GOWORK=off GOTOOLCHAIN=local GOPROXY="$runtime_dependency_goproxy" GOSUMDB=off GOMODCACHE="$tool_go_mod_cache" \
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

// remoteBaselineSeedScriptDirectCache 保持直读层装配在缓存初始化分支的原始顺序。
const remoteBaselineSeedScriptDirectCache = `    direct_layer_count=$BASELINE_DIRECT_CACHE_LAYER_COUNT
    case "$direct_layer_count" in 1|2|3) ;; *) echo 'direct cache layer count is invalid' >&2; exit 1;; esac
    rm -rf "$go_build_cache"
    install -d -m 0700 "$go_build_cache"
    test -x /previous/bin/super-dolphin-gate
    go_cache_proxy="/previous/bin/super-dolphin-gate worker go-cache-proxy"
    direct_layer_index=0
    while test "$direct_layer_index" -lt "$direct_layer_count"; do
      direct_layer_number=$((direct_layer_index + 1))
      eval "direct_layer_identity=\${BASELINE_DIRECT_CACHE_LAYER_$direct_layer_number:-}"
      test -n "$direct_layer_identity" || { echo "direct cache layer $direct_layer_number identity is missing" >&2; exit 1; }
      direct_layer_root=/direct-cache-layers/layer-$direct_layer_index/cache-seed/go-build
      test -d "$direct_layer_root" || { echo "direct cache layer $direct_layer_number root is missing" >&2; exit 1; }
      test -n "$(find "$direct_layer_root" -type f -print -quit)" || { echo "direct cache layer $direct_layer_number is empty" >&2; exit 1; }
      go_cache_proxy="$go_cache_proxy --seed $direct_layer_root"
      direct_layer_index=$((direct_layer_index + 1))
    done
    export GOCACHE="$go_build_cache"
    export GOCACHEPROG="$go_cache_proxy --private $go_build_cache"
    printf 'go build cache source: direct layers newest-first (%s); private delta publish\n' "$direct_layer_count"
`
