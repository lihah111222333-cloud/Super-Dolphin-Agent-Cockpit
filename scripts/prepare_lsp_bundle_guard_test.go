package main

import (
	"strings"
	"testing"
)

func TestPrepareLSPBundleScriptsInstallAstGrepCLI(t *testing.T) {
	for _, scriptPath := range []string{"prepare_lsp_bundle_macos.sh", "prepare_lsp_bundle_linux.sh"} {
		t.Run(scriptPath, func(t *testing.T) {
			script := readScript(t, scriptPath)

			assertScriptContains(t, script, "@ast-grep/cli")
			assertScriptContains(t, script, "write_path_wrapper sg node_modules/.bin/sg")
			assertScriptContains(t, script, "test -x \"$lsp_dir/$target\"")
			assertScriptOrder(t, script, "@ast-grep/cli", "write_path_wrapper sg node_modules/.bin/sg")
		})
	}
}

func TestPrepareLSPBundleScriptsInstallBashLanguageServer(t *testing.T) {
	for _, scriptPath := range []string{"prepare_lsp_bundle_macos.sh", "prepare_lsp_bundle_linux.sh"} {
		t.Run(scriptPath, func(t *testing.T) {
			script := readScript(t, scriptPath)

			assertScriptContains(t, script, "bash-language-server")
			assertScriptDoesNotContain(t, script, "node_modules/.bin/bash-language-server")
			assertScriptContains(t, script, "write_path_wrapper bash-language-server node_modules/bash-language-server/out/cli.js")
			assertScriptOrder(t, script, "bash-language-server", "write_path_wrapper bash-language-server node_modules/bash-language-server/out/cli.js")
		})
	}
}

func TestPrepareLSPBundleScriptsInstallShellcheckForShellDiagnostics(t *testing.T) {
	for _, scriptPath := range []string{"prepare_lsp_bundle_macos.sh", "prepare_lsp_bundle_linux.sh"} {
		t.Run(scriptPath, func(t *testing.T) {
			script := readScript(t, scriptPath)

			assertScriptContains(t, script, "shellcheck")
			assertScriptContains(t, script, "\"$lsp_dir/node_modules/.bin/shellcheck\" --version")
			assertScriptContains(t, script, "write_path_wrapper shellcheck node_modules/shellcheck/bin/shellcheck")
			assertScriptOrder(t, script, "shellcheck", "write_path_wrapper shellcheck node_modules/shellcheck/bin/shellcheck")
			assertScriptOrder(t, script, "\"$lsp_dir/node_modules/.bin/shellcheck\" --version", "write_path_wrapper shellcheck node_modules/shellcheck/bin/shellcheck")
		})
	}
}

func TestPrepareLSPBundleScriptsIncludeAstGrepInManifestAndChecksums(t *testing.T) {
	for _, scriptPath := range []string{"prepare_lsp_bundle_macos.sh", "prepare_lsp_bundle_linux.sh"} {
		t.Run(scriptPath, func(t *testing.T) {
			script := readScript(t, scriptPath)

			assertScriptContains(t, script, "'sg|bin/sg|[\"ast-grep\"]'")
			assertScriptContains(t, script, "> \"$lsp_dir/lsp-manifest.json\"")
			assertScriptContains(t, script, "> \"$lsp_dir/lsp-checksums.sha256\"")
		})
	}
}

func TestPrepareLSPBundleScriptsIncludeBashLanguageServerInManifestAndChecksums(t *testing.T) {
	for _, scriptPath := range []string{"prepare_lsp_bundle_macos.sh", "prepare_lsp_bundle_linux.sh"} {
		t.Run(scriptPath, func(t *testing.T) {
			script := readScript(t, scriptPath)

			assertScriptContains(t, script, "'bash-language-server|bin/bash-language-server|[\"shellscript\"]'")
			assertScriptContains(t, script, "> \"$lsp_dir/lsp-manifest.json\"")
			assertScriptContains(t, script, "> \"$lsp_dir/lsp-checksums.sha256\"")
		})
	}
}

