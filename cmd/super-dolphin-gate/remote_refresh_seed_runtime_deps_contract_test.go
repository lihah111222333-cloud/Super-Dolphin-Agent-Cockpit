package main

import (
	"strings"
	"testing"
)

func TestRemoteBaselineSeedRuntimeDependencyIdentityChainContract(t *testing.T) {
	for _, fragment := range []string{
		`LC_ALL=C grep -Eq '^sha256:[0-9a-f]{64}$'`,
		`if test -n "$expected_runtime_dependency_digest"; then`,
		`manifest_runtime_dependency_digest" != "$expected_runtime_dependency_digest`,
		`manifest_runtime_seed_manifest_sha256" = "$expected_runtime_seed_manifest_sha256`,
		`runtime dependency identity projection advanced without content change`,
		`require_sha256_digest "$manifest_runtime_dependency_digest"`,
		`require_sha256_digest "$runtime_deps_base_digest"`,
		`require_sha256_digest "$runtime_deps_target_digest"`,
		`tarfile.open(archive_path, "r|gz")`,
		`os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW`,
		`components[:-1].index("rootfs")`,
		`relative = posixpath.relpath(target, posixpath.dirname(name))`,
		`runtime dependency delta contains an invalid hard link`,
		`link not in regular_files`,
		`os.link(staged_path(link), target, follow_symlinks=False)`,
		`runtime dependency delta contains entries below a symbolic link`,
		`os.symlink(relative, target)`,
	} {
		if !strings.Contains(remoteBaselineSeedScript, fragment) {
			t.Fatalf("runtime dependency identity-chain contract is missing %q", fragment)
		}
	}
	if strings.Contains(remoteBaselineSeedScript, `sha256:[0-9a-f][0-9a-f]*`) {
		t.Fatal("runtime dependency identity validation must not use a variable-width shell glob")
	}
	for _, forbidden := range []string{`tar -xzf "$runtime_deps_delta"`, `os.walk(runtime_root, followlinks=False)`} {
		if strings.Contains(remoteBaselineSeedScriptRuntimeDepsReplay, forbidden) {
			t.Fatalf("runtime dependency replay still uses a second extraction pass %q", forbidden)
		}
	}
	if strings.Contains(remoteBaselineSeedScriptDirectCachePublish, `sh '{}'`) {
		t.Fatal("direct-cache publish must receive xargs inputs after the two fixed shell arguments")
	}
	for _, fragment := range []string{`sh "$go_build_cache" "$direct_cache_root"`, `shift 2`, `for source_path do`} {
		if !strings.Contains(remoteBaselineSeedScriptDirectCachePublish, fragment) {
			t.Fatalf("direct-cache publish argument binding is missing %q", fragment)
		}
	}
}
