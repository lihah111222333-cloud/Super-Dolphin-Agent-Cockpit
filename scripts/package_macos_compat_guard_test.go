package main

import "testing"

func TestPackageMacOSScriptRejectsStartupBinariesAboveTargetMacOS(t *testing.T) {
	script := readScript(t, "package_macos.sh")
	versionBody := functionBody(t, script, "version_gt")
	minosBody := functionBody(t, script, "macho_minos_versions")
	verifyBody := functionBody(t, script, "verify_startup_macos_compatibility")

	assertScriptContains(t, script, "verify_macho_macos_compatibility()")
	assertScriptContains(t, script, "phase_start \"macos startup compatibility\"")
	assertScriptContains(t, script, "export MACOSX_DEPLOYMENT_TARGET=\"$macos_min_version\"")
	assertScriptContains(t, script, "macos_cgo_cflags=\"${CGO_CFLAGS:+$CGO_CFLAGS }-mmacosx-version-min=$macos_min_version\"")
	assertScriptContains(t, script, "macos_cgo_cxxflags=\"${CGO_CXXFLAGS:+$CGO_CXXFLAGS }-mmacosx-version-min=$macos_min_version\"")
	assertScriptContains(t, script, "macos_cgo_ldflags=\"${CGO_LDFLAGS:+$CGO_LDFLAGS }-mmacosx-version-min=$macos_min_version\"")
	assertScriptContains(t, script, "export CGO_CFLAGS=\"$macos_cgo_cflags\"")
	assertScriptContains(t, script, "export CGO_CXXFLAGS=\"$macos_cgo_cxxflags\"")
	assertScriptContains(t, script, "export CGO_LDFLAGS=\"$macos_cgo_ldflags\"")
	assertScriptOrder(t, script, "export CGO_LDFLAGS=\"$macos_cgo_ldflags\"", "make build-peer-binaries")
	assertScriptContains(t, script, "verify_startup_macos_compatibility \"$macos_min_version\"")
	assertScriptDoesNotContain(t, script, "bundle_homebrew_dylibs \"$resources/postgres/$platform\"")
	assertScriptOrder(t, script, "verify_startup_macos_compatibility \"$macos_min_version\"", "phase_start \"write plist\"")

	assertScriptContains(t, versionBody, "IFS=. read -r -a left_parts")
	assertScriptContains(t, versionBody, "left_num=$((10#${left_parts[$i]:-0}))")
	assertScriptContains(t, minosBody, "LC_BUILD_VERSION")
	assertScriptContains(t, minosBody, "$1 == \"minos\"")
	assertScriptContains(t, minosBody, "LC_VERSION_MIN_MACOSX")
	for _, want := range []string{
		"\"$macos/agent-terminal\"",
		"\"$resources/bin/mcp-orch\"",
		"\"$resources/bin/mcp-lsp\"",
	} {
		assertScriptContains(t, verifyBody, want)
	}
	assertScriptDoesNotContain(t, verifyBody, "postgres_root")
}
