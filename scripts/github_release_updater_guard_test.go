package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/appupdate"
)

func TestWindowsPackagingFailsClosedWithoutUpdateRoutes(t *testing.T) {
	script := readScript(t, "package_windows.ps1")
	verify := readScript(t, "verify_packaged_app_windows.ps1")
	local := readScript(t, "package_windows_local.ps1")
	wrapper := readScript(t, "package_windows_github_release.ps1")
	publisher := readScript(t, "publish_github_release.sh")

	for _, want := range []string{
		"SUPER_DOLPHIN_UPDATE_ENABLED",
		"SUPER_DOLPHIN_UPDATE_MANIFEST_URL",
		"SUPER_DOLPHIN_UPDATE_GITHUB_REPO",
		"SUPER_DOLPHIN_UPDATE_PUBLIC_KEY",
		"SUPER_DOLPHIN_UPDATE_CHANNEL",
		"SUPER_DOLPHIN_UPDATE_STAGE_DIR",
		"SUPER_DOLPHIN_UPDATE_HELPER_PATH",
		"SUPER_DOLPHIN_UPDATE_TARGET_APP_PATH",
		"SUPER_DOLPHIN_UPDATE_PLATFORM",
		"SUPER_DOLPHIN_UPDATE_VERSION",
		"SUPER_DOLPHIN_UPDATE_ALLOW_UNSIGNED",
		"SUPER_DOLPHIN_UPDATE_WINDOWS_PUBLISHER",
		"SUPER_DOLPHIN_UPDATE_WINDOWS_THUMBPRINT",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptContains(t, script, "package-owned updates are unsupported for $Platform; reject $name")
	assertScriptContains(t, verify, "packaged .env contains update override")
	assertScriptDoesNotContain(t, script, "Write-PackagedUpdateEnv")
	assertScriptContains(t, verify, "function Verify-UpdateEnv()")
	assertScriptDoesNotContain(t, local, "Forward-UpdateEnv")
	assertScriptDoesNotContain(t, local, "SUPER_DOLPHIN_UPDATE_")
	assertScriptContains(t, wrapper, "check/install/publish capabilities are all disabled")
	assertScriptDoesNotContain(t, publisher, "windows-arm64|")
	assertScriptContains(t, publisher, "distribution_asset_names=(")
	assertScriptContains(t, publisher, "Super-Dolphin-windows-arm64.exe")
	assertScriptDoesNotContain(t, publisher, "Super-Dolphin-windows-arm64.update.json")
}

func TestPackageEnvExampleDocumentsGitHubReleaseUpdateInputsWithoutSecrets(t *testing.T) {
	example := readScript(t, "../.env.packaging.example")
	for _, want := range []string{
		"SUPER_DOLPHIN_UPDATE_GITHUB_REPO=super-dolphin/releases",
		"SUPER_DOLPHIN_UPDATE_PUBLIC_KEY=",
		"SUPER_DOLPHIN_UPDATE_CHANNEL=gray",
		"SUPER_DOLPHIN_UPDATE_MINIMUM_VERSION=0.0.0",
		"SUPER_DOLPHIN_UPDATE_PREVIOUS_DMG=",
		"SUPER_DOLPHIN_UPDATE_PREVIOUS_APP=",
		"SUPER_DOLPHIN_ALLOW_LOCAL_PREVIOUS_RELEASE_TEST=",
		"SUPER_DOLPHIN_UPDATE_SIGNING_KEY=",
	} {
		assertScriptContains(t, example, want)
	}
	assertScriptDoesNotContain(t, example, "xiaoxiaotest9527-bit/-")
	assertScriptDoesNotContain(t, example, "BEGIN PRIVATE KEY")
	assertScriptDoesNotContain(t, example, "sk-")
	assertScriptDoesNotContain(t, example, "SUPER_DOLPHIN_UPDATE_PREVIOUS_ENV_FILE")
	assertScriptDoesNotContain(t, example, "SUPER_DOLPHIN_UPDATE_WINDOWS_PUBLISHER")
	assertScriptDoesNotContain(t, example, "SUPER_DOLPHIN_UPDATE_WINDOWS_THUMBPRINT")
}

func TestGitHubReleasePackagingWrappersProduceCanonicalAssets(t *testing.T) {
	macos := readScript(t, "package_macos_github_release.sh")
	windows := readScript(t, "package_windows_github_release.ps1")

	for _, want := range []string{
		"require_github_release_repo",
		"github_release_repo=\"$(require_github_release_repo)\"",
		"package_version=\"${SUPER_DOLPHIN_PACKAGE_VERSION:-$default_package_version}\"",
		"Super-Dolphin-darwin-arm64.dmg",
		"Super-Dolphin-darwin-arm64.update.json",
		"SUPER_DOLPHIN_UPDATE_GITHUB_REPO=\"$github_release_repo\"",
		"SUPER_DOLPHIN_UPDATE_PREVIOUS_DMG",
		"SUPER_DOLPHIN_UPDATE_PREVIOUS_APP",
		"SUPER_DOLPHIN_ALLOW_LOCAL_PREVIOUS_RELEASE_TEST",
		"resolve_update_public_key",
		"previous_public_key_from_dmg",
		"-print-package-trust-public-key",
		"go run ./cmd/super-dolphin-release-manifest",
		"-public-key \"$SUPER_DOLPHIN_UPDATE_PUBLIC_KEY\"",
		"-artifact-url \"$artifact_url\"",
	} {
		assertScriptContains(t, macos, want)
	}
	assertScriptDoesNotContain(t, macos, "SUPER_DOLPHIN_UPDATE_GITHUB_REPO:-xiaoxiaotest9527-bit/-")
	assertScriptContains(t, macos, "known placeholder update repo is not allowed")
	assertScriptDoesNotContain(t, macos, "Contents/Resources/.env")
	assertScriptDoesNotContain(t, macos, "SUPER_DOLPHIN_UPDATE_PREVIOUS_ENV_FILE")
	assertScriptContains(t, macos, "resolve_update_public_key\n\nrequire_clean_release_tree\n\ngo run \"$root/cmd/super-dolphin-release-manifest\"")
	assertScriptOrder(t, macos, "./scripts/package_macos.sh", "go run ./cmd/super-dolphin-release-manifest")

	assertScriptContains(t, windows, "check/install/publish capabilities are all disabled")
	assertScriptDoesNotContain(t, windows, "super-dolphin-release-manifest")
	assertScriptDoesNotContain(t, windows, ".update.json")
}

func TestPackageScriptsEnforceExactlyOneUpdateSourceAndRepoDenylist(t *testing.T) {
	macosPackage := readScript(t, "package_macos.sh")

	for scriptName, script := range map[string]string{
		"package_macos.sh": macosPackage,
	} {
		t.Run(scriptName, func(t *testing.T) {
			assertScriptContains(t, script, "SUPER_DOLPHIN_UPDATE_MANIFEST_URL and SUPER_DOLPHIN_UPDATE_GITHUB_REPO are mutually exclusive")
			assertScriptContains(t, script, "known placeholder update repo is not allowed")
			assertScriptContains(t, script, "xiaoxiaotest9527-bit/-")
		})
	}
}

func TestGitHubReleaseWrappersRequireCleanTreeAndRecordBuildCommit(t *testing.T) {
	macos := readScript(t, "package_macos_github_release.sh")
	windows := readScript(t, "package_windows_github_release.ps1")

	for _, want := range []string{
		"require_clean_release_tree",
		"SUPER_DOLPHIN_RELEASE_DIRTY_WHITELIST",
		"git status --porcelain=v1 --untracked-files=all",
		"release worktree has uncommitted changes",
		"build_commit=\"$(git rev-parse HEAD)\"",
		"SUPER_DOLPHIN_RELEASE_BUILD_COMMIT=\"$build_commit\"",
		"release-build-commit.txt",
	} {
		assertScriptContains(t, macos, want)
	}
	assertScriptOrder(t, macos, "require_clean_release_tree", "./scripts/package_macos.sh")
	assertScriptOrder(t, macos, "build_commit=\"$(git rev-parse HEAD)\"", "go run ./cmd/super-dolphin-release-manifest")

	assertScriptContains(t, windows, "check/install/publish capabilities are all disabled")
	assertScriptDoesNotContain(t, windows, "Require-CleanReleaseTree")
}

func TestGitHubReleasePublisherGuardsDraftPublish(t *testing.T) {
	script := readScript(t, "publish_github_release.sh")

	for _, want := range []string{
		"github_repo=\"$(require_github_release_repo)\"",
		"Super-Dolphin-darwin-arm64.dmg",
		"Super-Dolphin-darwin-arm64.update.json",
		"SUPER_DOLPHIN_UPDATE_SIGNING_KEY",
		"SUPER_DOLPHIN_UPDATE_PUBLIC_KEY",
		"SUPER_DOLPHIN_UPDATE_PREVIOUS_DMG",
		"SUPER_DOLPHIN_UPDATE_PREVIOUS_APP",
		"download_formal_previous_dmg",
		"-expected-package-source \"$github_repo\"",
		"-expected-package-signer \"$expected_signer\"",
		"trusted_current_package_signer",
		"previous DMG must contain exactly one top-level app bundle",
		"-print-package-trust-public-key",
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
	assertScriptContains(t, script, "release_asset_specs=(")
	assertScriptContains(t, script, "distribution_asset_names=(")
	assertScriptContains(t, script, "Super-Dolphin-windows-arm64.exe")
	assertScriptDoesNotContain(t, script, "windows-arm64|")
	assertScriptDoesNotContain(t, script, "Super-Dolphin-windows-arm64.update.json")
	assertScriptDoesNotContain(t, script, "Contents/Resources/.env")
	assertScriptDoesNotContain(t, script, "SUPER_DOLPHIN_UPDATE_PREVIOUS_ENV_FILE")
	assertScriptOrder(t, script, "resolve_update_public_key", "validate_release_assets")
	assertScriptOrderAfter(t, script, "gh release create \"$tag\"", "gh release create \"$tag\"", "verify_uploaded_asset_digests")
	assertScriptOrderAfter(t, script, "gh release create \"$tag\"", "verify_uploaded_asset_digests", "gh release edit \"$tag\"")
}

func TestGitHubReleasePublisherCanVerifyManualUploads(t *testing.T) {
	script := readScript(t, "publish_github_release.sh")

	for _, want := range []string{
		"--verify-existing",
		"verify_existing=1",
		"require_existing_release",
		"require_existing_release_marked_latest",
		"releases?per_page=100",
		".tag_name != \\\"$tag\\\"",
		"verify_uploaded_asset_digests",
		"existing GitHub release verified",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptOrderAfter(t, script, "if [[ \"$verify_existing\" != \"1\" ]]", "else\n  resolve_update_public_key", "validate_release_assets")
	assertScriptOrderAfter(t, script, "else\n  resolve_update_public_key", "resolve_update_public_key", "validate_release_assets")
	mainAnchor := "validate_release_assets\nrequire_gh_access\n\nif [[ \"$verify_existing\" == \"1\" ]]"
	assertScriptContains(t, script, mainAnchor)
	assertScriptOrderAfter(t, script, mainAnchor, "require_existing_release", "verify_uploaded_asset_digests")
	assertScriptOrderAfter(t, script, mainAnchor, "verify_uploaded_asset_digests", "require_existing_release_marked_latest")
	assertScriptOrderAfter(t, script, mainAnchor, "if [[ \"$verify_existing\" == \"1\" ]]", "if [[ \"$dry_run\" == \"1\" ]]")
}

func TestGitHubReleasePublisherVerifyExistingRejectsLocalPreviousPackageProof(t *testing.T) {
	previousApp := writePreviousPackageTrustFixture(t, base64.StdEncoding.EncodeToString(make([]byte, 32)))
	cmd := exec.Command("bash", "publish_github_release.sh", "--verify-existing")
	cmd.Env = appendWSLEnvKeysWithGitWorktree(t, []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"VERSION=v9.9.9",
		"SUPER_DOLPHIN_UPDATE_GITHUB_REPO=super-dolphin/releases",
		"SUPER_DOLPHIN_UPDATE_PUBLIC_KEY=dGVzdC1wdWJsaWMta2V5",
		"SUPER_DOLPHIN_UPDATE_PREVIOUS_APP=" + bashArg("", previousApp),
	}, "PATH")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected verify-existing without previous proof to fail, got:\n%s", output)
	}
	if !strings.Contains(string(output), "local previous APP/DMG overrides are allowed only for explicit non-release dry-run tests") {
		t.Fatalf("expected formal gate to reject local previous proof, got:\n%s", output)
	}
	if strings.Contains(string(output), "missing or empty release asset") {
		t.Fatalf("verify-existing continued to asset validation without previous proof:\n%s", output)
	}
}

func TestGitHubReleasePublisherCanInspectLatestAssets(t *testing.T) {
	script := readScript(t, "publish_github_release.sh")

	for _, want := range []string{
		"--inspect-latest",
		"inspect_latest=1",
		"inspect_latest_release_assets",
		"GitHub latest release has canonical release assets",
		"GitHub latest release $latest_tag missing asset",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptOrderAfter(t, script, "if [[ \"$inspect_latest\" == \"1\" ]]", "require_gh_access", "inspect_latest_release_assets")
	assertScriptOrderAfter(t, script, "if [[ \"$inspect_latest\" == \"1\" ]]", "if [[ \"$inspect_latest\" == \"1\" ]]", "require_tag")
	assertScriptContains(t, script, "validate_release_assets\nrequire_gh_access\n\nif [[ \"$verify_existing\" == \"1\" ]]")
}

func TestGitHubReleasePublisherCanPrintReleaseContext(t *testing.T) {
	script := readScript(t, "publish_github_release.sh")

	for _, want := range []string{
		"--print-context",
		"print_context=1",
		"print_release_context",
		"candidate tag check:",
		"candidate release:",
		"public key source:",
		"env_path_status SUPER_DOLPHIN_UPDATE_SIGNING_KEY file",
		"stage dir: set VERSION or SUPER_DOLPHIN_RELEASE_STAGE_DIR",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptOrderAfter(t, script, "if [[ \"$print_context\" == \"1\" ]]", "require_gh_access", "print_release_context")
	assertScriptOrderAfter(t, script, "if [[ \"$print_context\" == \"1\" ]]", "if [[ \"$print_context\" == \"1\" ]]", "require_tag")

	binDir := t.TempDir()
	writeGitHubReleaseFakeGH(t, binDir, "v1.0.3", map[string]struct {
		digest string
		size   int
	}{
		"Super-Dolphin-darwin-arm64.dmg":         {digest: strings.Repeat("a", 64), size: 10},
		"Super-Dolphin-darwin-arm64.update.json": {digest: strings.Repeat("b", 64), size: 10},
		"Super-Dolphin-windows-arm64.exe":        {digest: strings.Repeat("c", 64), size: 10},
	})
	previousApp := writePreviousPackageTrustFixture(t, base64.StdEncoding.EncodeToString(make([]byte, 32)))
	signingKey := filepath.Join(t.TempDir(), "ed25519.key")
	if err := os.WriteFile(signingKey, []byte("hidden signing key"), 0o600); err != nil {
		t.Fatalf("write signing key marker: %v", err)
	}

	cmd := exec.Command("bash", "publish_github_release.sh", "--print-context")
	cmd.Env = appendWSLEnvKeysWithGitWorktree(t, []string{
		"PATH=" + bashArg("", binDir) + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"SUPER_DOLPHIN_UPDATE_GITHUB_REPO=super-dolphin/releases",
		"SUPER_DOLPHIN_UPDATE_PUBLIC_KEY=super-secret-public-key",
		"SUPER_DOLPHIN_UPDATE_PREVIOUS_APP=" + bashArg("", previousApp),
		"SUPER_DOLPHIN_UPDATE_SIGNING_KEY=" + bashArg("", signingKey),
	}, "PATH")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("print-context failed: %v\n%s", err, output)
	}
	for _, want := range []string{
		"GitHub release context",
		"latest: v1.0.3 https://github.com/super-dolphin/releases/releases/tag/v1.0.3",
		"candidate tag: unset",
		"remote asset: Super-Dolphin-darwin-arm64.dmg present",
		"stage dir: set VERSION or SUPER_DOLPHIN_RELEASE_STAGE_DIR to check local staged assets",
		"public key source: SUPER_DOLPHIN_UPDATE_PUBLIC_KEY configured",
		"SUPER_DOLPHIN_UPDATE_PREVIOUS_APP: configured (directory exists)",
		"SUPER_DOLPHIN_UPDATE_SIGNING_KEY: configured (file exists)",
	} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("print-context output missing %q:\n%s", want, output)
		}
	}
	for _, secret := range []string{"super-secret-public-key", previousApp, signingKey, "hidden signing key"} {
		if strings.Contains(string(output), secret) {
			t.Fatalf("print-context leaked secret/path %q:\n%s", secret, output)
		}
	}
}

