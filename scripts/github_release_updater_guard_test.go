package main

import "testing"

func TestWindowsPackagingEmbedsAppUpdateConfig(t *testing.T) {
	script := readScript(t, "package_windows.ps1")
	verify := readScript(t, "verify_packaged_app_windows.ps1")
	local := readScript(t, "package_windows_local.ps1")

	for _, want := range []string{
		"SUPER_DOLPHIN_UPDATE_ENABLED",
		"SUPER_DOLPHIN_UPDATE_MANIFEST_URL",
		"SUPER_DOLPHIN_UPDATE_GITHUB_REPO",
		"SUPER_DOLPHIN_UPDATE_PUBLIC_KEY",
		"SUPER_DOLPHIN_UPDATE_CHANNEL",
		"SUPER_DOLPHIN_UPDATE_VERSION",
		"Resolve-UpdateConfig",
		"Write-PackagedUpdateEnv -BundleRoot $Stage",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptOrder(t, script, "Resolve-UpdateConfig", "New-Item -ItemType Directory -Force -Path (Join-Path $Stage 'bin')")
	assertScriptOrder(t, script, "Write-PackagedRelayEnv -BundleRoot $Stage", "Write-PackagedUpdateEnv -BundleRoot $Stage")
	assertScriptOrder(t, script, "Write-PackagedUpdateEnv -BundleRoot $Stage", "verify_packaged_app_windows.ps1') $Stage")

	for _, want := range []string{
		"Verify-UpdateEnv",
		"SUPER_DOLPHIN_UPDATE_MANIFEST_URL or SUPER_DOLPHIN_UPDATE_GITHUB_REPO is required when app update is enabled",
		"SUPER_DOLPHIN_UPDATE_GITHUB_REPO must be owner/repo without whitespace",
		"decoded SUPER_DOLPHIN_UPDATE_PUBLIC_KEY must be 32 bytes",
		"packaged app update env verified",
	} {
		assertScriptContains(t, verify, want)
	}
	assertScriptContains(t, local, "$env:SUPER_DOLPHIN_UPDATE_GITHUB_REPO")
	assertScriptContains(t, local, "$env:SUPER_DOLPHIN_UPDATE_PUBLIC_KEY")
	assertScriptContains(t, local, "$env:SUPER_DOLPHIN_UPDATE_CHANNEL")
}

func TestGitHubReleasePackagingWrappersProduceCanonicalAssets(t *testing.T) {
	macos := readScript(t, "package_macos_github_release.sh")
	windows := readScript(t, "package_windows_github_release.ps1")

	for _, want := range []string{
		"github_release_repo=\"${SUPER_DOLPHIN_UPDATE_GITHUB_REPO:-xiaoxiaotest9527-bit/-}\"",
		"package_version=\"${SUPER_DOLPHIN_PACKAGE_VERSION:-$default_package_version}\"",
		"Super-Dolphin-darwin-arm64.dmg",
		"Super-Dolphin-darwin-arm64.update.json",
		"SUPER_DOLPHIN_UPDATE_GITHUB_REPO=\"$github_release_repo\"",
		"go run ./cmd/super-dolphin-release-manifest",
		"-public-key \"$SUPER_DOLPHIN_UPDATE_PUBLIC_KEY\"",
		"-artifact-url \"$artifact_url\"",
	} {
		assertScriptContains(t, macos, want)
	}
	assertScriptOrder(t, macos, "./scripts/package_macos.sh", "go run ./cmd/super-dolphin-release-manifest")

	for _, want := range []string{
		"$GitHubReleaseRepo = Get-EnvOrDefault -Name 'SUPER_DOLPHIN_UPDATE_GITHUB_REPO' -Default 'xiaoxiaotest9527-bit/-'",
		"$PackageVersion = Get-EnvOrDefault -Name 'SUPER_DOLPHIN_PACKAGE_VERSION'",
		"Super-Dolphin-windows-arm64.exe",
		"Super-Dolphin-windows-arm64.update.json",
		"$env:SUPER_DOLPHIN_UPDATE_GITHUB_REPO = $GitHubReleaseRepo",
		"cmd/super-dolphin-release-manifest",
		"-public-key",
		"-artifact-url",
	} {
		assertScriptContains(t, windows, want)
	}
	assertScriptOrder(t, windows, "scripts\\package_windows_local.ps1", "cmd/super-dolphin-release-manifest")
}

func TestGitHubReleasePublisherGuardsDraftPublish(t *testing.T) {
	script := readScript(t, "publish_github_release.sh")

	for _, want := range []string{
		"github_repo=\"${SUPER_DOLPHIN_UPDATE_GITHUB_REPO:-xiaoxiaotest9527-bit/-}\"",
		"Super-Dolphin-darwin-arm64.dmg",
		"Super-Dolphin-darwin-arm64.update.json",
		"Super-Dolphin-windows-arm64.exe",
		"Super-Dolphin-windows-arm64.update.json",
		"SUPER_DOLPHIN_UPDATE_SIGNING_KEY",
		"SUPER_DOLPHIN_UPDATE_PUBLIC_KEY",
		"SUPER_DOLPHIN_UPDATE_PREVIOUS_DMG",
		"SUPER_DOLPHIN_UPDATE_PREVIOUS_APP",
		"SUPER_DOLPHIN_UPDATE_PREVIOUS_ENV_FILE",
		"go run ./cmd/super-dolphin-release-manifest -check-key",
		"go run ./cmd/super-dolphin-release-manifest -verify-manifest",
		"gh auth status",
		"gh release create \"$tag\"",
		"--draft",
		"verify_uploaded_asset_digests",
		"gh release edit \"$tag\"",
		"--draft=false",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptOrder(t, script, "require_previous_update_public_key", "validate_release_assets")
	assertScriptOrder(t, script, "gh release create \"$tag\"", "verify_uploaded_asset_digests")
	assertScriptOrder(t, script, "verify_uploaded_asset_digests", "gh release edit \"$tag\"")
}

func TestReleaseManifestCommandCanVerifyKeyAndManifest(t *testing.T) {
	source := readScript(t, "../cmd/super-dolphin-release-manifest/main.go")

	for _, want := range []string{
		"checkKey",
		"verifyManifest",
		"publicKey",
		"currentVersion",
		"verifySigningKeyMatchesPublicKey",
		"verifyExistingManifest",
		"appupdate.VerifySignedManifest",
	} {
		assertScriptContains(t, source, want)
	}
}
