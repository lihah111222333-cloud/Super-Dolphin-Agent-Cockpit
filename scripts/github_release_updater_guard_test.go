package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestPackageEnvExampleDocumentsGitHubReleaseUpdateInputsWithoutSecrets(t *testing.T) {
	example := readScript(t, "../.env.packaging.example")
	for _, want := range []string{
		"SUPER_DOLPHIN_UPDATE_GITHUB_REPO=xiaoxiaotest9527-bit/-",
		"SUPER_DOLPHIN_UPDATE_PUBLIC_KEY=",
		"SUPER_DOLPHIN_UPDATE_CHANNEL=gray",
		"SUPER_DOLPHIN_UPDATE_MINIMUM_VERSION=0.0.0",
		"SUPER_DOLPHIN_UPDATE_PREVIOUS_DMG=",
		"SUPER_DOLPHIN_UPDATE_PREVIOUS_APP=",
		"SUPER_DOLPHIN_UPDATE_PREVIOUS_ENV_FILE=",
		"SUPER_DOLPHIN_UPDATE_SIGNING_KEY=",
	} {
		assertScriptContains(t, example, want)
	}
	assertScriptDoesNotContain(t, example, "BEGIN PRIVATE KEY")
	assertScriptDoesNotContain(t, example, "sk-")
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
		"SUPER_DOLPHIN_UPDATE_PREVIOUS_ENV_FILE",
		"resolve_update_public_key",
		"previous_public_key_from_dmg",
		"go run ./cmd/super-dolphin-release-manifest",
		"-public-key \"$SUPER_DOLPHIN_UPDATE_PUBLIC_KEY\"",
		"-artifact-url \"$artifact_url\"",
	} {
		assertScriptContains(t, macos, want)
	}
	assertScriptDoesNotContain(t, macos, "SUPER_DOLPHIN_UPDATE_GITHUB_REPO:-xiaoxiaotest9527-bit/-")
	assertScriptOrder(t, macos, "resolve_update_public_key", "go run \"$root/cmd/super-dolphin-release-manifest\"")
	assertScriptOrder(t, macos, "./scripts/package_macos.sh", "go run ./cmd/super-dolphin-release-manifest")

	for _, want := range []string{
		"Resolve-GitHubReleaseRepo",
		"$GitHubReleaseRepo = Resolve-GitHubReleaseRepo",
		"$PackageVersion = Get-EnvOrDefault -Name 'SUPER_DOLPHIN_PACKAGE_VERSION'",
		"Super-Dolphin-windows-arm64.exe",
		"Super-Dolphin-windows-arm64.update.json",
		"$env:SUPER_DOLPHIN_UPDATE_GITHUB_REPO = $GitHubReleaseRepo",
		"SUPER_DOLPHIN_UPDATE_PREVIOUS_PUBLIC_KEY",
		"Assert-UpdatePublicKeyContinuity",
		"cmd/super-dolphin-release-manifest",
		"-public-key",
		"-artifact-url",
	} {
		assertScriptContains(t, windows, want)
	}
	assertScriptDoesNotContain(t, windows, "Default 'xiaoxiaotest9527-bit/-'")
	assertScriptOrder(t, windows, "scripts\\package_windows_local.ps1", "cmd/super-dolphin-release-manifest")
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
	assertScriptDoesNotContain(t, script, "Super-Dolphin-windows-arm64.exe")
	assertScriptDoesNotContain(t, script, "Super-Dolphin-windows-arm64.update.json")
	assertScriptOrder(t, script, "require_previous_update_public_key", "validate_release_assets")
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
		"verify_uploaded_asset_digests",
		"existing GitHub release verified",
	} {
		assertScriptContains(t, script, want)
	}
	assertScriptOrderAfter(t, script, "if [[ \"$verify_existing\" != \"1\" ]]", "else\n  resolve_update_public_key", "validate_release_assets")
	assertScriptOrderAfter(t, script, "else\n  resolve_update_public_key", "resolve_update_public_key", "require_previous_update_public_key")
	assertScriptOrderAfter(t, script, "else\n  resolve_update_public_key", "require_previous_update_public_key", "validate_release_assets")
	assertScriptOrder(t, script, "validate_release_assets", "if [[ \"$verify_existing\" == \"1\" ]]")
	assertScriptOrderAfter(t, script, "if [[ \"$verify_existing\" == \"1\" ]]", "require_existing_release", "verify_uploaded_asset_digests")
	assertScriptOrderAfter(t, script, "if [[ \"$verify_existing\" == \"1\" ]]", "verify_uploaded_asset_digests", "require_existing_release_marked_latest")
	assertScriptOrder(t, script, "if [[ \"$verify_existing\" == \"1\" ]]", "if [[ \"$dry_run\" == \"1\" ]]")
}