func TestGitHubReleasePublisherPrintContextShowsCandidateVersionStatus(t *testing.T) {
	binDir := t.TempDir()
	writeGitHubReleaseFakeGH(t, binDir, "v1.0.3", map[string]struct {
		digest string
		size   int
	}{})

	cmd := exec.Command("bash", "publish_github_release.sh", "--print-context")
	cmd.Env = appendWSLEnvKeysWithGitWorktree(t, []string{
		"PATH=" + bashArg("", binDir) + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"VERSION=v1.0.4",
		"SUPER_DOLPHIN_UPDATE_GITHUB_REPO=super-dolphin/releases",
	}, "PATH")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("print-context failed: %v\n%s", err, output)
	}
	for _, want := range []string{
		"candidate tag: v1.0.4",
		"candidate tag check: greater than latest",
		"candidate release: already exists",
		"stage dir:",
		"local staged: Super-Dolphin-darwin-arm64.dmg missing",
	} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("print-context output missing %q:\n%s", want, output)
		}
	}
}

func TestGitHubReleasePublisherDownloadsLatestPreviousDMG(t *testing.T) {
	content := []byte("previous dmg bytes")
	sum := sha256.Sum256(content)
	binDir := t.TempDir()
	writeGitHubReleaseFakeGH(t, binDir, "v1.0.3", map[string]struct {
		digest string
		size   int
	}{
		"Super-Dolphin-darwin-arm64.dmg": {digest: hex.EncodeToString(sum[:]), size: len(content)},
	})
	writeGitHubReleaseFakeCurl(t, binDir, content)
	outputDir := filepath.Join(t.TempDir(), "proof dir")
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		t.Fatalf("create output dir: %v", err)
	}

	cmd := exec.Command("bash", "publish_github_release.sh", "--download-latest-previous-dmg", bashArg("", outputDir))
	cmd.Env = appendWSLEnvKeysWithGitWorktree(t, []string{
		"PATH=" + bashArg("", binDir) + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"SUPER_DOLPHIN_UPDATE_GITHUB_REPO=super-dolphin/releases",
	}, "PATH")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("download previous dmg failed: %v\n%s", err, output)
	}
	target := filepath.Join(outputDir, "Super-Dolphin-v1.0.3-darwin-arm64.dmg")
	if got, err := os.ReadFile(target); err != nil || string(got) != string(content) {
		t.Fatalf("downloaded DMG = %q, %v; want %q", got, err, content)
	}
	bashTarget := bashArg("", target)
	for _, want := range []string{
		"previous DMG downloaded and verified: " + bashTarget,
		"export SUPER_DOLPHIN_UPDATE_PREVIOUS_DMG=" + strings.ReplaceAll(bashTarget, " ", "\\ "),
	} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("download output missing %q:\n%s", want, output)
		}
	}
}

