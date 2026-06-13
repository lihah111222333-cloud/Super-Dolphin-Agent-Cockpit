package main

import "testing"

func TestPackageWindowsScriptBuildsNativeWindowsPackage(t *testing.T) {
	script := readScript(t, "package_windows.ps1")

	assertScriptContains(t, script, "package_windows.ps1 must run on Windows")
	assertScriptContains(t, script, "Resolve-RepoRoot")
	assertScriptContains(t, script, "keep package_windows.ps1 under <repo>\\scripts")
	assertScriptContains(t, script, "$GoOS -ne 'windows'")
	assertScriptContains(t, script, "Invoke-WindowsGoBuild -Output (Join-Path $Root 'bin/agent-terminal.exe') -Package './cmd/agent-terminal' -LdFlags $windowsGuiLdFlags")
	assertScriptContains(t, script, "Invoke-WindowsGoBuild -Output (Join-Path $Root 'bin/mcp-orch.exe') -Package './cmd/mcp-orch' -LdFlags $windowsGuiLdFlags")
	assertScriptContains(t, script, "Invoke-WindowsGoBuild -Output (Join-Path $Root 'bin/mcp-lsp.exe') -Package './cmd/mcp-lsp' -LdFlags $windowsGuiLdFlags")
	assertScriptContains(t, script, "Invoke-WindowsGoBuild -Output (Join-Path $Root 'bin/mcp-ida.exe') -Package './cmd/mcp-ida' -LdFlags $windowsGuiLdFlags")
	assertScriptContains(t, script, "SUPER_DOLPHIN_SKIP_FRONTEND_BUILD")
	assertScriptContains(t, script, "npm run build")
	assertScriptContains(t, script, "Copy-DirectoryClean -Source (Join-Path $Root 'frontend-app/dist') -Destination (Join-Path $Root 'cmd/agent-terminal/frontend/dist')")
	assertScriptDoesNotContain(t, script, "Copy-PostgresRuntime")
	assertScriptDoesNotContain(t, script, "SUPER_DOLPHIN_POSTGRES_DIST")
	assertScriptContains(t, script, "$ProgressPreference = 'SilentlyContinue'")
	assertScriptContains(t, script, "function New-WindowsZip")
	assertScriptContains(t, script, "Get-Command tar.exe")
	assertScriptContains(t, script, "& $tar.Source -a -cf $ZipPath $stageName")
	assertScriptContains(t, script, "Compress-Archive")
	assertScriptContains(t, script, "$zipPath = Join-Path $dist \"$AppName-$Version-$Platform.zip\"")
	assertScriptOrder(t, script, "Build-CurrentFrontendApp", "Invoke-WindowsGoBuild -Output (Join-Path $Root 'bin/agent-terminal.exe') -Package './cmd/agent-terminal' -LdFlags $windowsGuiLdFlags")
}

func TestPackageWindowsScriptCopiesSQLiteRuntimeMigrations(t *testing.T) {
	script := readScript(t, "package_windows.ps1")

	assertScriptContains(t, script, "function Copy-SQLiteMigrations")
	assertScriptContains(t, script, "$source = Join-Path $Root 'internal/platform/db/sqlite/migrations'")
	assertScriptContains(t, script, "$destination = Join-Path $BundleRoot 'internal/platform/db/sqlite/migrations'")
	assertScriptContains(t, script, "missing SQLite migrations directory: $source")
	assertScriptContains(t, script, "missing SQLite migration files under $source")
	assertScriptContains(t, script, "Copy-SQLiteMigrations -BundleRoot $Stage")
	assertScriptDoesNotContain(t, script, "Copy-DirectoryClean -Source (Join-Path $Root 'migrations') -Destination (Join-Path $Stage 'migrations')")
	assertScriptOrder(t, script, "Copy-SQLiteMigrations -BundleRoot $Stage", "Write-RuntimeManifest -BundleRoot $Stage")
}

