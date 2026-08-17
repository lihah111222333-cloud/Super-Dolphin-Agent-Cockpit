package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	lspinstaller "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
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

func TestPrepareLSPBundleScriptsRegisterBundledClangdForMQL(t *testing.T) {
	for _, scriptPath := range []string{"prepare_lsp_bundle_macos.sh", "prepare_lsp_bundle_linux.sh"} {
		t.Run(scriptPath, func(t *testing.T) {
			script := readScript(t, scriptPath)

			assertScriptContains(t, script, "clangd_bin=\"${SUPER_DOLPHIN_CLANGD_BIN:-$(command -v clangd || true)}\"")
			assertScriptContains(t, script, "missing clangd; set SUPER_DOLPHIN_CLANGD_BIN")
			assertScriptContains(t, script, "cp \"$clangd_bin\" \"$lsp_dir/bin/clangd\"")
			assertScriptContains(t, script, "\"$lsp_dir/bin/clangd\" --version")
			assertScriptContains(t, script, "'clangd|bin/clangd|[\"c\",\"cpp\",\"objective-c\",\"objective-cpp\",\"mql\",\"mql4\",\"mql5\",\"mq4\",\"mq5\",\"mqh\"]'")
			for _, forbidden := range []string{"/usr/bin/clangd", "/opt/homebrew/opt/llvm/bin/clangd", "/usr/local/opt/llvm/bin/clangd"} {
				assertScriptDoesNotContain(t, script, forbidden)
			}
		})
	}

	windowsScript := readScript(t, "prepare_lsp_bundle_windows.ps1")
	assertScriptContains(t, windowsScript, "$ClangdBin = if ($env:SUPER_DOLPHIN_CLANGD_BIN)")
	assertScriptContains(t, windowsScript, "missing clangd; set SUPER_DOLPHIN_CLANGD_BIN")
	assertScriptContains(t, windowsScript, "Copy-Item -LiteralPath $ClangdBin -Destination (Join-Path $LspDir 'bin/clangd.exe')")
	assertScriptContains(t, windowsScript, "bundled clangd failed --version smoke")
	assertScriptContains(t, windowsScript, "id = 'clangd'; path = 'bin/clangd.exe'")
	assertScriptDoesNotContain(t, windowsScript, `C:\Program Files\LLVM\bin\clangd.exe`)

	for _, scriptPath := range []string{"prepare_lsp_bundle_macos.sh", "prepare_lsp_bundle_linux.sh", "prepare_lsp_bundle_windows.ps1"} {
		t.Run(scriptPath+"_language_contract", func(t *testing.T) {
			got := clangdManifestLanguageIDs(t, readScript(t, scriptPath))
			want := contract.ClangdLanguageIDs()
			if !slices.Equal(got, want) {
				t.Fatalf("clangd manifest languages = %v, want contract %v", got, want)
			}
		})
	}
}

func clangdManifestLanguageIDs(t *testing.T, script string) []string {
	t.Helper()
	match := regexp.MustCompile(`clangd\|bin/clangd(?:\.exe)?\|(\[[^\]\r\n]+\])`).FindStringSubmatch(script)
	if len(match) == 2 {
		var languages []string
		if err := json.Unmarshal([]byte(match[1]), &languages); err != nil {
			t.Fatalf("parse clangd manifest languages: %v", err)
		}
		if len(languages) == 0 {
			t.Fatal("clangd manifest languages are empty")
		}
		return languages
	}
	match = regexp.MustCompile(`id = 'clangd'; path = 'bin/clangd\.exe'.*?languages = @\(([^)]*)\)`).FindStringSubmatch(script)
	if len(match) != 2 {
		t.Fatal("clangd manifest language descriptor not found")
	}
	entries := regexp.MustCompile(`'([^']+)'`).FindAllStringSubmatch(match[1], -1)
	languages := make([]string, 0, len(entries))
	for _, entry := range entries {
		languages = append(languages, entry[1])
	}
	if len(languages) == 0 {
		t.Fatal("clangd manifest languages are empty")
	}
	return languages
}