type previousDMGProofCase struct {
	name          string
	appCount      int
	bundleVersion string
	codesign      string
	trustSigner   string
	trustValid    bool
	want          string
}

func TestGitHubReleasePreviousDMGProofRejectsForgedOrAmbiguousBundles(t *testing.T) {
	tests := []previousDMGProofCase{
		{name: "valid", appCount: 1, bundleVersion: "1.0.3", codesign: "TEAM-EXACT", trustSigner: "TEAM-EXACT", trustValid: true},
		{name: "forged app", appCount: 1, bundleVersion: "1.0.3", codesign: "TEAM-EXACT", trustSigner: "TEAM-EXACT", want: "forged package trust"},
		{name: "multiple apps", appCount: 2, bundleVersion: "1.0.3", codesign: "TEAM-EXACT", trustSigner: "TEAM-EXACT", trustValid: true, want: "exactly one top-level app bundle; found 2"},
		{name: "version mismatch", appCount: 1, bundleVersion: "1.0.2", codesign: "TEAM-EXACT", trustSigner: "TEAM-EXACT", trustValid: true, want: "does not match exact GitHub release v1.0.3"},
		{name: "signer mismatch", appCount: 1, bundleVersion: "1.0.3", codesign: "TEAM-OTHER", trustSigner: "TEAM-EXACT", trustValid: true, want: "does not match trusted current package signer TEAM-EXACT"},
		{name: "self signed attacker", appCount: 1, bundleVersion: "1.0.3", codesign: "TEAM-ATTACKER", trustSigner: "TEAM-ATTACKER", trustValid: true, want: "does not match trusted current package signer TEAM-EXACT"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runPreviousDMGProofCase(t, test)
		})
	}
}

