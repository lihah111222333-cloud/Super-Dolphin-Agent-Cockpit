package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageLinuxScriptBundlesVerifiedCodexArtifact(t *testing.T) {
	script := readScript(t, "package_linux.sh")

	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_ARTIFACT")
	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_SHA256")
	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_VERSION")
	assertScriptContains(t, script, "SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX:-1")
	assertScriptContains(t, script, "packaged Codex CLI artifact is required")
	assertScriptContains(t, script, "Codex CLI artifact checksum mismatch")
	assertScriptContains(t, script, "copy_packaged_codex \"$stage\" \"$stage/bin/codex\"")
	assertScriptContains(t, script, "write_codex_manifest \"$stage\"")
	assertScriptDoesNotContain(t, script, "command -v codex")
	assertScriptDoesNotContain(t, script, "/Applications/Codex.app")
	assertScriptOrder(t, script, "resolve_packaged_codex_artifact", "mkdir -p \"$stage/bin\"")
	assertScriptOrder(t, script, "copy_packaged_codex \"$stage\" \"$stage/bin/codex\"", "tar -C \"$dist\"")
}

func TestPackageLinuxScriptBundlesVerifiedLSPBundle(t *testing.T) {
	script := readScript(t, "package_linux.sh")
	body := functionBody(t, script, "write_runtime_manifest")

	assertScriptContains(t, script, "SUPER_DOLPHIN_LSP_PROFILE:-standard")
	assertScriptContains(t, script, "unsupported SUPER_DOLPHIN_LSP_PROFILE")
	assertScriptContains(t, script, "SUPER_DOLPHIN_LSP_BUNDLE_DIR")
	assertScriptContains(t, script, "lsp-manifest.json")
	assertScriptContains(t, script, "lsp-checksums.sha256")
	assertScriptContains(t, script, "packaged LSP bundle checksum mismatch")
	assertScriptContains(t, script, "go|bin/go")
	assertScriptContains(t, script, "packaged LSP bundle missing Go toolchain executable")
	for _, want := range []string{
		"gopls",
		"typescript-language-server",
		"vscode-langservers-extracted",
		"vscode-css-language-server",
		"pyright",
		"pyright-langserver",
		"rust-analyzer",
		"sg",
		"bin/sg",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptContains(t, script, "if [[ \"$lsp_profile\" == \"full\" ]]; then")
	assertScriptContains(t, script, "lsp_server_specs+=(\"jdtls|bin/jdtls\")")
	assertScriptContains(t, script, "resolve_packaged_lsp_bundle")
	assertScriptContains(t, script, "copy_packaged_lsp_bundle \"$stage\"")
	assertScriptContains(t, script, "write_lsp_manifest \"$stage\"")
	assertScriptContains(t, script, "rust-analyzer, sg, and jdtls only for full profile")
	assertScriptContains(t, body, "\"lsp_bundle_path\": \"lsp\"")
	assertScriptContains(t, body, "\"lsp_manifest_path\": \"lsp/lsp-manifest.json\"")
	assertScriptOrder(t, script, "resolve_packaged_lsp_bundle", "mkdir -p \"$stage/bin\"")
	assertScriptOrder(t, script, "copy_packaged_lsp_bundle \"$stage\"", "write_lsp_manifest \"$stage\"")
	assertScriptOrder(t, script, "write_lsp_manifest \"$stage\"", "write_runtime_manifest \"$stage\"")
}

func TestPackageLinuxLSPBundleRequiresNoSystemPythonShadowStubs(t *testing.T) {
	script := readScript(t, "package_linux.sh")

	assertScriptContains(t, script, "lsp_shadow_execs=(python python3)")
	assertScriptContains(t, script, "packaged LSP bundle missing Python shadow executable")
	assertScriptContains(t, script, "\"$dest_root/bin/$shadow_exec\"")
}

func TestPackageLinuxScriptRecordsStagedCodexDigest(t *testing.T) {
	script := readScript(t, "package_linux.sh")
	body := functionBody(t, script, "write_codex_manifest")

	assertScriptContains(t, body, "source_sha256")
	assertScriptContains(t, body, "package_sha256")
	assertScriptContains(t, body, "sha256_file \"$bundle_root/bin/codex\"")
}

func TestPackageLinuxRunScriptPrefersBundledCodexBin(t *testing.T) {
	script := readScript(t, "package_linux.sh")

	assertScriptContains(t, script, "export PATH=\"$here/bin:${PATH:-}\"")
	assertScriptContains(t, script, "export GO_AGENT_PEER_BIN_DIR=\"$here/bin\"")
	assertScriptDoesNotContain(t, script, "GO_AGENT_PEER_BIN_DIR:+")
	assertScriptContains(t, script, "export SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX=1")
	assertScriptContains(t, script, "bundled_execs=(mcp-orch mcp-lsp mcp-ida gopls go typescript-language-server vscode-css-language-server pyright-langserver rust-analyzer sg)")
	assertScriptContains(t, script, "if grep -q '\"jdtls\"' \"$SUPER_DOLPHIN_LSP_MANIFEST\"; then")
	assertScriptContains(t, script, "bundled_execs+=(jdtls)")
	assertScriptContains(t, script, "missing bundled executable: $here/bin/$bundled_exec")
	assertScriptDoesNotContain(t, script, "gopls check")
	assertScriptOrder(t, script, "bundled_execs=(mcp-orch mcp-lsp mcp-ida gopls go typescript-language-server vscode-css-language-server pyright-langserver rust-analyzer sg)", "exec \"$here/bin/agent-terminal\"")
}

func TestPackageLinuxRunScriptExportsLSPManifest(t *testing.T) {
	script := readScript(t, "package_linux.sh")

	assertScriptContains(t, script, "export SUPER_DOLPHIN_LSP_BUNDLE_DIR=\"$here/lsp\"")
	assertScriptContains(t, script, "export SUPER_DOLPHIN_LSP_MANIFEST=\"$here/lsp/lsp-manifest.json\"")
	assertScriptOrder(t, script, "export SUPER_DOLPHIN_LSP_BUNDLE_DIR=\"$here/lsp\"", "export SUPER_DOLPHIN_LSP_MANIFEST=\"$here/lsp/lsp-manifest.json\"")
	assertScriptOrder(t, script, "export SUPER_DOLPHIN_LSP_MANIFEST=\"$here/lsp/lsp-manifest.json\"", "for bundled_exec in")
}

func TestPackageLinuxScriptBundlesModelRegistry(t *testing.T) {
	script := readScript(t, "package_linux.sh")

	assertScriptContains(t, script, "copy_model_registry()")
	assertScriptContains(t, script, "local src=\"$root/cmd/mcp-orch/tools/modelregistry/models.yaml\"")
	assertScriptContains(t, script, "missing model registry: $src")
	assertScriptContains(t, script, "cp -f \"$src\" \"$stage/models.yaml\"")
	assertScriptContains(t, script, "copy_model_registry \"$stage\"")
	assertScriptContains(t, script, "export SUPER_DOLPHIN_MODEL_REGISTRY=\"$here/models.yaml\"")
	assertScriptOrder(t, script, "copy_model_registry \"$stage\"", "cat > \"$stage/run.sh\"")
	assertScriptOrder(t, script, "export SUPER_DOLPHIN_MODEL_REGISTRY=\"$here/models.yaml\"", "exec \"$here/bin/agent-terminal\"")
}

func TestPackageLinuxScriptEmbedsNewFrontendApp(t *testing.T) {
	script := readScript(t, "package_linux.sh")

	assertScriptContains(t, script, "build_current_frontend_app")
	assertScriptContains(t, script, "cd \"$root/frontend-app\"")
	assertScriptContains(t, script, "rsync -a --delete \"$root/frontend-app/dist\"/ \"$root/cmd/agent-terminal/frontend/dist\"/")
	assertScriptContains(t, script, "go build -o bin/agent-terminal ./cmd/agent-terminal")
	assertScriptDoesNotContain(t, script, "cd \"$root/cmd/agent-terminal/frontend\"")
	assertScriptDoesNotContain(t, script, "make build-agent-terminal-plain")
	assertScriptOrder(t, script, "npm run build", "rsync -a --delete \"$root/frontend-app/dist\"/ \"$root/cmd/agent-terminal/frontend/dist\"/")
	assertScriptOrder(t, script, "rsync -a --delete \"$root/frontend-app/dist\"/ \"$root/cmd/agent-terminal/frontend/dist\"/", "go build -o bin/agent-terminal ./cmd/agent-terminal")
}

func TestPackageLinuxScriptRequiresFrontendAppDistWhenSkippingBuild(t *testing.T) {
	script := readScript(t, "package_linux.sh")

	assertScriptContains(t, script, "SUPER_DOLPHIN_SKIP_FRONTEND_BUILD")
	assertScriptContains(t, script, "frontend dist missing; unset SUPER_DOLPHIN_SKIP_FRONTEND_BUILD or run npm run build first")
	assertScriptContains(t, script, "[[ ! -f \"$root/frontend-app/dist/index.html\" ]]")
	assertScriptOrder(t, script, "[[ ! -f \"$root/frontend-app/dist/index.html\" ]]", "rsync -a --delete \"$root/frontend-app/dist\"/ \"$root/cmd/agent-terminal/frontend/dist\"/")
}

func TestPackageLinuxScriptWritesRuntimeManifestContract(t *testing.T) {
	script := readScript(t, "package_linux.sh")
	assertScriptContains(t, script, "write_runtime_manifest \"$stage\"")
	assertScriptOrder(t, script, "write_runtime_manifest \"$stage\"", "tar -C \"$dist\"")

	scriptPath, err := filepath.Abs("package_linux.sh")
	if err != nil {
		t.Fatalf("Abs(package_linux.sh) error = %v", err)
	}
	stage := t.TempDir()
	harness := `#!/usr/bin/env bash
set -euo pipefail
source ` + bashQuote(bashArg("", scriptPath)) + `
platform=linux-amd64
write_runtime_manifest ` + bashQuote(bashArg("", stage)) + `
`
	harnessPath := filepath.Join(t.TempDir(), "package-linux-runtime-manifest-test.sh")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o700); err != nil {
		t.Fatalf("write runtime manifest harness: %v", err)
	}
	cmd := exec.Command("bash", bashArg("", harnessPath))
	cmd.Env = packageScriptValidationEnv(t, "linux", nil)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("write_runtime_manifest failed: %v; output=%s", err, out)
	}

	raw, err := os.ReadFile(filepath.Join(stage, "runtime-manifest.json"))
	if err != nil {
		t.Fatalf("read runtime-manifest.json: %v", err)
	}
	var manifest map[string]string
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("runtime-manifest.json is not JSON: %v\n%s", err, raw)
	}
	wants := map[string]string{
		"bundled_codex_path":              "bin/codex",
		"bundled_gopls_path":              "bin/gopls",
		"lsp_bundle_path":                 "lsp",
		"lsp_manifest_path":               "lsp/lsp-manifest.json",
		"model_registry_path":             "models.yaml",
		"embedded_postgres_resource_path": "postgres/linux-amd64",
	}
	for key, want := range wants {
		got := manifest[key]
		if got != want {
			t.Fatalf("runtime manifest %s = %q, want %q", key, got, want)
		}
		if filepath.IsAbs(got) {
			t.Fatalf("runtime manifest %s = %q, want relocatable relative path", key, got)
		}
	}
}

