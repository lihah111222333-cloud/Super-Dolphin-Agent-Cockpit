package main

// 本文件是公共跨平台的 macOS LSP 预构建静态门禁，只读取脚本契约，故意不加 darwin build tag。

import "testing"

func TestPackageMacOSScriptPrunesNonMacOSLSPPrebuilds(t *testing.T) {
	script := readScript(t, "package_macos.sh")
	body := functionBody(t, script, "prune_packaged_lsp_non_macos_prebuilds")
	copyBody := functionBody(t, script, "copy_packaged_lsp_bundle")

	assertScriptContains(t, script, "lsp_node_prebuild_arch()")
	assertScriptContains(t, body, "find \"$dest_root/node_modules\" -type d -name prebuilds -print0")
	assertScriptContains(t, body, "\"darwin-$prebuild_arch\"|darwin-\"$prebuild_arch\"-*|darwin-\"$prebuild_arch\"+*)")
	assertScriptContains(t, body, "rm -rf \"$platform_dir\"")
	assertScriptOrder(t, copyBody, "rsync -aL --delete \"$packaged_lsp_bundle_dir\"/ \"$dest_root\"/", "prune_packaged_lsp_non_macos_prebuilds \"$dest_root\"")
	assertScriptOrder(t, copyBody, "prune_packaged_lsp_non_macos_prebuilds \"$dest_root\"", "for spec in \"${lsp_server_specs[@]}\"; do")
}
