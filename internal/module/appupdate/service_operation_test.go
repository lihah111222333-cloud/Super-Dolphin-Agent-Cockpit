package appupdate

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type updateOperationTestFixture struct {
	svc             *service
	verifierEntered chan struct{}
	verifierRelease chan struct{}
	quitCalled      chan struct{}
	manifestCalls   atomic.Int32
	artifactCalls   atomic.Int32
	verifierCalls   atomic.Int32
	launchCalls     atomic.Int32
	quitCalls       atomic.Int32
}

func TestUpdateOperationsRejectConcurrentCallsAndLatchAfterHelperStart(t *testing.T) {
	fixture := newUpdateOperationTestFixture(t)
	installDone := make(chan error, 1)
	go func() {
		_, err := fixture.svc.InstallLatest(context.Background())
		installDone <- err
	}()

	waitForSignal(t, fixture.verifierEntered, "first install verifier")
	requireUpdateOperationsRejected(t, fixture.svc, "concurrent")
	close(fixture.verifierRelease)
	if err := <-installDone; err != nil {
		t.Fatalf("InstallLatest() error = %v", err)
	}
	requireUpdateOperationsRejected(t, fixture.svc, "latched")
	fixture.requireSingleQuit(t)
	fixture.requireSingleSideEffect(t)
}

func newUpdateOperationTestFixture(t *testing.T) *updateOperationTestFixture {
	t.Helper()
	publicKey, privateKey := testManifestKeypair(t)
	artifactBody := []byte("windows installer")
	payload := testManifestPayload()
	payload.Artifacts[0] = UpdateArtifact{
		Platform: "windows-amd64",
		URL:      "https://updates.example.com/Super-Dolphin-windows-amd64.exe",
		SHA256:   sha256Hex(artifactBody),
		Size:     int64(len(artifactBody)),
	}
	fixture := &updateOperationTestFixture{
		verifierEntered: make(chan struct{}),
		verifierRelease: make(chan struct{}),
		quitCalled:      make(chan struct{}, 2),
	}
	stageDir := appUpdateRealTempDir(t)
	cfg := testServiceConfig(publicKey, stageDir, "1.2.2")
	cfg.Platform = "windows-amd64"
	cfg.WindowsPublisher = "Super Dolphin Test Publisher"
	cfg.WindowsThumbprint = strings.Repeat("a", 40)
	fixture.svc = newService(cfg, newUpdateOperationHTTPClient(t, fixture, payload, artifactBody, privateKey), func() {
		fixture.quitCalls.Add(1)
		fixture.quitCalled <- struct{}{}
	})
	fixture.svc.windowsSignatureVerifier = fixture.blockingSignatureVerifier
	fixture.svc.launchCommand = func(*exec.Cmd) (bool, error) {
		fixture.launchCalls.Add(1)
		return true, nil
	}
	writeSelectedInstallFixtureForPlatform(t, fixture.svc, "windows-amd64", filepath.Join(stageDir, exeFilename))
	return fixture
}

func newUpdateOperationHTTPClient(t *testing.T, fixture *updateOperationTestFixture, payload ManifestPayload, artifactBody, privateKey []byte) *http.Client {
	t.Helper()
	rawManifest := signTestManifest(t, privateKey, payload)
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case "https://updates.example.test/manifest.json":
			fixture.manifestCalls.Add(1)
			return okResponse(req, bytes.NewReader(rawManifest)), nil
		case payload.Artifacts[0].URL:
			fixture.artifactCalls.Add(1)
			return okResponse(req, bytes.NewReader(artifactBody)), nil
		default:
			return notFoundResponse(req), nil
		}
	})}
}

func (fixture *updateOperationTestFixture) blockingSignatureVerifier(_, _, _ string) error {
	if fixture.verifierCalls.Add(1) == 1 {
		close(fixture.verifierEntered)
		<-fixture.verifierRelease
	}
	return nil
}

func requireUpdateOperationsRejected(t *testing.T, svc *service, state string) {
	t.Helper()
	for _, operation := range updateOperationsForTest(svc) {
		if err := operation.run(); err == nil || !strings.Contains(err.Error(), "already") {
			t.Errorf("%s %s error = %v, want operation rejection", state, operation.name, err)
		}
	}
}

func updateOperationsForTest(svc *service) []struct {
	name string
	run  func() error
} {
	return []struct {
		name string
		run  func() error
	}{
		{name: "Download", run: func() error { _, err := svc.Download(context.Background()); return err }},
		{name: "Install", run: func() error { _, err := svc.Install(context.Background()); return err }},
		{name: "InstallLatest", run: func() error { _, err := svc.InstallLatest(context.Background()); return err }},
	}
}

func (fixture *updateOperationTestFixture) requireSingleQuit(t *testing.T) {
	t.Helper()
	waitForSignal(t, fixture.quitCalled, "single RequestQuit")
	select {
	case <-fixture.quitCalled:
		t.Fatal("RequestQuit called more than once")
	case <-time.After(2 * installQuitDelay):
	}
}

func (fixture *updateOperationTestFixture) requireSingleSideEffect(t *testing.T) {
	t.Helper()
	for name, got := range map[string]int32{
		"manifest requests":     fixture.manifestCalls.Load(),
		"artifact downloads":    fixture.artifactCalls.Load(),
		"install verifications": fixture.verifierCalls.Load(),
		"helper launches":       fixture.launchCalls.Load(),
		"RequestQuit callbacks": fixture.quitCalls.Load(),
	} {
		if got != 1 {
			t.Errorf("%s = %d, want 1", name, got)
		}
	}
}