func runPreviousDMGProofCase(t *testing.T, test previousDMGProofCase) {
	t.Helper()
	binDir := t.TempDir()
	writePreviousDMGInspectionTools(t, binDir)
	mountSource := writePreviousDMGApps(t, test.appCount, test.codesign)
	dmg := filepath.Join(t.TempDir(), "previous.dmg")
	if err := os.WriteFile(dmg, []byte("dmg"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "publish_github_release.sh", "--verify-previous-dmg-test", dmg, "v1.0.3", "TEAM-EXACT")
	valid := "0"
	if test.trustValid {
		valid = "1"
	}
	cmd.Env = appendWSLEnvKeysWithGitWorktree(t, []string{
		"PATH=" + bashArg("", binDir) + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"SUPER_DOLPHIN_UPDATE_GITHUB_REPO=super-dolphin/releases", "SUPER_DOLPHIN_ALLOW_LOCAL_PREVIOUS_RELEASE_TEST=1",
		"FAKE_DMG_MOUNT_SOURCE=" + bashArg("", mountSource), "FAKE_BUNDLE_VERSION=" + test.bundleVersion,
		"FAKE_CODESIGN_SIGNER=" + test.codesign, "FAKE_TRUST_SIGNER=" + test.trustSigner, "FAKE_TRUST_VALID=" + valid,
	}, "PATH")
	output, err := cmd.CombinedOutput()
	if test.want == "" && err != nil {
		t.Fatalf("valid previous DMG proof failed: %v\n%s", err, output)
	}
	if test.want != "" && (err == nil || !strings.Contains(string(output), test.want)) {
		t.Fatalf("previous DMG proof error = %v, output:\n%s\nwant %q", err, output, test.want)
	}
}

func writePreviousDMGApps(t *testing.T, count int, signers ...string) string {
	t.Helper()
	mountSource := t.TempDir()
	if len(signers) > 1 {
		t.Fatal("at most one fake signer is allowed")
	}
	for i := range count {
		app := filepath.Join(mountSource, fmt.Sprintf("Super Dolphin %d.app", i+1))
		if err := os.MkdirAll(filepath.Join(app, "Contents", "Resources"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
		if len(signers) == 1 {
			writeFakeCodesignSigner(t, app, signers[0])
		}
	}
	return mountSource
}

func writePreviousDMGInspectionTools(t *testing.T, binDir string) {
	t.Helper()
	scripts := map[string]string{
		"hdiutil": `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "attach" ]]; then
  source="${2:-}"
  mount=""
  while [[ $# -gt 0 ]]; do
    if [[ "${1:-}" == "-mountpoint" ]]; then mount="${2:-}"; break; fi
    shift
  done
  [[ -n "$mount" ]]
  mount_source="$FAKE_DMG_MOUNT_SOURCE"
  if [[ -n "${FAKE_CURRENT_DMG:-}" && "$source" == "$FAKE_CURRENT_DMG" ]]; then
    mount_source="$FAKE_CURRENT_DMG_MOUNT_SOURCE"
  fi
  cp -R "$mount_source"/. "$mount"/
  exit 0
fi
[[ "${1:-}" == "detach" ]]
`,
		"plutil": `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$FAKE_BUNDLE_VERSION"
`,
		"codesign": `#!/usr/bin/env bash
set -euo pipefail
app=""
for arg in "$@"; do app="$arg"; done
marker="$app/Contents/Resources/.fake-codesign-signer"
signer="${FAKE_CODESIGN_SIGNER:-}"
if [[ -f "$marker" ]]; then signer="$(cat "$marker")"; fi
printf 'TeamIdentifier=%s\n' "$signer" >&2
`,
		"go": `#!/usr/bin/env bash
set -euo pipefail
[[ "${1:-}" == "run" && "${2:-}" == "./cmd/super-dolphin-release-manifest" ]]
if [[ " $* " != *" -print-package-trust-public-key "* ]]; then
  exit 0
fi
[[ "${FAKE_TRUST_VALID:-0}" == "1" ]] || { echo "forged package trust" >&2; exit 1; }
expected_source=""
expected_signer=""
while [[ $# -gt 0 ]]; do
  case "${1:-}" in
    -expected-package-source) expected_source="${2:-}"; shift 2 ;;
    -expected-package-signer) expected_signer="${2:-}"; shift 2 ;;
    *) shift ;;
  esac
done
[[ "$expected_source" == "super-dolphin/releases" ]] || { echo "trust source mismatch" >&2; exit 1; }
[[ "$expected_signer" == "$FAKE_TRUST_SIGNER" ]] || { echo "trust signer mismatch" >&2; exit 1; }
printf 'dGVzdC1wdWJsaWMta2V5\n'
`,
	}
	for name, content := range scripts {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(content), 0o700); err != nil {
			t.Fatalf("write fake %s: %v", name, err)
		}
	}
}

func TestGitHubReleasePublisherVerifyExistingExecutesDigestCheck(t *testing.T) {
	stageDir := canonicalTempDir(t)
	assetDigests := map[string]struct {
		digest string
		size   int
	}{}
	for name, content := range map[string]string{
		"Super-Dolphin-darwin-arm64.dmg":         "darwin artifact",
		"Super-Dolphin-darwin-arm64.update.json": `{"darwin":"manifest"}`,
		"Super-Dolphin-windows-arm64.exe":        "windows arm64 artifact",
	} {
		raw := []byte(content)
		if err := os.WriteFile(filepath.Join(stageDir, name), raw, 0o600); err != nil {
			t.Fatalf("write staged asset %s: %v", name, err)
		}
		sum := sha256.Sum256(raw)
		assetDigests[name] = struct {
			digest string
			size   int
		}{digest: hex.EncodeToString(sum[:]), size: len(raw)}
	}

	binDir := t.TempDir()
	publicKey := base64.StdEncoding.EncodeToString(make([]byte, 32))
	writeGitHubReleaseFakeGo(t, binDir, publicKey)
	writeGitHubReleaseFakeGH(t, binDir, "v9.9.9", assetDigests)
	writeGitHubReleaseFakeCurl(t, binDir, []byte("darwin artifact"))
	previousApp := writePreviousPackageTrustFixture(t, publicKey)
	writeFakeCodesignSigner(t, previousApp, "TEAM-EXACT")
	currentMount := writePreviousDMGApps(t, 1, "TEAM-EXACT")
	writePreviousDMGInspectionTools(t, binDir)

	cmd := exec.Command("bash", "publish_github_release.sh", "--verify-existing", "--stage-dir", bashArg("", stageDir))
	cmd.Env = appendWSLEnvKeysWithGitWorktree(t, []string{
		"PATH=" + bashArg("", binDir) + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"VERSION=v9.9.9",
		"SUPER_DOLPHIN_UPDATE_GITHUB_REPO=super-dolphin/releases",
		"FAKE_DMG_MOUNT_SOURCE=" + bashArg("", filepath.Dir(previousApp)),
		"FAKE_CURRENT_DMG=" + bashArg("", filepath.Join(stageDir, "Super-Dolphin-darwin-arm64.dmg")),
		"FAKE_CURRENT_DMG_MOUNT_SOURCE=" + bashArg("", currentMount),
		"FAKE_GH_PREVIOUS_TAG=v9.9.8",
		"FAKE_BUNDLE_VERSION=9.9.8",
		"FAKE_CODESIGN_SIGNER=TEAM-EXACT",
		"FAKE_TRUST_SIGNER=TEAM-EXACT",
		"FAKE_TRUST_VALID=1",
	}, "PATH")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("verify-existing failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "existing GitHub release verified: https://github.com/super-dolphin/releases/releases/tag/v9.9.9") {
		t.Fatalf("verify-existing output missing success line:\n%s", output)
	}
}

func TestGitHubReleasePublisherInspectLatestAcceptsMacOSAssets(t *testing.T) {
	binDir := t.TempDir()
	writeGitHubReleaseFakeGH(t, binDir, "v1.0.3", map[string]struct {
		digest string
		size   int
	}{
		"Super-Dolphin-darwin-arm64.dmg":         {digest: strings.Repeat("a", 64), size: 10},
		"Super-Dolphin-darwin-arm64.update.json": {digest: strings.Repeat("b", 64), size: 10},
		"Super-Dolphin-windows-arm64.exe":        {digest: strings.Repeat("c", 64), size: 10},
	})

	cmd := exec.Command("bash", "publish_github_release.sh", "--inspect-latest")
	cmd.Env = appendWSLEnvKeysWithGitWorktree(t, []string{
		"PATH=" + bashArg("", binDir) + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"SUPER_DOLPHIN_UPDATE_GITHUB_REPO=super-dolphin/releases",
	}, "PATH")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect-latest failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "GitHub latest release has canonical release assets: https://github.com/super-dolphin/releases/releases/tag/v1.0.3") {
		t.Fatalf("inspect-latest output missing success line:\n%s", output)
	}
}

func TestGitHubReleasePublisherRequiresVersionBeforeGitHubAccess(t *testing.T) {
	binDir := t.TempDir()
	writeFailingGitHubReleaseFakeGH(t, binDir)

	cmd := exec.Command("bash", "publish_github_release.sh", "--dry-run")
	cmd.Env = appendWSLEnvKeysWithGitWorktree(t, []string{
		"PATH=" + bashArg("", binDir) + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"SUPER_DOLPHIN_UPDATE_GITHUB_REPO=super-dolphin/releases",
	}, "PATH")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected dry-run without VERSION to fail, got:\n%s", output)
	}
	if !strings.Contains(string(output), "VERSION is required, for example v1.0.4") {
		t.Fatalf("expected VERSION error before GitHub access, got:\n%s", output)
	}
	if strings.Contains(string(output), "gh should not be called") {
		t.Fatalf("script called gh before requiring VERSION:\n%s", output)
	}
}

