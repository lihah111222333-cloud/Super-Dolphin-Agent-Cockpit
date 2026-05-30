package main

import "testing"

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
