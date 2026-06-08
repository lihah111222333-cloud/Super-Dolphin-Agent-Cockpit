package main

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageMacOSScriptRejectsNonCanonicalUpdateGitHubRepo(t *testing.T) {
	publicKey := base64.StdEncoding.EncodeToString(make([]byte, 32))
	output, err := runPackageMacOSUpdateConfigResolver(t, map[string]string{
		"SUPER_DOLPHIN_RELEASE_PROFILE":             "gray-unsigned",
		"SUPER_DOLPHIN_UPDATE_GITHUB_REPO":          "example/wrong",
		"SUPER_DOLPHIN_UPDATE_PUBLIC_KEY":           publicKey,
		"SUPER_DOLPHIN_UPDATE_CHANNEL":              "gray",
		"SUPER_DOLPHIN_CODEX_RELAY_BASE_URL":        "https://relay.example.com",
		"SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN": "bootstrap-token",
		"SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF": "bootstrap-proof",
	})
	if err == nil {
		t.Fatal("expected package_macos.sh to reject a non-canonical update GitHub repo")
	}
	if !strings.Contains(output, "SUPER_DOLPHIN_UPDATE_GITHUB_REPO must be xiaoxiaotest9527-bit/-") {
		t.Fatalf("expected canonical repo error, got:\n%s", output)
	}
}

func TestPublishGitHubUpdateAssetsScriptUploadsPlatformAssets(t *testing.T) {
	script := readScript(t, "publish_github_update_assets.sh")

	for _, want := range []string{
		"required_update_github_repo=\"xiaoxiaotest9527-bit/-\"",
		"asset_extension_for_platform()",
		"darwin-*) printf '.dmg",
		"windows-*) printf '.exe",
		"artifact_name=\"Super-Dolphin-$update_platform$artifact_extension\"",
		"manifest_name=\"Super-Dolphin-$update_platform.update.json\"",
		"run_local_manifest_verification()",
		"SUPER_DOLPHIN_UPDATE_PLATFORM=\"$update_platform\" DMG_PATH=\"$artifact_path\" LATEST_JSON_PATH=\"$manifest_path\" docs/scripts/macos_release_smoke.sh manifest",
		"go run ./cmd/super-dolphin-release-manifest",
		"SUPER_DOLPHIN_UPDATE_GITHUB_REPO must be xiaoxiaotest9527-bit/-",
		"SUPER_DOLPHIN_UPDATE_ARTIFACT_URL must match https://github.com/$update_github_repo/releases/download/<tag>/$artifact_name",
		"gh release upload \"$release_tag\" \"$artifact_path\" \"$manifest_path\" --repo \"$update_github_repo\" --clobber",
		"gh release view \"$release_tag\" --repo \"$update_github_repo\" --json assets --jq '.assets[].name'",
		"verify_release_asset \"$artifact_name\"",
		"verify_release_asset \"$manifest_name\"",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptOrder(t, script, "run_local_manifest_verification", "gh release upload \"$release_tag\"")
	assertScriptOrder(t, script, "gh release upload \"$release_tag\"", "verify_release_asset \"$artifact_name\"")
}

func TestMacOSGrayReleaseDocsListCrossPlatformUpdateAssetFormat(t *testing.T) {
	doc := readScript(t, "../docs/packaging/macos-gray-release.md")

	for _, want := range []string{
		"Super-Dolphin-darwin-arm64.dmg",
		"Super-Dolphin-darwin-arm64.update.json",
		"Super-Dolphin-darwin-amd64.dmg",
		"Super-Dolphin-darwin-amd64.update.json",
		"Super-Dolphin-windows-amd64.exe",
		"Super-Dolphin-windows-amd64.update.json",
		"Super-Dolphin-windows-arm64.exe",
		"Super-Dolphin-windows-arm64.update.json",
		"https://github.com/xiaoxiaotest9527-bit/-/releases/download/v1.2.3/Super-Dolphin-windows-amd64.exe",
	} {
		assertScriptContains(t, doc, want)
	}
}

func runPackageMacOSUpdateConfigResolver(t *testing.T, values map[string]string) (string, error) {
	t.Helper()

	script := readScript(t, "package_macos.sh")
	harness := scriptPrefixThroughFunction(t, script, "resolve_update_config") + "\nresolve_update_config\n"
	harnessPath := filepath.Join(t.TempDir(), "package_macos_update_config.sh")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o700); err != nil {
		t.Fatalf("write update config harness: %v", err)
	}

	cmd := exec.Command("bash", bashArg("", harnessPath))
	cmd.Dir = "."
	cmd.Env = packageScriptValidationEnv(t, "darwin", values)
	output, err := cmd.CombinedOutput()
	return string(output), err
}
