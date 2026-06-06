package appupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestProvideConfigRequiresHTTPSManifestWithHost(t *testing.T) {
	t.Setenv(envUpdateEnabled, "1")
	t.Setenv(envUpdateManifestURL, "http://updates.example.test/manifest.json")
	t.Setenv(envUpdatePublicKey, base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv(envUpdateStageDir, t.TempDir())
	t.Setenv(envUpdateHelperPath, "/bin/echo")
	t.Setenv(envUpdateTargetApp, "/Applications/Super Dolphin.app")
	t.Setenv(envVersion, "1.0.0")

	_, err := ProvideConfig(&platformconfig.Config{})
	if err == nil || !strings.Contains(err.Error(), "HTTPS with host") {
		t.Fatalf("ProvideConfig() error = %v, want HTTPS manifest URL rejection", err)
	}
}

func TestProvideConfigReadsCurrentVersionFromInfoPlist(t *testing.T) {
	publicKey, _ := testManifestKeypair(t)
	targetApp := filepath.Join(t.TempDir(), "Super Dolphin.app")
	writeInfoPlist(t, targetApp, "1.2.3")
	t.Setenv(envUpdateEnabled, "1")
	t.Setenv(envUpdateManifestURL, "https://updates.example.test/manifest.json")
	t.Setenv(envUpdatePublicKey, base64.StdEncoding.EncodeToString(publicKey))
	t.Setenv(envUpdateStageDir, t.TempDir())
	t.Setenv(envUpdateHelperPath, "/bin/echo")
	t.Setenv(envUpdateTargetApp, targetApp)
	t.Setenv(envUpdatePlatform, "darwin-arm64")
	t.Setenv(envVersion, "")
	t.Setenv(envUpdateVersion, "")

	cfg, err := ProvideConfig(&platformconfig.Config{})
	if err != nil {
		t.Fatalf("ProvideConfig() error = %v", err)
	}
	if cfg.CurrentVersion != "1.2.3" {
		t.Fatalf("CurrentVersion = %q, want Info.plist version", cfg.CurrentVersion)
	}
}

func TestProvideConfigDerivesPackagedUpdatePaths(t *testing.T) {
	publicKey, _ := testManifestKeypair(t)
	home := t.TempDir()
	targetApp := filepath.Join(t.TempDir(), "Super Dolphin.app")
	resources := filepath.Join(targetApp, "Contents", "Resources")
	helper := filepath.Join(resources, "bin", updaterHelperName)
	writeInfoPlist(t, targetApp, "1.2.4")
	if err := os.MkdirAll(filepath.Dir(helper), 0o755); err != nil {
		t.Fatalf("MkdirAll(helper dir) error = %v", err)
	}
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(helper) error = %v", err)
	}
	t.Setenv(envUpdateEnabled, "1")
	t.Setenv(envUpdateManifestURL, "https://updates.example.test/manifest.json")
	t.Setenv(envUpdatePublicKey, base64.StdEncoding.EncodeToString(publicKey))
	t.Setenv(envUpdateChannel, "gray")
	t.Setenv(envRuntimeResources, resources)
	t.Setenv(envSuperDolphinHome, home)

	cfg, err := ProvideConfig(&platformconfig.Config{})
	if err != nil {
		t.Fatalf("ProvideConfig() error = %v", err)
	}
	if cfg.StageDir != filepath.Join(home, "updates") {
		t.Fatalf("StageDir = %q, want packaged home updates dir", cfg.StageDir)
	}
	if cfg.HelperPath != helper {
		t.Fatalf("HelperPath = %q, want bundled updater helper", cfg.HelperPath)
	}
	if cfg.TargetAppPath != targetApp {
		t.Fatalf("TargetAppPath = %q, want current packaged app", cfg.TargetAppPath)
	}
	if cfg.CurrentVersion != "1.2.4" {
		t.Fatalf("CurrentVersion = %q, want Info.plist version", cfg.CurrentVersion)
	}
}

func TestCheckMapsErrNoUpdateToUnavailable(t *testing.T) {
	publicKey, privateKey := testManifestKeypair(t)
	payload := testManifestPayload()
	rawManifest := signTestManifest(t, privateKey, payload)
	svc := newService(testServiceConfig(publicKey, t.TempDir(), "1.2.3"), httpClientFor(map[string][]byte{
		"https://updates.example.test/manifest.json": rawManifest,
	}), nil)

	result, err := svc.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.Available {
		t.Fatalf("Check() Available = true, want false for ErrNoUpdate")
	}
}

