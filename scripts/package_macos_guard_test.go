package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPackageMacOSScriptBundlesRuntimeContracts(t *testing.T) {
	script := readScript(t, "package_macos.sh")
	verify := readScript(t, "verify_packaged_app_macos.sh")

	assertScriptContains(t, script, "copy_model_registry \"$resources\"")
	assertScriptContains(t, script, "write_runtime_manifest \"$resources\" \"$platform\"")
	assertScriptContains(t, script, "write_packaged_relay_env \"$resources\"")
	assertScriptContains(t, script, "bundle_git_dylibs \"$resources\"")
	assertScriptContains(t, script, "copy_packaged_codex \"$resources\" \"$resources/bin/codex\"")
	assertScriptContains(t, script, "resolve_packaged_ffmpeg")
	assertScriptContains(t, script, "copy_packaged_ffmpeg \"$resources\"")
	assertScriptContains(t, script, "cp -f \"$packaged_ffmpeg_bin\" \"$resources/bin/ffmpeg\"")
	assertScriptContains(t, script, "brew install ffmpeg")
	assertScriptContains(t, script, "go build -o bin/super-dolphin-updater ./cmd/super-dolphin-updater")
	assertScriptContains(t, script, "go build -o bin/super-dolphin-guard ./cmd/super-dolphin-guard")
	assertScriptContains(t, script, "cp \"$root/bin/super-dolphin-updater\" \"$resources/bin/super-dolphin-updater\"")
	assertScriptOrder(t, script, "copy_packaged_codex \"$resources\" \"$resources/bin/codex\"", "cp \"$root/bin/super-dolphin-updater\" \"$resources/bin/super-dolphin-updater\"")
	assertScriptOrder(t, script, "copy_packaged_ffmpeg \"$resources\"", "bundle_git_dylibs \"$resources\"")
	assertScriptContains(t, script, "write_codex_manifest \"$resources\"")
	assertScriptDoesNotContain(t, script, "command -v codex")
	assertScriptDoesNotContain(t, script, "/Applications/Codex.app")
	assertScriptContains(t, script, "verify_no_homebrew_dylib_refs \"Git\"")
	assertScriptContains(t, script, "@rpath/*.dylib")
	assertScriptContains(t, script, "xml_escape()")
	assertScriptContains(t, script, "plist_app_name=\"$(xml_escape \"$app_name\")\"")
	assertScriptContains(t, script, "macos_min_version=\"${SUPER_DOLPHIN_MACOS_MIN_VERSION:-13.0}\"")
	assertScriptContains(t, script, "sign_macho_tree \"$codesign_identity\" \"$macos\" \"$resources/bin\" \"$resources/lib\"")
	assertScriptContains(t, verify, "$resources/bin/super-dolphin-updater")
	assertScriptContains(t, verify, "$resources/bin/super-dolphin-guard")
	assertScriptContains(t, verify, "$resources/bin/ffmpeg")
	assertScriptContains(t, verify, "verify_packaged_ffmpeg")
	assertScriptContains(t, verify, "packaged ffmpeg smoke verified")
	assertScriptContains(t, verify, "verify_package_update_trust")
	assertScriptContains(t, verify, "update-trust.json")
	assertScriptContains(t, verify, "package-owned update trust helper digest mismatch")
	assertScriptDoesNotContain(t, verify, "go run")
	assertScriptDoesNotContain(t, verify, "go env")
	assertScriptContains(t, verify, "packaged .env must not contain SUPER_DOLPHIN_UPDATE_* overrides")
}

func TestPackageMacOSScriptRequiresExactSignerForPackageUpdates(t *testing.T) {
	script := readScript(t, "package_macos.sh")

	assertScriptContains(t, script, "dev-local|gray|gray-unsigned")
	assertScriptContains(t, script, "updates_enabled_for_profile()")
	assertScriptContains(t, script, "[[ \"$release_profile\" == \"gray\" || \"$release_profile\" == \"gray-unsigned\" ]]")
	assertScriptContains(t, script, "package-owned updates require an exact TeamIdentifier")
	assertScriptContains(t, script, "-package-trust-signer")
	assertScriptDoesNotContain(t, script, "SUPER_DOLPHIN_UPDATE_ALLOW_UNSIGNED=1")
	assertScriptOrder(t, script, "resolve_release_profile", "resolve_update_config")
	assertScriptOrder(t, script, "sign_macho_tree", "write_packaged_update_trust \"$resources\"")
}

func TestPackageMacOSScriptWritesRuntimeManifest(t *testing.T) {
	script := readScript(t, "package_macos.sh")
	body := functionBody(t, script, "write_runtime_manifest")

	assertScriptContains(t, body, "runtime-manifest.json")
	assertScriptContains(t, body, "\"bundled_codex_path\": \"bin/codex\"")
	assertScriptContains(t, body, "\"bundled_gopls_path\": \"bin/gopls\"")
	assertScriptContains(t, body, "\"lsp_bundle_path\": \"lsp\"")
	assertScriptContains(t, body, "\"lsp_manifest_path\": \"lsp/lsp-manifest.json\"")
	assertScriptContains(t, body, "\"model_registry_path\": \"models.yaml\"")
	assertScriptDoesNotContain(t, body, "embedded_postgres_resource_path")
	assertScriptDoesNotContain(t, body, "$root")
	assertScriptOrder(t, script, "copy_packaged_codex \"$resources\" \"$resources/bin/codex\"", "write_runtime_manifest \"$resources\" \"$platform\"")
	assertScriptOrder(t, script, "copy_packaged_lsp_bundle \"$resources\"", "write_runtime_manifest \"$resources\" \"$platform\"")
	assertScriptOrder(t, script, "copy_model_registry \"$resources\"", "write_runtime_manifest \"$resources\" \"$platform\"")
	assertScriptOrder(t, script, "write_runtime_manifest \"$resources\" \"$platform\"", "\"$root/scripts/verify_packaged_app_macos.sh\" \"$app\"")
}

