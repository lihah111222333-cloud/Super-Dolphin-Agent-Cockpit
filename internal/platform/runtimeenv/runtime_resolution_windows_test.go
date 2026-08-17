//go:build windows

package runtimeenv

import (
	"path/filepath"
	"testing"
)

// 这些测试依赖 Windows 包根和 .exe 路径规则；build tag 保证平台权限/路径差异显式可见。
func TestResolveRuntimeWindowsPackageRootValidManifestResolvesPackaged(t *testing.T) {
	root := t.TempDir()
	writeWindowsPackagedRuntimeFixture(t, root, "windows-amd64")

	got, err := ResolveRuntime(RuntimeResolveInput{
		GOOS:     "windows",
		GOARCH:   "amd64",
		Env:      map[string]string{processRoleEnv: string(ProcessRoleOwner), packageRootEnv: root},
		UserHome: `C:\Users\alice`,
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

func TestResolveRuntimeWindowsExecutableInPackageBinAutoDetectsPackagedRoot(t *testing.T) {
	root := t.TempDir()
	writeWindowsPackagedRuntimeFixture(t, root, "windows-amd64")

	got, err := ResolveRuntime(RuntimeResolveInput{
		GOOS:           "windows",
		GOARCH:         "amd64",
		Env:            map[string]string{processRoleEnv: string(ProcessRoleOwner)},
		ExecutablePath: filepath.Join(root, "bin", "agent-terminal.exe"),
		UserHome:       `C:\Users\alice`,
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
