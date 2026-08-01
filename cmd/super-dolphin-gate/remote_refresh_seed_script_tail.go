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