func TestDownloadVerifiesArtifactAndWritesSelectedUpdate(t *testing.T) {
	publicKey, privateKey := testManifestKeypair(t)
	artifactBody := []byte("signed dmg bytes")
	payload := testManifestPayload()
	payload.Artifacts[0].Size = int64(len(artifactBody))
	payload.Artifacts[0].SHA256 = sha256Hex(artifactBody)
	rawManifest := signTestManifest(t, privateKey, payload)
	stageDir := t.TempDir()
	svc := newService(testServiceConfig(publicKey, stageDir, "1.2.2"), httpClientFor(map[string][]byte{
		"https://updates.example.test/manifest.json":                rawManifest,
		"https://updates.example.com/Super-Dolphin-1.2.3-arm64.dmg": artifactBody,
	}), nil)

	result, err := svc.Download(context.Background())
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if result.StagedManifestPath != filepath.Join(stageDir, selectedUpdateFilename) {
		t.Fatalf("StagedManifestPath = %q, want selected update path", result.StagedManifestPath)
	}
	staged, err := readSelectedUpdate(result.StagedManifestPath)
	if err != nil {
		t.Fatalf("readSelectedUpdate() error = %v", err)
	}
	if staged.DMGPath != result.DMGPath {
		t.Fatalf("staged DMGPath = %q, want %q", staged.DMGPath, result.DMGPath)
	}
	gotBody, err := os.ReadFile(result.DMGPath)
	if err != nil {
		t.Fatalf("ReadFile(DMGPath) error = %v", err)
	}
	if !bytes.Equal(gotBody, artifactBody) {
		t.Fatalf("downloaded body = %q, want %q", gotBody, artifactBody)
	}
}

func TestDownloadRejectsArtifactSHA256Mismatch(t *testing.T) {
	publicKey, privateKey := testManifestKeypair(t)
	payload := testManifestPayload()
	payload.Artifacts[0].Size = 3
	payload.Artifacts[0].SHA256 = strings.Repeat("0", 64)
	rawManifest := signTestManifest(t, privateKey, payload)
	svc := newService(testServiceConfig(publicKey, t.TempDir(), "1.2.2"), httpClientFor(map[string][]byte{
		"https://updates.example.test/manifest.json":                rawManifest,
		"https://updates.example.com/Super-Dolphin-1.2.3-arm64.dmg": []byte("dmg"),
	}), nil)

	_, err := svc.Download(context.Background())
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("Download() error = %v, want sha256 mismatch", err)
	}
}

func TestInstallRequiresRequestQuitBeforeStartingHelper(t *testing.T) {
	stageDir := t.TempDir()
	marker := filepath.Join(stageDir, "helper.started")
	helper := writeHelperScript(t, marker, 0)
	svc := newService(Config{
		Enabled:       true,
		StageDir:      stageDir,
		HelperPath:    helper,
		TargetAppPath: "/Applications/Super Dolphin.app",
	}, nil, nil)
	writeSelectedInstallFixture(t, svc)

	_, err := svc.Install(context.Background())
	if err == nil || !strings.Contains(err.Error(), "request quit callback") {
		t.Fatalf("Install() error = %v, want missing RequestQuit", err)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("helper marker stat error = %v, want not started", statErr)
	}
}

func TestInstallIgnoresCanceledContextAfterDetachedHelperStarts(t *testing.T) {
	stageDir := t.TempDir()
	marker := filepath.Join(stageDir, "helper.started")
	helper := writeHelperScript(t, marker, 200*time.Millisecond)
	quitCalled := false
	svc := newService(Config{
		Enabled:       true,
		StageDir:      stageDir,
		HelperPath:    helper,
		TargetAppPath: "/Applications/Super Dolphin.app",
	}, nil, func() {
		quitCalled = true
	})
	writeSelectedInstallFixture(t, svc)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := svc.Install(ctx)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if !result.Started {
		t.Fatalf("Install() Started = false, want true")
	}
	if !quitCalled {
		t.Fatal("RequestQuit was not called")
	}
	waitForFile(t, marker)
}