func TestPrepareLSPBundleScriptsIncludeShellcheckInManifestAndChecksums(t *testing.T) {
	for _, scriptPath := range []string{"prepare_lsp_bundle_macos.sh", "prepare_lsp_bundle_linux.sh"} {
		t.Run(scriptPath, func(t *testing.T) {
			script := readScript(t, scriptPath)

			assertScriptContains(t, script, "'shellcheck|bin/shellcheck|[\"shellcheck\"]'")
			assertScriptContains(t, script, "> \"$lsp_dir/lsp-manifest.json\"")
			assertScriptContains(t, script, "> \"$lsp_dir/lsp-checksums.sha256\"")
		})
	}
}

func TestPrepareLSPBundleWindowsWritesManifestLanguagesAsArrays(t *testing.T) {
	script := readScript(t, "prepare_lsp_bundle_windows.ps1")

	assertScriptContains(t, script, "[string[]](ConvertFrom-Json -InputObject $parts[2])")
	assertScriptContains(t, script, "LSP manifest languages for $serverId must be a non-empty JSON array")
	assertScriptDoesNotContain(t, script, "$languages = $parts[2] | ConvertFrom-Json")
}

func TestPrepareLSPBundleMacOSStandardProfileExcludesJDTLSManifestEntry(t *testing.T) {
	script := readScript(t, "prepare_lsp_bundle_macos.sh")
	standardStart := strings.Index(script, "lsp_specs=(")
	if standardStart < 0 {
		t.Fatal("prepare_lsp_bundle_macos.sh missing lsp_specs")
	}
	fullStart := strings.Index(script, "if [[ \"$lsp_profile\" == \"full\" ]]; then\n  lsp_specs+=")
	if fullStart < 0 {
		t.Fatal("prepare_lsp_bundle_macos.sh missing full profile lsp_specs append")
	}
	standardSpecs := script[standardStart:fullStart]

	assertScriptContains(t, script, "lsp_profile=\"${SUPER_DOLPHIN_LSP_PROFILE:-standard}\"")
	assertScriptContains(t, script, "lsp_specs+=('jdtls|bin/jdtls|[\"java\"]')")
	assertScriptDoesNotContain(t, standardSpecs, "jdtls")
}

func TestPrepareLSPBundleScriptsDoNotInvokeHostPython3(t *testing.T) {
	for _, scriptPath := range []string{"prepare_lsp_bundle_macos.sh", "prepare_lsp_bundle_linux.sh"} {
		t.Run(scriptPath, func(t *testing.T) {
			script := readScript(t, scriptPath)

			assertScriptDoesNotContain(t, script, "command -v python3")
			assertScriptDoesNotContain(t, script, " python3 -")
			assertScriptContains(t, script, "rm -f \"$lsp_dir/jdtls/bin/jdtls\"")
		})
	}
}

func TestPrepareLSPBundleScriptsShadowSystemPythonFallbacks(t *testing.T) {
	for _, scriptPath := range []string{"prepare_lsp_bundle_macos.sh", "prepare_lsp_bundle_linux.sh"} {
		t.Run(scriptPath, func(t *testing.T) {
			script := readScript(t, scriptPath)

			assertScriptContains(t, script, "write_no_system_python_stub()")
			assertScriptContains(t, script, "write_no_system_python_stub python")
			assertScriptContains(t, script, "write_no_system_python_stub python3")
			assertScriptContains(t, script, "Packaged Super Dolphin does not bundle a Python interpreter")
			assertScriptOrder(t, script, "write_no_system_python_stub python", "write_node_wrapper pyright-langserver")
		})
	}
}

func TestPrepareLSPBundleScriptsPruneRuntimeOnlyArtifacts(t *testing.T) {
	for _, scriptPath := range []string{"prepare_lsp_bundle_macos.sh", "prepare_lsp_bundle_linux.sh"} {
		t.Run(scriptPath, func(t *testing.T) {
			script := readScript(t, scriptPath)

			assertScriptContains(t, script, "prune_lsp_bundle_runtime_only_artifacts()")
			assertScriptContains(t, script, "rm -rf \"$lsp_dir/jdk/jmods\"")
			assertScriptContains(t, script, "rm -rf \"$lsp_dir/jdk/demo\"")
			assertScriptContains(t, script, "rm -rf \"$lsp_dir/jdk/include\"")
			assertScriptContains(t, script, "rm -rf \"$lsp_dir/node_modules/@ast-grep/cli-\"*")
			assertScriptContains(t, script, "rm -f \"$lsp_dir/node_modules/@ast-grep/cli/ast-grep\"")
			assertScriptContains(t, script, "test -x \"$lsp_dir/node_modules/@ast-grep/cli/sg\"")
			assertScriptOrder(t, script, "prune_lsp_bundle_runtime_only_artifacts", "echo \"==> writing LSP manifest and checksums\"")
		})
	}
}

