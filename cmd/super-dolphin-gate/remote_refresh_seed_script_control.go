package main

// remoteBaselineSeedScriptGateHelpers 验证重放层中的 gate 二进制及候选身份。
const remoteBaselineSeedScriptGateHelpers = `
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
`

// remoteBaselineSeedScriptRuntimeDepsReplay 安全重放逐代完整运行时依赖层。
const remoteBaselineSeedScriptRuntimeDepsReplay = `      runtime_deps_count=$(grep -o '"name":"runtime-deps"' "$manifest" | wc -l | tr -d ' ')
      case "$runtime_deps_count" in 0)
        test "$manifest_runtime_dependency_digest" = "$expected_runtime_dependency_digest"
        ;; 1)
        runtime_deps_delta=$layer_root/runtime-deps.delta.tar.gz
        runtime_deps_digest=$(sed -n 's/.*"name":"runtime-deps","archive":"runtime-deps.delta.tar.gz","sha256":"\([^"]*\)".*/\1/p' "$manifest")
        runtime_deps_size=$(sed -n 's/.*"name":"runtime-deps","archive":"runtime-deps.delta.tar.gz","sha256":"[^"]*","size":\([0-9][0-9]*\).*/\1/p' "$manifest")
        runtime_deps_base_digest=$(sed -n 's/.*"name":"runtime-deps".*"base_runtime_dependency_digest":"\([^"]*\)".*/\1/p' "$manifest")
        runtime_deps_target_digest=$(sed -n 's/.*"name":"runtime-deps".*"target_runtime_dependency_digest":"\([^"]*\)".*/\1/p' "$manifest")
        test -n "$runtime_deps_digest"; test -n "$runtime_deps_size"; test -f "$runtime_deps_delta"
        test "$runtime_deps_base_digest" = "$expected_runtime_dependency_digest"
        test "$runtime_deps_target_digest" = "$manifest_runtime_dependency_digest"
        test "$(digest_file "$runtime_deps_delta")" = "$runtime_deps_digest"
        test "$(stat -c '%s' "$runtime_deps_delta")" = "$runtime_deps_size"
        "$payload_root/runtime/python/bin/python3" - "$runtime_deps_delta" <<'PY'
import json
import posixpath
import sys
import tarfile

archive_path = sys.argv[1]
seen = set()
symlinks = set()
directories = set()
manifest = None
expanded = 0
with tarfile.open(archive_path, "r:gz") as archive:
    for member in archive:
        name = member.name.rstrip("/")
        clean = posixpath.normpath(name)
        if not name or name.startswith("/") or clean != name or clean == ".." or clean.startswith("../"):
            raise SystemExit("runtime dependency delta contains an unsafe path")
        if clean in seen:
            raise SystemExit("runtime dependency delta contains a duplicate path")
        seen.add(clean)
        if member.islnk():
            raise SystemExit("runtime dependency delta contains an unsupported entry type")
        if member.issym():
            target = member.linkname
            resolved_target = posixpath.normpath(posixpath.join(posixpath.dirname(clean), target))
            if not target or target.startswith("/") or (resolved_target != "runtime" and not resolved_target.startswith("runtime/")):
                raise SystemExit("runtime dependency delta contains an escaping symbolic link")
            symlinks.add(clean)
        elif not (member.isdir() or member.isfile()):
            raise SystemExit("runtime dependency delta contains an unsupported entry type")
        if clean != "runtime" and not clean.startswith("runtime/"):
            raise SystemExit("runtime dependency delta contains a forbidden path")
        if member.isdir():
            directories.add(clean)
        if clean == "runtime/manifest.json":
            if not member.isfile():
                raise SystemExit("runtime dependency manifest is not a regular file")
            manifest_file = archive.extractfile(member)
            if manifest_file is None:
                raise SystemExit("runtime dependency manifest cannot be read")
            try:
                manifest = json.load(manifest_file)
            except (UnicodeDecodeError, json.JSONDecodeError) as error:
                raise SystemExit("runtime dependency manifest is invalid") from error
        expanded += member.size
        if expanded > 20 << 30:
            raise SystemExit("runtime dependency delta expanded size is too large")
if any(path.startswith(link + "/") for link in symlinks for path in seen):
    raise SystemExit("runtime dependency delta contains entries below a symbolic link")
required_directories = {
    "runtime/bin", "runtime/go", "runtime/python", "runtime/node", "runtime/rootfs",
    "runtime/go-mod-cache", "runtime/go-proxy", "runtime/frontend",
    "runtime/frontend/node_modules", "runtime/frontend/npm-cache",
}
if not required_directories.issubset(directories):
    raise SystemExit("runtime dependency delta is missing required runtime directories")
required_manifest_fields = {
    "schema_version", "go_sum_sha256", "module_proxy_lock_sha256",
    "module_proxy_tree_sha256", "go_mod_cache_tree_sha256", "package_lock_sha256",
    "node_modules_tree_sha256", "npm_cache_tree_sha256", "ripgrep_sha256", "sqruff_sha256",
}
if not isinstance(manifest, dict) or not required_manifest_fields.issubset(manifest) or any(manifest[field] in (None, "") for field in required_manifest_fields):
    raise SystemExit("runtime dependency delta has an incomplete runtime manifest")
PY
        runtime_deps_stage=$stage/runtime-deps-$generation
        mkdir -p "$runtime_deps_stage"
        tar -xzf "$runtime_deps_delta" -C "$runtime_deps_stage"
        test -d "$runtime_deps_stage/runtime"; test -f "$runtime_deps_stage/runtime/manifest.json"
        previous_runtime=$runtime_deps_stage/previous-runtime
        mv "$payload_root/runtime" "$previous_runtime"
        if ! mv "$runtime_deps_stage/runtime" "$payload_root/runtime"; then
          mv "$previous_runtime" "$payload_root/runtime"
          exit 1
        fi
        if ! rm -rf "$go_build_cache" || ! install -d -m 0700 "$go_build_cache"; then
          mv "$payload_root/runtime" "$runtime_deps_stage/runtime"
          mv "$previous_runtime" "$payload_root/runtime"
          exit 1
        fi
        rm -rf "$previous_runtime"
        expected_runtime_dependency_digest=$manifest_runtime_dependency_digest
        ;; *) echo "baseline delta contains duplicate runtime-deps layers" >&2; exit 1;; esac
`
