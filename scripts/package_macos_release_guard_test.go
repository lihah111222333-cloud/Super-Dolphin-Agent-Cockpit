package main

import (
	"os"
	"testing"
)

func TestPackageMacOSScriptRequiresPackagedCodexRelayConfig(t *testing.T) {
	raw, err := os.ReadFile("package_macos.sh")
	if err != nil {
		t.Fatalf("read package_macos.sh: %v", err)
	}
	script := string(raw)

	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_RELAY_BASE_URL")
	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN")
	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF")
	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_RELAY_API_KEY")
	assertScriptContains(t, script, "is required and must not be whitespace-only")
	assertScriptContains(t, script, "packaged_relay_bootstrap_token=\"${SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN:-}\"")
	assertScriptContains(t, script, "packaged_relay_api_key=\"${SUPER_DOLPHIN_CODEX_RELAY_API_KEY:-}\"")
	assertScriptContains(t, script, "packaged_relay_bootstrap_token=\"$packaged_relay_api_key\"")
	assertScriptContains(t, script, "\"$codex_relay_privileged_api_key_env\" \"$packaged_relay_api_key\"")
	assertScriptContains(t, script, "$resources/.env")
}

func TestPackageMacOSScriptRejectsWhitespaceOnlyPackagedCodexRelayEnv(t *testing.T) {
	assertPackageScriptRejectsWhitespaceOnlyPackagedCodexRelayEnv(t, "package_macos.sh", "darwin")
}

func TestPackageLinuxScriptRequiresPackagedCodexRelayConfig(t *testing.T) {
	script := readScript(t, "package_linux.sh")

	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_RELAY_BASE_URL")
	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN")
	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF")
	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_RELAY_API_KEY")
	assertScriptContains(t, script, "is required and must not be whitespace-only")
	assertScriptContains(t, script, "privileged Codex relay API key env is not allowed")
	assertScriptContains(t, script, "packaged_relay_bootstrap_token=\"${SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN:-}\"")
	assertScriptDoesNotContain(t, script, "packaged_relay_api_key=\"${SUPER_DOLPHIN_CODEX_RELAY_API_KEY:-}\"")
	assertScriptContains(t, script, "$stage/.env")
	assertScriptOrder(t, script, "resolve_packaged_relay_env", "mkdir -p \"$stage/bin\"")
	assertScriptOrder(t, script, "rsync -aL --delete \"$pg_src\"/", "write_packaged_relay_env \"$stage\"")
	assertScriptOrder(t, script, "write_packaged_relay_env \"$stage\"", "tar -C \"$dist\"")
}

func TestPackageLinuxScriptRejectsWhitespaceOnlyPackagedCodexRelayEnv(t *testing.T) {
	assertPackageScriptRejectsWhitespaceOnlyPackagedCodexRelayEnv(t, "package_linux.sh", "linux")
}

func TestPackageMacOSScriptRejectsNonRelocatablePostgresLayout(t *testing.T) {
	script := readScript(t, "package_macos.sh")

	assertScriptContains(t, script, "verify_postgres_relocatable_layout \"$pg_src\"")
	assertScriptContains(t, script, "pg_config\" --bindir")
	assertScriptContains(t, script, "pg_config\" --sharedir")
	assertScriptContains(t, script, "compiled_prefix=\"${compiled_bindir%/bin}\"")
	assertScriptContains(t, script, "\"$compiled_prefix/share\"|\"$compiled_prefix/share/\"*")
	assertScriptContains(t, script, "Homebrew PostgreSQL cannot be copied directly")
	assertScriptOrder(t, script, "verify_postgres_relocatable_layout \"$pg_src\"", "rsync -aL --delete \"$pg_src\"/")
}

func TestPackageMacOSScriptVerifiesStagedPostgresFiles(t *testing.T) {
	script := readScript(t, "package_macos.sh")
	body := functionBody(t, script, "verify_postgres_runtime")

	assertScriptContains(t, body, "\"$pg_bundle/bin/initdb\" --version")
	assertScriptContains(t, body, "\"$pg_bundle/bin/postgres\" --version")
	assertScriptContains(t, body, "\"$pg_bundle/bin/pg_ctl\" --version")
	assertScriptContains(t, body, "verify_postgres_share_dir \"$pg_bundle\"")
	assertScriptDoesNotContain(t, body, "pg_config\" --sharedir")
	assertScriptDoesNotContain(t, body, "compiled_sharedir")
}

func TestPackageMacOSScriptRunsBundleVerifierAfterCodesignBeforeDmg(t *testing.T) {
	script := readScript(t, "package_macos.sh")

	assertScriptContains(t, script, "\"$root/scripts/verify_packaged_app_macos.sh\" \"$app\"")
	assertScriptOrder(t, script, "codesign \"${codesign_args[@]}\" \"$app\"", "\"$root/scripts/verify_packaged_app_macos.sh\" \"$app\"")
	assertScriptOrder(t, script, "\"$root/scripts/verify_packaged_app_macos.sh\" \"$app\"", "hdiutil create -volname \"$app_name\"")
}

func TestVerifyPackagedAppMacOSChecksBundledAstGrep(t *testing.T) {
	script := readScript(t, "verify_packaged_app_macos.sh")

	assertScriptContains(t, script, "\"sg|bin/sg\"")
	assertScriptContains(t, script, "\"$resources/lsp/bin/sg\"")
	assertScriptContains(t, script, "sg)")
	assertScriptContains(t, script, "printf '%s\\n' \"--help\"")
}