func TestPackageMacOSScriptRequiresVerifiedBundledCodexArtifact(t *testing.T) {
	script := readScript(t, "package_macos.sh")

	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_ARTIFACT")
	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_SHA256")
	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_VERSION")
	assertScriptContains(t, script, "SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX:-1")
	assertScriptContains(t, script, "packaged Codex CLI artifact is required")
	assertScriptContains(t, script, "Codex CLI artifact checksum mismatch")
	assertScriptContains(t, script, "Codex CLI artifact checksum verified")
	assertScriptContains(t, script, "resolve_packaged_codex_binary")
	assertScriptContains(t, script, "platform_pkg=\"@openai/codex-darwin-arm64\"")
	assertScriptContains(t, script, "target_triple=\"aarch64-apple-darwin\"")
	assertScriptContains(t, script, "platform_pkg=\"@openai/codex-darwin-x64\"")
	assertScriptContains(t, script, "target_triple=\"x86_64-apple-darwin\"")
	assertScriptContains(t, script, "candidate=\"$source_pkg/node_modules/$platform_pkg/vendor/$target_triple/codex/codex\"")
	assertScriptContains(t, script, "packaged Codex CLI artifact resolved to non-Mach-O binary")
	assertScriptContains(t, script, "run_packaged_smoke_check \"Codex CLI app-server\" \"$dest\" app-server --help")
	assertScriptContains(t, script, "packaged Codex CLI failed app-server validation")
	assertScriptContains(t, script, "failed during packaged smoke check")
	assertScriptDoesNotContain(t, script, "copy_packaged_codex_vendor")
	assertScriptDoesNotContain(t, script, "\"vendor_path\": \"vendor\"")
	assertScriptDoesNotContain(t, script, "\"vendor_sha256\": \"$vendor_sha256\"")
	assertScriptDoesNotContain(t, script, "\"$resources/vendor\"")
	assertScriptOrder(t, script, "resolve_packaged_codex_artifact", "mkdir -p \"$macos\" \"$resources/bin\"")
	assertScriptOrder(t, script, "copy_packaged_codex \"$resources\" \"$resources/bin/codex\"", "bundle_git_dylibs \"$resources\"")
}

func TestPackageMacOSScriptRequiresVerifiedLSPBundle(t *testing.T) {
	script := readScript(t, "package_macos.sh")
	body := functionBody(t, script, "write_runtime_manifest")
	assertScriptContains(t, script, "SUPER_DOLPHIN_LSP_BUNDLE_DIR")
	assertScriptContains(t, script, "lsp-manifest.json")
	assertScriptContains(t, script, "lsp-checksums.sha256")
	assertScriptContains(t, script, "packaged LSP bundle checksum mismatch")
	for _, want := range []string{"lsp_shadow_execs=(python python3)", "packaged LSP bundle missing Python shadow executable", "\"$dest_root/bin/$shadow_exec\""} {
		assertScriptContains(t, script, want)
	}
	for _, want := range []string{"go|bin/go", "packaged LSP bundle missing Go toolchain executable"} {
		assertScriptContains(t, script, want)
	}
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
		"jdtls",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptContains(t, script, "resolve_packaged_lsp_bundle")
	assertScriptContains(t, script, "copy_packaged_lsp_bundle \"$resources\"")
	assertScriptContains(t, script, "write_lsp_manifest \"$resources\"")
	assertScriptContains(t, script, "sign_macho_tree \"$codesign_identity\" \"$macos\" \"$resources/bin\" \"$resources/lib\" \"$resources/libexec\" \"$resources/lsp\"")
	assertScriptContains(t, body, "\"lsp_bundle_path\": \"lsp\"")
	assertScriptContains(t, body, "\"lsp_manifest_path\": \"lsp/lsp-manifest.json\"")
	assertScriptOrder(t, script, "resolve_packaged_lsp_bundle", "mkdir -p \"$macos\" \"$resources/bin\"")
	assertScriptOrder(t, script, "copy_packaged_lsp_bundle \"$resources\"", "sign_macho_tree \"$codesign_identity\"")
	assertScriptOrder(t, script, "sign_macho_tree \"$codesign_identity\"", "write_lsp_manifest \"$resources\"")
	assertScriptOrder(t, script, "write_lsp_manifest \"$resources\"", "write_runtime_manifest \"$resources\" \"$platform\"")
}

func TestPackageMacOSScriptStandardProfileDoesNotRequireJDTLS(t *testing.T) {
	script := readScript(t, "package_macos.sh")
	standardStart := strings.Index(script, "lsp_server_specs=(")
	if standardStart < 0 {
		t.Fatal("package_macos.sh missing lsp_server_specs")
	}
	fullStart := strings.Index(script, "if [[ \"$lsp_profile\" == \"full\" ]]; then\n  lsp_server_specs+=")
	if fullStart < 0 {
		t.Fatal("package_macos.sh missing full profile lsp_server_specs append")
	}
	standardSpecs := script[standardStart:fullStart]

	assertScriptContains(t, script, "lsp_profile=\"${SUPER_DOLPHIN_LSP_PROFILE:-standard}\"")
	assertScriptContains(t, script, "lsp_server_specs+=(\"jdtls|bin/jdtls\")")
	assertScriptDoesNotContain(t, standardSpecs, "jdtls")
}

func TestPackageMacOSScriptRequiresAndCopiesShellAndSQLLSPTools(t *testing.T) {
	script := readScript(t, "package_macos.sh")

	for _, spec := range []string{"\"bash-language-server|bin/bash-language-server\"", "\"sqruff|bin/sqruff\"", "\"shellcheck|bin/shellcheck\""} {
		assertScriptContains(t, script, spec)
		assertScriptOrder(t, script, spec, "resolve_packaged_lsp_bundle")
	}
	assertScriptContains(t, script, "packaged LSP bundle is required; set $lsp_bundle_dir_env")
	assertScriptContains(t, script, "packaged LSP bundle missing executable $server_id")
	assertScriptContains(t, script, "copy_packaged_lsp_bundle \"$resources\"")
	assertScriptContains(t, script, "ln -s \"../lsp/$rel_path\" \"$link_path\"")
}

