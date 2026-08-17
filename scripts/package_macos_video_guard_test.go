package main

// 本文件是公共跨平台的 macOS 视频交付静态门禁，只读取脚本契约，故意不加 darwin build tag。

import "testing"

func TestPackageMacOSScriptSupportsOptInPackagedVideoAPIKey(t *testing.T) {
	script := readScript(t, "package_macos.sh")
	local := readScript(t, "package_macos_local.sh")

	assertScriptContains(t, script, "video_api_key_env=\"SILICONFLOW_API_KEY\"")
	assertScriptContains(t, script, "package_video_api_key_opt_in_env=\"SUPER_DOLPHIN_PACKAGE_INCLUDE_VIDEO_API_KEY\"")
	assertScriptContains(t, script, "macos_min_version_env=\"SUPER_DOLPHIN_MACOS_MIN_VERSION\"")
	assertScriptContains(t, script, "resolve_packaged_video_env")
	assertScriptContains(t, script, "resolve_macos_min_version")
	assertScriptContains(t, script, "write_packaged_video_env \"$resources\"")
	assertScriptContains(t, script, "printf '%s=%s\\n' \"$video_api_key_env\" \"$packaged_video_api_key\"")
	assertScriptContains(t, script, "validate_env_file_value \"$video_api_key_env\" \"$packaged_video_api_key\"")
	assertScriptContains(t, script, "must be 1, true, yes, on, 0, false, no, or off")
	assertScriptContains(t, script, "$macos_min_version_env must be a dotted numeric version such as 13.0")
	assertScriptContains(t, script, "plist_macos_min_version=\"$(xml_escape \"$macos_min_version\")\"")
	assertScriptContains(t, script, "<string>$plist_macos_min_version</string>")
	assertScriptOrder(t, script, "resolve_packaged_video_env", "mkdir -p \"$macos\" \"$resources/bin\"")
	assertScriptOrder(t, script, "resolve_macos_min_version", "mkdir -p \"$macos\" \"$resources/bin\"")
	assertScriptOrder(t, script, "write_packaged_relay_env \"$resources\"", "write_packaged_video_env \"$resources\"")
	assertScriptOrder(t, script, "write_packaged_video_env \"$resources\"", "phase_start \"codesign macho tree\"")

	assertScriptContains(t, local, "SUPER_DOLPHIN_PACKAGE_INCLUDE_VIDEO_API_KEY=\"${SUPER_DOLPHIN_PACKAGE_INCLUDE_VIDEO_API_KEY:-0}\"")
	assertScriptContains(t, local, "SILICONFLOW_API_KEY=\"${SILICONFLOW_API_KEY:-}\"")
	assertScriptContains(t, local, "SUPER_DOLPHIN_MACOS_MIN_VERSION=\"${SUPER_DOLPHIN_MACOS_MIN_VERSION:-13.0}\"")
	assertScriptOrder(t, local, "SILICONFLOW_API_KEY=\"${SILICONFLOW_API_KEY:-}\"", "\"$root/scripts/package_macos.sh\"")
}