func TestPackageLinuxCopyModelRegistryFailsFastWhenSourceMissing(t *testing.T) {
	scriptPath, err := filepath.Abs("package_linux.sh")
	if err != nil {
		t.Fatalf("Abs(package_linux.sh) error = %v", err)
	}
	root := t.TempDir()
	stage := t.TempDir()
	harness := `#!/usr/bin/env bash
set -euo pipefail
source ` + bashQuote(bashArg("", scriptPath)) + `
root=` + bashQuote(bashArg("", root)) + `
copy_model_registry ` + bashQuote(bashArg("", stage)) + `
`
	harnessPath := filepath.Join(t.TempDir(), "package-linux-copy-test.sh")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o700); err != nil {
		t.Fatalf("write copy model registry harness: %v", err)
	}
	cmd := exec.Command("bash", bashArg("", harnessPath))
	cmd.Env = packageScriptValidationEnv(t, "linux", nil)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("copy_model_registry succeeded, want missing registry failure; output=%s", out)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("copy_model_registry error = %T %[1]v, want exit error; output=%s", err, out)
	}
	if exitErr.ExitCode() == 0 {
		t.Fatalf("copy_model_registry exit code = 0, want non-zero; output=%s", out)
	}
	want := "missing model registry: " + bashArg("", filepath.Join(root, "cmd/mcp-orch/tools/modelregistry/models.yaml"))
	if got := strings.TrimSpace(string(out)); got != want {
		t.Fatalf("copy_model_registry output = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(stage, "models.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged models.yaml stat error = %v, want not exist", err)
	}
}

