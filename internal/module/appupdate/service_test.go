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

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdatefailure"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestProvideConfigRequiresHTTPSManifestWithHost(t *testing.T) {
	t.Setenv(envUpdateEnabled, "1")
	t.Setenv(envUpdateManifestURL, "http://updates.example.test/manifest.json")
	t.Setenv(envUpdatePublicKey, base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv(envUpdateStageDir, appUpdateRealTempDir(t))
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
	t.Setenv(envUpdateGitHubRepo, testValidGitHubRepo)
	t.Setenv(envUpdatePublicKey, base64.StdEncoding.EncodeToString(publicKey))
	t.Setenv(envUpdateStageDir, appUpdateRealTempDir(t))
	t.Setenv(envUpdateHelperPath, "/bin/echo")
	t.Setenv(envUpdateTargetApp, "/Applications/Super Dolphin.app")
	t.Setenv(envUpdatePlatform, "darwin-arm64")
	t.Setenv(envVersion, "1.0.0")

	cfg, err := ProvideConfig(&platformconfig.Config{})
	if err != nil {
		t.Fatalf("ProvideConfig() error = %v", err)
	}
	if cfg.GitHubRepo != testValidGitHubRepo {
		t.Fatalf("GitHubRepo = %q, want configured repo", cfg.GitHubRepo)
	}
	if cfg.ManifestURL != "" {
		t.Fatalf("ManifestURL = %q, want empty legacy manifest URL", cfg.ManifestURL)
	}
}

func TestProvideConfigRejectsBothUpdateSources(t *testing.T) {
	publicKey, _ := testManifestKeypair(t)
	t.Setenv(envUpdateEnabled, "1")
	t.Setenv(envUpdateManifestURL, "https://updates.example.test/manifest.json")
	t.Setenv(envUpdateGitHubRepo, testValidGitHubRepo)
	t.Setenv(envUpdatePublicKey, base64.StdEncoding.EncodeToString(publicKey))
	t.Setenv(envUpdateStageDir, appUpdateRealTempDir(t))
	t.Setenv(envUpdateHelperPath, "/bin/echo")
	t.Setenv(envUpdateTargetApp, "/Applications/Super Dolphin.app")
	t.Setenv(envUpdatePlatform, "darwin-arm64")
	t.Setenv(envVersion, "1.0.0")

	_, err := ProvideConfig(&platformconfig.Config{})
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("ProvideConfig() error = %v, want exactly-one update source rejection", err)
	}
}

func TestProvideConfigRejectsWindowsUpdatesWithoutAuthenticodeGate(t *testing.T) {
	publicKey, _ := testManifestKeypair(t)
	t.Setenv(envUpdateEnabled, "1")
	t.Setenv(envUpdateManifestURL, "https://updates.example.test/manifest.json")
	t.Setenv(envUpdatePublicKey, base64.StdEncoding.EncodeToString(publicKey))
	t.Setenv(envUpdateStageDir, appUpdateRealTempDir(t))
	t.Setenv(envUpdatePlatform, "windows-amd64")
	t.Setenv(envVersion, "1.0.0")

	_, err := ProvideConfig(&platformconfig.Config{})
	if err == nil || !strings.Contains(err.Error(), "SUPER_DOLPHIN_UPDATE_WINDOWS_") {
		t.Fatalf("ProvideConfig() error = %v, want Windows publisher requirement", err)
	}
}

