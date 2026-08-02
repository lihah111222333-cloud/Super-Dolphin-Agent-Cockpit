package main

import (
	"strings"
	"testing"
)

func TestRemoteBaselineSeedRuntimeDependencyIdentityChainContract(t *testing.T) {
	for _, fragment := range []string{
		`LC_ALL=C grep -Eq '^sha256:[0-9a-f]{64}$'`,
		`if test -n "$expected_runtime_dependency_digest"; then`,
		`if test -z "$expected_runtime_dependency_digest"; then`,
		`test -z "$manifest_runtime_dependency_digest"`,
		`require_sha256_digest "$manifest_runtime_dependency_digest"`,
		`require_sha256_digest "$runtime_deps_base_digest"`,
		`require_sha256_digest "$runtime_deps_target_digest"`,
		`target.startswith("/") and clean.startswith("runtime/rootfs/")`,
		`posixpath.join("runtime/rootfs", target.lstrip("/"))`,
		`runtime dependency delta contains an invalid hard link`,
		`link_target not in regular_files`,
		`os.walk(runtime_root, followlinks=False)`,
		`runtime dependency delta contains an absolute link outside rootfs`,
		`os.symlink(relative_target, link_path)`,
	} {
		if !strings.Contains(remoteBaselineSeedScript, fragment) {
			t.Fatalf("runtime dependency identity-chain contract is missing %q", fragment)
		}
	}
	if strings.Contains(remoteBaselineSeedScript, `sha256:[0-9a-f][0-9a-f]*`) {
		t.Fatal("runtime dependency identity validation must not use a variable-width shell glob")
	}
}