func TestPackageMacOSScriptsDoNotInvokeHostPython3(t *testing.T) {
	for _, scriptPath := range []string{"package_macos.sh", "package_macos_local.sh"} {
		t.Run(scriptPath, func(t *testing.T) {
			script := readScript(t, scriptPath)
			assertScriptDoesNotContain(t, script, "command -v python3")
			assertScriptDoesNotContain(t, script, " python3 -")
		})
	}
}

func TestPackageMacOSRemovesPythonBackedGitP4(t *testing.T) {
	script := readScript(t, "package_macos.sh")

	assertScriptContains(t, script, "rm -f \"$resources/libexec/git-core/git-p4\"")
}

func TestPackageMacOSScriptsEmitTimingLogs(t *testing.T) {
	macos := readScript(t, "package_macos.sh")
	local := readScript(t, "package_macos_local.sh")
	verify := readScript(t, "verify_packaged_app_macos.sh")

	for name, script := range map[string]string{
		"package_macos.sh":             macos,
		"package_macos_local.sh":       local,
		"verify_packaged_app_macos.sh": verify,
	} {
		t.Run(name, func(t *testing.T) {
			assertScriptContains(t, script, "phase_start()")
			assertScriptContains(t, script, "phase_end()")
			assertScriptContains(t, script, "done in ${elapsed}s")
		})
	}

	for _, want := range []string{
		"frontend build",
		"go binaries",
		"bundle git dylibs",
		"bundle lsp dylibs",
		"codesign macho tree",
		"verify packaged app",
		"create dmg",
	} {
		assertScriptContains(t, macos, "phase_start \""+want+"\"")
	}
	assertScriptContains(t, local, "phase_start \"prepare lsp bundle")
	assertScriptContains(t, local, "phase_start \"package macos")
	assertScriptContains(t, local, "resolve_local_codex_binary")
	assertScriptContains(t, local, "SUPER_DOLPHIN_CODEX_ARTIFACT=\"$codex_artifact\"")
	assertScriptContains(t, local, "SUPER_DOLPHIN_CODEX_SHA256=\"$(shasum -a 256 \"$codex_artifact\"")
	assertScriptDoesNotContain(t, local, "SUPER_DOLPHIN_CODEX_SHA256=\"$(shasum -a 256 \"$codex_bin\"")
	assertScriptContains(t, verify, "phase_start \"homebrew dylib scan\"")
}

func TestPackageMacOSScriptEmbedsNewFrontendApp(t *testing.T) {
	script := readScript(t, "package_macos.sh")

	assertScriptContains(t, script, "cd \"$root/frontend-app\"")
	assertScriptContains(t, script, "frontend-app/required-dist-entries.txt")
	assertScriptContains(t, script, "require_frontend_entries \"$root/frontend-app/dist\" \"frontend dist\"")
	assertScriptContains(t, script, "require_frontend_entries \"$root/cmd/agent-terminal/web-dist\" \"embedded frontend dist\"")
	assertScriptContains(t, script, "missing required entry $entry")
	assertScriptContains(t, script, "rsync -a --delete --exclude .gitkeep \"$root/frontend-app/dist\"/ \"$root/cmd/agent-terminal/web-dist\"/")
	assertScriptContains(t, script, "make APP_COMMIT=\"$app_commit\" build-peer-binaries")
	assertScriptContains(t, script, "go build -ldflags \"$schema_build_identity_ldflag\" -o bin/agent-terminal ./cmd/agent-terminal")
	assertScriptDoesNotContain(t, script, "cd \"$root/cmd/agent-terminal/frontend\"")
	assertScriptDoesNotContain(t, script, "make build-agent-terminal-plain")
	assertScriptOrder(t, script, "npm run build", "rsync -a --delete --exclude .gitkeep \"$root/frontend-app/dist\"/ \"$root/cmd/agent-terminal/web-dist\"/")
	assertScriptOrder(t, script, "rsync -a --delete --exclude .gitkeep \"$root/frontend-app/dist\"/ \"$root/cmd/agent-terminal/web-dist\"/", "go build -ldflags \"$schema_build_identity_ldflag\" -o bin/agent-terminal ./cmd/agent-terminal")
}

// macOSInstallerGenerationBlock 提取 DMG 安装脚本生成区块，避免测试只盯单个 heredoc 形态而漏掉分段生成后的安装边界。
func macOSInstallerGenerationBlock(t *testing.T, script string) string {
	t.Helper()
	start := strings.Index(script, "install_script=\"$staging/安装 $app_name.command\"")
	if start < 0 {
		t.Fatal("package_macos.sh missing DMG install script path")
	}
	end := strings.Index(script[start:], "chmod 755 \"$install_script\"")
	if end < 0 {
		t.Fatal("package_macos.sh missing install script chmod")
	}
	return script[start : start+end]
}

// macOSAppNamePattern 读取脚本内 APP_NAME 白名单表达式，用样例验证 slash、控制字符和 shell 元字符都会被拒绝。
func macOSAppNamePattern(t *testing.T, script string) string {
	t.Helper()
	const marker = "local pattern='"
	_, rest, ok := strings.Cut(script, marker)
	if !ok {
		t.Fatal("package_macos.sh missing APP_NAME validation pattern")
	}
	pattern, _, ok := strings.Cut(rest, "'")
	if !ok {
		t.Fatal("package_macos.sh APP_NAME validation pattern is not quoted")
	}
	return pattern
}

// TestPackageMacOSInstallerUsesCustomAppName 锁住 DMG installer 必须使用验证后的 APP_NAME literal，避免 staging app 与安装脚本名称不一致。
func TestPackageMacOSInstallerUsesCustomAppName(t *testing.T) {
	script := readScript(t, "package_macos.sh")
	installerBlock := macOSInstallerGenerationBlock(t, script)

	assertScriptContains(t, script, "app_name=\"${APP_NAME:-Super Dolphin}\"")
	assertScriptDoesNotContain(t, installerBlock, "APP_NAME=\"Super Dolphin\"")
	assertScriptContains(t, installerBlock, "APP_NAME=$install_app_name_literal")
	assertScriptContains(t, installerBlock, "SRC_APP=\"$SRC_DIR/$APP_NAME.app\"")
}

