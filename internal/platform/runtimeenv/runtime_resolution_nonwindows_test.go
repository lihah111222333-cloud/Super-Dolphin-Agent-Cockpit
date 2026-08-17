//go:build !windows

package runtimeenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 这些测试依赖 Unix 文件权限或符号链接能力；非 Windows build tag 替代了运行时 Skip。
func TestResolveRuntimeExplicitLinuxPackageRootValidManifestResolvesPackaged(t *testing.T) {
	root := t.TempDir()
	writePackagedRuntimeFixture(t, root, "linux-amd64")

	got, err := ResolveRuntime(RuntimeResolveInput{
		GOOS:     "linux",
		GOARCH:   "amd64",
		Env:      map[string]string{processRoleEnv: string(ProcessRoleOwner), packageRootEnv: root},
		UserHome: "/home/alice",
	})
	if err != nil {
		t.Fatalf("ResolveRuntime() error = %v", err)
	}
	if got.RuntimeMode != RuntimeModePackaged {
		t.Fatalf("ResolveRuntime() mode = %q, want packaged", got.RuntimeMode)
	}
	if got.PackagedRuntime == nil || got.PackagedRuntime.ResourcesDir != root {
		t.Fatalf("ResolveRuntime() packaged runtime = %#v, want root %q", got.PackagedRuntime, root)
	}
}

func TestResolveRuntimeManifestRejectsEscapingSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writePackagedRuntimeFixture(t, root, "linux-amd64")
	if err := os.RemoveAll(filepath.Join(root, "lsp")); err != nil {
		t.Fatalf("remove lsp fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "lsp-manifest.json"), []byte(`{"servers":{}}`+"\n"), 0o644); err != nil {
		t.Fatalf("write outside lsp manifest: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "lsp")); err != nil {
		t.Fatalf("symlink escaped lsp bundle: %v", err)
	}

	_, err := ResolveRuntime(RuntimeResolveInput{
		GOOS:     "linux",
		GOARCH:   "amd64",
		Env:      map[string]string{processRoleEnv: string(ProcessRoleOwner), packageRootEnv: root},
		UserHome: "/home/alice",
	})
	if err == nil {
		t.Fatal("ResolveRuntime() error = nil, want symlink escape failure")
	}
	if !strings.Contains(err.Error(), "escapes package root") {
		t.Fatalf("ResolveRuntime() error = %v, want escape failure", err)
	}
}
