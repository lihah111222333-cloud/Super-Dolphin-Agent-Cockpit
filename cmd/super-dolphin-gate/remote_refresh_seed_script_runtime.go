package main

const remoteBaselineSeedScriptLSP = `  if test "$reuse_lsp_dependencies" = 1 && test -n "$previous_runtime" && \
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

`

const remoteBaselineSeedScriptRuntime = `
use_runtime $payload_root/runtime
go_mod_cache=$payload_root/runtime/go-mod-cache
test -d "$go_mod_cache"
for tool in git go node npm python3 rg make gcc pkg-config Xvfb xkbcomp xvfb-run; do
  command -v "$tool" >/dev/null
done
test -d "$SUPER_DOLPHIN_RUNTIME_ROOT/rootfs/usr/share/X11/xkb"
test "$(go version | awk '{print $3}')" = "$BASELINE_GO_TOOLCHAIN"
test "$(node --version)" = "v24.18.0"
test "$(npm --version)" = "11.16.0"
test "$(python3 --version)" = "Python 3.11.2"
printf '[baseline-seed] portable runtime validated\n'

if test "$gate_cli_ready" != 1; then
  compile_gate_cli
fi
test -x "$payload_root/bin/super-dolphin-gate"
verify_source_tree_clean
if test "$previous_baseline" = 1 && test "$seeds_changed" = 0 && \
   { test "$BASELINE_SOURCE_MODE" = reuse || \
     { test "$BASELINE_SOURCE_MODE" = delta && test "$BASELINE_SOURCE_BASE_TREE" = "$BASELINE_MAIN_TREE"; }; }; then
  printf 'go build cache mode: unchanged reuse; refresh skipped\n'
elif test "$previous_baseline" = 1; then
  printf 'go build cache mode: incremental refresh\n'
  refresh_go_build_cache
else
  printf 'go build cache mode: bootstrap refresh\n'
  refresh_go_build_cache
fi
test -d "$go_build_cache"
verify_source_tree_clean
rm -f $payload_root/runtime/manifest.json
chmod -R a+rX $payload_root
chmod 0755 $payload_root/bin/super-dolphin-gate
chmod 0755 $payload_root/runtime/bin/* $payload_root/runtime/go/bin/* $payload_root/runtime/node/bin/* $payload_root/runtime/python/bin/*
$payload_root/bin/super-dolphin-gate worker runtime-seed write "$payload_root/source" $payload_root/runtime
$payload_root/bin/super-dolphin-gate worker runtime-seed verify "$payload_root/source" $payload_root/runtime
runtime_layer_reusable=0
if test "$previous_layered" = 1 && test "$seeds_changed" = 0 && \
   cmp -s "$previous_runtime_manifest" "$payload_root/runtime/manifest.json"; then
  runtime_layer_reusable=1
fi
if test "$BASELINE_STORAGE_MODE" = delta && test "$runtime_layer_reusable" != 1 && test "$BASELINE_TOOLCHAIN_CHANGED" != true; then
  echo 'runtime seed changed but no incremental runtime layer was produced; full Anchor rebuild is forbidden' >&2
  exit 1
fi

archive_layer() {
  destination=$1
  shift
  tar --format=gnu --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner --use-compress-program='gzip -1 -n' \
    -C "$payload_root" -cf "$destination" "$@"
}
measure_layer() {
  layer_path=$1
  layer_digest=$(digest_file "$layer_path")
  layer_size=$(stat -c '%s' "$layer_path")
  test "$layer_size" -gt 0
}

gate_digest=$(digest_file $payload_root/bin/super-dolphin-gate)
gate_size=$(stat -c '%s' "$payload_root/bin/super-dolphin-gate")
runtime_manifest_digest=$(digest_file $payload_root/runtime/manifest.json)
ca_bundle=$payload_root/runtime/rootfs/etc/ssl/certs/ca-certificates.crt
ca_bundle_digest=$(digest_file "$ca_bundle")
ca_bundle_size=$(stat -c '%s' "$ca_bundle")
test "$gate_size" -gt 0
test "$ca_bundle_size" -gt 0
mkdir -p "$oss_output/bin"
cp $payload_root/bin/super-dolphin-gate "$oss_output/bin/super-dolphin-gate"
cp "$ca_bundle" "$oss_output/ca-certificates.crt"
if test "$BASELINE_STORAGE_MODE" = delta; then
  source_archive_path=$oss_output/source.delta.bundle
  cp /input/source.bundle "$source_archive_path"
  run_logged layer-measure-source-delta measure_layer "$source_archive_path"
  source_archive_digest=$layer_digest; source_archive_size=$layer_size
  runtime_go_layer_json=
  if test "$BASELINE_TOOLCHAIN_CHANGED" = true; then
    runtime_go_archive_path=$oss_output/runtime-go.delta.tar.gz
    run_logged layer-archive-runtime-go-delta archive_layer "$runtime_go_archive_path" runtime/go runtime/manifest.json
    run_logged layer-measure-runtime-go-delta measure_layer "$runtime_go_archive_path"
    runtime_go_archive_digest=$layer_digest; runtime_go_archive_size=$layer_size
    runtime_go_layer_json=",{\"generation\":$BASELINE_GENERATION,\"kind\":\"delta\",\"name\":\"runtime-go\",\"archive\":\"runtime-go.delta.tar.gz\",\"sha256\":\"$runtime_go_archive_digest\",\"size\":$runtime_go_archive_size}"
  fi
  go_cache_archive_path=$oss_output/go-build-cache.delta.tar.gz
  run_logged layer-archive-go-cache-delta archive_layer "$go_cache_archive_path" cache-seed
  run_logged layer-measure-go-cache-delta measure_layer "$go_cache_archive_path"
  go_cache_archive_digest=$layer_digest; go_cache_archive_size=$layer_size
  cat > "$oss_output/baseline-manifest.json" <<EOF
{"schema_version":$BASELINE_MANIFEST_SCHEMA_VERSION,"generation":$BASELINE_GENERATION,"main_commit":"$BASELINE_MAIN_COMMIT","main_tree":"$BASELINE_MAIN_TREE","platform":"$BASELINE_PLATFORM","policy_digest":"$BASELINE_POLICY_DIGEST","toolchain_digest":"$BASELINE_TOOLCHAIN_DIGEST","runtime_image":"$BASELINE_RUNTIME_IMAGE","gate_source_sha256":"$BASELINE_GATE_SOURCE_SHA256","gate_binary_sha256":"$gate_digest","gate_binary_size":$gate_size,"runtime_seed_manifest_sha256":"$runtime_manifest_digest","ca_bundle_sha256":"$ca_bundle_digest","ca_bundle_size":$ca_bundle_size,"storage_mode":"delta","layers":[{"generation":$BASELINE_GENERATION,"kind":"delta","name":"source","archive":"source.delta.bundle","sha256":"$source_archive_digest","size":$source_archive_size,"base_commit":"$BASELINE_SOURCE_BASE_COMMIT","base_tree":"$BASELINE_SOURCE_BASE_TREE","target_commit":"$BASELINE_MAIN_COMMIT","target_tree":"$BASELINE_MAIN_TREE"}$runtime_go_layer_json,{"generation":$BASELINE_GENERATION,"kind":"delta","name":"go-build-cache","archive":"go-build-cache.delta.tar.gz","sha256":"$go_cache_archive_digest","size":$go_cache_archive_size}]}
EOF
else
  runtime_archive_path=$oss_output/runtime-deps.tar.gz
  run_logged layer-archive-runtime archive_layer "$runtime_archive_path" runtime
  run_logged layer-measure-runtime measure_layer "$runtime_archive_path"
  runtime_archive_digest=$layer_digest; runtime_archive_size=$layer_size
  source_archive_path=$oss_output/source.tar.gz
  run_logged layer-archive-source archive_layer "$source_archive_path" source frontend-embed
  run_logged layer-measure-source measure_layer "$source_archive_path"
  source_archive_digest=$layer_digest; source_archive_size=$layer_size
  go_cache_archive_path=$oss_output/go-build-cache.tar.gz
  run_logged layer-archive-go-cache archive_layer "$go_cache_archive_path" cache-seed
  run_logged layer-measure-go-cache measure_layer "$go_cache_archive_path"
  go_cache_archive_digest=$layer_digest; go_cache_archive_size=$layer_size
  cat > "$oss_output/baseline-manifest.json" <<EOF
{"schema_version":$BASELINE_MANIFEST_SCHEMA_VERSION,"generation":$BASELINE_GENERATION,"main_commit":"$BASELINE_MAIN_COMMIT","main_tree":"$BASELINE_MAIN_TREE","platform":"$BASELINE_PLATFORM","policy_digest":"$BASELINE_POLICY_DIGEST","toolchain_digest":"$BASELINE_TOOLCHAIN_DIGEST","runtime_image":"$BASELINE_RUNTIME_IMAGE","gate_source_sha256":"$BASELINE_GATE_SOURCE_SHA256","gate_binary_sha256":"$gate_digest","gate_binary_size":$gate_size,"runtime_seed_manifest_sha256":"$runtime_manifest_digest","ca_bundle_sha256":"$ca_bundle_digest","ca_bundle_size":$ca_bundle_size,"storage_mode":"anchor","layers":[{"generation":$BASELINE_GENERATION,"kind":"anchor","name":"runtime-deps","archive":"runtime-deps.tar.gz","sha256":"$runtime_archive_digest","size":$runtime_archive_size},{"generation":$BASELINE_GENERATION,"kind":"anchor","name":"source","archive":"source.tar.gz","sha256":"$source_archive_digest","size":$source_archive_size},{"generation":$BASELINE_GENERATION,"kind":"anchor","name":"go-build-cache","archive":"go-build-cache.tar.gz","sha256":"$go_cache_archive_digest","size":$go_cache_archive_size}]}
EOF
fi

printf 'SUPER_DOLPHIN_BASELINE_READY generation=%s commit=%s tree=%s\n' \
  "$BASELINE_GENERATION" "$BASELINE_MAIN_COMMIT" "$BASELINE_MAIN_TREE"
`