// TestPackageMacOSInstallerRejectsUnsafeAppName 覆盖 macOS APP_NAME 白名单，防止斜杠、控制字符和 shell 元字符进入安装脚本生成。
func TestPackageMacOSInstallerRejectsUnsafeAppName(t *testing.T) {
	script := readScript(t, "package_macos.sh")
	pattern := macOSAppNamePattern(t, script)
	re := regexp.MustCompile(pattern)

	for _, name := range []string{"Super Dolphin", "Super-Dolphin Beta_2.0", "Super.Dolphin"} {
		if !re.MatchString(name) {
			t.Fatalf("APP_NAME pattern rejects safe name %q", name)
		}
	}
	for _, name := range []string{"", "-Super Dolphin", "/Applications/Super Dolphin", "Super/Dolphin", "Super;Dolphin", "Super$(open)", "Super`open`", "Super\nDolphin"} {
		if re.MatchString(name) {
			t.Fatalf("APP_NAME pattern accepts unsafe name %q", name)
		}
	}

	assertScriptContains(t, script, "validate_macos_app_name()")
	assertScriptContains(t, script, "local pattern='^[A-Za-z0-9][A-Za-z0-9._ -]{0,63}$'")
	assertScriptContains(t, script, "validate_macos_app_name \"$app_name\"")
	assertScriptContains(t, script, "invalid APP_NAME")
	assertScriptContains(t, script, "install_app_name_literal=\"$(shell_quote_literal \"$app_name\")\"")
}

func TestPackageMacOSDMGInstallScriptStagesAndRollsBackApplication(t *testing.T) {
	script := readScript(t, "package_macos.sh")
	installScript := macOSInstallerGenerationBlock(t, script)

	for _, want := range []string{
		"STAGED_APP=",
		"BACKUP_APP=",
		"rollback_install()",
		"trap rollback_install ERR",
		"ditto \"$SRC_APP\" \"$STAGED_APP\"",
		"mv \"$DEST_APP\" \"$BACKUP_APP\"",
		"mv \"$STAGED_APP\" \"$DEST_APP\"",
		"rm -rf \"$BACKUP_APP\"",
	} {
		assertScriptContains(t, installScript, want)
	}
	assertScriptDoesNotContain(t, installScript, "rm -rf \"$DEST_APP\"")
	assertScriptOrder(t, installScript, "ditto \"$SRC_APP\" \"$STAGED_APP\"", "mv \"$DEST_APP\" \"$BACKUP_APP\"")
	assertScriptOrder(t, installScript, "mv \"$DEST_APP\" \"$BACKUP_APP\"", "mv \"$STAGED_APP\" \"$DEST_APP\"")
}

func TestPackageMacOSScriptUsesLinearDylibQueue(t *testing.T) {
	script := readScript(t, "package_macos.sh")
	body := functionBody(t, script, "bundle_macho_dylibs")

	assertScriptContains(t, body, "local queue_index=0")
	assertScriptContains(t, body, "while ((queue_index < ${#queue[@]})); do")
	assertScriptContains(t, body, "local file=\"${queue[$queue_index]}\"")
	assertScriptContains(t, body, "((queue_index += 1))")
	assertScriptContains(t, body, "mark_seen_file")
	assertScriptContains(t, body, "enqueue_macho_candidate")
	assertScriptDoesNotContain(t, body, "queue=(\"${queue[@]:1}\")")
}

func TestPackageMacOSScriptFiltersMachOBeforeDylibQueueDedup(t *testing.T) {
	script := readScript(t, "package_macos.sh")
	body := functionBody(t, script, "bundle_macho_dylibs")

	assertScriptContains(t, body, "is_macho \"$file\" || continue\n    enqueue_macho_candidate \"$file\"")
	assertScriptContains(t, body, "is_macho \"$file\" || continue\n    mark_seen_file \"$file\" || continue")
	assertScriptDoesNotContain(t, body, "queued_file")
	assertScriptDoesNotContain(t, body, "grep -Fxq \"$candidate\" \"$queued_file\"")
}

func TestPackageMacOSScriptDoesNotScanEntireLSPTreeForMachODylibs(t *testing.T) {
	script := readScript(t, "package_macos.sh")
	body := functionBody(t, script, "bundle_lsp_dylibs")

	assertScriptContains(t, body, "local macho_roots=(")
	assertScriptContains(t, body, "\"$lsp_root/bin\"")
	assertScriptContains(t, body, "\"$lsp_root/node/bin\"")
	assertScriptContains(t, body, "\"$lsp_root/jdk/bin\"")
	assertScriptContains(t, body, "\"$lsp_root/jdk/lib\"")
	assertScriptContains(t, body, "\"$lsp_root/jdtls\"")
	assertScriptContains(t, body, "[[ -e \"$root_dir\" ]] && macho_roots+=(\"$root_dir\")")
	assertScriptContains(t, body, "bundle_macho_dylibs \"$lsp_root/lib\" lsp \"$lsp_root\" \"${macho_roots[@]}\"")
	assertScriptContains(t, body, "verify_no_homebrew_dylib_refs \"LSP\" \"${macho_roots[@]}\" \"$lsp_root/lib\"")
	assertScriptDoesNotContain(t, body, "bundle_macho_dylibs \"$lsp_root/lib\" lsp \"$lsp_root\" \"$lsp_root\"")
	assertScriptDoesNotContain(t, body, "verify_no_homebrew_dylib_refs \"LSP\" \"$lsp_root\"")
}