func TestPrepareLSPBundleScriptsBundleSqruff(t *testing.T) {
	for _, scriptPath := range []string{"prepare_lsp_bundle_macos.sh", "prepare_lsp_bundle_linux.sh"} {
		t.Run(scriptPath, func(t *testing.T) {
			script := readScript(t, scriptPath)

			assertScriptContains(t, script, "sqruff_bin=")
			assertScriptContains(t, script, "cp \"$sqruff_bin\" \"$lsp_dir/bin/sqruff\"")
			assertScriptContains(t, script, "missing sqruff")
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

func TestPrepareLSPBundleScriptsIncludeSQLLanguageServerInManifestAndChecksums(t *testing.T) {
	for _, scriptPath := range []string{"prepare_lsp_bundle_macos.sh", "prepare_lsp_bundle_linux.sh"} {
		t.Run(scriptPath, func(t *testing.T) {
			script := readScript(t, scriptPath)

			assertScriptContains(t, script, "'sqruff|bin/sqruff|[\"sql\"]'")
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

	assertScriptContains(t, script, "$languages = [string[]]$spec.languages")
	assertScriptContains(t, script, "LSP manifest languages for $serverId must be a non-empty JSON array")
	assertScriptContains(t, script, "version = $version")
	assertScriptContains(t, script, "sha256 = $digest")
	assertScriptDoesNotContain(t, script, "version = 'bundled'")
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
	assertScriptContains(t, script, "Get-WindowsHostIdentity")
	assertScriptContains(t, script, "RtlGetVersion")
	assertScriptContains(t, script, "IsWow64Process2")
	assertScriptContains(t, script, "cross-architecture Windows LSP packaging is forbidden")
	assertScriptContains(t, script, "require Windows 10.0 build 19041 or newer")
	assertScriptDoesNotContain(t, script, "(& go env GOOS).Trim()")
	assertScriptDoesNotContain(t, script, "(& go env GOARCH).Trim()")
	assertScriptContains(t, script, "node.exe")
	assertScriptContains(t, script, "& $NpmBin install --prefix $LspDir --save-exact @LSPNpmPackages")
	assertScriptContains(t, script, "typescript-language-server")
	assertScriptContains(t, script, "vscode-langservers-extracted")
	assertScriptContains(t, script, "pyright")
	assertScriptContains(t, script, "bash-language-server")
	assertScriptContains(t, script, "sqruff")
	assertScriptContains(t, script, "shellcheck")
	assertScriptContains(t, script, "@ast-grep/cli")
	assertScriptContains(t, script, "Write-NodeCmdWrapper -Name 'typescript-language-server.cmd'")
	assertScriptContains(t, script, "Write-NodeCmdWrapper -Name 'vscode-css-language-server.cmd'")
	assertScriptContains(t, script, "Write-NodeCmdWrapper -Name 'vscode-html-language-server.cmd'")
	assertScriptContains(t, script, "Write-NodeCmdWrapper -Name 'vscode-json-language-server.cmd'")
	assertScriptContains(t, script, "Write-NodeCmdWrapper -Name 'vscode-markdown-language-server.cmd'")
	assertScriptContains(t, script, "Write-NodeCmdWrapper -Name 'pyright-langserver.cmd'")
	assertScriptContains(t, script, "Write-NodeCmdWrapper -Name 'yaml-language-server.cmd'")
	assertScriptContains(t, script, "Write-NodeCmdWrapper -Name 'vue-language-server.cmd'")
	assertScriptContains(t, script, "Write-NodeCmdWrapper -Name 'svelteserver.cmd'")
	assertScriptContains(t, script, "Write-NodeCmdWrapper -Name 'intelephense.cmd'")
	assertScriptContains(t, script, "Write-NodeCmdWrapper -Name 'docker-langserver.cmd'")
	assertScriptContains(t, script, "Write-NodeCmdWrapper -Name 'graphql-lsp.cmd'")
	assertScriptContains(t, script, "Write-NodeCmdWrapper -Name 'prisma-language-server.cmd'")
	assertScriptContains(t, script, "Write-NodeCmdWrapper -Name 'bash-language-server.cmd'")
	assertScriptContains(t, script, "Copy-Item -LiteralPath $SqruffBin -Destination (Join-Path $LspDir 'bin/sqruff.exe')")
	assertScriptContains(t, script, "node_modules/shellcheck/bin/shellcheck.js")
	assertScriptContains(t, script, "shellcheck npm launcher failed to prepare bundled executable")
	assertScriptContains(t, script, "SUPER_DOLPHIN_SHELLCHECK_BIN")
	assertScriptContains(t, script, "SUPER_DOLPHIN_WINDOWS_OMIT_SHELLCHECK")
	assertScriptContains(t, script, "shellcheck will not be included")
	assertScriptContains(t, script, "missing ARM64 shellcheck.exe; shellcheck npm package does not publish win32-arm64")
	assertScriptContains(t, script, "$shellcheck = Resolve-ShellcheckExecutable")
	assertScriptOrder(t, script, "function Resolve-ShellcheckExecutable", "$shellcheck = Resolve-ShellcheckExecutable")
	assertScriptContains(t, script, "SUPER_DOLPHIN_MSVC_RUNTIME_DIR")
	assertScriptContains(t, script, "function Resolve-WindowsVCLibsDesktopDirectory")
	assertScriptContains(t, script, "Microsoft.VCLibs.arm64.14.00.Desktop.appx")
	assertScriptContains(t, script, "Microsoft.VCLibs.x64.14.00.Desktop.appx")
	assertScriptContains(t, script, "Microsoft.VCLibs.x86.14.00.Desktop.appx")
	assertScriptContains(t, script, "Microsoft.VCLibs.140.00.UWPDesktop")
	assertScriptContains(t, script, "Windows VCLibs Desktop SHA-256 mismatch")
	assertScriptContains(t, script, "Windows VCLibs Desktop Appx entry escapes extraction root")
	assertScriptContains(t, script, "[IO.FileShare]::None")
	assertScriptContains(t, script, "node_modules/@ast-grep/cli/ast-grep.exe")
	assertScriptContains(t, script, "bin/ast-grep.exe")
	assertScriptContains(t, script, "msvcp140_atomic_wait.dll")
	assertScriptContains(t, script, "vcruntime140.dll")
	assertScriptContains(t, script, "bundled ast-grep smoke failed")
	assertScriptOrder(t, script, "node_modules/@ast-grep/cli/sg.exe", "node_modules/@ast-grep/cli/ast-grep.exe")
	assertScriptOrder(t, script, "Copy-Item -LiteralPath $astGrep", "$MSVCRuntimeDir = Resolve-WindowsVCLibsDesktopDirectory")
	assertScriptContains(t, script, "Write-GoToolchainWrapper")
	assertScriptContains(t, script, "id = 'go'; path = 'bin/go.cmd'")
	assertScriptContains(t, script, "id = 'gopls'; path = 'bin/gopls.exe'")
	assertScriptContains(t, script, "id = 'sg'; path = 'bin/sg.exe'")
	assertScriptContains(t, script, "id = 'sqruff'; path = 'bin/sqruff.exe'")
	assertScriptContains(t, script, "'bin/ast-grep.exe'")
	assertScriptContains(t, script, "'bin/vcruntime140.dll'")
	assertScriptContains(t, script, "'amd64' { 'win32-x64' }")
	assertScriptContains(t, script, "'arm64' { 'win32-arm64' }")
	assertScriptContains(t, script, "'x86' { 'win32-ia32' }")
	assertScriptContains(t, script, "0x014C { return 'x86' }")
	assertScriptContains(t, script, "python.cmd")
	assertScriptContains(t, script, "python3.cmd")
	assertScriptContains(t, script, "lsp-manifest.json")
	assertScriptContains(t, script, "lsp-checksums.sha256")
	assertScriptOrder(t, script, "& $NpmBin install --prefix $LspDir --save-exact @LSPNpmPackages", "Write-NodeCmdWrapper -Name 'typescript-language-server.cmd'")
	assertScriptOrder(t, script, "Write-GoToolchainWrapper", "Write-LSPManifestAndChecksums")
}

func TestPrepareLSPBundleWindowsArchitectureAliasesMatchGoContract(t *testing.T) {
	script := readScript(t, "prepare_lsp_bundle_windows.ps1")
	testGroups := []struct {
		goCanonical         string
		powerShellCanonical string
		aliases             []string
	}{
		{goCanonical: lspinstaller.WindowsHostArchARM64, powerShellCanonical: "arm64", aliases: []string{"arm64", "aarch64", "armv8", "arm64-v8a"}},
		{goCanonical: lspinstaller.WindowsHostArchX64, powerShellCanonical: "amd64", aliases: []string{"amd64", "x64", "x86_64", "x86-64"}},
		{goCanonical: lspinstaller.WindowsHostArchX86, powerShellCanonical: "x86", aliases: []string{"386", "x86", "i386", "i486", "i586", "i686", "x86-32", "ia32"}},
	}
	for _, group := range testGroups {
		quotedAliases := make([]string, 0, len(group.aliases))
		for _, alias := range group.aliases {
			got, err := lspinstaller.NormalizeWindowsArchitectureAlias(alias)
			if err != nil || got != group.goCanonical {
				t.Fatalf("NormalizeWindowsArchitectureAlias(%q) = %q, %v; want %q", alias, got, err, group.goCanonical)
			}
			quotedAliases = append(quotedAliases, fmt.Sprintf("'%s'", alias))
		}
		powerShellBranch := fmt.Sprintf("{ $_ -in @(%s) } { '%s'; break }", strings.Join(quotedAliases, ", "), group.powerShellCanonical)
		assertScriptContains(t, script, powerShellBranch)
	}
	assertScriptContains(t, script, "installer.NormalizeWindowsArchitectureAlias")
}

func TestPrepareLSPBundleWindowsPinsNpmDependencyVersions(t *testing.T) {
	script := readScript(t, "prepare_lsp_bundle_windows.ps1")

	assertScriptContains(t, script, "$LSPNpmPackages = @(")
	for _, want := range []string{
		"typescript-language-server@5.3.0",
		"typescript@5.9.3",
		"vscode-langservers-extracted@4.10.0",
		"vscode-markdown-languageservice@0.5.0-alpha.11",
		"pyright@1.1.412",
		"yaml-language-server@1.24.0",
		"@vue/language-server@3.3.9",
		"svelte-language-server@0.18.4",
		"intelephense@1.18.5",
		"dockerfile-language-server-nodejs@0.15.0",
		"graphql-language-service-cli@3.5.0",
		"@prisma/language-server@31.11.0",
		"bash-language-server@5.6.0",
		"shellcheck@4.1.0",
		"@ast-grep/cli@0.43.0",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptContains(t, script, "if (-not $OmitShellcheck -and $ShellcheckBin.Trim() -eq '' -and $WindowsPackageArch -ne 'arm64')")
	assertScriptContains(t, script, "$LSPNpmPackages += 'shellcheck@4.1.0'")
	assertScriptContains(t, script, "& $NpmBin install --prefix $LspDir --save-exact @LSPNpmPackages")
	assertScriptContains(t, script, "'vscode-markdown-languageservice' = '0.5.0-alpha.11'")
	assertScriptDoesNotContain(t, script, "npm install --prefix $LspDir typescript-language-server typescript vscode-langservers-extracted pyright bash-language-server shellcheck @ast-grep/cli")
}

func TestPrepareLSPBundleWindowsShellcheckNpmCohortMatchesArchitectureAndOverrideBranches(t *testing.T) {
	script := readScript(t, "prepare_lsp_bundle_windows.ps1")
	condition := "-not $OmitShellcheck -and $ShellcheckBin.Trim() -eq '' -and $WindowsPackageArch -ne 'arm64'"
	assertScriptContains(t, script, "if ("+condition+") {")
	assertScriptContains(t, script, "$LSPNpmPackages += 'shellcheck@4.1.0'")
	assertScriptContains(t, script, "$expectedNpmPackageVersions['shellcheck'] = '4.1.0'")
	assertScriptOrder(t, script, "$LSPNpmPackages += 'shellcheck@4.1.0'", "$expectedNpmPackageVersions['shellcheck'] = '4.1.0'")
	assertScriptContains(t, script, "if ($OmitShellcheck) {")
	assertScriptContains(t, script, "if ($WindowsPackageArch -eq 'arm64') {")
}

func TestPrepareLSPBundleWindowsCoversDefaultNodeAdapterCohort(t *testing.T) {
	script := readScript(t, "prepare_lsp_bundle_windows.ps1")

	for _, want := range []string{
		"id = 'typescript-language-server'; path = 'bin/typescript-language-server.cmd'",
		"id = 'vscode-langservers-extracted'; path = 'bin/vscode-css-language-server.cmd'",
		"id = 'vscode-html-language-server'; path = 'bin/vscode-html-language-server.cmd'",
		"id = 'vscode-json-language-server'; path = 'bin/vscode-json-language-server.cmd'",
		"id = 'vscode-markdown-language-server'; path = 'bin/vscode-markdown-language-server.cmd'",
		"id = 'pyright'; path = 'bin/pyright-langserver.cmd'",
		"id = 'yaml-language-server'; path = 'bin/yaml-language-server.cmd'",
		"id = 'vue-language-server'; path = 'bin/vue-language-server.cmd'",
		"id = 'svelteserver'; path = 'bin/svelteserver.cmd'",
		"id = 'intelephense'; path = 'bin/intelephense.cmd'",
		"id = 'docker-langserver'; path = 'bin/docker-langserver.cmd'",
		"id = 'graphql-lsp'; path = 'bin/graphql-lsp.cmd'",
		"id = 'prisma-language-server'; path = 'bin/prisma-language-server.cmd'",
		"id = 'bash-language-server'; path = 'bin/bash-language-server.cmd'",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptContains(t, script, "Get-SHA256File $fullPath")
	assertScriptContains(t, script, "Get-NpmPackageVersion")
	assertScriptContains(t, script, "Get-ExecutableVersion")
	assertScriptContains(t, script, "Get-JDTLSVersion")
}

func TestPrepareLSPBundleWindowsFullProfileBundlesJavaRuntimeAndJDTLS(t *testing.T) {
	script := readScript(t, "prepare_lsp_bundle_windows.ps1")

	assertScriptContains(t, script, "$JDTLSHome = if ($env:SUPER_DOLPHIN_JDTLS_HOME)")
	assertScriptContains(t, script, "$JDKHome = if ($env:SUPER_DOLPHIN_JDK_HOME)")
	assertScriptContains(t, script, "Write-JavaRuntimeWrapper")
	assertScriptContains(t, script, "Write-JDTLSWrapper")
	assertScriptContains(t, script, "id = 'java'; path = 'bin/java.cmd'")
	assertScriptContains(t, script, "id = 'jdtls'; path = 'bin/jdtls.cmd'")
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
