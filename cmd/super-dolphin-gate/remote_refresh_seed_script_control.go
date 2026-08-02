package main

const remoteBaselineSeedScriptDirectCachePublishLegacy = `publish_direct_cache() {
  publish_parallelism=$((BASELINE_SEED_GO_PARALLELISM * 8))
  find "$go_build_cache" -type f -print0 | xargs -0 -r -P "$publish_parallelism" -n 1 sh -c '
    source_path=$1
    source_root=$2
    target_root=$3
    relative=${source_path#"$source_root"/}
    install -D -m 0444 "$source_path" "$target_root/$relative"
  ' sh '{}' "$go_build_cache" "$direct_cache_root"
  find "$direct_cache_root" -type d -exec chmod 0555 '{}' +
}`

const remoteBaselineSeedScriptDirectCachePublish = `publish_direct_cache() {
  cp -a "$go_build_cache/." "$direct_cache_root/"
  find "$direct_cache_root" -type f -exec chmod 0444 '{}' +
  find "$direct_cache_root" -type d -exec chmod 0555 '{}' +
}`

// remoteBaselineSeedScriptGateHelpers 验证重放层中的 gate 二进制及候选身份。
const remoteBaselineSeedScriptGateHelpers = `
previous_gate_source_sha256=
previous_gate_platform=
previous_gate_toolchain_digest=
require_sha256_digest() {
  printf '%s\n' "$1" | LC_ALL=C grep -Eq '^sha256:[0-9a-f]{64}$'
}
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
        if test -z "$expected_runtime_dependency_digest"; then
          test -z "$manifest_runtime_dependency_digest"
        else
          require_sha256_digest "$manifest_runtime_dependency_digest"
          test "$manifest_runtime_dependency_digest" = "$expected_runtime_dependency_digest"
        fi
        ;; 1)
        runtime_deps_delta=$layer_root/runtime-deps.delta.tar.gz
        runtime_deps_digest=$(sed -n 's/.*"name":"runtime-deps","archive":"runtime-deps.delta.tar.gz","sha256":"\([^"]*\)".*/\1/p' "$manifest")
        runtime_deps_size=$(sed -n 's/.*"name":"runtime-deps","archive":"runtime-deps.delta.tar.gz","sha256":"[^"]*","size":\([0-9][0-9]*\).*/\1/p' "$manifest")
        runtime_deps_base_digest=$(sed -n 's/.*"name":"runtime-deps".*"base_runtime_dependency_digest":"\([^"]*\)".*/\1/p' "$manifest")
        runtime_deps_target_digest=$(sed -n 's/.*"name":"runtime-deps".*"target_runtime_dependency_digest":"\([^"]*\)".*/\1/p' "$manifest")
        require_sha256_digest "$runtime_deps_digest"; test -n "$runtime_deps_size"; test -f "$runtime_deps_delta"
        require_sha256_digest "$manifest_runtime_dependency_digest"
        require_sha256_digest "$runtime_deps_target_digest"
        if test -z "$expected_runtime_dependency_digest"; then
          require_sha256_digest "$runtime_deps_base_digest"
        else
          require_sha256_digest "$runtime_deps_base_digest"
          test "$runtime_deps_base_digest" = "$expected_runtime_dependency_digest"
        fi
        test "$runtime_deps_target_digest" = "$manifest_runtime_dependency_digest"
        test "$(digest_file "$runtime_deps_delta")" = "$runtime_deps_digest"
        test "$(stat -c '%s' "$runtime_deps_delta")" = "$runtime_deps_size"
        runtime_deps_stage=$stage/runtime-deps-$generation
        test ! -e "$runtime_deps_stage"
        mkdir -m 0700 "$runtime_deps_stage"
        "$payload_root/runtime/python/bin/python3" - "$runtime_deps_delta" "$runtime_deps_stage" <<'PY'
import json
import os
import posixpath
import stat
import sys
import tarfile

archive_path = sys.argv[1]
stage = os.path.abspath(sys.argv[2])
if os.path.islink(stage) or not os.path.isdir(stage) or os.listdir(stage):
    raise SystemExit("runtime dependency staging root is invalid")

seen = set()
symlinks = set()
regular_files = set()
directories = set()
directory_modes = {}
manifest_bytes = None
expanded = 0

def within_runtime(name):
    return name == "runtime" or name.startswith("runtime/")

def archive_name(raw):
    name = raw.rstrip("/")
    clean = posixpath.normpath(name)
    if not name or name.startswith("/") or clean != name or clean in (".", "..") or clean.startswith("../"):
        raise SystemExit("runtime dependency delta contains an unsafe path")
    if not within_runtime(clean):
        raise SystemExit("runtime dependency delta contains a forbidden path")
    return clean

def staged_path(name):
    return os.path.join(stage, *name.split("/"))

def require_directory_parent(name):
    parent = posixpath.dirname(name)
    if parent in ("", "."):
        return
    for link in symlinks:
        if parent == link or parent.startswith(link + "/"):
            raise SystemExit("runtime dependency delta contains entries below a symbolic link")
    if parent not in directories:
        raise SystemExit("runtime dependency delta parent directory is missing: " + parent)
    info = os.lstat(staged_path(parent))
    if not stat.S_ISDIR(info.st_mode) or stat.S_ISLNK(info.st_mode):
        raise SystemExit("runtime dependency delta parent is not a directory")

def relative_symlink_target(name, link):
    if not link:
        raise SystemExit("runtime dependency delta contains an escaping symbolic link")
    if posixpath.isabs(link):
        components = name.split("/")
        try:
            rootfs_index = components[:-1].index("rootfs")
        except ValueError as error:
            raise SystemExit("runtime dependency delta contains an escaping symbolic link") from error
        rootfs = "/".join(components[:rootfs_index + 1])
        target = posixpath.join(rootfs, posixpath.normpath(link).lstrip("/"))
        if target == rootfs:
            raise SystemExit("runtime dependency delta contains an escaping symbolic link")
        relative = posixpath.relpath(target, posixpath.dirname(name))
    else:
        relative = link
    resolved = posixpath.normpath(posixpath.join(posixpath.dirname(name), relative))
    if not within_runtime(resolved):
        raise SystemExit("runtime dependency delta contains an escaping symbolic link")
    return relative

with tarfile.open(archive_path, "r|gz") as archive:
    for member in archive:
        clean = archive_name(member.name)
        if clean in seen:
            raise SystemExit("runtime dependency delta contains a duplicate path")
        require_directory_parent(clean)
        seen.add(clean)
        expanded += member.size
        if member.size < 0 or expanded > 20 << 30:
            raise SystemExit("runtime dependency delta expanded size is too large")
        target = staged_path(clean)
        mode = member.mode & 0o777
        if member.isdir():
            os.mkdir(target, 0o700)
            directories.add(clean)
            directory_modes[clean] = mode
            continue
        if member.issym():
            if member.size != 0:
                raise SystemExit("runtime dependency delta contains an invalid symbolic link")
            if any(path.startswith(clean + "/") for path in seen):
                raise SystemExit("runtime dependency delta contains entries below a symbolic link")
            relative = relative_symlink_target(clean, member.linkname)
            os.symlink(relative, target)
            symlinks.add(clean)
            continue
        if member.islnk():
            link = posixpath.normpath(member.linkname)
            if member.size != 0 or not member.linkname or posixpath.isabs(member.linkname) or not within_runtime(link) or link not in regular_files:
                raise SystemExit("runtime dependency delta contains an invalid hard link")
            os.link(staged_path(link), target, follow_symlinks=False)
            continue
        if not member.isfile():
            raise SystemExit("runtime dependency delta contains an unsupported entry type")
        source = archive.extractfile(member)
        if source is None:
            raise SystemExit("runtime dependency delta file cannot be read")
        flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW
        descriptor = os.open(target, flags, mode)
        captured = bytearray() if clean == "runtime/manifest.json" else None
        written = 0
        with os.fdopen(descriptor, "wb", closefd=True) as output:
            while True:
                block = source.read(1024 * 1024)
                if not block:
                    break
                output.write(block)
                written += len(block)
                if captured is not None:
                    captured.extend(block)
        if written != member.size:
            raise SystemExit("runtime dependency delta file size changed during extraction")
        regular_files.add(clean)
        if captured is not None:
            manifest_bytes = bytes(captured)

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
try:
    manifest = json.loads(manifest_bytes)
except (TypeError, UnicodeDecodeError, json.JSONDecodeError) as error:
    raise SystemExit("runtime dependency manifest is invalid") from error
if not isinstance(manifest, dict) or not required_manifest_fields.issubset(manifest) or any(manifest[field] in (None, "") for field in required_manifest_fields):
    raise SystemExit("runtime dependency delta has an incomplete runtime manifest")
for directory in sorted(directories, key=lambda value: value.count("/"), reverse=True):
    os.chmod(staged_path(directory), directory_modes[directory])
PY
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