func TestVerifyPackagedAppMacOSRunsRealLSPSmokeEntries(t *testing.T) {
	script := readScript(t, "verify_packaged_app_macos.sh")

	assertScriptContains(t, script, "verify_packaged_go_lsp_smoke()")
	assertScriptContains(t, script, "verify_packaged_java_lsp_smoke()")
	assertScriptContains(t, script, "verify_packaged_ast_grep_smoke()")
	assertScriptContains(t, script, "go.mod")
	assertScriptContains(t, script, "go toolchain is not bundled")
	assertScriptContains(t, script, "Main.java")
	assertScriptContains(t, script, "sg run")
	assertScriptOrder(t, script, "verify_lsp_manifest", "verify_packaged_go_lsp_smoke")
	assertScriptOrder(t, script, "verify_packaged_go_lsp_smoke", "verify_packaged_java_lsp_smoke")
	assertScriptOrder(t, script, "verify_packaged_java_lsp_smoke", "verify_packaged_ast_grep_smoke")
}

func TestVerifyPackagedAppMacOSScriptContracts(t *testing.T) {
	script := readScript(t, "verify_packaged_app_macos.sh")

	assertScriptContains(t, script, "required_execs=(")
	assertScriptContains(t, script, "\"$macos/agent-terminal\"")
	assertScriptContains(t, script, "\"$resources/bin/mcp-orch\"")
	assertScriptContains(t, script, "\"$resources/bin/mcp-lsp\"")
	assertScriptContains(t, script, "\"$resources/bin/mcp-ida\"")
	assertScriptContains(t, script, "\"$resources/bin/codex\"")
	assertScriptContains(t, script, "\"$resources/bin/gopls\"")
	assertScriptContains(t, script, "\"$resources/codex-manifest.json\"")
	assertScriptContains(t, script, "\"$resources/runtime-manifest.json\"")
	assertScriptContains(t, script, "\"$resources/lsp/lsp-manifest.json\"")
	for _, want := range []string{"\"$resources/lsp/bin/python\"", "\"$resources/lsp/bin/python3\"", "\"$resources/lsp/bin/go\""} {
		assertScriptContains(t, script, want)
	}
	assertScriptContains(t, script, "Packaged Super Dolphin does not bundle a Python interpreter")
	assertScriptContains(t, script, "verify_lsp_manifest")
	assertScriptContains(t, script, "LSP manifest verified")
	assertScriptContains(t, script, "LSP server smoke verified")
	assertScriptContains(t, script, "lsp_smoke_path=\"$resources/bin:$resources/lsp/bin:$resources/lsp/node/bin:$resources/lsp/node_modules/.bin:/usr/bin:/bin:/usr/sbin:/sbin\"")
	assertScriptContains(t, script, "bundled_codex_path")
	assertScriptContains(t, script, "bundled_gopls_path")
	assertScriptContains(t, script, "lsp_manifest_path")
	assertScriptContains(t, script, "model_registry_path")
	assertScriptContains(t, script, "embedded_postgres_resource_path")
	assertScriptContains(t, script, "\"$pg/bin/postgres\"")
	assertScriptContains(t, script, "\"$pg/bin/initdb\"")
	assertScriptContains(t, script, "\"$pg/bin/pg_ctl\"")
	assertScriptContains(t, script, "\"$pg/bin/pg_config\"")
	assertScriptContains(t, script, "\"$resources/migrations\"")
	assertScriptContains(t, script, "find \"$pg/share\" -name postgres.bki -type f")
	assertScriptContains(t, script, "-print -quit")
	assertScriptContains(t, script, "find -L \"$app\" -type l -print")
	assertScriptContains(t, script, "is_macho()")
	assertScriptContains(t, script, "is_macho \"$file\" || continue")
	assertScriptOrder(t, script, "is_macho \"$file\" || continue", "otool -L \"$file\"")
	assertScriptContains(t, script, "otool -L \"$file\"")
	assertScriptContains(t, script, "/opt/homebrew/")
	assertScriptDoesNotContain(t, script, "vendor_path")
	assertScriptDoesNotContain(t, script, "vendor_sha256")
	assertScriptDoesNotContain(t, script, "sha256_tree")
	assertScriptContains(t, script, "\"$resources/bin/codex\" app-server --help")
}

func TestMacOSReleaseSmokeScriptFailFastContracts(t *testing.T) {
	script := readScript(t, "../docs/scripts/macos_release_smoke.sh")

	assertScriptContains(t, script, "set -euo pipefail")
	assertScriptContains(t, script, "docs/reviews/smoke-logs/2026-05-28")
	assertScriptContains(t, script, "scripts/verify_packaged_app_macos.sh")
	assertScriptContains(t, script, "hdiutil attach")
	assertScriptContains(t, script, "xcrun stapler validate")
	assertScriptContains(t, script, "spctl -a -vv -t open")
	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_RELAY_BASE_URL")
	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN")
	assertScriptContains(t, script, "SUPER_DOLPHIN_PRODUCTION_RELAY_SMOKE")
	assertScriptContains(t, script, "kern.hv_vmm_present")
	assertScriptContains(t, script, "route -n get default")
	assertScriptContains(t, script, "blocker")
	assertScriptContains(t, script, "CODEX_HOME")
	assertScriptContains(t, script, "app-server --help")
	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_RELEASE_API_URL")
	assertScriptContains(t, script, "startup")
	assertScriptDoesNotContain(t, script, " RETURN")
}

func TestBuildRelocatablePostgresScriptDoesNotRequireCompiledShareDir(t *testing.T) {
	script := readScript(t, "build_relocatable_postgres_macos.sh")

	assertScriptContains(t, script, "postgres.bki")
	assertScriptDoesNotContain(t, script, "pg_config\" --sharedir")
}