func TestPackageMacOSScriptOnlyAddsRpathsWhenRelinkingBundledDeps(t *testing.T) {
	script := readScript(t, "package_macos.sh")
	body := functionBody(t, script, "bundle_macho_dylibs")

	assertScriptContains(t, body, "local rpaths_added=0")
	assertScriptContains(t, body, "if [[ \"$rpaths_added\" == \"0\" ]]; then\n          add_bundle_rpaths \"$rpath_kind\" \"$bundle_root\" \"$file\"\n          rpaths_added=1\n        fi")
	assertScriptContains(t, body, "if ! install_name_tool -change")
	assertScriptContains(t, body, "failed to rewrite dylib reference")
	assertScriptDoesNotContain(t, body, "is_macho \"$file\" || continue\n    add_bundle_rpaths")
}

func TestPackageMacOSScriptNormalizesBundledDylibInstallIDs(t *testing.T) {
	script := readScript(t, "package_macos.sh")
	body := functionBody(t, script, "bundle_macho_dylibs")

	assertScriptContains(t, body, "find \"${roots[@]}\" \"$lib_dir\" -type f -name '*.dylib'")
	assertScriptContains(t, body, "install_name_tool -id \"@rpath/$(basename \"$dylib\")\" \"$dylib\"")
}

func TestPackageMacOSScriptRecordsFinalPackagedCodexDigest(t *testing.T) {
	script := readScript(t, "package_macos.sh")
	body := functionBody(t, script, "write_codex_manifest")

	assertScriptContains(t, body, "source_sha256")
	assertScriptContains(t, body, "package_sha256")
	assertScriptContains(t, body, "sha256_file \"$bundle_root/bin/codex\"")
	assertScriptOrder(t, script, "sign_macho_tree \"$codesign_identity\"", "write_codex_manifest \"$resources\"")
	assertScriptOrder(t, script, "write_codex_manifest \"$resources\"", "codesign \"${codesign_args[@]}\" \"$app\"")
}

func TestPackageMacOSScriptEnforcesGrayReleaseProfile(t *testing.T) {
	script := readScript(t, "package_macos.sh")
	profileBody := functionBody(t, script, "resolve_release_profile")
	updateBody := functionBody(t, script, "resolve_update_config")
	trustBody := functionBody(t, script, "write_packaged_update_trust")

	for _, want := range []string{"release_profile=\"${SUPER_DOLPHIN_RELEASE_PROFILE:-dev-local}\"", "require_developer_id_codesign", "CODESIGN_IDENTITY must be a Developer ID Application identity for gray releases", "NOTARY_PROFILE is required for gray releases"} {
		assertScriptContains(t, script, want)
	}
	for _, want := range []string{"dev-local|gray|gray-unsigned", "unsupported SUPER_DOLPHIN_RELEASE_PROFILE=$release_profile; expected dev-local, gray, or gray-unsigned"} {
		assertScriptContains(t, profileBody, want)
	}
	for _, want := range []string{"SUPER_DOLPHIN_UPDATE_MANIFEST_URL", "SUPER_DOLPHIN_UPDATE_GITHUB_REPO", "SUPER_DOLPHIN_UPDATE_PUBLIC_KEY", "SUPER_DOLPHIN_UPDATE_CHANNEL", "SUPER_DOLPHIN_UPDATE_MANIFEST_URL or SUPER_DOLPHIN_UPDATE_GITHUB_REPO is required when app update is enabled", "SUPER_DOLPHIN_UPDATE_MANIFEST_URL must be an HTTPS URL with a host", "SUPER_DOLPHIN_UPDATE_GITHUB_REPO must be owner/repo without whitespace", "^https://[^/?#]+", "decoded SUPER_DOLPHIN_UPDATE_PUBLIC_KEY must be 32 bytes"} {
		assertScriptContains(t, updateBody, want)
	}
	for _, want := range []string{"-package-trust-out", "-package-trust-enabled", "-package-trust-source-kind", "-package-trust-source-value", "-package-trust-signer"} {
		assertScriptContains(t, trustBody, want)
	}
	assertScriptDoesNotContain(t, trustBody, "printf '{")
	assertScriptOrder(t, script, "resolve_release_profile", "resolve_update_config")
	assertScriptOrder(t, script, "resolve_update_config", "write_packaged_update_trust \"$resources\"")
}

func TestPackageMacOSScriptUsesDMGAsOnlyReleaseArtifact(t *testing.T) {
	script := readScript(t, "package_macos.sh")

	for _, want := range []string{"dmg_path=\"$dist/$app_name.dmg\"", "write_dmg_checksum()", "hdiutil create -volname \"$app_name\" -srcfolder \"$staging\" -ov -format UDZO \"$dmg_path\"", "xcrun notarytool submit \"$dmg_path\" --keychain-profile \"$NOTARY_PROFILE\" --wait", "xcrun stapler staple \"$dmg_path\"", "spctl -a -t open --context context:primary-signature -v \"$dmg_path\"", "write_dmg_checksum \"$dmg_path\""} {
		assertScriptContains(t, script, want)
	}
	assertScriptDoesNotContain(t, script, ".app.zip")
	assertScriptOrder(t, script, "hdiutil create -volname \"$app_name\"", "xcrun notarytool submit \"$dmg_path\"")
	assertScriptOrder(t, script, "xcrun notarytool submit \"$dmg_path\"", "xcrun stapler staple \"$dmg_path\"")
	assertScriptOrder(t, script, "xcrun stapler staple \"$dmg_path\"", "spctl -a -t open --context context:primary-signature -v \"$dmg_path\"")
	assertScriptOrder(t, script, "spctl -a -t open --context context:primary-signature -v \"$dmg_path\"", "write_dmg_checksum \"$dmg_path\"")
}