func writeGitHubReleaseFakeGo(t *testing.T, binDir, publicKey string) {
	t.Helper()
	content := `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "run" && "${2:-}" == "./cmd/super-dolphin-release-manifest" ]]; then
  if [[ " $* " == *" -print-package-trust-public-key "* ]]; then
    printf '%s\n' ` + bashQuote(publicKey) + `
  fi
  exit 0
fi
echo "unexpected go invocation: $*" >&2
exit 1
`
	if err := os.WriteFile(filepath.Join(binDir, "go"), []byte(content), 0o700); err != nil {
		t.Fatalf("write fake go: %v", err)
	}
}

func writePreviousPackageTrustFixture(t *testing.T, publicKey string) string {
	t.Helper()
	appPath := filepath.Join(t.TempDir(), "Super Dolphin.app")
	resources := filepath.Join(appPath, "Contents", "Resources")
	binDir := filepath.Join(resources, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatalf("create previous package Resources: %v", err)
	}
	updater := filepath.Join(binDir, "super-dolphin-updater")
	guard := filepath.Join(binDir, "super-dolphin-guard")
	if err := os.WriteFile(updater, []byte("previous updater"), 0o700); err != nil {
		t.Fatalf("write previous updater: %v", err)
	}
	if err := os.WriteFile(guard, []byte("previous guard"), 0o700); err != nil {
		t.Fatalf("write previous Guard: %v", err)
	}
	if err := appupdate.WritePackageTrust(appupdate.PackageTrustWriteRequest{
		OutputPath: filepath.Join(resources, "update-trust.json"),
		Platform:   "darwin-arm64", Enabled: true,
		SourceKind: "github", SourceValue: "super-dolphin/releases",
		ManifestKey: publicKey, Channel: "gray", SignerIdentity: "TEAM-EXACT",
		UpdaterPath: updater, GuardPath: guard,
	}); err != nil {
		t.Fatalf("write previous package trust: %v", err)
	}
	return appPath
}