func TestUpdateOperationKeepsLatchAfterStartedHelperReleaseFailure(t *testing.T) {
	stageDir := appUpdateRealTempDir(t)
	svc := newService(Config{
		Enabled: true, StageDir: stageDir, Platform: "windows-amd64",
		WindowsPublisher: "Super Dolphin Test Publisher", WindowsThumbprint: strings.Repeat("a", 40),
	}, nil, func() {})
	svc.windowsSignatureVerifier = func(_, _, _ string) error { return nil }
	svc.launchCommand = func(*exec.Cmd) (bool, error) { return true, errors.New("injected release failure") }
	writeSelectedInstallFixtureForPlatform(t, svc, "windows-amd64", filepath.Join(stageDir, exeFilename))

	if _, err := svc.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "injected release failure") {
		t.Fatalf("first Install() error = %v, want release failure", err)
	}
	if _, err := svc.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("second Install() error = %v, want permanent latch rejection", err)
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

func writeArgsHelperScriptWithName(t *testing.T, argsPath, name string) string {
	t.Helper()
	return writeArgsHelperScriptAt(t, argsPath, filepath.Join(t.TempDir(), name))
}

func writeArgsHelperScriptAt(t *testing.T, argsPath, helper string) string {
	t.Helper()
	tempArgsPath := argsPath + ".tmp"
	body := "#!/usr/bin/env bash\nprintf '%s\\n' \"$*\" > " + shellQuote(tempArgsPath) + "\n" +
		"mv " + shellQuote(tempArgsPath) + " " + shellQuote(argsPath) + "\n"
	if err := os.WriteFile(helper, []byte(body), 0o755); err != nil {
		t.Fatalf("WriteFile(helper) error = %v", err)
	}
	return helper
}

func installCommandForTest(t *testing.T, svc *service, stageDir string, staged selectedUpdate) (*exec.Cmd, string) {
	t.Helper()
	stage, err := openStageBoundary(stageDir)
	if err != nil {
		t.Fatalf("openStageBoundary() error = %v", err)
	}
	defer func() {
		if closeErr := stage.Close(); closeErr != nil {
			t.Errorf("stage.Close() error = %v", closeErr)
		}
	}()
	cmd, helper, logFile, err := svc.installCommand(stage, staged, recoveryTestGeneration)
	if err != nil {
		t.Fatalf("installCommand() error = %v", err)
	}
	if logFile == nil {
		t.Fatal("installCommand() log file = nil, want inherited safe log descriptor")
	}
	if err := logFile.Close(); err != nil {
		t.Fatalf("logFile.Close() error = %v", err)
	}
	return cmd, helper
}

func requireDetachedHelperCommand(t *testing.T, cmd *exec.Cmd, wants ...string) {
	t.Helper()
	joinedArgs := strings.Join(cmd.Args, " ")
	for _, want := range wants {
		if !strings.Contains(joinedArgs, want) {
			t.Fatalf("command args = %q, want %q", joinedArgs, want)
		}
	}
	if len(cmd.ExtraFiles) != 1 {
		t.Fatalf("command inherited files = %d, want safe helper log descriptor", len(cmd.ExtraFiles))
	}
}

func writeWindowsInstallerFixture(t *testing.T, stageDir, argsPath string) string {
	t.Helper()
	installer := filepath.Join(stageDir, exeFilename)
	if err := os.Rename(writeArgsHelperScriptWithName(t, argsPath, exeFilename), installer); err != nil {
		t.Fatalf("Rename(installer) error = %v", err)
	}
	return installer
}

func expectedWindowsSignatureVerifier(t *testing.T, installer string) func(string, string, string) error {
	t.Helper()
	return func(path, publisher, thumbprint string) error {
		t.Helper()
		if path != installer {
			t.Fatalf("signature path = %q, want %q", path, installer)
		}
		if publisher != "Super Dolphin Test Publisher" || thumbprint != strings.Repeat("a", 40) {
			t.Fatalf("signature gate = (%q, %q), want configured publisher/thumbprint", publisher, thumbprint)
		}
		return nil
	}
}

func requireSilentWindowsInstall(t *testing.T, svc *service, argsPath string) InstallResult {
	t.Helper()
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
	return result
}

func durationSecondsString(d time.Duration) string {
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(d.Seconds(), 'f', 3, 64), "0"), ".")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func writeSelectedInstallFixture(t *testing.T, svc *service) {
	t.Helper()
	dmgPath := filepath.Join(svc.cfg.StageDir, dmgFilename)
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
		artifactURL = testGitHubReleaseAssetURL("v1.2.3", "Super-Dolphin-windows-amd64.exe")
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
	stage, err := openStageBoundary(svc.cfg.StageDir)
	if err != nil {
		t.Fatalf("openStageBoundary() error = %v", err)
	}
	writeErr := writeSelectedUpdate(stage, staged)
	closeErr := stage.Close()
	if writeErr != nil {
		t.Fatalf("writeSelectedUpdate() error = %v", writeErr)
	}
	if closeErr != nil {
		t.Fatalf("stage.Close() error = %v", closeErr)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
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