func TestInstallPassesAllowUnsignedToHelper(t *testing.T) {
	stageDir := t.TempDir()
	argsPath := filepath.Join(stageDir, "helper.args")
	helper := writeArgsHelperScript(t, argsPath)
	svc := newService(Config{
		Enabled:       true,
		StageDir:      stageDir,
		HelperPath:    helper,
		TargetAppPath: "/Applications/Super Dolphin.app",
		AllowUnsigned: true,
	}, nil, func() {})
	writeSelectedInstallFixture(t, svc)

	result, err := svc.Install(context.Background())
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if !result.Started {
		t.Fatalf("Install() Started = false, want true")
	}
	waitForFile(t, argsPath)
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("ReadFile(argsPath) error = %v", err)
	}
	if !strings.Contains(string(args), "-allow-unsigned") {
		t.Fatalf("helper args = %q, want -allow-unsigned", string(args))
	}
}

func testServiceConfig(publicKey []byte, stageDir, currentVersion string) Config {
	return Config{
		Enabled:        true,
		ManifestURL:    "https://updates.example.test/manifest.json",
		PublicKey:      publicKey,
		Channel:        "stable",
		StageDir:       stageDir,
		HelperPath:     "/bin/echo",
		TargetAppPath:  "/Applications/Super Dolphin.app",
		Platform:       "darwin-arm64",
		CurrentVersion: currentVersion,
	}
}

func httpClientFor(responses map[string][]byte) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, ok := responses[req.URL.String()]
		if !ok {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Status:     "404 Not Found",
				Body:       io.NopCloser(strings.NewReader("not found")),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(bytes.NewReader(body)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func writeInfoPlist(t *testing.T, appPath, version string) {
	t.Helper()
	contents := filepath.Join(appPath, "Contents")
	if err := os.MkdirAll(contents, 0o755); err != nil {
		t.Fatalf("MkdirAll(Contents) error = %v", err)
	}
	body := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
<key>CFBundleShortVersionString</key>
<string>` + version + `</string>
</dict>
</plist>
`
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(Info.plist) error = %v", err)
	}
}

func writeHelperScript(t *testing.T, marker string, sleep time.Duration) string {
	t.Helper()
	helper := filepath.Join(t.TempDir(), "helper.sh")
	body := "#!/usr/bin/env bash\nprintf started > " + shellQuote(marker) + "\n"
	if sleep > 0 {
		body += "sleep " + durationSecondsString(sleep) + "\n"
	}
	if err := os.WriteFile(helper, []byte(body), 0o755); err != nil {
		t.Fatalf("WriteFile(helper) error = %v", err)
	}
	return helper
}

func writeArgsHelperScript(t *testing.T, argsPath string) string {
	t.Helper()
	helper := filepath.Join(t.TempDir(), "helper-args.sh")
	body := "#!/usr/bin/env bash\nprintf '%s\\n' \"$*\" > " + shellQuote(argsPath) + "\n"
	if err := os.WriteFile(helper, []byte(body), 0o755); err != nil {
		t.Fatalf("WriteFile(helper) error = %v", err)
	}
	return helper
}

func durationSecondsString(d time.Duration) string {
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(d.Seconds(), 'f', 3, 64), "0"), ".")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func writeSelectedInstallFixture(t *testing.T, svc *service) {
	t.Helper()
	if err := os.MkdirAll(svc.cfg.StageDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(StageDir) error = %v", err)
	}
	dmgPath := filepath.Join(svc.cfg.StageDir, "fixture.dmg")
	if err := os.WriteFile(dmgPath, []byte("dmg"), 0o600); err != nil {
		t.Fatalf("WriteFile(dmg) error = %v", err)
	}
	staged := selectedUpdate{
		Payload: testManifestPayload(),
		Artifact: UpdateArtifact{
			Platform: "darwin-arm64",
			URL:      "https://updates.example.com/Super-Dolphin-1.2.3-arm64.dmg",
			SHA256:   sha256Hex([]byte("dmg")),
			Size:     3,
		},
		DMGPath:      dmgPath,
		DownloadedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := writeSelectedUpdate(svc.stagedManifestPath(), staged); err != nil {
		t.Fatalf("writeSelectedUpdate() error = %v", err)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}
