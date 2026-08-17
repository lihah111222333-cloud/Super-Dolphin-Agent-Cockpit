package runtimeenv

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// 本文件其余 ResolveRuntime 测试显式传入 GOOS/GOARCH，验证跨平台 dispatcher；不依赖宿主平台，故故意保留在公共测试文件。
// 依赖 Unix 文件权限、符号链接权限或 Windows 路径规则的测试位于带精确 build tag 的平台测试文件。

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

func assertPackagedCapabilitiesWithoutPostgres(t *testing.T, got RuntimeCapabilities) {
	t.Helper()
	if !got.BundledLSP || !got.BundledCodex || !got.BundledSidecars {
		t.Fatalf("ResolveRuntime() capabilities = %#v, want packaged capabilities", got)
	}
	if _, ok := reflect.TypeOf(got).FieldByName("EmbeddedPostgres"); ok {
		t.Fatalf("ResolveRuntime() capabilities = %#v, must not advertise embedded Postgres", got)
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
	writeRuntimeManifestFixture(t, resources, platform)
}

func writeWindowsPackagedRuntimeFixture(t *testing.T, resources, platform string) {
	t.Helper()
	_ = platform
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
	manifest := `{
  "bundled_codex_path": "bin/codex.exe",
  "bundled_gopls_path": "bin/gopls.exe",
  "lsp_bundle_path": "lsp",
  "lsp_manifest_path": "lsp/lsp-manifest.json",
  "model_registry_path": "models.yaml"
}
`
	if err := os.WriteFile(filepath.Join(resources, "runtime-manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write runtime manifest: %v", err)
	}
}

func writeRuntimeManifestFixture(t *testing.T, resources, platform string) {
	t.Helper()
	goos, _, _ := strings.Cut(platform, "-")
	manifest := `{
  "bundled_codex_path": "bin/` + executableNameForOS(goos, "codex") + `",
  "bundled_gopls_path": "bin/` + executableNameForOS(goos, "gopls") + `",
  "lsp_bundle_path": "lsp",
  "lsp_manifest_path": "lsp/lsp-manifest.json",
  "model_registry_path": "models.yaml"
}
`
	if err := os.WriteFile(filepath.Join(resources, "runtime-manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write runtime manifest: %v", err)
	}
}
