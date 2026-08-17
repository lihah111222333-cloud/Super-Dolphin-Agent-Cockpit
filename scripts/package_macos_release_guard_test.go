package main

// 本文件是公共跨平台的 macOS 发布静态门禁，只读取脚本契约，故意不加 darwin build tag。

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
	assertScriptContains(t, script, "$codex_relay_privileged_api_key_env must not be set for macOS packaging")
	assertScriptDoesNotContain(t, script, "packaged_relay_bootstrap_token=\"$packaged_relay_api_key\"")
	assertScriptDoesNotContain(t, script, "\"$codex_relay_privileged_api_key_env\" \"$packaged_relay_api_key\"")
	assertScriptDoesNotContain(t, script, "printf '%s=%s\n' \"$codex_relay_privileged_api_key_env\"")
	assertScriptContains(t, functionBody(t, script, "write_packaged_relay_env"), "\"$codex_relay_bootstrap_proof_env\" \"$packaged_relay_bootstrap_proof\"")
	assertScriptContains(t, script, "$resources/.env")
}

func TestPackageMacOSScriptRejectsPlaceholderRelayForReleaseProfiles(t *testing.T) {
	script := readScript(t, "package_macos.sh")
	body := functionBody(t, script, "validate_release_relay_url")

	assertScriptContains(t, script, "validate_release_relay_url")
	assertScriptContains(t, body, "$codex_relay_base_url_env must be an HTTPS URL with host for $release_profile releases")
	assertScriptContains(t, body, "$codex_relay_base_url_env must not use a local or placeholder host")
	for _, want := range []string{"http://127.*", "https://127.*", "http://localhost*", "https://localhost*", "*.invalid*", "*.test*"} {
		assertScriptContains(t, body, want)
	}
	assertScriptOrder(t, script, "validate_env_file_value \"$codex_relay_bootstrap_proof_env\"", "validate_release_relay_url")
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
	assertScriptOrder(t, script, "write_packaged_relay_env \"$stage\"", "tar -C \"$dist\"")
}

func TestPackageLinuxScriptRejectsWhitespaceOnlyPackagedCodexRelayEnv(t *testing.T) {
	assertPackageScriptRejectsWhitespaceOnlyPackagedCodexRelayEnv(t, "package_linux.sh", "linux")
}

func TestPackageMacOSScriptDoesNotRequireBundledPostgresRuntime(t *testing.T) {
	script := readScript(t, "package_macos.sh")

	for _, forbidden := range []string{
		"SUPER_DOLPHIN_POSTGRES_DIST",
		"embedded_postgres_resource_path",
		"verify_postgres_runtime",
		"verify_postgres_relocatable_layout",
		"pg_ctl",
		"initdb",
		"rsync -aL --delete \"$pg_src\"/",
		"add_postgres_rpaths",
		"postgres)",
		"embedded_postgres_resource_path",
		"SUPER_DOLPHIN_POSTGRES_",
		"packaged postgres",
		"bundled PostgreSQL",
	} {
		assertScriptDoesNotContain(t, script, forbidden)
	}
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

func TestVerifyPackagedAppMacOSChecksBundledBashLanguageServer(t *testing.T) {
	script := readScript(t, "verify_packaged_app_macos.sh")

	assertScriptContains(t, script, "\"bash-language-server|bin/bash-language-server\"")
	assertScriptContains(t, script, "\"$resources/bin/bash-language-server\"")
	assertScriptContains(t, script, "bash-language-server)")
	assertScriptContains(t, script, "printf '%s\\n' \"--version\"")
}

func TestVerifyPackagedAppMacOSChecksBundledSQLLanguageServer(t *testing.T) {
	script := readScript(t, "verify_packaged_app_macos.sh")

	assertScriptContains(t, script, "\"sqruff|bin/sqruff\"")
	assertScriptContains(t, script, "\"$resources/bin/sqruff\"")
	assertScriptContains(t, script, "printf '%s\\n' \"--version\"")
}

func TestVerifyPackagedAppMacOSChecksBundledShellcheck(t *testing.T) {
	script := readScript(t, "verify_packaged_app_macos.sh")

	assertScriptContains(t, script, "\"shellcheck|bin/shellcheck\"")
	assertScriptContains(t, script, "\"$resources/bin/shellcheck\"")
	assertScriptContains(t, script, "shellcheck)")
	assertScriptContains(t, script, "printf '%s\\n' \"--version\"")
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
	assertScriptContains(t, script, "sqlite_migrations_dir=\"$resources/internal/platform/db/sqlite/migrations\"")
	assertScriptContains(t, script, "missing SQLite migration files under $sqlite_migrations_dir")
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
	assertScriptDoesNotContain(t, script, "embedded_postgres_resource_path")
	assertScriptDoesNotContain(t, script, "$pg/bin/")
	assertScriptDoesNotContain(t, script, "postgres.bki")
	assertScriptDoesNotContain(t, script, "$resources/migrations")
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
	assertScriptDoesNotContain(t, script, "embedded_postgres_resource_path")
	assertScriptDoesNotContain(t, script, "$resources/postgres/")
	assertScriptDoesNotContain(t, script, " RETURN")
}