func TestPrepareLSPBundleScriptsEmbedGoToolchainForGopls(t *testing.T) {
	for _, scriptPath := range []string{"prepare_lsp_bundle_macos.sh", "prepare_lsp_bundle_linux.sh"} {
		t.Run(scriptPath, func(t *testing.T) {
			script := readScript(t, scriptPath)

			assertScriptContains(t, script, "go_toolchain_src=")
			assertScriptContains(t, script, "copy_go_toolchain()")
			assertScriptContains(t, script, "rsync -a --delete \"$go_toolchain_src/\" \"$lsp_dir/go/\"")
			assertScriptContains(t, script, "write_go_toolchain_wrapper()")
			assertScriptContains(t, script, "while [[ -L \"$source\" ]]")
			assertScriptContains(t, script, "target=\"$(readlink \"$source\")\"")
			assertScriptContains(t, script, "export GOROOT=\"$here/../go\"")
			assertScriptContains(t, script, "exec \"$GOROOT/bin/go\" \"$@\"")
			assertScriptContains(t, script, "env GOROOT")
			assertScriptContains(t, script, "'go|bin/go|[\"go-toolchain\"]'")
			assertScriptOrder(t, script, "copy_go_toolchain", "write_go_toolchain_wrapper")
			assertScriptOrder(t, script, "write_go_toolchain_wrapper", "echo \"==> writing LSP manifest and checksums\"")
		})
	}
}

func TestPrepareLSPBundleMacOSResolvesHostToolDefaultsDynamically(t *testing.T) {
	script := readScript(t, "prepare_lsp_bundle_macos.sh")

	assertScriptContains(t, script, "default_node_dist=\"$(node -p 'require(\"path\").dirname(require(\"path\").dirname(process.execPath))' 2>/dev/null || true)\"")
	assertScriptContains(t, script, "node_src=\"${SUPER_DOLPHIN_NODE_DIST:-$default_node_dist}\"")
	assertScriptContains(t, script, "gopls_bin=\"${SUPER_DOLPHIN_GOPLS_BIN:-$(command -v gopls || true)}\"")
	assertScriptContains(t, script, "go_toolchain_src=\"${SUPER_DOLPHIN_GO_TOOLCHAIN_DIR:-$(go env GOROOT)}\"")
	assertScriptContains(t, script, "resolve_rust_analyzer_bin()")
	assertScriptContains(t, script, "rust_analyzer_bin=\"$(resolve_rust_analyzer_bin \"${SUPER_DOLPHIN_RUST_ANALYZER_BIN:-}\")\"")
	assertScriptContains(t, script, "rust-analyzer resolves to rustup shim without a default toolchain")
	assertScriptDoesNotContain(t, script, "/Users/ai/.local/node")
	assertScriptDoesNotContain(t, script, "/Users/ai/.local/go")
}

func TestPrepareLSPBundleScriptsExposeBundledJavaRuntime(t *testing.T) {
	for _, scriptPath := range []string{"prepare_lsp_bundle_macos.sh", "prepare_lsp_bundle_linux.sh"} {
		t.Run(scriptPath, func(t *testing.T) {
			script := readScript(t, scriptPath)

			assertScriptContains(t, script, "write_java_runtime_wrapper()")
			assertScriptContains(t, script, "export JAVA_HOME=\"$here/../jdk\"")
			assertScriptContains(t, script, "exec \"$JAVA_HOME/bin/java\" \"$@\"")
			assertScriptContains(t, script, "\"$lsp_dir/bin/java\" -version")
			assertScriptOrder(t, script, "write_java_runtime_wrapper", "echo \"==> writing LSP manifest and checksums\"")
		})
	}
}

