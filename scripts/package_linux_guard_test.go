package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
)

func TestPackageLinuxRejectsUnsupportedPackageUpdates(t *testing.T) {
	script := readScript(t, "package_linux.sh")
	assertScriptContains(t, script, "reject_unsupported_package_updates")
	assertScriptContains(t, script, "package-owned updates are unsupported for $platform")
	for _, name := range recovery.PackageTrustOverrideNames() {
		assertScriptContains(t, script, name)
	}
}

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
		"bash-language-server",
		"sqruff",
		"shellcheck",
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
	assertScriptContains(t, script, "rust-analyzer, bash-language-server, sqruff, shellcheck, sg, and jdtls only for full profile")
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
	assertScriptContains(t, script, "bundled_execs=(mcp-orch mcp-lsp mcp-schema-compiler-helper mcp-ida gopls go typescript-language-server vscode-css-language-server pyright-langserver rust-analyzer bash-language-server sqruff shellcheck sg)")
	assertScriptContains(t, script, "if grep -q '\"jdtls\"' \"$SUPER_DOLPHIN_LSP_MANIFEST\"; then")
	assertScriptContains(t, script, "bundled_execs+=(jdtls)")
	assertScriptContains(t, script, "missing bundled executable: $here/bin/$bundled_exec")
	assertScriptDoesNotContain(t, script, "gopls check")
	assertScriptOrder(t, script, "bundled_execs=(mcp-orch mcp-lsp mcp-schema-compiler-helper mcp-ida gopls go typescript-language-server vscode-css-language-server pyright-langserver rust-analyzer bash-language-server sqruff shellcheck sg)", "exec \"$here/bin/agent-terminal\"")
}