func TestPackageLinuxVerifierScriptContracts(t *testing.T) {
	script := readScript(t, "verify_packaged_app_linux.sh")
	for _, want := range []string{
		"runtime-manifest.json",
		"codex-manifest.json",
		"lsp/lsp-manifest.json",
		"verify_runtime_manifest",
		"verify_codex_manifest",
		"verify_lsp_manifest",
		"broken symlinks",
		"package root contains escaped symlink",
		"postgres.bki",
		"sha256_file",
		"tar -xzf",
	} {
		assertScriptContains(t, script, want)
	}
}

func TestVerifyPackagedAppLinuxRequiresRuntimeManifest(t *testing.T) {
	stage := writeMinimalPackagedLinuxStage(t)
	if err := os.Remove(filepath.Join(stage, "runtime-manifest.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove runtime manifest: %v", err)
	}
	output, err := runVerifyPackagedAppLinux(t, stage)
	if err == nil {
		t.Fatalf("expected Linux verifier to reject missing runtime manifest, got success:\n%s", output)
	}
	if !strings.Contains(output, "missing runtime manifest") {
		t.Fatalf("expected missing runtime manifest error, got:\n%s", output)
	}
}

func TestVerifyPackagedAppLinuxAcceptsStageAndRejectsLSPDigestMismatch(t *testing.T) {
	stage := writeMinimalPackagedLinuxStage(t)
	writeRuntimeManifest(t, stage, map[string]string{
		"bundled_codex_path":              "bin/codex",
		"bundled_gopls_path":              "bin/gopls",
		"lsp_bundle_path":                 "lsp",
		"lsp_manifest_path":               "lsp/lsp-manifest.json",
		"model_registry_path":             "models.yaml",
		"embedded_postgres_resource_path": "postgres/linux-amd64",
	})
	output, err := runVerifyPackagedAppLinux(t, stage)
	if err != nil {
		t.Fatalf("expected Linux verifier to accept complete stage, got %v:\n%s", err, output)
	}

	writeFile(t, filepath.Join(stage, "lsp", "bin", "rust-analyzer"), "#!/bin/sh\nexit 9\n", 0o755)
	output, err = runVerifyPackagedAppLinux(t, stage)
	if err == nil {
		t.Fatalf("expected Linux verifier to reject LSP digest mismatch, got success:\n%s", output)
	}
	if !strings.Contains(output, "LSP packaged digest mismatch") {
		t.Fatalf("expected LSP digest mismatch, got:\n%s", output)
	}
}