func writeFailingGitHubReleaseFakeGH(t *testing.T, binDir string) {
	t.Helper()
	content := `#!/usr/bin/env bash
set -euo pipefail
echo "gh should not be called before VERSION is validated" >&2
exit 99
`
	if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(content), 0o700); err != nil {
		t.Fatalf("write failing fake gh: %v", err)
	}
}

func writeGitHubReleaseFakeGH(t *testing.T, binDir, latestTag string, assets map[string]struct {
	digest string
	size   int
}) {
	t.Helper()
	var queryChecks strings.Builder
	for name, asset := range assets {
		fmt.Fprintf(&queryChecks, "    if [[ \"$query\" == *%s* ]]; then\n", bashQuote(name))
		queryChecks.WriteString("      if [[ \"$query\" == *'.digest' ]]; then\n")
		fmt.Fprintf(&queryChecks, "        printf 'sha256:%s\\n'\n", asset.digest)
		queryChecks.WriteString("      elif [[ \"$query\" == *'.size' ]]; then\n")
		fmt.Fprintf(&queryChecks, "        printf '%d\\n'\n", asset.size)
		queryChecks.WriteString("      elif [[ \"$query\" == *'.browser_download_url' ]]; then\n")
		fmt.Fprintf(&queryChecks, "        printf 'https://downloads.example.test/%s\\n'\n", name)
		queryChecks.WriteString("      fi\n")
		queryChecks.WriteString("      exit 0\n")
		queryChecks.WriteString("    fi\n")
	}
	content := `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "auth" && "${2:-}" == "status" ]]; then
  exit 0
fi
if [[ "${1:-}" == "release" && "${2:-}" == "view" ]]; then
  exit 0
fi
if [[ "${1:-}" == "api" ]]; then
  endpoint="${2:-}"
  query=""
  while [[ $# -gt 0 ]]; do
    if [[ "${1:-}" == "--jq" ]]; then
      query="${2:-}"
      break
    fi
    shift
  done
  if [[ -n "${FAKE_GH_API_LOG:-}" ]]; then
    printf '%s|%s\n' "$endpoint" "$query" >>"$FAKE_GH_API_LOG"
  fi
  if [[ "$endpoint" == "repos/super-dolphin/releases" ]]; then
    exit 0
  fi
  if [[ "$endpoint" == "repos/super-dolphin/releases/releases/latest" && "$query" == ".tag_name" ]]; then
    printf '%s\n' ` + bashQuote(latestTag) + `
    exit 0
  fi
  if [[ "$endpoint" == "repos/super-dolphin/releases/releases/latest" && "$query" == ".html_url" ]]; then
    printf 'https://github.com/super-dolphin/releases/releases/tag/%s\n' ` + bashQuote(latestTag) + `
    exit 0
  fi
  if [[ "$endpoint" == "repos/super-dolphin/releases/releases/tags/"* && "$query" == ".html_url" ]]; then
    printf 'https://github.com/super-dolphin/releases/releases/tag/%s\n' "${endpoint##*/}"
    exit 0
  fi
  if [[ "$endpoint" == "repos/super-dolphin/releases/releases?per_page=100" ]]; then
    printf '%s\n' "${FAKE_GH_PREVIOUS_TAG:-v0.0.1}"
    exit 0
  fi
  if [[ "$endpoint" == "repos/super-dolphin/releases/releases/latest" || "$endpoint" == "repos/super-dolphin/releases/releases/tags/"* ]]; then
` + queryChecks.String() + `
    exit 0
  fi
fi
echo "unexpected gh invocation: $*" >&2
exit 1
`
	if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(content), 0o700); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
}