func TestPackageWindowsScriptUsesIncrementalBuildPhaseCache(t *testing.T) {
	script := readScript(t, "package_windows.ps1")

	assertScriptContains(t, script, "SUPER_DOLPHIN_SKIP_BUILD_CACHE")
	assertScriptContains(t, script, "$BuildCacheDir = Join-Path $Root '.build-cache/phases'")
	assertScriptContains(t, script, "function Get-BuildPhaseHash")
	assertScriptContains(t, script, "function Test-BuildPhaseCache")
	assertScriptContains(t, script, "function Save-BuildPhaseCache")
	assertScriptContains(t, script, "cache hit")

	assertScriptContains(t, script, "Test-BuildPhaseCache -Name 'frontend' -Paths @((Join-Path $Root 'frontend-app/src'), (Join-Path $Root 'frontend-app/package-lock.json'))")
	assertScriptContains(t, script, "Save-BuildPhaseCache")
	assertScriptOrder(t, script, "Test-BuildPhaseCache -Name 'frontend'", "& npm ci")
	assertScriptOrderAfter(t, script, "Test-BuildPhaseCache -Name 'frontend'", "& npm run build", "Save-BuildPhaseCache")

	assertScriptContains(t, script, "Test-BuildPhaseCache -Name 'go-binaries'")
	assertScriptContains(t, script, "(Join-Path $Root 'cmd'), (Join-Path $Root 'internal'), (Join-Path $Root 'pkg'), (Join-Path $Root 'go.sum')")
	assertScriptContains(t, script, "@('GOOS=windows', \"GOARCH=$WindowsPackageArch\"")
	assertScriptContains(t, script, "$windowsGuiLdFlags = '-H=windowsgui'")
	assertScriptContains(t, script, `"WINDOWS_GUI_LDFLAGS=$windowsGuiLdFlags"`)
	assertScriptOrder(t, script, "Test-BuildPhaseCache -Name 'go-binaries'", "Invoke-WindowsGoBuild -Output (Join-Path $Root 'bin/mcp-orch.exe') -Package './cmd/mcp-orch' -LdFlags $windowsGuiLdFlags")
	assertScriptOrderAfter(t, script, "Test-BuildPhaseCache -Name 'go-binaries'", "Invoke-WindowsGoBuild -Output (Join-Path $Root 'bin/mcp-ida.exe') -Package './cmd/mcp-ida' -LdFlags $windowsGuiLdFlags", "Save-BuildPhaseCache")

	assertScriptOrder(t, script, "Assert-WindowsNativeArchitecture -Path (Join-Path $Root 'bin/agent-terminal.exe')", "Copy-PackagedLSPBundle -BundleRoot $Stage")
	assertScriptOrder(t, script, "Copy-PackagedLSPBundle -BundleRoot $Stage", "Write-LSPManifest -BundleRoot $Stage")
}

func TestPackageWindowsBuildsDesktopExeWithoutConsoleWindow(t *testing.T) {
	script := readScript(t, "package_windows.ps1")

	assertScriptContains(t, script, "Invoke-WindowsGoBuild -Output (Join-Path $Root 'bin/agent-terminal.exe') -Package './cmd/agent-terminal' -LdFlags $windowsGuiLdFlags")
	assertScriptContains(t, script, "& go build -ldflags $LdFlags -o $Output $Package")
	assertScriptDoesNotContain(t, script, "Invoke-WindowsGoBuild -Output (Join-Path $Root 'bin/agent-terminal.exe') -Package './cmd/agent-terminal'\n")
}