func TestPrepareLSPBundleJDTLSWrapperDoesNotExecHomebrewPythonScript(t *testing.T) {
	for _, scriptPath := range []string{"prepare_lsp_bundle_macos.sh", "prepare_lsp_bundle_linux.sh"} {
		t.Run(scriptPath, func(t *testing.T) {
			script := readScript(t, scriptPath)

			assertScriptContains(t, script, "-Dosgi.sharedConfiguration.area=")
			assertScriptContains(t, script, "org.eclipse.equinox.launcher_*.jar")
			assertScriptContains(t, script, "exec \"$JAVA_HOME/bin/java\"")
			assertScriptDoesNotContain(t, script, "exec \"$here/../jdtls/bin/jdtls\"")
		})
	}
}

func TestPrepareLSPBundleWindowsScriptContracts(t *testing.T) {
	script := readScript(t, "prepare_lsp_bundle_windows.ps1")

	assertScriptContains(t, script, "prepare_lsp_bundle_windows.ps1 must run on Windows")
	assertScriptContains(t, script, "Resolve-RepoRoot")
	assertScriptContains(t, script, "keep prepare_lsp_bundle_windows.ps1 under <repo>\\scripts")
	assertScriptContains(t, script, "$GoOS -ne 'windows'")
	assertScriptContains(t, script, "node.exe")
	assertScriptContains(t, script, "& $NpmBin install --prefix $LspDir @LSPNpmPackages")
	assertScriptContains(t, script, "typescript-language-server")
	assertScriptContains(t, script, "vscode-langservers-extracted")
	assertScriptContains(t, script, "pyright")
	assertScriptContains(t, script, "bash-language-server")
	assertScriptContains(t, script, "shellcheck")
	assertScriptContains(t, script, "@ast-grep/cli")
	assertScriptContains(t, script, "Write-NodeCmdWrapper -Name 'typescript-language-server.cmd'")
	assertScriptContains(t, script, "Write-NodeCmdWrapper -Name 'vscode-css-language-server.cmd'")
	assertScriptContains(t, script, "Write-NodeCmdWrapper -Name 'pyright-langserver.cmd'")
	assertScriptContains(t, script, "Write-NodeCmdWrapper -Name 'bash-language-server.cmd'")
	assertScriptContains(t, script, "node_modules/shellcheck/bin/shellcheck.js")
	assertScriptContains(t, script, "shellcheck npm launcher failed to prepare bundled executable")
	assertScriptContains(t, script, "SUPER_DOLPHIN_SHELLCHECK_BIN")
	assertScriptContains(t, script, "SUPER_DOLPHIN_WINDOWS_OMIT_SHELLCHECK")
	assertScriptContains(t, script, "shellcheck will not be included")
	assertScriptContains(t, script, "missing ARM64 shellcheck.exe; shellcheck npm package does not publish win32-arm64")
	assertScriptContains(t, script, "$shellcheck = Resolve-ShellcheckExecutable")
	assertScriptOrder(t, script, "function Resolve-ShellcheckExecutable", "$shellcheck = Resolve-ShellcheckExecutable")
	assertScriptContains(t, script, "SUPER_DOLPHIN_MSVC_RUNTIME_DIR")
	assertScriptContains(t, script, "node_modules/@ast-grep/cli/ast-grep.exe")
	assertScriptContains(t, script, "bin/ast-grep.exe")
	assertScriptContains(t, script, "vcruntime140.dll")
	assertScriptContains(t, script, "bundled ast-grep smoke failed")
	assertScriptOrder(t, script, "node_modules/@ast-grep/cli/sg.exe", "node_modules/@ast-grep/cli/ast-grep.exe")
	assertScriptOrder(t, script, "Copy-Item -LiteralPath $astGrep", "Copy-Item -LiteralPath $vcruntime140")
	assertScriptContains(t, script, "Write-GoToolchainWrapper")
	assertScriptContains(t, script, "go|bin/go.cmd|[\"go-toolchain\"]")
	assertScriptContains(t, script, "gopls|bin/gopls.exe|[\"go\",\"gomod\",\"gosum\",\"gowork\"]")
	assertScriptContains(t, script, "sg|bin/sg.exe|[\"ast-grep\"]")
	assertScriptContains(t, script, "'bin/ast-grep.exe'")
	assertScriptContains(t, script, "'bin/vcruntime140.dll'")
	assertScriptContains(t, script, "'amd64' { 'win32-x64' }")
	assertScriptContains(t, script, "'arm64' { 'win32-arm64' }")
	assertScriptContains(t, script, "python.cmd")
	assertScriptContains(t, script, "python3.cmd")
	assertScriptContains(t, script, "lsp-manifest.json")
	assertScriptContains(t, script, "lsp-checksums.sha256")
	assertScriptOrder(t, script, "& $NpmBin install --prefix $LspDir @LSPNpmPackages", "Write-NodeCmdWrapper -Name 'typescript-language-server.cmd'")
	assertScriptOrder(t, script, "Write-GoToolchainWrapper", "Write-LSPManifestAndChecksums")
}