func TestProvideConfigReadsCurrentVersionFromInfoPlist(t *testing.T) {
	publicKey, _ := testManifestKeypair(t)
	targetApp := filepath.Join(t.TempDir(), "Super Dolphin.app")
	writeInfoPlist(t, targetApp, "1.2.3")
	t.Setenv(envUpdateEnabled, "1")
	t.Setenv(envUpdateManifestURL, "https://updates.example.test/manifest.json")
	t.Setenv(envUpdatePublicKey, base64.StdEncoding.EncodeToString(publicKey))
	t.Setenv(envUpdateStageDir, appUpdateRealTempDir(t))
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
	t.Setenv(envUpdatePlatform, "darwin-arm64")
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
	svc := newService(testServiceConfig(publicKey, appUpdateRealTempDir(t), "1.2.3"), httpClientFor(map[string][]byte{
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

func TestCheckRejectsChunkedManifestWithoutContentLengthExceedingBodyLimit(t *testing.T) {
	publicKey, _ := testManifestKeypair(t)
	oversized := bytes.Repeat([]byte("x"), int(maxUpdateManifestBodyBytes)+1)
	svc := newService(testServiceConfig(publicKey, appUpdateRealTempDir(t), "1.2.3"), chunkedHTTPClientFor(map[string][]byte{
		"https://updates.example.test/manifest.json": oversized,
	}), nil)

	_, err := svc.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Check() error = %v, want body limit rejection", err)
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
	stageDir := appUpdateRealTempDir(t)
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
	stage, err := openStageBoundary(stageDir)
	if err != nil {
		t.Fatalf("openStageBoundary() error = %v", err)
	}
	defer func() {
		if closeErr := stage.Close(); closeErr != nil {
			t.Errorf("stage.Close() error = %v", closeErr)
		}
	}()
	staged, err := readSelectedUpdate(stage)
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
	stageDir := appUpdateRealTempDir(t)
	svc := newService(testServiceConfig(publicKey, stageDir, "1.2.2"), httpClientFor(map[string][]byte{
		"https://updates.example.test/manifest.json":                rawManifest,
		"https://updates.example.com/Super-Dolphin-1.2.3-arm64.dmg": []byte("dmg"),
	}), nil)

	_, err := svc.Download(context.Background())
	if !errors.Is(err, contract.ErrUpdateIntegrityInvalid) {
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
	stageDir := appUpdateRealTempDir(t)
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
	if _, statErr := os.Lstat(artifactPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Lstat(%q) error = %v, want final artifact absent", artifactPath, statErr)
	}
	temporary, statErr := os.Lstat(artifactPath + ".tmp")
	if statErr != nil {
		t.Fatalf("Lstat(temporary artifact) error = %v, want retained safe temporary file", statErr)
	}
	if !temporary.Mode().IsRegular() || temporary.Size() != payload.Artifacts[0].Size {
		t.Fatalf("temporary artifact = mode %v size %d, want regular file with %d bytes", temporary.Mode(), temporary.Size(), payload.Artifacts[0].Size)
	}
}

func TestDownloadRejectsStageAliasBeforeNetwork(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixture requires developer mode or elevated privilege on Windows")
	}
	for _, tc := range []struct {
		name      string
		entryName string
	}{
		{name: "artifact tmp", entryName: dmgFilename + ".tmp"},
		{name: "selected update", entryName: selectedUpdateFilename},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stageDir := appUpdateRealTempDir(t)
			outside := filepath.Join(appUpdateRealTempDir(t), "outside")
			sentinel := []byte("do not overwrite")
			if err := os.WriteFile(outside, sentinel, 0o600); err != nil {
				t.Fatalf("WriteFile(outside) error = %v", err)
			}
			if err := os.Symlink(outside, filepath.Join(stageDir, tc.entryName)); err != nil {
				t.Fatalf("Symlink(stage entry) error = %v", err)
			}
			publicKey, _ := testManifestKeypair(t)
			networkCalls := 0
			svc := newService(testServiceConfig(publicKey, stageDir, "1.2.2"), &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				networkCalls++
				return nil, errors.New("network must not be called for an aliased stage entry")
			})}, nil)

			_, err := svc.Download(context.Background())
			if err == nil || !strings.Contains(err.Error(), "alias") {
				t.Fatalf("Download() error = %v, want stage alias rejection", err)
			}
			if networkCalls != 0 {
				t.Fatalf("network calls = %d, want stage rejection before manifest fetch", networkCalls)
			}
			got, readErr := os.ReadFile(outside)
			if readErr != nil || !bytes.Equal(got, sentinel) {
				t.Fatalf("outside content = %q, read error = %v, want untouched sentinel", got, readErr)
			}
		})
	}
}

