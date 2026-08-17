package main

// 本文件是公共跨平台的 macOS FFmpeg 交付静态门禁，只读取脚本契约，故意不加 darwin build tag。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageMacOSScriptBundlesFFmpegForVideoTools(t *testing.T) {
	script := readScript(t, "package_macos.sh")
	verify := readScript(t, "verify_packaged_app_macos.sh")

	for _, want := range []string{
		"SUPER_DOLPHIN_FFMPEG_BIN",
		"SUPER_DOLPHIN_AUTO_INSTALL_FFMPEG",
		"resolve_packaged_ffmpeg",
		"copy_packaged_ffmpeg \"$resources\"",
		"cp -f \"$packaged_ffmpeg_bin\" \"$resources/bin/ffmpeg\"",
		"brew install ffmpeg",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptOrder(t, script, "resolve_packaged_ffmpeg", "mkdir -p \"$macos\" \"$resources/bin\"")
	assertScriptOrder(t, script, "copy_packaged_ffmpeg \"$resources\"", "bundle_git_dylibs \"$resources\"")
	assertScriptContains(t, verify, "$resources/bin/ffmpeg")
	assertScriptContains(t, verify, "verify_packaged_ffmpeg")
	assertScriptContains(t, verify, "packaged ffmpeg smoke verified")
}

func TestPackageMacOSLocalScriptChecksHostFFmpegDependency(t *testing.T) {
	script := readScript(t, "package_macos_local.sh")

	for _, want := range []string{
		"SUPER_DOLPHIN_FFMPEG_BIN",
		"SUPER_DOLPHIN_AUTO_INSTALL_FFMPEG",
		"resolve_or_install_host_ffmpeg",
		"brew install ffmpeg",
		"ffmpeg verified:",
		"Homebrew failed to install ffmpeg",
		"SUPER_DOLPHIN_FFMPEG_BIN=\"$ffmpeg_bin\"",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptDoesNotContain(t, script, "postgres_dist")
	assertScriptOrder(t, script, "SUPER_DOLPHIN_FFMPEG_BIN=\"$ffmpeg_bin\"", "\"$root/scripts/package_macos.sh\"")
}

func TestVerifyPackagedAppMacOSRejectsMissingBundledFFmpeg(t *testing.T) {
	app := writeMinimalPackagedMacOSApp(t)
	resources := filepath.Join(app, "Contents", "Resources")
	writeRuntimeManifest(t, resources, map[string]string{
		"bundled_codex_path":  "bin/codex",
		"bundled_gopls_path":  "bin/gopls",
		"lsp_bundle_path":     "lsp",
		"lsp_manifest_path":   "lsp/lsp-manifest.json",
		"model_registry_path": "models.yaml",
	})
	if err := os.Remove(filepath.Join(resources, "bin", "ffmpeg")); err != nil {
		t.Fatalf("remove bundled ffmpeg: %v", err)
	}

	output, err := runVerifyPackagedAppMacOS(t, app)
	if err == nil {
		t.Fatalf("expected packaged app verifier to reject missing ffmpeg, got success:\n%s", output)
	}
	if !strings.Contains(output, "missing executable:") || !strings.Contains(output, "Resources/bin/ffmpeg") {
		t.Fatalf("expected missing ffmpeg executable error, got:\n%s", output)
	}
}

func TestPackageEnvExampleDocumentsFFmpegWithoutSecrets(t *testing.T) {
	example := readScript(t, "../.env.packaging.example")

	assertScriptContains(t, example, "SUPER_DOLPHIN_FFMPEG_BIN=")
	assertScriptDoesNotContain(t, example, "SILICONFLOW_API_KEY=")
}
