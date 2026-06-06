package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	assertScriptOrder(t, script, "test -d \"$postgres_dist\"", "resolve_or_install_host_ffmpeg\n\npackage_one()")
	assertScriptOrder(t, script, "resolve_or_install_host_ffmpeg\n\npackage_one()", "package_one()")
	assertScriptOrder(t, script, "SUPER_DOLPHIN_FFMPEG_BIN=\"$ffmpeg_bin\"", "\"$root/scripts/package_macos.sh\"")
}

func TestVerifyPackagedAppMacOSRejectsMissingBundledFFmpeg(t *testing.T) {
	app := writeMinimalPackagedMacOSApp(t)
	resources := filepath.Join(app, "Contents", "Resources")
	writeDefaultMacOSRuntimeManifest(t, resources)
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