func TestInstallRequiresRequestQuitBeforeStartingHelper(t *testing.T) {
	stageDir := appUpdateRealTempDir(t)
	marker := filepath.Join(stageDir, "helper.started")
	helper := writeHelperScript(t, marker, 0)
	svc := newService(Config{
		Enabled:       true,
		StageDir:      stageDir,
		HelperPath:    helper,
		TargetAppPath: "/Applications/Super Dolphin.app",
		Platform:      "darwin-arm64",
	}, nil, nil)
	writeSelectedInstallFixture(t, svc)

	_, err := svc.Install(context.Background())
	if err == nil || err.Error() != "app update request quit callback is not configured" {
		t.Fatalf("Install() error = %v, want missing RequestQuit", err)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("helper marker stat error = %v, want not started", statErr)
	}
}

func TestInstallIgnoresCanceledContextAfterDetachedHelperStarts(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("detached Darwin helper launch requires a Darwin host")
	}
	stageDir := appUpdateRealTempDir(t)
	marker := filepath.Join(stageDir, "helper.started")
	helper := writeHelperScript(t, marker, 200*time.Millisecond)
	quitCalled := make(chan struct{}, 1)
	svc := newService(Config{
		Enabled:       true,
		StageDir:      stageDir,
		HelperPath:    helper,
		TargetAppPath: "/Applications/Super Dolphin.app",
		Platform:      "darwin-arm64",
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
	if runtime.GOOS != "darwin" {
		t.Skip("Darwin helper arguments require a Darwin host")
	}
	stageDir := appUpdateRealTempDir(t)
	argsPath := filepath.Join(stageDir, "helper.args")
	helper := writeArgsHelperScriptWithName(t, argsPath, "helper-args.sh")
	svc := newService(Config{
		Enabled:       true,
		StageDir:      stageDir,
		HelperPath:    helper,
		TargetAppPath: "/Applications/Super Dolphin.app",
		AllowUnsigned: true,
		Platform:      "darwin-arm64",
	}, nil, func() {})
	seedPreJournalFailure(t, stageDir, "UPDATE_SIGNATURE_INVALID")
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
	assertPreJournalFailureHidden(t, stageDir, "Install")
}

func TestInstallRejectsHelperLogAliasBeforeStartingHelper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("darwin helper launch uses /bin/sh")
	}
	stageDir := appUpdateRealTempDir(t)
	marker := filepath.Join(stageDir, "helper.started")
	helper := writeHelperScript(t, marker, 0)
	svc := newService(Config{
		Enabled:       true,
		StageDir:      stageDir,
		HelperPath:    helper,
		TargetAppPath: "/Applications/Super Dolphin.app",
	}, nil, func() {})
	writeSelectedInstallFixture(t, svc)
	outside := filepath.Join(appUpdateRealTempDir(t), "outside.log")
	sentinel := []byte("do not overwrite")
	if err := os.WriteFile(outside, sentinel, 0o600); err != nil {
		t.Fatalf("WriteFile(outside) error = %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(stageDir, helperLogFilename)); err != nil {
		t.Fatalf("Symlink(helper log path) error = %v", err)
	}

	_, err := svc.Install(context.Background())
	if err == nil || err.Error() != "open app update stage: app update stage entry \"super-dolphin-updater.log\" is an alias or reparse point" {
		t.Fatalf("Install() error = %v, want helper log alias rejection", err)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("helper marker stat error = %v, want helper not started", statErr)
	}
	got, readErr := os.ReadFile(outside)
	if readErr != nil || !bytes.Equal(got, sentinel) {
		t.Fatalf("outside content = %q, read error = %v, want untouched sentinel", got, readErr)
	}
}

func TestInstallCommandUsesDetachedLauncherForMacHelper(t *testing.T) {
	stageDir := appUpdateRealTempDir(t)
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

	cmd, gotHelper := installCommandForTest(t, svc, stageDir, staged)
	if gotHelper != helper {
		t.Fatalf("helper = %q, want %q", gotHelper, helper)
	}
	if filepath.Base(cmd.Path) != "sh" {
		t.Fatalf("command path = %q, want shell launcher", cmd.Path)
	}
	requireDetachedHelperCommand(t, cmd, "nohup", helper, "-wait-pid "+strconv.Itoa(os.Getpid()), "-log", "/dev/fd/1", "-pre-journal-generation "+recoveryTestGeneration)
}