func TestGitHubReleasePublisherVerifyExistingRequiresPreviousPackageProof(t *testing.T) {
	cmd := exec.Command("bash", "publish_github_release.sh", "--verify-existing")
	cmd.Env = appendWSLEnvKeysWithGitWorktree(t, []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"VERSION=v9.9.9",
		"SUPER_DOLPHIN_UPDATE_GITHUB_REPO=super-dolphin/releases",
		"SUPER_DOLPHIN_UPDATE_PUBLIC_KEY=dGVzdC1wdWJsaWMta2V5",
	}, "PATH")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected verify-existing without previous proof to fail, got:\n%s", output)
	}
	if !strings.Contains(string(output), "required to prove old clients trust this update key") {
		t.Fatalf("expected previous package proof error, got:\n%s", output)
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
		"GitHub latest release has canonical update assets",
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
	})
	previousEnv := filepath.Join(t.TempDir(), "previous.env")
	if err := os.WriteFile(previousEnv, []byte("SUPER_DOLPHIN_UPDATE_PUBLIC_KEY=hidden\n"), 0o600); err != nil {
		t.Fatalf("write previous env: %v", err)
	}
	signingKey := filepath.Join(t.TempDir(), "ed25519.key")
	if err := os.WriteFile(signingKey, []byte("hidden signing key"), 0o600); err != nil {
		t.Fatalf("write signing key marker: %v", err)
	}

	cmd := exec.Command("bash", "publish_github_release.sh", "--print-context")
	cmd.Env = appendWSLEnvKeysWithGitWorktree(t, []string{
		"PATH=" + bashArg("", binDir) + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"SUPER_DOLPHIN_UPDATE_GITHUB_REPO=super-dolphin/releases",
		"SUPER_DOLPHIN_UPDATE_PUBLIC_KEY=super-secret-public-key",
		"SUPER_DOLPHIN_UPDATE_PREVIOUS_ENV_FILE=" + bashArg("", previousEnv),
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
		"SUPER_DOLPHIN_UPDATE_PREVIOUS_ENV_FILE: configured (file exists)",
		"SUPER_DOLPHIN_UPDATE_SIGNING_KEY: configured (file exists)",
	} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("print-context output missing %q:\n%s", want, output)
		}
	}
	for _, secret := range []string{"super-secret-public-key", previousEnv, signingKey, "hidden signing key"} {
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

func TestGitHubReleasePublisherVerifyExistingExecutesDigestCheck(t *testing.T) {
	stageDir := t.TempDir()
	assetDigests := map[string]struct {
		digest string
		size   int
	}{}
	for name, content := range map[string]string{
		"Super-Dolphin-darwin-arm64.dmg":         "darwin artifact",
		"Super-Dolphin-darwin-arm64.update.json": `{"darwin":"manifest"}`,
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
	writeGitHubReleaseFakeGo(t, binDir)
	writeGitHubReleaseFakeGH(t, binDir, "v9.9.9", assetDigests)
	previousEnv := filepath.Join(t.TempDir(), "previous.env")
	if err := os.WriteFile(previousEnv, []byte("SUPER_DOLPHIN_UPDATE_PUBLIC_KEY=dGVzdC1wdWJsaWMta2V5\n"), 0o600); err != nil {
		t.Fatalf("write previous env: %v", err)
	}

	cmd := exec.Command("bash", "publish_github_release.sh", "--verify-existing", "--stage-dir", bashArg("", stageDir))
	cmd.Env = appendWSLEnvKeysWithGitWorktree(t, []string{
		"PATH=" + bashArg("", binDir) + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"VERSION=v9.9.9",
		"SUPER_DOLPHIN_UPDATE_GITHUB_REPO=super-dolphin/releases",
		"SUPER_DOLPHIN_UPDATE_PREVIOUS_ENV_FILE=" + bashArg("", previousEnv),
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
	if !strings.Contains(string(output), "GitHub latest release has canonical update assets: https://github.com/super-dolphin/releases/releases/tag/v1.0.3") {
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

func writeGitHubReleaseFakeGo(t *testing.T, binDir string) {
	t.Helper()
	content := `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "run" && "${2:-}" == "./cmd/super-dolphin-release-manifest" ]]; then
  exit 0
fi
echo "unexpected go invocation: $*" >&2
exit 1
`
	if err := os.WriteFile(filepath.Join(binDir, "go"), []byte(content), 0o700); err != nil {
		t.Fatalf("write fake go: %v", err)
	}
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
		queryChecks.WriteString("    if [[ \"$query\" == *" + bashQuote(name) + "* ]]; then\n")
		queryChecks.WriteString("      if [[ \"$query\" == *'.digest' ]]; then\n")
		queryChecks.WriteString(fmt.Sprintf("        printf 'sha256:%s\\n'\n", asset.digest))
		queryChecks.WriteString("      elif [[ \"$query\" == *'.size' ]]; then\n")
		queryChecks.WriteString(fmt.Sprintf("        printf '%d\\n'\n", asset.size))
		queryChecks.WriteString("      elif [[ \"$query\" == *'.browser_download_url' ]]; then\n")
		queryChecks.WriteString("        printf 'https://downloads.example.test/" + name + "\\n'\n")
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

func writeGitHubReleaseFakeCurl(t *testing.T, binDir string, content []byte) {
	t.Helper()
	contentPath := filepath.Join(binDir, "curl-content")
	if err := os.WriteFile(contentPath, content, 0o600); err != nil {
		t.Fatalf("write fake curl content: %v", err)
	}
	script := `#!/usr/bin/env bash
set -euo pipefail
out=""
while [[ $# -gt 0 ]]; do
  if [[ "${1:-}" == "-o" ]]; then
    out="${2:-}"
    shift 2
    continue
  fi
  shift
done
[[ -n "$out" ]] || { echo "missing -o" >&2; exit 1; }
cp ` + bashQuote(bashArg("", contentPath)) + ` "$out"
`
	if err := os.WriteFile(filepath.Join(binDir, "curl"), []byte(script), 0o700); err != nil {
		t.Fatalf("write fake curl: %v", err)
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
	assertScriptOrder(t, script, "resolve_update_public_key", "require_previous_update_public_key")
	assertScriptOrderAfter(t, script, "if [[ \"$verify_existing\" != \"1\" ]]", "resolve_update_public_key", "go run ./cmd/super-dolphin-release-manifest -check-key")
	assertScriptOrderAfter(t, script, "if [[ \"$verify_existing\" != \"1\" ]]", "go run ./cmd/super-dolphin-release-manifest -check-key", "validate_release_assets")
	assertScriptOrderAfter(t, script, "else\n  resolve_update_public_key", "resolve_update_public_key", "validate_release_assets")
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