func TestPackageLinuxRunScriptDeclaresPackagedRuntime(t *testing.T) {
	script := readScript(t, "package_linux.sh")

	for _, want := range []string{
		"export SUPER_DOLPHIN_PACKAGE_ROOT=\"$here\"",
		"export SUPER_DOLPHIN_RUNTIME_MODE=packaged",
		"export SUPER_DOLPHIN_PACKAGED_LAUNCHER=1",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptOrder(t, script, "here=\"$(cd \"$(dirname \"${BASH_SOURCE[0]}\")\" && pwd)\"", "export SUPER_DOLPHIN_PACKAGE_ROOT=\"$here\"")
	assertScriptOrder(t, script, "export SUPER_DOLPHIN_PACKAGE_ROOT=\"$here\"", "export SUPER_DOLPHIN_RUNTIME_MODE=packaged")
	assertScriptOrder(t, script, "export SUPER_DOLPHIN_RUNTIME_MODE=packaged", "export SUPER_DOLPHIN_PACKAGED_LAUNCHER=1")
	assertScriptOrder(t, script, "export SUPER_DOLPHIN_PACKAGED_LAUNCHER=1", "exec \"$here/bin/agent-terminal\"")
}

func TestPackageLinuxRunsVerifierBeforeTarReady(t *testing.T) {
	script := readScript(t, "package_linux.sh")

	assertScriptContains(t, script, "\"$root/scripts/verify_packaged_app_linux.sh\" \"$stage\"")
	assertScriptOrder(t, script, "write_runtime_manifest \"$stage\"", "\"$root/scripts/verify_packaged_app_linux.sh\" \"$stage\"")
	assertScriptOrder(t, script, "\"$root/scripts/verify_packaged_app_linux.sh\" \"$stage\"", "tar -C \"$dist\"")
	assertScriptOrder(t, script, "\"$root/scripts/verify_packaged_app_linux.sh\" \"$stage\"", "Linux package ready")
}

func TestPackageLinuxScriptRequiresAndCopiesBashLanguageServer(t *testing.T) {
	script := readScript(t, "package_linux.sh")

	assertScriptContains(t, script, "\"bash-language-server|bin/bash-language-server\"")
	assertScriptContains(t, script, "packaged LSP bundle is required; set $lsp_bundle_dir_env")
	assertScriptContains(t, script, "bash-language-server")
	assertScriptContains(t, script, "packaged LSP bundle missing executable $server_id")
	assertScriptContains(t, script, "copy_packaged_lsp_bundle \"$stage\"")
	assertScriptContains(t, script, "ln -s \"../lsp/$rel_path\" \"$link_path\"")
	assertScriptOrder(t, script, "\"bash-language-server|bin/bash-language-server\"", "resolve_packaged_lsp_bundle")
}

func TestPackageLinuxScriptRequiresAndCopiesSQLLanguageServer(t *testing.T) {
	script := readScript(t, "package_linux.sh")

	assertScriptContains(t, script, "\"sqruff|bin/sqruff\"")
	assertScriptContains(t, script, "packaged LSP bundle is required; set $lsp_bundle_dir_env")
	assertScriptContains(t, script, "sqruff")
	assertScriptContains(t, script, "packaged LSP bundle missing executable $server_id")
	assertScriptContains(t, script, "copy_packaged_lsp_bundle \"$stage\"")
	assertScriptContains(t, script, "ln -s \"../lsp/$rel_path\" \"$link_path\"")
	assertScriptOrder(t, script, "\"sqruff|bin/sqruff\"", "resolve_packaged_lsp_bundle")
}

func TestPackageLinuxScriptRequiresAndCopiesShellcheck(t *testing.T) {
	script := readScript(t, "package_linux.sh")

	assertScriptContains(t, script, "\"shellcheck|bin/shellcheck\"")
	assertScriptContains(t, script, "packaged LSP bundle is required; set $lsp_bundle_dir_env")
	assertScriptContains(t, script, "shellcheck")
	assertScriptContains(t, script, "packaged LSP bundle missing executable $server_id")
	assertScriptContains(t, script, "copy_packaged_lsp_bundle \"$stage\"")
	assertScriptContains(t, script, "ln -s \"../lsp/$rel_path\" \"$link_path\"")
	assertScriptOrder(t, script, "\"shellcheck|bin/shellcheck\"", "resolve_packaged_lsp_bundle")
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

func TestPackageLinuxScriptCopiesSQLiteRuntimeMigrations(t *testing.T) {
	script := readScript(t, "package_linux.sh")

	assertScriptContains(t, script, "copy_sqlite_migrations()")
	assertScriptContains(t, script, "local src=\"$root/internal/platform/db/sqlite/migrations\"")
	assertScriptContains(t, script, "local dest=\"$bundle_root/internal/platform/db/sqlite/migrations\"")
	assertScriptContains(t, script, "missing SQLite migrations directory: $src")
	assertScriptContains(t, script, "missing SQLite migration files under $src")
	assertScriptContains(t, script, "copy_sqlite_migrations \"$stage\"")
	assertScriptDoesNotContain(t, script, "cp -R \"$root/migrations\" \"$stage/migrations\"")
	assertScriptOrder(t, script, "copy_sqlite_migrations \"$stage\"", "write_runtime_manifest \"$stage\"")
	assertPackageScriptCopySQLiteMigrationsFailsFast(t, "package_linux.sh", "linux")
}

func TestPackageLinuxScriptEmbedsNewFrontendApp(t *testing.T) {
	script := readScript(t, "package_linux.sh")

	assertScriptContains(t, script, "build_current_frontend_app")
	assertScriptContains(t, script, "cd \"$root/frontend-app\"")
	assertScriptContains(t, script, "rsync -a --delete --exclude .gitkeep \"$root/frontend-app/dist\"/ \"$root/cmd/agent-terminal/web-dist\"/")
	assertScriptContains(t, script, "make APP_COMMIT=\"$app_commit\" build-peer-binaries")
	assertScriptContains(t, script, "go build -ldflags \"$schema_build_identity_ldflag\" -o bin/agent-terminal ./cmd/agent-terminal")
	assertScriptDoesNotContain(t, script, "cd \"$root/cmd/agent-terminal/frontend\"")
	assertScriptDoesNotContain(t, script, "make build-agent-terminal-plain")
	assertScriptOrder(t, script, "npm run build", "rsync -a --delete --exclude .gitkeep \"$root/frontend-app/dist\"/ \"$root/cmd/agent-terminal/web-dist\"/")
	assertScriptOrder(t, script, "rsync -a --delete --exclude .gitkeep \"$root/frontend-app/dist\"/ \"$root/cmd/agent-terminal/web-dist\"/", "go build -ldflags \"$schema_build_identity_ldflag\" -o bin/agent-terminal ./cmd/agent-terminal")
}

func TestPackageLinuxScriptRequiresFrontendAppDistWhenSkippingBuild(t *testing.T) {
	script := readScript(t, "package_linux.sh")

	assertScriptContains(t, script, "SUPER_DOLPHIN_SKIP_FRONTEND_BUILD")
	assertScriptContains(t, script, "frontend-app/required-dist-entries.txt")
	assertScriptContains(t, script, "require_frontend_entries \"$root/frontend-app/dist\" \"frontend dist\"")
	assertScriptContains(t, script, "require_frontend_entries \"$root/cmd/agent-terminal/web-dist\" \"embedded frontend dist\"")
	assertScriptContains(t, script, "missing required entry $entry")
	assertScriptOrder(t, script, "require_frontend_entries \"$root/frontend-app/dist\" \"frontend dist\"", "rsync -a --delete --exclude .gitkeep \"$root/frontend-app/dist\"/ \"$root/cmd/agent-terminal/web-dist\"/")
}

func TestPackageLinuxFrontendGuardRejectsMissingRecoveryEntry(t *testing.T) {
	root := t.TempDir()
	writeFixTestGuardFile(t, root, "frontend-app/required-dist-entries.txt", "index.html\nrecovery.html\n")
	writeFixTestGuardFile(t, root, "frontend-app/dist/index.html", "<html>ok</html>\n")
	scriptPath, err := filepath.Abs("package_linux.sh")
	if err != nil {
		t.Fatalf("Abs(package_linux.sh) error = %v", err)
	}
	harness := "source " + bashQuote(bashArg("", scriptPath)) + "\n" +
		"root=" + bashQuote(bashArg("", root)) + "\n" +
		"require_frontend_entries \"$root/frontend-app/dist\" \"frontend dist\"\n"
	cmd := exec.Command("bash", "-c", harness)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("package Linux frontend guard accepted dist without recovery.html:\n%s", output)
	}
	want := "frontend dist missing required entry recovery.html: " + filepath.Join(root, "frontend-app", "dist", "recovery.html")
	if !strings.Contains(string(output), want) {
		t.Fatalf("package Linux frontend guard missing diagnostic %q:\n%s", want, output)
	}
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
		"bundled_codex_path":  "bin/codex",
		"bundled_gopls_path":  "bin/gopls",
		"lsp_bundle_path":     "lsp",
		"lsp_manifest_path":   "lsp/lsp-manifest.json",
		"model_registry_path": "models.yaml",
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
	if _, ok := manifest["embedded_postgres_resource_path"]; ok {
		t.Fatal("runtime manifest must not include embedded_postgres_resource_path")
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
	if got := stripWSLInteropBanner(string(out)); got != want {
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
		"sha256_file",
		"tar -xzf",
		"sqlite_migrations_dir=\"$package_root/internal/platform/db/sqlite/migrations\"",
		"missing SQLite migration files under $sqlite_migrations_dir",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptDoesNotContain(t, script, "embedded_postgres_resource_path")
	assertScriptDoesNotContain(t, script, "postgres.bki")
	assertScriptDoesNotContain(t, script, "$pg/bin/")
	assertScriptDoesNotContain(t, script, "$package_root/migrations")
}

func TestVerifyPackagedAppLinuxRequiresNode225(t *testing.T) {
	stage := writeMinimalPackagedLinuxStage(t)
	writeRuntimeManifest(t, stage, map[string]string{
		"bundled_codex_path":  "bin/codex",
		"bundled_gopls_path":  "bin/gopls",
		"lsp_bundle_path":     "lsp",
		"lsp_manifest_path":   "lsp/lsp-manifest.json",
		"model_registry_path": "models.yaml",
	})
	nodePath := filepath.Join(stage, "lsp", "node", "bin", "node")

	invalidFixtures := []struct {
		name  string
		write func()
	}{
		{name: "missing", write: func() {
			if err := os.Remove(nodePath); err != nil {
				t.Fatalf("remove packaged Node fixture: %v", err)
			}
		}},
		{name: "not executable", write: func() {
			writeFile(t, nodePath, "#!/bin/sh\nprintf 'v22.5.0\\n'\n", 0o644)
		}},
		{name: "unparseable", write: func() {
			writePackagedNodeFixture(t, nodePath, "not-a-version")
		}},
		{name: "below minimum", write: func() {
			writePackagedNodeFixture(t, nodePath, "22.4.9")
		}},
	}
	for _, tc := range invalidFixtures {
		t.Run(tc.name, func(t *testing.T) {
			tc.write()
			output, err := runVerifyPackagedAppLinux(t, stage)
			if err == nil || !strings.Contains(output, "packaged Node.js >= 22.5.0 is required by @bytebase/dbhub@0.23.0") {
				t.Fatalf("expected packaged Node rejection, err=%v output:\n%s", err, output)
			}
		})
	}

	writePackagedNodeFixture(t, nodePath, "22.5.0")
	output, err := runVerifyPackagedAppLinux(t, stage)
	if err != nil {
		t.Fatalf("expected Node 22.5.0 acceptance, got %v:\n%s", err, output)
	}
}

func TestVerifyPackagedAppLinuxChecksBundledBashLanguageServer(t *testing.T) {
	script := readScript(t, "verify_packaged_app_linux.sh")

	assertScriptContains(t, script, "\"bash-language-server|bin/bash-language-server\"")
	assertScriptContains(t, script, "\"$package_root/bin/bash-language-server\"")
	assertScriptContains(t, script, "bash-language-server)")
	assertScriptContains(t, script, "printf '%s\\n' \"--version\"")
}

func TestVerifyPackagedAppLinuxChecksBundledSQLLanguageServer(t *testing.T) {
	script := readScript(t, "verify_packaged_app_linux.sh")

	assertScriptContains(t, script, "\"sqruff|bin/sqruff\"")
	assertScriptContains(t, script, "\"$package_root/bin/sqruff\"")
	assertScriptContains(t, script, "printf '%s\\n' \"--version\"")
}

func TestVerifyPackagedAppLinuxChecksBundledShellcheck(t *testing.T) {
	script := readScript(t, "verify_packaged_app_linux.sh")

	assertScriptContains(t, script, "\"shellcheck|bin/shellcheck\"")
	assertScriptContains(t, script, "\"$package_root/bin/shellcheck\"")
	assertScriptContains(t, script, "shellcheck)")
	assertScriptContains(t, script, "printf '%s\\n' \"--version\"")
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

func TestVerifyPackagedAppLinuxRejectsLauncherMissingRuntimeEnv(t *testing.T) {
	stage := writeMinimalPackagedLinuxStage(t)
	writeRuntimeManifest(t, stage, map[string]string{
		"bundled_codex_path":  "bin/codex",
		"bundled_gopls_path":  "bin/gopls",
		"lsp_bundle_path":     "lsp",
		"lsp_manifest_path":   "lsp/lsp-manifest.json",
		"model_registry_path": "models.yaml",
	})
	writeFile(t, filepath.Join(stage, "run.sh"), `#!/usr/bin/env bash
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export SUPER_DOLPHIN_PACKAGE_ROOT="$here"
export SUPER_DOLPHIN_RUNTIME_MODE=packaged
exec "$here/bin/agent-terminal" "$@"
`, 0o755)

	output, err := runVerifyPackagedAppLinux(t, stage)
	if err == nil {
		t.Fatalf("expected Linux verifier to reject launcher missing packaged env, got success:\n%s", output)
	}
	if !strings.Contains(output, "Linux launcher missing packaged runtime env") {
		t.Fatalf("expected launcher runtime env error, got:\n%s", output)
	}
}

func TestVerifyPackagedAppLinuxRejectsOnlyLegacyTopLevelMigrations(t *testing.T) {
	stage := writeMinimalPackagedLinuxStage(t)
	writeRuntimeManifest(t, stage, map[string]string{
		"bundled_codex_path":  "bin/codex",
		"bundled_gopls_path":  "bin/gopls",
		"lsp_bundle_path":     "lsp",
		"lsp_manifest_path":   "lsp/lsp-manifest.json",
		"model_registry_path": "models.yaml",
	})
	if err := os.RemoveAll(sqliteMigrationsPath(stage)); err != nil {
		t.Fatalf("remove SQLite migrations: %v", err)
	}
	writeFile(t, filepath.Join(stage, "migrations", "0001.sql"), "select 1;\n", 0o644)

	output, err := runVerifyPackagedAppLinux(t, stage)
	if err == nil {
		t.Fatalf("expected Linux verifier to reject legacy-only migrations, got success:\n%s", output)
	}
	if !strings.Contains(output, "missing SQLite migration files under") {
		t.Fatalf("expected missing SQLite migrations error, got:\n%s", output)
	}
}

func TestVerifyPackagedAppLinuxAcceptsStageAndRejectsLSPDigestMismatch(t *testing.T) {
	stage := writeMinimalPackagedLinuxStage(t)
	writeRuntimeManifest(t, stage, map[string]string{
		"bundled_codex_path":  "bin/codex",
		"bundled_gopls_path":  "bin/gopls",
		"lsp_bundle_path":     "lsp",
		"lsp_manifest_path":   "lsp/lsp-manifest.json",
		"model_registry_path": "models.yaml",
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