func TestMacOSReleaseSmokeScriptCoversGrayUpdateManifest(t *testing.T) {
	script := readScript(t, "../docs/scripts/macos_release_smoke.sh")

	for _, want := range []string{
		"update-loop",
		"manifest",
		"latest_json_path",
		"SUPER_DOLPHIN_UPDATE_MANIFEST_URL",
		"SUPER_DOLPHIN_UPDATE_PUBLIC_KEY",
		"SUPER_DOLPHIN_UPDATE_SIGNING_KEY",
		"SUPER_DOLPHIN_UPDATE_ARTIFACT_URL",
		"verify_dmg_checksum",
		"validate_update_public_key",
		"go run ./cmd/super-dolphin-release-manifest",
		"-out \"$generated_manifest\"",
		"cmp -s \"$generated_manifest\" \"$latest_json_path\"",
		"BLOCKER:",
		"does not install or execute a real update loop",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptOrder(t, script, "verify_dmg_checksum \"$dmg_path\"", "go run ./cmd/super-dolphin-release-manifest")
	assertScriptOrder(t, script, "go run ./cmd/super-dolphin-release-manifest", "cmp -s \"$generated_manifest\" \"$latest_json_path\"")
}

func TestVerifyPackagedAppMacOSSupportsUpdateDMG(t *testing.T) {
	script := readScript(t, "verify_packaged_app_macos.sh")

	for _, want := range []string{"UPDATE_DMG", "hdiutil attach \"$UPDATE_DMG\" -nobrowse -readonly -mountpoint \"$dmg_mount\"", "trap detach_update_dmg EXIT", "hdiutil detach \"$dmg_mount\"", "find \"$dmg_mount\" -maxdepth 1 -name '*.app' -type d"} {
		assertScriptContains(t, script, want)
	}
	assertScriptOrder(t, script, "hdiutil attach \"$UPDATE_DMG\"", "app=\"$(find \"$dmg_mount\"")
	assertScriptOrder(t, script, "app=\"$(find \"$dmg_mount\"", "resources=\"$app/Contents/Resources\"")
}

func TestVerifyPackagedAppMacOSChecksFinalCodexDigest(t *testing.T) {
	script := readScript(t, "verify_packaged_app_macos.sh")

	assertScriptContains(t, script, "sha256_file()")
	assertScriptContains(t, script, "package_sha256")
	assertScriptContains(t, script, "sha256_file \"$resources/bin/codex\"")
	assertScriptContains(t, script, "Codex packaged digest mismatch")
}

func writeDefaultMacOSRuntimeManifest(t *testing.T, resources string) {
	t.Helper()
	writeRuntimeManifest(t, resources, map[string]string{
		"bundled_codex_path":  "bin/codex",
		"bundled_gopls_path":  "bin/gopls",
		"lsp_bundle_path":     "lsp",
		"lsp_manifest_path":   "lsp/lsp-manifest.json",
		"model_registry_path": "models.yaml",
	})
}

func TestVerifyPackagedAppMacOSAcceptsRuntimeManifestContract(t *testing.T) {
	app := writeMinimalPackagedMacOSApp(t)
	resources := filepath.Join(app, "Contents", "Resources")
	writeDefaultMacOSRuntimeManifest(t, resources)

	output, err := runVerifyPackagedAppMacOS(t, app)
	if err != nil {
		t.Fatalf("expected packaged app verifier to accept runtime manifest contract, got %v:\n%s", err, output)
	}
}

func TestVerifyPackagedAppMacOSDoesNotRequireGoToolchain(t *testing.T) {
	app := writeMinimalPackagedMacOSApp(t)
	resources := filepath.Join(app, "Contents", "Resources")
	writeDefaultMacOSRuntimeManifest(t, resources)

	output, err := runVerifyPackagedAppMacOSWithEnv(t, app, []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"})
	if err != nil {
		t.Fatalf("expected packaged app verifier to run without go on PATH, got %v:\n%s", err, output)
	}
	if !strings.Contains(output, "LSP manifest verified") {
		t.Fatalf("expected LSP manifest verification under clean PATH, got:\n%s", output)
	}
	if !strings.Contains(output, "LSP server smoke verified") {
		t.Fatalf("expected LSP server version smoke under clean PATH, got:\n%s", output)
	}
	if !strings.Contains(output, "==> [packaged go lsp smoke] done") {
		t.Fatalf("expected Go semantic smoke to use bundled go under clean PATH, got:\n%s", output)
	}
}

func TestVerifyPackagedAppMacOSSmokesJDTLSWhenBundled(t *testing.T) {
	app := writeMinimalPackagedMacOSApp(t)
	resources := filepath.Join(app, "Contents", "Resources")
	serverPath := filepath.Join(resources, "lsp", "bin", "jdtls")
	writeFile(t, serverPath, "#!/bin/sh\necho jdtls smoke ran >&2\nexit 42\n", 0o755)
	writeLSPManifest(t, resources)
	writeDefaultMacOSRuntimeManifest(t, resources)

	output, err := runVerifyPackagedAppMacOS(t, app)
	if err == nil {
		t.Fatalf("expected verifier to smoke bundled jdtls and fail, got success:\n%s", output)
	}
	if !strings.Contains(output, "LSP server jdtls version smoke failed") || !strings.Contains(output, "jdtls smoke ran") {
		t.Fatalf("expected jdtls smoke failure output, got:\n%s", output)
	}
}

func TestVerifyPackagedAppMacOSTypeScriptLSPExecutableCheckPassesUnderCleanPath(t *testing.T) {
	app := writeMinimalPackagedMacOSApp(t)
	resources := filepath.Join(app, "Contents", "Resources")
	nodePath := filepath.Join(resources, "lsp", "node", "bin", "node")
	serverPath := filepath.Join(resources, "lsp", "bin", "typescript-language-server")
	writePackagedNodeFixture(t, nodePath, "22.5.0")
	writeFile(t, serverPath, "#!/bin/sh\nnode_path=\"$(command -v node || true)\"\nexpected="+shellSingleQuoted(nodePath)+"\nif [ \"$node_path\" != \"$expected\" ]; then\n  echo \"expected bundled node $expected, got ${node_path:-<missing>}\" >&2\n  exit 42\nfi\n\"$node_path\" --version >/dev/null\nexit 0\n", 0o755)
	writeLSPManifest(t, resources)
	writeDefaultMacOSRuntimeManifest(t, resources)

	output, err := runVerifyPackagedAppMacOSWithEnv(t, app, []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"})
	if err != nil {
		t.Fatalf("expected LSP version smoke to use bundled node under clean PATH, got %v:\n%s", err, output)
	}
	if !strings.Contains(output, "LSP server executable verified: typescript-language-server") {
		t.Fatalf("expected TypeScript LSP executable check to pass under clean PATH, got:\n%s", output)
	}
}

func TestVerifyPackagedAppMacOSRequiresNode225(t *testing.T) {
	app := writeMinimalPackagedMacOSApp(t)
	resources := filepath.Join(app, "Contents", "Resources")
	writeDefaultMacOSRuntimeManifest(t, resources)
	nodePath := filepath.Join(resources, "lsp", "node", "bin", "node")

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
		{name: "release candidate", write: func() {
			writePackagedNodeFixture(t, nodePath, "22.5.0-rc.1")
		}},
		{name: "arbitrary suffix", write: func() {
			writePackagedNodeFixture(t, nodePath, "22.5.0-not semver")
		}},
		{name: "build metadata", write: func() {
			writePackagedNodeFixture(t, nodePath, "22.5.0+build")
		}},
		{name: "extra text", write: func() {
			writePackagedNodeFixture(t, nodePath, "22.5.0 extra")
		}},
		{name: "leading blank line", write: func() {
			writePackagedNodeOutputFixture(t, nodePath, "\nv22.5.0\n")
		}},
		{name: "trailing blank line", write: func() {
			writePackagedNodeOutputFixture(t, nodePath, "v22.5.0\n\n")
		}},
		{name: "below minimum minor", write: func() {
			writePackagedNodeFixture(t, nodePath, "22.4.99")
		}},
		{name: "below minimum major", write: func() {
			writePackagedNodeFixture(t, nodePath, "21.99.99")
		}},
	}
	for _, tc := range invalidFixtures {
		t.Run(tc.name, func(t *testing.T) {
			tc.write()
			output, err := runVerifyPackagedAppMacOS(t, app)
			if err == nil || !strings.Contains(output, "packaged Node.js >= 22.5.0 is required by @bytebase/dbhub@0.23.0") {
				t.Fatalf("expected packaged Node rejection, err=%v output:\n%s", err, output)
			}
		})
	}

	for _, version := range []string{"22.5.0", "22.5.1", "23.0.0"} {
		t.Run("accept "+version, func(t *testing.T) {
			writePackagedNodeFixture(t, nodePath, version)
			output, err := runVerifyPackagedAppMacOS(t, app)
			if err != nil {
				t.Fatalf("expected Node %s acceptance, got %v:\n%s", version, err, output)
			}
		})
	}
}

func TestVerifyPackagedAppMacOSAcceptsMinifiedLSPManifestWithReorderedFields(t *testing.T) {
	app := writeMinimalPackagedMacOSApp(t)
	resources := filepath.Join(app, "Contents", "Resources")
	writeDefaultMacOSRuntimeManifest(t, resources)
	writeMinifiedReorderedLSPManifest(t, filepath.Join(resources, "lsp"), "lsp/")

	output, err := runVerifyPackagedAppMacOS(t, app)
	if err != nil {
		t.Fatalf("expected verifier to accept minified reordered LSP manifest, got %v:\n%s", err, output)
	}
}

func TestPackageScriptsWriteLSPManifestAcceptsMinifiedSourceWithReorderedFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script string
		goos   string
	}{
		{name: "macos", script: "package_macos.sh", goos: "darwin"},
		{name: "linux", script: "package_linux.sh", goos: "linux"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundleRoot := writePackageLSPStage(t)
			writeMinifiedReorderedLSPManifest(t, filepath.Join(bundleRoot, "lsp"), "")

			output, err := runPackageWriteLSPManifest(t, tc.script, tc.goos, bundleRoot)
			if err != nil {
				t.Fatalf("write_lsp_manifest rejected minified reordered source manifest: %v\n%s", err, output)
			}

			manifest := readLSPManifest(t, filepath.Join(bundleRoot, "lsp", "lsp-manifest.json"))
			assertPackagedLSPServerPathsAndDigests(t, bundleRoot, manifest)
		})
	}
}

