package runtimeenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	if !got.Capabilities.EmbeddedPostgres || !got.Capabilities.BundledLSP || !got.Capabilities.BundledCodex {
		t.Fatalf("ResolveRuntime() capabilities = %#v, want packaged capabilities", got.Capabilities)
	}
}

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

func TestResolveRuntimeRepoRootManifestDoesNotTriggerDevExecutablePackaged(t *testing.T) {
	repo := t.TempDir()
	writePackagedRuntimeFixture(t, repo, "linux-amd64")

	got, err := ResolveRuntime(RuntimeResolveInput{
		GOOS:           "linux",
		GOARCH:         "amd64",
		Env:            map[string]string{processRoleEnv: string(ProcessRoleOwner), projectRootEnv: repo},
		ExecutablePath: filepath.Join(repo, "bin", "agent-terminal"),
		UserHome:       "/home/alice",
	})
	if err != nil {
		t.Fatalf("ResolveRuntime() error = %v, want dev unaffected by repo manifest", err)
	}
	if got.RuntimeMode != RuntimeModeDev {
		t.Fatalf("ResolveRuntime() mode = %q, want dev", got.RuntimeMode)
	}
}

func TestResolveRuntimeAmbientDevCannotOverrideExplicitPackagedRoot(t *testing.T) {
	root := t.TempDir()

	_, err := ResolveRuntime(RuntimeResolveInput{
		GOOS:   "linux",
		GOARCH: "amd64",
		Env: map[string]string{
			processRoleEnv:      string(ProcessRoleOwner),
			runtimeModeEnv:      string(RuntimeModeDev),
			packageRootEnv:      root,
			packagedLauncherEnv: "1",
		},
		UserHome: "/home/alice",
	})
	if err == nil {
		t.Fatal("ResolveRuntime() error = nil, want packaged manifest failure despite ambient dev")
	}
	if !strings.Contains(err.Error(), "runtime manifest") {
		t.Fatalf("ResolveRuntime() error = %v, want runtime manifest failure", err)
	}
}

func TestResolveRuntimeSidecarRequiresParentContract(t *testing.T) {
	_, err := ResolveRuntime(RuntimeResolveInput{
		GOOS:   "linux",
		GOARCH: "amd64",
		Env:    map[string]string{processRoleEnv: string(ProcessRoleSidecar)},
	})
	if err == nil {
		t.Fatal("ResolveRuntime() error = nil, want parent launch contract failure")
	}
	if !strings.Contains(err.Error(), "parent launch contract") {
		t.Fatalf("ResolveRuntime() error = %v, want parent launch contract failure", err)
	}
}

func TestResolveRuntimeSidecarConsumesParentContractWithoutAutoDetect(t *testing.T) {
	repo := t.TempDir()
	writePackagedRuntimeFixture(t, repo, "linux-amd64")

	got, err := ResolveRuntime(RuntimeResolveInput{
		GOOS:   "linux",
		GOARCH: "amd64",
		Env: map[string]string{
			processRoleEnv: string(ProcessRoleSidecar),
			runtimeModeEnv: string(RuntimeModeDev),
			projectRootEnv: repo,
		},
	})
	if err != nil {
		t.Fatalf("ResolveRuntime() error = %v", err)
	}
	if got.ProcessRole != ProcessRoleSidecar || got.RuntimeMode != RuntimeModeDev {
		t.Fatalf("ResolveRuntime() = role %q mode %q, want sidecar/dev", got.ProcessRole, got.RuntimeMode)
	}
	if got.PackagedRuntime != nil {
		t.Fatalf("ResolveRuntime() packaged runtime = %#v, want no auto-detected package", got.PackagedRuntime)
	}
}

func TestResolveRuntimeManifestRejectsEscapingSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writePackagedRuntimeFixture(t, root, "linux-amd64")
	if err := os.RemoveAll(filepath.Join(root, "postgres")); err != nil {
		t.Fatalf("remove postgres fixture: %v", err)
	}
	makeDirs(t, filepath.Join(outside, "linux-amd64"))
	if err := os.Symlink(outside, filepath.Join(root, "postgres")); err != nil {
		t.Fatalf("symlink escaped postgres: %v", err)
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

func writePackagedRuntimeFixture(t *testing.T, resources, platform string) {
	t.Helper()
	writeBundledSidecars(t, filepath.Join(resources, "bin"))
	writeExecutable(t, filepath.Join(resources, "bin"), "codex")
	writeExecutable(t, filepath.Join(resources, "bin"), "gopls")
	makeDirs(t, filepath.Join(resources, "lsp"))
	if err := os.WriteFile(filepath.Join(resources, "models.yaml"), []byte("models: []\n"), 0o644); err != nil {
		t.Fatalf("write models.yaml: %v", err)
	}
	makeDirs(t, filepath.Join(resources, "postgres", platform, "bin"), filepath.Join(resources, "postgres", platform, "share"))
	writeRuntimeManifestFixture(t, resources, platform)
}

func writeWindowsPackagedRuntimeFixture(t *testing.T, resources, platform string) {
	t.Helper()
	writeExecutable(t, filepath.Join(resources, "bin"), "mcp-orch.exe")
	writeExecutable(t, filepath.Join(resources, "bin"), "mcp-lsp.exe")
	writeExecutable(t, filepath.Join(resources, "bin"), "mcp-ida.exe")
	writeExecutable(t, filepath.Join(resources, "bin"), "codex.exe")
	writeExecutable(t, filepath.Join(resources, "bin"), "gopls.exe")
	makeDirs(t, filepath.Join(resources, "lsp"))
	if err := os.WriteFile(filepath.Join(resources, "lsp", "lsp-manifest.json"), []byte(`{"servers":{}}`+"\n"), 0o644); err != nil {
		t.Fatalf("write lsp manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(resources, "models.yaml"), []byte("models: []\n"), 0o644); err != nil {
		t.Fatalf("write models.yaml: %v", err)
	}
	makeDirs(t, filepath.Join(resources, "postgres", platform, "bin"), filepath.Join(resources, "postgres", platform, "share"))
	manifest := `{
  "bundled_codex_path": "bin/codex.exe",
  "bundled_gopls_path": "bin/gopls.exe",
  "lsp_bundle_path": "lsp",
  "lsp_manifest_path": "lsp/lsp-manifest.json",
  "model_registry_path": "models.yaml",
  "embedded_postgres_resource_path": "postgres/` + platform + `"
}
`
	if err := os.WriteFile(filepath.Join(resources, "runtime-manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write runtime manifest: %v", err)
	}
}

func writeRuntimeManifestFixture(t *testing.T, resources, platform string) {
	t.Helper()
	manifest := `{
  "bundled_codex_path": "bin/codex",
  "bundled_gopls_path": "bin/gopls",
  "lsp_bundle_path": "lsp",
  "lsp_manifest_path": "lsp/lsp-manifest.json",
  "model_registry_path": "models.yaml",
  "embedded_postgres_resource_path": "postgres/` + platform + `"
}
`
	if err := os.WriteFile(filepath.Join(resources, "runtime-manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write runtime manifest: %v", err)
	}
}