func TestInstallStartsWindowsInstallerWithSilentFlag(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a shell-script fixture with .exe name")
	}
	stageDir := appUpdateRealTempDir(t)
	argsPath := filepath.Join(stageDir, "installer.args")
	installer := writeWindowsInstallerFixture(t, stageDir, argsPath)
	quitCalled := make(chan struct{}, 1)
	svc := newService(Config{
		Enabled:           true,
		StageDir:          stageDir,
		Platform:          "windows-amd64",
		WindowsPublisher:  "Super Dolphin Test Publisher",
		WindowsThumbprint: strings.Repeat("a", 40),
	}, nil, func() {
		quitCalled <- struct{}{}
	})
	svc.windowsSignatureVerifier = expectedWindowsSignatureVerifier(t, installer)
	writeSelectedInstallFixtureForPlatform(t, svc, "windows-amd64", installer)

	result := requireSilentWindowsInstall(t, svc, argsPath)
	if result.Helper != installer {
		t.Fatalf("Install() Helper = %q, want installer path", result.Helper)
	}
	waitForSignal(t, quitCalled, "RequestQuit")
}

func TestInstallRejectsWindowsInstallerBeforeStartWhenAuthenticodeGateFails(t *testing.T) {
	stageDir := appUpdateRealTempDir(t)
	argsPath := filepath.Join(stageDir, "installer.args")
	installer := filepath.Join(stageDir, exeFilename)
	if err := os.Rename(writeArgsHelperScriptWithName(t, argsPath, exeFilename), installer); err != nil {
		t.Fatalf("Rename(installer) error = %v", err)
	}
	quitCalled := make(chan struct{}, 1)
	svc := newService(Config{
		Enabled:           true,
		StageDir:          stageDir,
		Platform:          "windows-amd64",
		WindowsPublisher:  "Super Dolphin Test Publisher",
		WindowsThumbprint: strings.Repeat("a", 40),
	}, nil, func() {
		quitCalled <- struct{}{}
	})
	svc.windowsSignatureVerifier = func(path, publisher, thumbprint string) error {
		return errors.New("mock authenticode rejected")
	}
	writeSelectedInstallFixtureForPlatform(t, svc, "windows-amd64", installer)

	_, err := svc.Install(context.Background())
	if err == nil || err.Error() != "mock authenticode rejected" {
		t.Fatalf("Install() error = %v, want Authenticode rejection", err)
	}
	if _, statErr := os.Stat(argsPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("installer args stat error = %v, want installer not started", statErr)
	}
	select {
	case <-quitCalled:
		t.Fatal("RequestQuit called, want install fail-fast before shutdown")
	default:
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
			return notFoundResponse(req), nil
		}
		return okResponse(req, bytes.NewReader(body)), nil
	})}
}

func chunkedHTTPClientFor(responses map[string][]byte) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, ok := responses[req.URL.String()]
		if !ok {
			return notFoundResponse(req), nil
		}
		return chunkedOKResponse(req, bytes.NewReader(body)), nil
	})}
}

func chunkedOKResponse(req *http.Request, body io.Reader) *http.Response {
	resp := okResponse(req, body)
	resp.ContentLength = -1
	resp.TransferEncoding = []string{"chunked"}
	return resp
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

func TestInstallRejectsTamperedArtifact(t *testing.T) {
	stageDir := appUpdateRealTempDir(t)
	svc := newService(Config{
		Enabled:       true,
		StageDir:      stageDir,
		HelperPath:    "/bin/echo",
		TargetAppPath: "/Applications/Super Dolphin.app",
		Platform:      "darwin-arm64",
	}, nil, func() {})
	// Write staged manifest with correct SHA-256, then overwrite artifact with different content.
	writeSelectedInstallFixture(t, svc)
	dmgPath := filepath.Join(stageDir, dmgFilename)
	if err := os.WriteFile(dmgPath, []byte("tampered"), 0o600); err != nil {
		t.Fatalf("WriteFile(tampered artifact) error = %v", err)
	}

	_, err := svc.Install(context.Background())
	if !errors.Is(err, contract.ErrUpdateIntegrityInvalid) {
		t.Fatalf("Install() error = %v, want sha256 mismatch rejection", err)
	}
}

func TestInstallRejectsSelectedPlatformMismatchBeforeAttempt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("darwin helper launch uses /bin/sh")
	}
	stageDir := appUpdateRealTempDir(t)
	marker := filepath.Join(stageDir, "helper.started")
	svc := newService(Config{
		Enabled: true, StageDir: stageDir, Platform: "darwin-arm64",
		HelperPath: writeHelperScript(t, marker, 0), TargetAppPath: "/Applications/Super Dolphin.app",
	}, nil, func() {})
	artifactPath := filepath.Join(stageDir, dmgFilename)
	writeSelectedInstallFixtureForPlatform(t, svc, "darwin-amd64", artifactPath)

	if _, err := svc.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "platform") {
		t.Fatalf("Install() error = %v, want selected platform mismatch", err)
	}
	assertInstallAttemptNotStarted(t, stageDir, marker)
}