func TestPackageScriptsWriteLSPManifestPreservesLanguages(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script string
		goos   string
	}{
		{name: "macos", script: "package_macos.sh", goos: "darwin"},
		{name: "linux", script: "package_linux.sh", goos: "linux"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundleRoot := writePackageLSPStage(t)
			writePrettyLSPManifestWithLanguages(t, filepath.Join(bundleRoot, "lsp"), "")

			output, err := runPackageWriteLSPManifest(t, tc.script, tc.goos, bundleRoot)
			if err != nil {
				t.Fatalf("write_lsp_manifest failed: %v\n%s", err, output)
			}

			manifest := readLSPManifest(t, filepath.Join(bundleRoot, "lsp", "lsp-manifest.json"))
			assertLSPManifestLanguages(t, manifest)
		})
	}
}

func TestVerifyPackagedAppMacOSRejectsMissingLSPManifest(t *testing.T) {
	app := writeMinimalPackagedMacOSApp(t)
	resources := filepath.Join(app, "Contents", "Resources")
	writeDefaultMacOSRuntimeManifest(t, resources)
	if err := os.Remove(filepath.Join(resources, "lsp", "lsp-manifest.json")); err != nil {
		t.Fatalf("remove LSP manifest: %v", err)
	}

	output, err := runVerifyPackagedAppMacOS(t, app)
	if err == nil {
		t.Fatalf("expected packaged app verifier to reject missing LSP manifest, got success:\n%s", output)
	}
	if !strings.Contains(output, "missing LSP manifest") {
		t.Fatalf("expected missing LSP manifest error, got:\n%s", output)
	}
}

func TestVerifyPackagedAppMacOSRejectsLSPManifestDigestMismatch(t *testing.T) {
	app := writeMinimalPackagedMacOSApp(t)
	resources := filepath.Join(app, "Contents", "Resources")
	writeDefaultMacOSRuntimeManifest(t, resources)
	writeFile(t, filepath.Join(resources, "lsp", "bin", "rust-analyzer"), "#!/bin/sh\nexit 9\n", 0o755)

	output, err := runVerifyPackagedAppMacOS(t, app)
	if err == nil {
		t.Fatalf("expected packaged app verifier to reject LSP digest mismatch, got success:\n%s", output)
	}
	if !strings.Contains(output, "LSP packaged digest mismatch") {
		t.Fatalf("expected LSP packaged digest mismatch error, got:\n%s", output)
	}
}

