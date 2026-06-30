package appupdate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const (
	testValidGitHubRepo   = "super-dolphin/releases"
	testBlockedGitHubRepo = "xiaoxiaotest9527-bit/-"
)

func TestCheckFetchesGitHubLatestReleaseManifestForCurrentPlatform(t *testing.T) {
	publicKey, privateKey := testManifestKeypair(t)
	payload := testGitHubManifestPayload(t, []byte("signed dmg bytes"), "darwin-arm64")
	rawManifest := signTestManifest(t, privateKey, payload)
	svc := newService(testGitHubServiceConfig(publicKey, t.TempDir(), "1.2.2", "darwin-arm64"), httpClientFor(map[string][]byte{
		testGitHubAPIURL(): []byte(testGitHubReleaseJSON(t, "v1.2.3", payload.Artifacts[0], "darwin-arm64")),
		testGitHubReleaseAssetURL(
			"v1.2.3",
			"Super-Dolphin-darwin-arm64.update.json",
		): rawManifest,
	}), nil)

	result, err := svc.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if !result.Available || result.Version != "v1.2.3" {
		t.Fatalf("Check() = %+v, want available v1.2.3", result)
	}
	if result.Artifact == nil || result.Artifact.Platform != "darwin-arm64" {
		t.Fatalf("Check() artifact = %+v, want darwin-arm64", result.Artifact)
	}
}

func TestCheckFetchesGitHubLatestReleaseManifestForWindowsEXE(t *testing.T) {
	publicKey, privateKey := testManifestKeypair(t)
	payload := testGitHubManifestPayload(t, []byte("signed exe bytes"), "windows-amd64")
	rawManifest := signTestManifest(t, privateKey, payload)
	svc := newService(testGitHubServiceConfig(publicKey, t.TempDir(), "1.2.2", "windows-amd64"), httpClientFor(map[string][]byte{
		testGitHubAPIURL(): []byte(testGitHubReleaseJSON(t, "v1.2.3", payload.Artifacts[0], "windows-amd64")),
		testGitHubReleaseAssetURL(
			"v1.2.3",
			"Super-Dolphin-windows-amd64.update.json",
		): rawManifest,
	}), nil)

	result, err := svc.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if !result.Available || result.Artifact == nil {
		t.Fatalf("Check() = %+v, want available windows artifact", result)
	}
	if result.Artifact.Platform != "windows-amd64" || !strings.HasSuffix(result.Artifact.URL, ".exe") {
		t.Fatalf("Check() artifact = %+v, want windows-amd64 exe", result.Artifact)
	}
}

func TestCheckRejectsGitHubReleaseMissingPlatformManifest(t *testing.T) {
	publicKey, _ := testManifestKeypair(t)
	artifact := testGitHubArtifact([]byte("signed dmg bytes"), "darwin-arm64")
	svc := newService(testGitHubServiceConfig(publicKey, t.TempDir(), "1.2.2", "darwin-arm64"), httpClientFor(map[string][]byte{
		testGitHubAPIURL(): []byte(testGitHubReleaseJSONWithoutManifest(t, "v1.2.3", artifact)),
	}), nil)

	_, err := svc.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Super-Dolphin-darwin-arm64.update.json") {
		t.Fatalf("Check() error = %v, want missing platform update manifest", err)
	}
}

func TestCheckRejectsGitHubReleaseMissingPlatformArtifact(t *testing.T) {
	publicKey, _ := testManifestKeypair(t)
	svc := newService(testGitHubServiceConfig(publicKey, t.TempDir(), "1.2.2", "windows-amd64"), httpClientFor(map[string][]byte{
		testGitHubAPIURL(): []byte(testGitHubReleaseJSONForAssets(t, []map[string]any{
			githubAssetMap(
				"Super-Dolphin-darwin-arm64.dmg",
				testGitHubReleaseAssetURL("v1.2.3", "Super-Dolphin-darwin-arm64.dmg"),
				12,
				strings.Repeat("a", 64),
			),
			githubAssetMap(
				"Super-Dolphin-windows-amd64.update.json",
				testGitHubReleaseAssetURL("v1.2.3", "Super-Dolphin-windows-amd64.update.json"),
				1234,
				strings.Repeat("b", 64),
			),
		}, "v1.2.3")),
	}), nil)

	_, err := svc.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Super-Dolphin-windows-amd64.exe") {
		t.Fatalf("Check() error = %v, want missing platform artifact", err)
	}
}

func TestCheckRejectsGitHubReleaseAssetDigestMismatch(t *testing.T) {
	publicKey, privateKey := testManifestKeypair(t)
	payload := testGitHubManifestPayload(t, []byte("signed dmg bytes"), "darwin-arm64")
	rawManifest := signTestManifest(t, privateKey, payload)
	releaseJSON := testGitHubReleaseJSONWithArtifactDigest(t, "v1.2.3", payload.Artifacts[0], "darwin-arm64", strings.Repeat("0", 64))
	svc := newService(testGitHubServiceConfig(publicKey, t.TempDir(), "1.2.2", "darwin-arm64"), httpClientFor(map[string][]byte{
		testGitHubAPIURL(): []byte(releaseJSON),
		testGitHubReleaseAssetURL(
			"v1.2.3",
			"Super-Dolphin-darwin-arm64.update.json",
		): rawManifest,
	}), nil)

	_, err := svc.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("Check() error = %v, want GitHub asset sha256 mismatch", err)
	}
}

