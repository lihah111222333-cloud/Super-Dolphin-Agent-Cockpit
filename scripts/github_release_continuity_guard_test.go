package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type releaseContinuityFixture struct {
	stageDir     string
	binDir       string
	previousApp  string
	currentDMG   string
	currentMount string
	apiLog       string
}

func TestGitHubReleaseContinuityUsesExactPreviousTagEndpoint(t *testing.T) {
	fixture := newReleaseContinuityFixture(t)
	output, err := fixture.run(t, "TEAM-EXACT", "TEAM-EXACT")
	if err != nil {
		t.Fatalf("verify-existing failed: %v\n%s", err, output)
	}
	log, err := os.ReadFile(fixture.apiLog)
	if err != nil {
		t.Fatal(err)
	}
	previousEndpoint := "repos/super-dolphin/releases/releases/tags/v9.9.8|"
	for _, field := range []string{".browser_download_url", ".digest", ".size"} {
		if !apiLogContains(string(log), previousEndpoint, field) {
			t.Fatalf("previous asset metadata %s did not use exact v9.9.8 endpoint:\n%s", field, log)
		}
	}
	if apiLogContains(string(log), "repos/super-dolphin/releases/releases/latest|", ".assets[]") {
		t.Fatalf("latest endpoint supplied asset metadata after exact tag resolution:\n%s", log)
	}
}

func TestGitHubReleaseContinuityRejectsSelfSignedPreviousSigner(t *testing.T) {
	fixture := newReleaseContinuityFixture(t)
	output, err := fixture.run(t, "TEAM-ATTACKER", "TEAM-ATTACKER")
	if err == nil || !strings.Contains(string(output), "previous app codesign signer TEAM-ATTACKER does not match trusted current package signer TEAM-EXACT") {
		t.Fatalf("self-signed previous package was not rejected: %v\n%s", err, output)
	}
}

func newReleaseContinuityFixture(t *testing.T) releaseContinuityFixture {
	t.Helper()
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
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		assetDigests[name] = struct {
			digest string
			size   int
		}{digest: hex.EncodeToString(sum[:]), size: len(raw)}
	}
	binDir := t.TempDir()
	writeGitHubReleaseFakeGH(t, binDir, "v9.9.9", assetDigests)
	writeGitHubReleaseFakeCurl(t, binDir, []byte("darwin artifact"))
	previousApp := writePreviousPackageTrustFixture(t, base64.StdEncoding.EncodeToString(make([]byte, 32)))
	currentMount := writePreviousDMGApps(t, 1, "TEAM-EXACT")
	writePreviousDMGInspectionTools(t, binDir)
	return releaseContinuityFixture{
		stageDir: stageDir, binDir: binDir, previousApp: previousApp,
		currentDMG:   filepath.Join(stageDir, "Super-Dolphin-darwin-arm64.dmg"),
		currentMount: currentMount, apiLog: filepath.Join(t.TempDir(), "gh-api.log"),
	}
}

func (fixture releaseContinuityFixture) run(t *testing.T, previousSigner, trustSigner string) ([]byte, error) {
	t.Helper()
	writeFakeCodesignSigner(t, fixture.previousApp, previousSigner)
	cmd := exec.Command("bash", "publish_github_release.sh", "--verify-existing", "--stage-dir", bashArg("", fixture.stageDir))
	cmd.Env = appendWSLEnvKeysWithGitWorktree(t, []string{
		"PATH=" + bashArg("", fixture.binDir) + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"VERSION=v9.9.9", "SUPER_DOLPHIN_UPDATE_GITHUB_REPO=super-dolphin/releases",
		"FAKE_DMG_MOUNT_SOURCE=" + bashArg("", filepath.Dir(fixture.previousApp)),
		"FAKE_CURRENT_DMG=" + bashArg("", fixture.currentDMG),
		"FAKE_CURRENT_DMG_MOUNT_SOURCE=" + bashArg("", fixture.currentMount),
		"FAKE_GH_PREVIOUS_TAG=v9.9.8", "FAKE_GH_API_LOG=" + bashArg("", fixture.apiLog),
		"FAKE_BUNDLE_VERSION=9.9.8", "FAKE_CODESIGN_SIGNER=" + previousSigner,
		"FAKE_TRUST_SIGNER=" + trustSigner, "FAKE_TRUST_VALID=1",
	}, "PATH")
	return cmd.CombinedOutput()
}

func apiLogContains(log, endpointPrefix, queryFragment string) bool {
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(line, endpointPrefix) && strings.Contains(line, queryFragment) {
			return true
		}
	}
	return false
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFakeCodesignSigner(t *testing.T, app, signer string) {
	t.Helper()
	path := filepath.Join(app, "Contents", "Resources", ".fake-codesign-signer")
	if err := os.WriteFile(path, []byte(signer), 0o600); err != nil {
		t.Fatal(err)
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