func TestPackageWindowsBuildsSidecarExesWithoutConsoleWindow(t *testing.T) {
	script := readScript(t, "package_windows.ps1")

	for _, want := range []string{
		"Invoke-WindowsGoBuild -Output (Join-Path $Root 'bin/mcp-orch.exe') -Package './cmd/mcp-orch' -LdFlags $windowsGuiLdFlags",
		"Invoke-WindowsGoBuild -Output (Join-Path $Root 'bin/mcp-lsp.exe') -Package './cmd/mcp-lsp' -LdFlags $windowsGuiLdFlags",
		"Invoke-WindowsGoBuild -Output (Join-Path $Root 'bin/mcp-ida.exe') -Package './cmd/mcp-ida' -LdFlags $windowsGuiLdFlags",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptDoesNotContain(t, script, "Invoke-WindowsGoBuild -Output (Join-Path $Root 'bin/mcp-orch.exe') -Package './cmd/mcp-orch'\n")
	assertScriptDoesNotContain(t, script, "Invoke-WindowsGoBuild -Output (Join-Path $Root 'bin/mcp-lsp.exe') -Package './cmd/mcp-lsp'\n")
	assertScriptDoesNotContain(t, script, "Invoke-WindowsGoBuild -Output (Join-Path $Root 'bin/mcp-ida.exe') -Package './cmd/mcp-ida'\n")
}

func TestPackageWindowsScriptsSupportExplicitTargetArchitecture(t *testing.T) {
	for _, scriptPath := range []string{"package_windows.ps1", "package_windows_local.ps1", "prepare_lsp_bundle_windows.ps1"} {
		t.Run(scriptPath, func(t *testing.T) {
			script := readScript(t, scriptPath)

			assertScriptContains(t, script, "SUPER_DOLPHIN_WINDOWS_ARCH")
			assertScriptContains(t, script, "function Resolve-WindowsPackageArch")
			assertScriptContains(t, script, "unsupported SUPER_DOLPHIN_WINDOWS_ARCH")
			assertScriptContains(t, script, "$WindowsPackageArch = Resolve-WindowsPackageArch")
			assertScriptContains(t, script, "$Platform = \"$GoOS-$WindowsPackageArch\"")
			assertScriptContains(t, script, "$env:SUPER_DOLPHIN_WINDOWS_ARCH = $WindowsPackageArch")
		})
	}
}

func TestPackageWindowsScriptsProbeGitRootWithoutBlockingSourceArchiveFallback(t *testing.T) {
	for _, scriptPath := range []string{"package_windows.ps1", "package_windows_local.ps1", "prepare_lsp_bundle_windows.ps1"} {
		t.Run(scriptPath, func(t *testing.T) {
			script := readScript(t, scriptPath)

			assertScriptContains(t, script, "$previousErrorActionPreference = $ErrorActionPreference")
			assertScriptContains(t, script, "$ErrorActionPreference = 'Continue'")
			assertScriptContains(t, script, "$gitExitCode = $LASTEXITCODE")
			assertScriptContains(t, script, "$ErrorActionPreference = $previousErrorActionPreference")
			assertScriptContains(t, script, "if ($gitExitCode -eq 0 -and $root -and $root.Trim() -ne '')")
			assertScriptContains(t, script, "Join-Path $root 'frontend-app/package.json'")
		})
	}
}

func TestPackageWindowsScriptBuildsWithTargetGoArch(t *testing.T) {
	script := readScript(t, "package_windows.ps1")

	assertScriptContains(t, script, "function Invoke-WindowsGoBuild")
	assertScriptContains(t, script, "$oldGOOS = $env:GOOS")
	assertScriptContains(t, script, "$oldGOARCH = $env:GOARCH")
	assertScriptContains(t, script, "$env:GOOS = 'windows'")
	assertScriptContains(t, script, "$env:GOARCH = $WindowsPackageArch")
	assertScriptContains(t, script, "& go build -o $Output $Package")
	assertScriptContains(t, script, "& go build -ldflags $LdFlags -o $Output $Package")
}

func TestPackageWindowsScriptsValidatePENativeArchitecture(t *testing.T) {
	for _, scriptPath := range []string{"package_windows.ps1", "prepare_lsp_bundle_windows.ps1", "verify_packaged_app_windows.ps1"} {
		t.Run(scriptPath, func(t *testing.T) {
			script := readScript(t, scriptPath)

			assertScriptContains(t, script, "function Get-PEMachineType")
			assertScriptContains(t, script, "0xAA64")
			assertScriptContains(t, script, "0x8664")
			assertScriptContains(t, script, "function Assert-WindowsNativeArchitecture")
			assertScriptContains(t, script, "expected ${ExpectedArch}")
			assertScriptContains(t, script, "Assert-WindowsNativeArchitecture")
		})
	}
}

func TestPackageWindowsVerifiesArm64NativeArtifactsBeforePackaging(t *testing.T) {
	script := readScript(t, "package_windows.ps1")

	for _, want := range []string{
		"Assert-WindowsNativeArchitecture -Path (Join-Path $Root 'bin/agent-terminal.exe') -ExpectedArch $WindowsPackageArch -Label 'agent-terminal'",
		"Assert-WindowsNativeArchitecture -Path (Join-Path $Root 'bin/mcp-orch.exe') -ExpectedArch $WindowsPackageArch -Label 'mcp-orch'",
		"Assert-WindowsNativeArchitecture -Path (Join-Path $Root 'bin/mcp-lsp.exe') -ExpectedArch $WindowsPackageArch -Label 'mcp-lsp'",
		"Assert-WindowsNativeArchitecture -Path (Join-Path $Root 'bin/mcp-ida.exe') -ExpectedArch $WindowsPackageArch -Label 'mcp-ida'",
		"Assert-WindowsNativeArchitecture -Path $script:PackagedCodexArtifact -ExpectedArch $WindowsPackageArch -Label 'Codex CLI artifact'",
		"Assert-LSPBundleNativeArchitecture -BundleDir $script:PackagedLSPBundleDir",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptDoesNotContain(t, script, "PostgreSQL $bin")
}

func TestWindowsBackgroundProcessesHideConsoleWindows(t *testing.T) {
	codexWindows := readScript(t, "../internal/provider/codexapp/process_windows.go")
	toolbridgeWindows := readScript(t, "../internal/platform/toolbridge/stdio_process_windows.go")
	embeddedpgWindows := readScript(t, "../internal/platform/embeddedpg/runtime_process_windows.go")

	assertScriptContains(t, codexWindows, "CreationFlags: 0x08000200")
	assertScriptContains(t, codexWindows, "HideWindow: true")
	assertScriptContains(t, toolbridgeWindows, "stdioCreateNoWindow        = 0x08000000")
	assertScriptContains(t, toolbridgeWindows, "CreationFlags: stdioCreateNewProcessGroup | stdioCreateNoWindow")
	assertScriptContains(t, toolbridgeWindows, "HideWindow:    true")
	assertScriptContains(t, embeddedpgWindows, "postgresCreateNoWindow        = 0x08000000")
	assertScriptContains(t, embeddedpgWindows, "CreationFlags: postgresCreateNewProcessGroup | postgresCreateNoWindow")
	assertScriptContains(t, embeddedpgWindows, "HideWindow:    true")
}

func TestPackageWindowsScriptFindsInnoCompiler(t *testing.T) {
	script := readScript(t, "package_windows.ps1")

	assertScriptContains(t, script, "function Resolve-InnoCompiler")
	assertScriptContains(t, script, "INNO_SETUP_ISCC")
	assertScriptContains(t, script, "Inno Setup 6\\ISCC.exe")
	assertScriptContains(t, script, "Inno Setup 7\\ISCC.exe")
	assertScriptContains(t, script, "Windows installer skipped: Inno Setup iscc.exe not found")
	assertScriptContains(t, script, "Windows installer ready")
	assertScriptContains(t, script, "& $iscc '/Qp' $iss")
	assertScriptOrder(t, script, "function Resolve-InnoCompiler", "function Try-BuildInstaller")
	assertScriptOrder(t, script, "New-WindowsZip -Stage $Stage -ZipPath $zipPath", "Try-BuildInstaller -Stage $Stage -Dist $dist")
}

func TestPackageWindowsScriptSupportsSelectiveArtifactsAndCleansStage(t *testing.T) {
	script := readScript(t, "package_windows.ps1")

	assertScriptContains(t, script, "[ValidateSet('all', 'installer', 'zip')]")
	assertScriptContains(t, script, "SUPER_DOLPHIN_WINDOWS_OUTPUT")
	assertScriptContains(t, script, "[switch]$KeepStage")
	assertScriptContains(t, script, "SUPER_DOLPHIN_WINDOWS_KEEP_STAGE")
	assertScriptContains(t, script, "$Artifact -in @('all', 'zip')")
	assertScriptContains(t, script, "$Artifact -in @('all', 'installer')")
	assertScriptContains(t, script, "$Artifact -in @('all', 'zip') -and (Test-Path -LiteralPath $zipPath)")
	assertScriptContains(t, script, "$Artifact -in @('all', 'installer') -and (Test-Path -LiteralPath $setupPath)")
	assertScriptContains(t, script, "New-WindowsZip -Stage $Stage -ZipPath $zipPath")
	assertScriptContains(t, script, "Windows zip ready")
	assertScriptContains(t, script, "Windows package stage kept")
	assertScriptContains(t, script, "Windows package stage cleaned")
	assertScriptContains(t, script, "Windows package artifacts ready under")
	assertScriptOrder(t, script, "verify_packaged_app_windows.ps1') $Stage", "if ($Artifact -in @('all', 'zip')) {")
	assertScriptOrder(t, script, "if ($Artifact -in @('all', 'zip')) {", "if ($Artifact -in @('all', 'installer')) {")
	assertScriptOrder(t, script, "if ($Artifact -in @('all', 'installer')) {", "Windows package stage cleaned")
}

func TestPackageWindowsInstallerShortcutsLaunchDesktopExeDirectly(t *testing.T) {
	script := readScript(t, "package_windows.ps1")

	assertScriptContains(t, script, `Name: "{autoprograms}\Super Dolphin"; Filename: "{app}\bin\agent-terminal.exe"; WorkingDir: "{app}"`)
	assertScriptContains(t, script, `Name: "{autodesktop}\Super Dolphin"; Filename: "{app}\bin\agent-terminal.exe"; WorkingDir: "{app}"; Tasks: desktopicon`)
	assertScriptDoesNotContain(t, script, `Name: "{autoprograms}\Super Dolphin"; Filename: "{app}\run.cmd"`)
	assertScriptDoesNotContain(t, script, `Name: "{autodesktop}\Super Dolphin"; Filename: "{app}\run.cmd"; Tasks: desktopicon`)
}

func TestPackageWindowsScriptWritesRuntimeManifestContract(t *testing.T) {
	script := readScript(t, "package_windows.ps1")
	body := functionBody(t, script, "Write-RuntimeManifest")

	assertScriptContains(t, script, "Write-RuntimeManifest -BundleRoot $Stage")
	assertScriptContains(t, body, "bundled_codex_path = 'bin/codex.exe'")
	assertScriptContains(t, body, "bundled_gopls_path = 'bin/gopls.exe'")
	assertScriptContains(t, body, "lsp_bundle_path = 'lsp'")
	assertScriptContains(t, body, "lsp_manifest_path = 'lsp/lsp-manifest.json'")
	assertScriptContains(t, body, "model_registry_path = 'models.yaml'")
	assertScriptDoesNotContain(t, body, "embedded_postgres_resource_path")
	assertScriptOrder(t, script, "Write-RuntimeManifest -BundleRoot $Stage", "New-WindowsZip -Stage $Stage -ZipPath $zipPath")
}

func TestPackageWindowsScriptBundlesVerifiedCodexAndLSP(t *testing.T) {
	script := readScript(t, "package_windows.ps1")

	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_ARTIFACT")
	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_SHA256")
	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_VERSION")
	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF")
	assertScriptContains(t, script, "$CodexRelayBootstrapProofEnv=$PackagedRelayBootstrapProof")
	assertScriptContains(t, script, "SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX")
	assertScriptContains(t, script, "packaged Codex CLI artifact is required")
	assertScriptContains(t, script, "Codex CLI artifact checksum mismatch")
	assertScriptContains(t, script, "Copy-PackagedCodex -BundleRoot $Stage -Destination (Join-Path $Stage 'bin/codex.exe')")
	assertScriptContains(t, script, "Write-CodexManifest -BundleRoot $Stage")
	assertScriptContains(t, script, "SUPER_DOLPHIN_LSP_BUNDLE_DIR")
	assertScriptContains(t, script, "lsp-manifest.json")
	assertScriptContains(t, script, "lsp-checksums.sha256")
	assertScriptContains(t, script, "packaged LSP bundle checksum mismatch")
	for _, want := range []string{
		"gopls|bin/gopls.exe|gopls.exe",
		"typescript-language-server|bin/typescript-language-server.cmd|typescript-language-server.cmd",
		"vscode-langservers-extracted|bin/vscode-css-language-server.cmd|vscode-css-language-server.cmd",
		"pyright|bin/pyright-langserver.cmd|pyright-langserver.cmd",
		"rust-analyzer|bin/rust-analyzer.exe|rust-analyzer.exe",
		"bash-language-server|bin/bash-language-server.cmd|bash-language-server.cmd",
		"shellcheck|bin/shellcheck.exe|shellcheck.exe",
		"sg|bin/sg.exe|sg.exe",
		"go|bin/go.cmd|go.cmd",
		"java|bin/java.cmd|java.cmd",
		"jdtls|bin/jdtls.cmd|jdtls.cmd",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptOrder(t, script, "Resolve-PackagedCodexArtifact", "New-Item -ItemType Directory -Force -Path (Join-Path $Stage 'bin')")
	assertScriptOrder(t, script, "Copy-PackagedLSPBundle -BundleRoot $Stage", "Write-LSPManifest -BundleRoot $Stage")
}

func TestPackageWindowsScriptSupportsPackagedVideoAPIKey(t *testing.T) {
	script := readScript(t, "package_windows.ps1")

	assertScriptContains(t, script, "$VideoAPIKeyEnv = 'SILICONFLOW_API_KEY'")
	assertScriptContains(t, script, "function Resolve-PackagedVideoEnv")
	assertScriptContains(t, script, "Validate-EnvFileValue -Label $VideoAPIKeyEnv -Value $script:PackagedVideoAPIKey")
	assertScriptContains(t, script, "$contentLines.Add(\"$VideoAPIKeyEnv=$PackagedVideoAPIKey\")")
	assertScriptOrder(t, script, "Resolve-PackagedVideoEnv", "Write-PackagedRelayEnv -BundleRoot $Stage")
	assertScriptOrder(t, script, "Write-PackagedRelayEnv -BundleRoot $Stage", "Write-RuntimeManifest -BundleRoot $Stage")
}

func TestPackageWindowsScriptBundlesFFmpegForVideoTools(t *testing.T) {
	script := readScript(t, "package_windows.ps1")
	verify := readScript(t, "verify_packaged_app_windows.ps1")

	for _, want := range []string{
		"$FFmpegBinEnv = 'SUPER_DOLPHIN_FFMPEG_BIN'",
		"$PackagedFFmpegRuntimeDir = ''",
		"function Resolve-PackagedFFmpeg",
		"function Copy-PackagedFFmpeg",
		"ffmpeg.exe",
		"Copy-Item -LiteralPath $PackagedFFmpegBin -Destination $dest -Force",
		"Get-ChildItem -LiteralPath $script:PackagedFFmpegRuntimeDir -Filter '*.dll' -File",
		"Copy-Item -LiteralPath $_.FullName -Destination $binDir -Force",
		"Assert-WindowsNativeArchitecture -Path $_.FullName -ExpectedArch $WindowsPackageArch -Label 'ffmpeg runtime DLL'",
		"Assert-WindowsNativeArchitecture -Path $script:PackagedFFmpegBin -ExpectedArch $WindowsPackageArch -Label 'ffmpeg'",
		"Copy-PackagedFFmpeg -BundleRoot $Stage",
		"set \"FFMPEG_PATH=%here%\\bin\\ffmpeg.exe\"",
		"$env:FFMPEG_PATH = Join-Path $Here 'bin\\ffmpeg.exe'",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptOrder(t, script, "Resolve-PackagedFFmpeg", "New-Item -ItemType Directory -Force -Path (Join-Path $Stage 'bin')")
	assertScriptOrder(t, script, "Copy-Item -LiteralPath $_.FullName -Destination $binDir -Force", "& $dest -version *> $null")
	assertScriptOrder(t, script, "Copy-PackagedFFmpeg -BundleRoot $Stage", "Write-RuntimeManifest -BundleRoot $Stage")
	assertScriptContains(t, verify, "function Verify-FFmpeg")
	assertScriptContains(t, verify, "bin/ffmpeg.exe")
	assertScriptContains(t, verify, "packaged ffmpeg smoke verified")
}

func TestPackageWindowsLocalScriptChecksHostFFmpegDependency(t *testing.T) {
	script := readScript(t, "package_windows_local.ps1")

	for _, want := range []string{
		"SUPER_DOLPHIN_FFMPEG_BIN",
		"$FFmpegBin = if ($env:SUPER_DOLPHIN_FFMPEG_BIN) { $env:SUPER_DOLPHIN_FFMPEG_BIN } else { Get-CommandSource 'ffmpeg.exe' }",
		"missing ffmpeg.exe; install ffmpeg or set SUPER_DOLPHIN_FFMPEG_BIN",
		"$env:SUPER_DOLPHIN_FFMPEG_BIN = $FFmpegBin",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptDoesNotContain(t, script, "SUPER_DOLPHIN_POSTGRES_DIST")
	assertScriptOrder(t, script, "$env:SUPER_DOLPHIN_FFMPEG_BIN = $FFmpegBin", "scripts/package_windows.ps1') @packageArgs")
}

func TestPackageWindowsRunScriptsAdvertisePackagedRuntime(t *testing.T) {
	script := readScript(t, "package_windows.ps1")

	assertScriptContains(t, script, "SUPER_DOLPHIN_PACKAGE_ROOT=%here%")
	assertScriptContains(t, script, "PROJECT_ROOT=%here%")
	assertScriptContains(t, script, "SUPER_DOLPHIN_MODEL_REGISTRY=%here%\\models.yaml")
	assertScriptDoesNotContain(t, script, "SUPER_DOLPHIN_POSTGRES_BIN_DIR")
	assertScriptContains(t, script, "$runCmd = $runCmd.Replace('__PLATFORM__', $Platform)")
	assertScriptContains(t, script, "$runPs1 = $runPs1.Replace('__PLATFORM__', $Platform)")
	assertScriptContains(t, script, "GO_AGENT_PEER_BIN_DIR=%here%\\bin")
	assertScriptContains(t, script, "SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX=1")
	assertScriptContains(t, script, "SUPER_DOLPHIN_LSP_BUNDLE_DIR=%here%\\lsp")
	assertScriptContains(t, script, "SUPER_DOLPHIN_LSP_MANIFEST=%here%\\lsp\\lsp-manifest.json")
	assertScriptContains(t, script, "SUPER_DOLPHIN_RUNTIME_MODE=packaged")
	assertScriptContains(t, script, "SUPER_DOLPHIN_PACKAGED_LAUNCHER=1")
	assertScriptContains(t, script, "$requiredExecutables += $spec.Split('|')[2]")
	assertScriptContains(t, script, "__REQUIRED_EXES__")
	assertScriptContains(t, script, "$runCmd = $runCmd.Replace('__REQUIRED_EXES__', $requiredExecutableList)")
	assertScriptContains(t, script, "agent-terminal.exe")
}

func TestPackageWindowsPreservesFullLSPProfileForVerification(t *testing.T) {
	script := readScript(t, "package_windows.ps1")

	assertScriptContains(t, script, "profile = $LSPProfile")
	assertScriptContains(t, script, "java|bin/java.cmd|java.cmd")
	assertScriptContains(t, script, "jdtls|bin/jdtls.cmd|jdtls.cmd")
	assertScriptOrder(t, script, "profile = $LSPProfile", "Write-Utf8NoBom -Path $sourceManifestPath")
}

func TestPackageWindowsCanExplicitlyOmitShellcheckForTemporaryArm64RunnablePackage(t *testing.T) {
	for _, scriptPath := range []string{"package_windows.ps1", "verify_packaged_app_windows.ps1"} {
		t.Run(scriptPath, func(t *testing.T) {
			script := readScript(t, scriptPath)

			assertScriptContains(t, script, "SUPER_DOLPHIN_WINDOWS_OMIT_SHELLCHECK")
			assertScriptContains(t, script, "shellcheck|bin/shellcheck.exe")
		})
	}
}

func TestPackageWindowsValidatesLSPManifestLanguages(t *testing.T) {
	script := readScript(t, "package_windows.ps1")

	assertScriptContains(t, script, "function Convert-LSPLanguagesArray()")
	assertScriptContains(t, script, "} elseif ($Value -is [string]) {")
	assertScriptContains(t, script, "languages = @($languages)")
	assertScriptContains(t, script, "must be a JSON array")
	assertScriptContains(t, script, "must be a non-empty JSON array")
	assertScriptContains(t, script, "Convert-LSPLanguagesArray -Value $languagesValue -Context $serverId")
}

func TestVerifyPackagedAppWindowsScriptContracts(t *testing.T) {
	script := readScript(t, "verify_packaged_app_windows.ps1")

	assertScriptContains(t, script, "runtime-manifest.json")
	assertScriptContains(t, script, "codex-manifest.json")
	assertScriptContains(t, script, "lsp/lsp-manifest.json")
	assertScriptContains(t, script, "internal/platform/db/sqlite/migrations")
	assertScriptContains(t, script, "missing SQLite migration files under")
	assertScriptContains(t, script, "Verify-RuntimeManifest")
	assertScriptContains(t, script, "Verify-CodexManifest")
	assertScriptContains(t, script, "Verify-LSPManifest")
	assertScriptContains(t, script, "Get-FileHash -Algorithm SHA256")
	assertScriptContains(t, script, "bin/codex.exe")
	assertScriptContains(t, script, "bin/gopls.exe")
	assertScriptContains(t, script, "$WindowsPackagePlatform")
	assertScriptContains(t, script, "codex.exe app-server --help")
	assertScriptContains(t, script, "LSP server smoke verified")
	assertScriptContains(t, script, "SUPER_DOLPHIN_WINDOWS_PACKAGE_PLATFORM")
	assertScriptContains(t, script, "package root name ending in windows-amd64 or windows-arm64")
	assertScriptContains(t, script, "Infer-WindowsPackagePlatform")
	assertScriptDoesNotContain(t, script, "embedded_postgres_resource_path")
	assertScriptDoesNotContain(t, script, "postgres.bki")
	assertScriptDoesNotContain(t, script, "Join-Path $PackageRoot 'migrations'")
	assertScriptContains(t, script, "ast-grep.exe")
	assertScriptContains(t, script, "vcruntime140.dll")
	assertScriptContains(t, script, "function Assert-JsonStringArray()")
	assertScriptContains(t, script, "} elseif ($Value -is [string]) {")
	assertScriptContains(t, script, "LSP server $serverId languages")
	assertScriptContains(t, script, "SUPER_DOLPHIN_WINDOWS_ARCH")
	assertScriptContains(t, script, "SUPER_DOLPHIN_WINDOWS_ARCH=$configuredArch conflicts with Windows package platform $script:WindowsPackagePlatform")
	assertScriptContains(t, script, "Assert-PackageNativeArchitecture")
	assertScriptContains(t, script, "$FullLSPServerSpecs = @(")
	assertScriptContains(t, script, "java|bin/java.cmd")
	assertScriptContains(t, script, "jdtls|bin/jdtls.cmd")
	assertScriptContains(t, script, "function Expected-LSPServerSpecs()")
	assertScriptContains(t, script, "$profile.Trim() -eq 'full'")
	assertScriptContains(t, script, "Expected-LSPServerSpecs -Manifest $manifest")
}

func TestVerifyPackagedAppWindowsUsesTarForZipExtraction(t *testing.T) {
	script := readScript(t, "verify_packaged_app_windows.ps1")

	assertScriptContains(t, script, "tar.exe -xf")
	assertScriptContains(t, script, "zip extraction failed")
	assertScriptDoesNotContain(t, script, "Expand-Archive -LiteralPath")
}

func TestPackageWindowsLocalHelperContracts(t *testing.T) {
	script := readScript(t, "package_windows_local.ps1")

	assertScriptContains(t, script, "Resolve-RepoRoot")
	assertScriptContains(t, script, "keep package_windows_local.ps1 under <repo>\\scripts")
	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN is required")
	assertScriptContains(t, script, "SUPER_DOLPHIN_CODEX_RELAY_API_KEY must not be set for packaging")
	assertScriptContains(t, script, "SUPER_DOLPHIN_LSP_PROFILE")
	assertScriptContains(t, script, "prepare_lsp_bundle_windows.ps1")
	assertScriptContains(t, script, "package_windows.ps1")
	assertScriptContains(t, script, "[ValidateSet('all', 'installer', 'zip')]")
	assertScriptContains(t, script, "SUPER_DOLPHIN_WINDOWS_OUTPUT")
	assertScriptContains(t, script, "SUPER_DOLPHIN_WINDOWS_KEEP_STAGE")
	assertScriptContains(t, script, "$RequestedWindowsOutput = $Artifact")
	assertScriptContains(t, script, "$RequestedKeepStage = $KeepStage.IsPresent")
	assertScriptContains(t, script, "$packageArgs = @{ Artifact = $RequestedWindowsOutput }")
	assertScriptContains(t, script, "$packageArgs.KeepStage = $true")
	assertScriptContains(t, script, "scripts/package_windows.ps1') @packageArgs")
	assertScriptContains(t, script, "Get-FileHash -Algorithm SHA256")
	assertScriptDoesNotContain(t, script, "SUPER_DOLPHIN_POSTGRES_DIST")
}

func TestPackageWindowsLocalFastModeOptimizesRepeatPackaging(t *testing.T) {
	script := readScript(t, "package_windows_local.ps1")

	for _, want := range []string{
		"[switch]$Fast",
		"SUPER_DOLPHIN_WINDOWS_FAST",
		"SUPER_DOLPHIN_REUSE_LSP_BUNDLE",
		"SUPER_DOLPHIN_PACKAGE_CACHE_DIR",
		"SUPER_DOLPHIN_WINDOWS_INSTALLER_COMPRESSION",
		"SUPER_DOLPHIN_WINDOWS_INSTALLER_SOLID_COMPRESSION",
		"$WindowsOutputConfigured = $PSBoundParameters.ContainsKey('Artifact') -or",
		"$FastMode = $Fast.IsPresent -or (Test-TruthyEnv 'SUPER_DOLPHIN_WINDOWS_FAST')",
		"if ($FastMode -and -not $WindowsOutputConfigured)",
		"$RequestedWindowsOutput = 'installer'",
		"$RequestedKeepStage = $true",
		"$env:SUPER_DOLPHIN_REUSE_LSP_BUNDLE = '1'",
		"$env:SUPER_DOLPHIN_WINDOWS_INSTALLER_COMPRESSION = 'zip'",
		"$env:SUPER_DOLPHIN_WINDOWS_INSTALLER_SOLID_COMPRESSION = 'no'",
		"fast Windows packaging enabled",
		"function Resolve-LSPBundleDir",
		"function Test-ExistingLSPBundle",
		"lsp-manifest.json",
		"lsp-checksums.sha256",
		"node/node.exe",
		"bin/gopls.exe",
		"bin/typescript-language-server.cmd",
		"bin/pyright-langserver.cmd",
		"bin/rust-analyzer.exe",
		"bin/sg.exe",
		"bin/go.cmd",
		"bin/shellcheck.exe",
		"bin/java.cmd",
		"bin/jdtls.cmd",
		"reusing existing Windows LSP bundle",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptOrder(t, script, "Test-ExistingLSPBundle -Path $lspDir -Profile $Profile", "prepare_lsp_bundle_windows.ps1")
	assertScriptOrder(t, script, "Resolve-LSPBundleDir -Profile $Profile", "$env:SUPER_DOLPHIN_LSP_BUNDLE_DIR = $lspDir")
	assertScriptOrder(t, script, "$env:SUPER_DOLPHIN_WINDOWS_INSTALLER_COMPRESSION = 'zip'", "scripts/package_windows.ps1') @packageArgs")
}
