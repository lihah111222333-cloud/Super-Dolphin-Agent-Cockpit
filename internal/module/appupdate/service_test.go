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
	"runtime"
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

func TestProvideConfigAcceptsGitHubRepoWithoutLegacyManifestURL(t *testing.T) {
	publicKey, _ := testManifestKeypair(t)
	t.Setenv(envUpdateEnabled, "1")
	t.Setenv(envUpdateGitHubRepo, "xiaoxiaotest9527-bit/-")
	t.Setenv(envUpdatePublicKey, base64.StdEncoding.EncodeToString(publicKey))
	t.Setenv(envUpdateStageDir, t.TempDir())
	t.Setenv(envUpdateHelperPath, "/bin/echo")
	t.Setenv(envUpdateTargetApp, "/Applications/Super Dolphin.app")
	t.Setenv(envUpdatePlatform, "darwin-arm64")
	t.Setenv(envVersion, "1.0.0")

	cfg, err := ProvideConfig(&platformconfig.Config{})
	if err != nil {
		t.Fatalf("ProvideConfig() error = %v", err)
	}
	if cfg.GitHubRepo != "xiaoxiaotest9527-bit/-" {
		t.Fatalf("GitHubRepo = %q, want configured repo", cfg.GitHubRepo)
	}
	if cfg.ManifestURL != "" {
		t.Fatalf("ManifestURL = %q, want empty legacy manifest URL", cfg.ManifestURL)
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
	if !result.Enabled {
		t.Fatalf("Check() Enabled = false, want true for configured updates")
	}
}

func TestCheckReportsDisabledWhenUpdateConfigDisabled(t *testing.T) {
	svc := newService(Config{}, nil, nil)

	result, err := svc.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.Enabled {
		t.Fatalf("Check() Enabled = true, want false for disabled updates")
	}
	if result.Available {
		t.Fatalf("Check() Available = true, want false for disabled updates")
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

func TestAppUpdateDownloadRejectsBodyLargerThanManifestSize(t *testing.T) {
	publicKey, privateKey := testManifestKeypair(t)
	artifactBody := []byte("oversized artifact body")
	payload := testManifestPayload()
	payload.Artifacts[0].Size = 3
	payload.Artifacts[0].SHA256 = sha256Hex(artifactBody[:3])
	rawManifest := signTestManifest(t, privateKey, payload)
	stageDir := t.TempDir()
	body := &trackedReadCloser{data: artifactBody, maxChunk: 1}
	svc := newService(testServiceConfig(publicKey, stageDir, "1.2.2"), &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case "https://updates.example.test/manifest.json":
			return okResponse(req, bytes.NewReader(rawManifest)), nil
		case "https://updates.example.com/Super-Dolphin-1.2.3-arm64.dmg":
			return okResponse(req, body), nil
		default:
			return notFoundResponse(req), nil
		}
	})}, nil)

	_, err := svc.Download(context.Background())
	if err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("Download() error = %v, want manifest size rejection", err)
	}
	if body.bytesRead() > int(payload.Artifacts[0].Size)+1 {
		t.Fatalf("download read %d bytes, want at most manifest size plus sentinel byte", body.bytesRead())
	}
	artifactPath := filepath.Join(stageDir, dmgFilename)
	for _, path := range []string{artifactPath, artifactPath + ".tmp"} {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("Stat(%q) error = %v, want file removed", path, statErr)
		}
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
	if runtime.GOOS == "windows" {
		t.Skip("darwin helper launch uses /bin/sh")
	}
	stageDir := t.TempDir()
	marker := filepath.Join(stageDir, "helper.started")
	helper := writeHelperScript(t, marker, 200*time.Millisecond)
	quitCalled := make(chan struct{}, 1)
	svc := newService(Config{
		Enabled:       true,
		StageDir:      stageDir,
		HelperPath:    helper,
		TargetAppPath: "/Applications/Super Dolphin.app",
	}, nil, func() {
		quitCalled <- struct{}{}
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
	waitForSignal(t, quitCalled, "RequestQuit")
	waitForFile(t, marker)
}

func TestInstallPassesAllowUnsignedToHelper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("darwin helper launch uses /bin/sh")
	}
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
	if !strings.Contains(string(args), "-wait-pid "+strconv.Itoa(os.Getpid())) {
		t.Fatalf("helper args = %q, want current process wait pid", string(args))
	}
}