func TestVerifyPackagedAppMacOSRequiresRuntimeManifest(t *testing.T) {
	app := writeMinimalPackagedMacOSApp(t)

	output, err := runVerifyPackagedAppMacOS(t, app)
	if err == nil {
		t.Fatalf("expected packaged app verifier to reject missing runtime manifest, got success:\n%s", output)
	}
	if !strings.Contains(output, "missing runtime manifest") {
		t.Fatalf("expected missing runtime manifest error, got:\n%s", output)
	}
}

func TestVerifyPackagedAppMacOSRejectsMissingBundledGopls(t *testing.T) {
	app := writeMinimalPackagedMacOSApp(t)
	resources := filepath.Join(app, "Contents", "Resources")
	writeDefaultMacOSRuntimeManifest(t, resources)
	if err := os.Remove(filepath.Join(resources, "bin", "gopls")); err != nil {
		t.Fatalf("remove bundled gopls: %v", err)
	}

	output, err := runVerifyPackagedAppMacOS(t, app)
	if err == nil {
		t.Fatalf("expected packaged app verifier to reject missing gopls, got success:\n%s", output)
	}
	if !strings.Contains(output, "runtime manifest bundled_gopls_path points to missing executable") {
		t.Fatalf("expected missing gopls manifest error, got:\n%s", output)
	}
}

func TestVerifyPackagedAppMacOSRejectsRuntimeManifestMismatch(t *testing.T) {
	app := writeMinimalPackagedMacOSApp(t)
	resources := filepath.Join(app, "Contents", "Resources")
	writeRuntimeManifest(t, resources, map[string]string{
		"bundled_codex_path":  "bin/missing-codex",
		"bundled_gopls_path":  "bin/gopls",
		"lsp_bundle_path":     "lsp",
		"lsp_manifest_path":   "lsp/lsp-manifest.json",
		"model_registry_path": "models.yaml",
	})

	output, err := runVerifyPackagedAppMacOS(t, app)
	if err == nil {
		t.Fatalf("expected packaged app verifier to reject runtime manifest mismatch, got success:\n%s", output)
	}
	if !strings.Contains(output, "runtime manifest bundled_codex_path mismatch") {
		t.Fatalf("expected bundled_codex_path mismatch error, got:\n%s", output)
	}
}

func TestVerifyPackagedAppMacOSRejectsRuntimeManifestSymlinkEscape(t *testing.T) {
	app := writeMinimalPackagedMacOSApp(t)
	resources := filepath.Join(app, "Contents", "Resources")
	outside := t.TempDir()
	if err := os.RemoveAll(filepath.Join(resources, "lsp")); err != nil {
		t.Fatalf("remove packaged lsp dir before symlink escape fixture: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(resources, "lsp")); err != nil {
		skipIfSymlinkPrivilegeNotHeld(t, err)
		t.Fatalf("symlink escaped lsp bundle: %v", err)
	}
	writeRuntimeManifest(t, resources, map[string]string{
		"bundled_codex_path":  "bin/codex",
		"bundled_gopls_path":  "bin/gopls",
		"lsp_bundle_path":     "lsp",
		"lsp_manifest_path":   "lsp/lsp-manifest.json",
		"model_registry_path": "models.yaml",
	})

	output, err := runVerifyPackagedAppMacOS(t, app)
	if err == nil {
		t.Fatalf("expected packaged app verifier to reject symlink escape, got success:\n%s", output)
	}
	if !strings.Contains(output, "escapes Contents/Resources") {
		t.Fatalf("expected runtime manifest symlink escape error, got:\n%s", output)
	}
}

func TestPackageScriptsGovernanceForbidPrivatePathsURLsAndInteractiveSecrets(t *testing.T) {
	for _, scriptPath := range []string{"package_macos.sh", "package_linux.sh", "package_macos_local.sh", "package_linux_local.sh"} {
		t.Run(scriptPath, func(t *testing.T) {
			script := readScript(t, scriptPath)
			assertScriptDoesNotContain(t, script, "/Users/ai")
			assertScriptDoesNotContain(t, script, "ai.wlll.shop")
			assertScriptDoesNotContain(t, script, "api.opusclaw.me")
			assertScriptDoesNotContain(t, script, "OPUSCLAW_API_KEY")
			assertScriptDoesNotContain(t, script, "read -rs")
			assertScriptDoesNotContain(t, script, "read -s")
			assertScriptDoesNotContain(t, script, "read -p")
			assertScriptDoesNotContain(t, script, "SUPER_DOLPHIN_CODEX_RELAY_API_KEY=")
		})
	}
}

func TestPackageEnvExampleDocumentsExistingReleaseInputsWithoutSecrets(t *testing.T) {
	example := readScript(t, "../.env.packaging.example")
	for _, want := range []string{
		"SUPER_DOLPHIN_LSP_BUNDLE_DIR=",
		"SUPER_DOLPHIN_FFMPEG_BIN=",
		"SUPER_DOLPHIN_CODEX_ARTIFACT=",
		"SUPER_DOLPHIN_CODEX_SHA256=",
		"SUPER_DOLPHIN_CODEX_VERSION=",
		"SUPER_DOLPHIN_CODEX_RELAY_BASE_URL=",
		"SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN=",
		"SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF=",
	} {
		assertScriptContains(t, example, want)
	}
	assertScriptDoesNotContain(t, example, "SUPER_DOLPHIN_POSTGRES_DIST=")
	for _, unwanted := range []string{"/Users/ai", "ai.wlll.shop", "api.opusclaw.me", "OPUSCLAW_API_KEY", "SUPER_DOLPHIN_CODEX_RELAY_API_KEY", "sk-"} {
		assertScriptDoesNotContain(t, example, unwanted)
	}
}