func TestCheckRejectsGitHubReleaseAssetSizeMismatch(t *testing.T) {
	publicKey, privateKey := testManifestKeypair(t)
	payload := testGitHubManifestPayload(t, []byte("signed dmg bytes"), "darwin-arm64")
	rawManifest := signTestManifest(t, privateKey, payload)
	releaseJSON := testGitHubReleaseJSONWithArtifactSize(t, "v1.2.3", payload.Artifacts[0], "darwin-arm64", payload.Artifacts[0].Size+1)
	svc := newService(testGitHubServiceConfig(publicKey, t.TempDir(), "1.2.2", "darwin-arm64"), httpClientFor(map[string][]byte{
		testGitHubAPIURL(): []byte(releaseJSON),
		testGitHubReleaseAssetURL(
			"v1.2.3",
			"Super-Dolphin-darwin-arm64.update.json",
		): rawManifest,
	}), nil)

	_, err := svc.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("Check() error = %v, want GitHub asset size mismatch", err)
	}
}

func testGitHubServiceConfig(publicKey []byte, stageDir, currentVersion, platform string) Config {
	cfg := testServiceConfig(publicKey, stageDir, currentVersion)
	cfg.ManifestURL = ""
	cfg.GitHubRepo = testValidGitHubRepo
	cfg.Platform = platform
	return cfg
}

func TestValidateGitHubRepoRejectsPlaceholderTestRepo(t *testing.T) {
	err := validateGitHubRepo(testBlockedGitHubRepo)
	if err == nil || !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("validateGitHubRepo() error = %v, want placeholder/test repo rejection", err)
	}
}

func testGitHubManifestPayload(t *testing.T, body []byte, platform string) ManifestPayload {
	t.Helper()
	payload := testManifestPayload()
	payload.Artifacts[0] = testGitHubArtifact(body, platform)
	return payload
}

func testGitHubArtifact(body []byte, platform string) UpdateArtifact {
	extension := ".dmg"
	if strings.HasPrefix(platform, "windows-") {
		extension = ".exe"
	}
	return UpdateArtifact{
		Platform: platform,
		URL:      testGitHubReleaseAssetURL("v1.2.3", "Super-Dolphin-"+platform+extension),
		SHA256:   sha256Hex(body),
		Size:     int64(len(body)),
	}
}

func testGitHubReleaseJSON(t *testing.T, tag string, artifact UpdateArtifact, platform string) string {
	t.Helper()
	return testGitHubReleaseJSONWithArtifactDigest(t, tag, artifact, platform, artifact.SHA256)
}

func testGitHubReleaseJSONWithoutManifest(t *testing.T, tag string, artifact UpdateArtifact) string {
	t.Helper()
	artifactName := "Super-Dolphin-" + artifact.Platform + ".dmg"
	if strings.HasPrefix(artifact.Platform, "windows-") {
		artifactName = "Super-Dolphin-" + artifact.Platform + ".exe"
	}
	return testGitHubReleaseJSONForAssets(t, []map[string]any{
		githubAssetMap(artifactName, artifact.URL, artifact.Size, artifact.SHA256),
	}, tag)
}

func testGitHubReleaseJSONWithArtifactDigest(t *testing.T, tag string, artifact UpdateArtifact, platform, digest string) string {
	t.Helper()
	return testGitHubReleaseJSONForAssets(t, githubAssetMaps(tag, artifact, platform, digest, artifact.Size), tag)
}

func testGitHubReleaseJSONWithArtifactSize(t *testing.T, tag string, artifact UpdateArtifact, platform string, size int64) string {
	t.Helper()
	return testGitHubReleaseJSONForAssets(t, githubAssetMaps(tag, artifact, platform, artifact.SHA256, size), tag)
}

func githubAssetMaps(tag string, artifact UpdateArtifact, platform, digest string, size int64) []map[string]any {
	artifactName := "Super-Dolphin-" + platform + ".dmg"
	if strings.HasPrefix(platform, "windows-") {
		artifactName = "Super-Dolphin-" + platform + ".exe"
	}
	return []map[string]any{
		githubAssetMap(artifactName, artifact.URL, size, digest),
		githubAssetMap(
			"Super-Dolphin-"+platform+".update.json",
			testGitHubReleaseAssetURL(tag, "Super-Dolphin-"+platform+".update.json"),
			1234,
			strings.Repeat("b", 64),
		),
	}
}

func testGitHubAPIURL() string {
	return "https://api.github.com/repos/" + testValidGitHubRepo + "/releases/latest"
}

func testGitHubReleaseAssetURL(tag, name string) string {
	return "https://github.com/" + testValidGitHubRepo + "/releases/download/" + tag + "/" + name
}

func testGitHubReleaseJSONForAssets(t *testing.T, assets []map[string]any, tag string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"tag_name": tag,
		"assets":   assets,
	})
	if err != nil {
		t.Fatalf("Marshal(release) error = %v", err)
	}
	return string(raw)
}

func githubAssetMap(name, downloadURL string, size int64, sha256 string) map[string]any {
	return map[string]any{
		"name":                 name,
		"browser_download_url": downloadURL,
		"size":                 size,
		"digest":               "sha256:" + sha256,
	}
}
