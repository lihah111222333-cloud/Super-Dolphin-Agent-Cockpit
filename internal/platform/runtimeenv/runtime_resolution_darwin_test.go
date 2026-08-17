//go:build darwin

package runtimeenv

import (
	"path/filepath"
	"strings"
	"testing"
)

// 这些测试依赖 macOS .app 布局；build tag 保证它们不会靠运行时 Skip 掩盖平台约束。
func TestResolveRuntimeMacOSDebugAppWithoutManifestResolvesOwnerDev(t *testing.T) {
	app := filepath.Join(t.TempDir(), "Super Dolphin.app")
	exe := filepath.Join(app, "Contents", "MacOS", "agent-terminal")

	got, err := ResolveRuntime(RuntimeResolveInput{
		GOOS:           "darwin",
		GOARCH:         "arm64",
		Env:            map[string]string{processRoleEnv: string(ProcessRoleOwner)},
		ExecutablePath: exe,
		UserHome:       "/Users/alice",
	})
	if err != nil {
		t.Fatalf("ResolveRuntime() error = %v, want debug app dev", err)
	}
	if got.ProcessRole != ProcessRoleOwner || got.RuntimeMode != RuntimeModeDev {
		t.Fatalf("ResolveRuntime() = role %q mode %q, want owner/dev", got.ProcessRole, got.RuntimeMode)
	}
	if got.PackagedRuntime != nil {
		t.Fatalf("ResolveRuntime() packaged runtime = %#v, want nil", got.PackagedRuntime)
	}
}

func TestResolveRuntimeMacOSAppWithBundledSidecarsMissingManifestFailsFast(t *testing.T) {
	app := filepath.Join(t.TempDir(), "Super Dolphin.app")
	resources := filepath.Join(app, "Contents", "Resources")
	writeOnlyBundledSidecars(t, filepath.Join(resources, "bin"))

	_, err := ResolveRuntime(RuntimeResolveInput{
		GOOS:           "darwin",
		GOARCH:         "arm64",
		Env:            map[string]string{processRoleEnv: string(ProcessRoleOwner)},
		ExecutablePath: filepath.Join(app, "Contents", "MacOS", "agent-terminal"),
		UserHome:       "/Users/alice",
	})
	if err == nil {
		t.Fatal("ResolveRuntime() error = nil, want bundled app missing manifest failure")
	}
	if !strings.Contains(err.Error(), "runtime manifest") {
		t.Fatalf("ResolveRuntime() error = %v, want runtime manifest failure", err)
	}
}

func TestResolveRuntimeExplicitMacOSPackageRootMissingManifestFailsFast(t *testing.T) {
	app := filepath.Join(t.TempDir(), "Super Dolphin.app")
	exe := filepath.Join(app, "Contents", "MacOS", "agent-terminal")

	_, err := ResolveRuntime(RuntimeResolveInput{
		GOOS:           "darwin",
		GOARCH:         "arm64",
		Env:            map[string]string{processRoleEnv: string(ProcessRoleOwner), packagedLauncherEnv: "1"},
		ExecutablePath: exe,
		UserHome:       "/Users/alice",
	})
	if err == nil {
		t.Fatal("ResolveRuntime() error = nil, want missing manifest failure")
	}
	if !strings.Contains(err.Error(), "runtime manifest") {
		t.Fatalf("ResolveRuntime() error = %v, want runtime manifest failure", err)
	}
}

func TestResolveRuntimeValidMacOSManifestResolvesPackaged(t *testing.T) {
	app := filepath.Join(t.TempDir(), "Super Dolphin.app")
	resources := filepath.Join(app, "Contents", "Resources")
	writePackagedRuntimeFixture(t, resources, "darwin-arm64")

	got, err := ResolveRuntime(RuntimeResolveInput{
		GOOS:           "darwin",
		GOARCH:         "arm64",
		Env:            map[string]string{processRoleEnv: string(ProcessRoleOwner)},
		ExecutablePath: filepath.Join(app, "Contents", "MacOS", "agent-terminal"),
		UserHome:       "/Users/alice",
	})
	if err != nil {
		t.Fatalf("ResolveRuntime() error = %v", err)
	}
	if got.ProcessRole != ProcessRoleOwner || got.RuntimeMode != RuntimeModePackaged {
		t.Fatalf("ResolveRuntime() = role %q mode %q, want owner/packaged", got.ProcessRole, got.RuntimeMode)
	}
	if got.PackagedRuntime == nil || got.PackagedRuntime.ResourcesDir != resources {
		t.Fatalf("ResolveRuntime() packaged runtime = %#v, want resources %q", got.PackagedRuntime, resources)
	}
	assertPackagedCapabilitiesWithoutPostgres(t, got.Capabilities)
}