func TestInstallRejectsSelectedArtifactOutsideStageBeforeAttempt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("darwin helper launch uses /bin/sh")
	}
	stageDir := appUpdateRealTempDir(t)
	marker := filepath.Join(stageDir, "helper.started")
	svc := newService(Config{
		Enabled: true, StageDir: stageDir, Platform: "darwin-arm64",
		HelperPath: writeHelperScript(t, marker, 0), TargetAppPath: "/Applications/Super Dolphin.app",
	}, nil, func() {})
	artifactPath := filepath.Join(appUpdateRealTempDir(t), dmgFilename)
	writeSelectedInstallFixtureForPlatform(t, svc, "darwin-arm64", artifactPath)

	if _, err := svc.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "outside stage boundary") {
		t.Fatalf("Install() error = %v, want staged artifact identity mismatch", err)
	}
	assertInstallAttemptNotStarted(t, stageDir, marker)
}

func TestInstallRejectsNonCleanSelectedArtifactAliasBeforeAttempt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("darwin helper launch uses /bin/sh")
	}
	stageDir := appUpdateRealTempDir(t)
	marker := filepath.Join(stageDir, "helper.started")
	svc := newService(Config{
		Enabled: true, StageDir: stageDir, Platform: "darwin-arm64",
		HelperPath: writeHelperScript(t, marker, 0), TargetAppPath: "/Applications/Super Dolphin.app",
	}, nil, func() {})
	artifactPath := stageDir + "/./" + dmgFilename
	writeSelectedInstallFixtureForPlatform(t, svc, "darwin-arm64", artifactPath)

	if _, err := svc.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "outside stage boundary") {
		t.Fatalf("Install() error = %v, want non-clean staged artifact rejection", err)
	}
	assertInstallAttemptNotStarted(t, stageDir, marker)
}

func TestInstallRejectsDivergentDarwinDMGPathBeforeAttempt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("darwin helper launch uses /bin/sh")
	}
	stageDir := appUpdateRealTempDir(t)
	marker := filepath.Join(stageDir, "helper.started")
	svc := newService(Config{
		Enabled: true, StageDir: stageDir, Platform: "darwin-arm64",
		HelperPath: writeHelperScript(t, marker, 0), TargetAppPath: "/Applications/Super Dolphin.app",
	}, nil, func() {})
	artifactPath := filepath.Join(stageDir, dmgFilename)
	writeSelectedInstallFixtureForPlatform(t, svc, "darwin-arm64", artifactPath)
	stage, err := openStageBoundary(svc.cfg.StageDir)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := readSelectedUpdate(stage)
	if err != nil {
		t.Fatal(err)
	}
	staged.DMGPath = filepath.Join(appUpdateRealTempDir(t), dmgFilename)
	if err := writeSelectedUpdate(stage, staged); err != nil {
		t.Fatal(err)
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "dmg path") {
		t.Fatalf("Install() error = %v, want divergent Darwin dmg_path rejection", err)
	}
	assertInstallAttemptNotStarted(t, stageDir, marker)
}

func assertInstallAttemptNotStarted(t *testing.T, stageDir, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("helper marker stat error = %v, want helper not started", err)
	}
	if _, err := os.Stat(filepath.Join(stageDir, appupdatefailure.Filename)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pre-journal stat error = %v, want generation not begun", err)
	}
}