func TestGitHubReleasePublisherCanDerivePublicKeyFromPreviousPackage(t *testing.T) {
	script := readScript(t, "publish_github_release.sh")

	for _, want := range []string{
		"resolve_update_public_key",
		"SUPER_DOLPHIN_UPDATE_PUBLIC_KEY=\"$previous_public_key\"",
		"export SUPER_DOLPHIN_UPDATE_PUBLIC_KEY",
		"read_previous_update_public_key",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptOrder(t, script, "read_previous_update_public_key", "resolve_update_public_key")
	assertScriptOrderAfter(t, script, "if [[ \"$verify_existing\" != \"1\" ]]", "resolve_update_public_key", "go run ./cmd/super-dolphin-release-manifest -check-key")
	assertScriptOrderAfter(t, script, "if [[ \"$verify_existing\" != \"1\" ]]", "go run ./cmd/super-dolphin-release-manifest -check-key", "validate_release_assets")
	assertScriptOrderAfter(t, script, "else\n  resolve_update_public_key", "resolve_update_public_key", "validate_release_assets")
}

func TestReleaseManifestCommandCanVerifyKeyAndManifest(t *testing.T) {
	source := readScript(t, "../cmd/super-dolphin-release-manifest/main.go")

	for _, want := range []string{
		"checkKey",
		"verifyManifest",
		"printPackageTrustKey",
		"publicKey",
		"currentVersion",
		"verifySigningKeyMatchesPublicKey",
		"verifyExistingManifest",
		"appupdate.VerifiedPackageTrustIdentity",
		"appupdate.VerifySignedManifest",
	} {
		assertScriptContains(t, source, want)
	}
}