func TestPrepareLSPBundleWindowsPinsNpmDependencyVersions(t *testing.T) {
	script := readScript(t, "prepare_lsp_bundle_windows.ps1")

	assertScriptContains(t, script, "$LSPNpmPackages = @(")
	for _, want := range []string{
		"typescript-language-server@5.3.0",
		"typescript@6.0.3",
		"vscode-langservers-extracted@4.10.0",
		"pyright@1.1.410",
		"bash-language-server@5.6.0",
		"shellcheck@4.1.0",
		"@ast-grep/cli@0.43.0",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptContains(t, script, "if (-not $OmitShellcheck -and $ShellcheckBin.Trim() -eq '' -and $WindowsPackageArch -ne 'arm64')")
	assertScriptContains(t, script, "$LSPNpmPackages += 'shellcheck@4.1.0'")
	assertScriptContains(t, script, "& $NpmBin install --prefix $LspDir @LSPNpmPackages")
	assertScriptDoesNotContain(t, script, "npm install --prefix $LspDir typescript-language-server typescript vscode-langservers-extracted pyright bash-language-server shellcheck @ast-grep/cli")
}

func TestPrepareLSPBundleWindowsFullProfileBundlesJavaRuntimeAndJDTLS(t *testing.T) {
	script := readScript(t, "prepare_lsp_bundle_windows.ps1")

	assertScriptContains(t, script, "$JDTLSHome = if ($env:SUPER_DOLPHIN_JDTLS_HOME)")
	assertScriptContains(t, script, "$JDKHome = if ($env:SUPER_DOLPHIN_JDK_HOME)")
	assertScriptContains(t, script, "Write-JavaRuntimeWrapper")
	assertScriptContains(t, script, "Write-JDTLSWrapper")
	assertScriptContains(t, script, "java|bin/java.cmd|[\"java-runtime\"]")
	assertScriptContains(t, script, "jdtls|bin/jdtls.cmd|[\"java\"]")
	assertScriptContains(t, script, "if ($LSPProfile -eq 'full') {")
	assertScriptContains(t, script, "missing jdtls; set SUPER_DOLPHIN_JDTLS_HOME")
	assertScriptContains(t, script, "missing JDK; set SUPER_DOLPHIN_JDK_HOME or JAVA_HOME")
	assertScriptContains(t, script, "Assert-WindowsNativeArchitecture -Path (Join-Path $JDKHome 'bin/java.exe') -ExpectedArch $WindowsPackageArch -Label 'JDK java'")
	assertScriptContains(t, script, "Copy-DirectoryClean -Source $JDTLSHome -Destination (Join-Path $LspDir 'jdtls')")
	assertScriptContains(t, script, "Copy-DirectoryClean -Source $JDKHome -Destination (Join-Path $LspDir 'jdk')")
	assertScriptContains(t, script, "Assert-WindowsNativeArchitecture -Path $javaPath -ExpectedArch $WindowsPackageArch -Label 'LSP bundle jdk/bin/java.exe'")
	assertScriptOrder(t, script, "Copy-DirectoryClean -Source $JDKHome -Destination (Join-Path $LspDir 'jdk')", "    Write-JavaRuntimeWrapper")
	assertScriptOrder(t, script, "    Write-JavaRuntimeWrapper", "    Write-JDTLSWrapper")
}