func TestInstallCommandUsesDetachedLauncherForMacHelper(t *testing.T) {
	stageDir := t.TempDir()
	helper := filepath.Join(stageDir, "helper.sh")
	svc := newService(Config{
		Enabled:       true,
		StageDir:      stageDir,
		HelperPath:    helper,
		TargetAppPath: "/Applications/Super Dolphin.app",
	}, nil, func() {})
	dmgPath := filepath.Join(stageDir, "fixture.dmg")
	staged := selectedUpdate{
		Artifact:     UpdateArtifact{Platform: "darwin-arm64"},
		ArtifactPath: dmgPath,
		DMGPath:      dmgPath,
	}

	cmd, gotHelper, err := svc.installCommand(staged)
	if err != nil {
		t.Fatalf("installCommand() error = %v", err)
	}
	if gotHelper != helper {
		t.Fatalf("helper = %q, want %q", gotHelper, helper)
	}
	if filepath.Base(cmd.Path) != "sh" {
		t.Fatalf("command path = %q, want shell launcher", cmd.Path)
	}
	joinedArgs := strings.Join(cmd.Args, " ")
	for _, want := range []string{"nohup", helper, "-wait-pid " + strconv.Itoa(os.Getpid()), "-log", "super-dolphin-updater.log"} {
		if !strings.Contains(joinedArgs, want) {
			t.Fatalf("command args = %q, want %q", joinedArgs, want)
		}
	}
}

func TestInstallStartsWindowsInstallerWithSilentFlag(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a shell-script fixture with .exe name")
	}
	stageDir := t.TempDir()
	argsPath := filepath.Join(stageDir, "installer.args")
	installer := writeArgsHelperScriptWithName(t, argsPath, "Super-Dolphin-windows-amd64.exe")
	quitCalled := make(chan struct{}, 1)
	svc := newService(Config{
		Enabled:  true,
		StageDir: stageDir,
		Platform: "windows-amd64",
	}, nil, func() {
		quitCalled <- struct{}{}
	})
	writeSelectedInstallFixtureForPlatform(t, svc, "windows-amd64", installer)

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
	if strings.TrimSpace(string(args)) != "/S" {
		t.Fatalf("installer args = %q, want /S", string(args))
	}
	if result.Helper != installer {
		t.Fatalf("Install() Helper = %q, want installer path", result.Helper)
	}
	waitForSignal(t, quitCalled, "RequestQuit")
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
			return notFoundResponse(req), nil
		}
		return okResponse(req, bytes.NewReader(body)), nil
	})}
}

func okResponse(req *http.Request, body io.Reader) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(body), Header: make(http.Header), Request: req}
}

func notFoundResponse(req *http.Request) *http.Response {
	return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Body: io.NopCloser(strings.NewReader("not found")), Header: make(http.Header), Request: req}
}

type trackedReadCloser struct {
	data     []byte
	maxChunk int
	read     int
}

func (r *trackedReadCloser) Read(p []byte) (int, error) {
	if r.read >= len(r.data) {
		return 0, io.EOF
	}
	n := len(r.data) - r.read
	if r.maxChunk > 0 && n > r.maxChunk {
		n = r.maxChunk
	}
	if n > len(p) {
		n = len(p)
	}
	copy(p, r.data[r.read:r.read+n])
	r.read += n
	return n, nil
}

func (r *trackedReadCloser) Close() error { return nil }

func (r *trackedReadCloser) bytesRead() int { return r.read }

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
	return writeArgsHelperScriptWithName(t, argsPath, "helper-args.sh")
}

func writeArgsHelperScriptWithName(t *testing.T, argsPath, name string) string {
	t.Helper()
	helper := filepath.Join(t.TempDir(), name)
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
	dmgPath := filepath.Join(svc.cfg.StageDir, "fixture.dmg")
	writeSelectedInstallFixtureForPlatform(t, svc, "darwin-arm64", dmgPath)
}

func writeSelectedInstallFixtureForPlatform(t *testing.T, svc *service, platform, artifactPath string) {
	t.Helper()
	if err := os.MkdirAll(svc.cfg.StageDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(StageDir) error = %v", err)
	}
	if _, err := os.Stat(artifactPath); err != nil {
		if err := os.WriteFile(artifactPath, []byte("dmg"), 0o700); err != nil {
			t.Fatalf("WriteFile(artifact) error = %v", err)
		}
	}
	artifactBytes, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("ReadFile(artifact) error = %v", err)
	}
	artifactURL := "https://updates.example.com/Super-Dolphin-1.2.3-arm64.dmg"
	if platform == "windows-amd64" {
		artifactURL = "https://github.com/xiaoxiaotest9527-bit/-/releases/download/v1.2.3/Super-Dolphin-windows-amd64.exe"
	}
	staged := selectedUpdate{
		Payload: testManifestPayload(),
		Artifact: UpdateArtifact{
			Platform: platform,
			URL:      artifactURL,
			SHA256:   sha256Hex(artifactBytes),
			Size:     int64(len(artifactBytes)),
		},
		DMGPath:      artifactPath,
		ArtifactPath: artifactPath,
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

func waitForSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}
