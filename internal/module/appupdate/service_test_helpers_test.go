package appupdate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdatefailure"
)

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

func seedPreJournalFailure(t *testing.T, stageDir string, code string) {
	t.Helper()
	failure, ok := contract.RecoveryFailureForCode(code, "")
	if !ok {
		t.Fatalf("RecoveryFailureForCode(%q) = false", code)
	}
	if err := appupdatefailure.Begin(stageDir, recoveryTestGeneration); err != nil {
		t.Fatal(err)
	}
	if err := appupdatefailure.Fail(stageDir, recoveryTestGeneration, failure); err != nil {
		t.Fatal(err)
	}
}

func assertPreJournalFailureHidden(t *testing.T, stageDir string, operation string) {
	t.Helper()
	if _, exists, err := appupdatefailure.ReadFailure(stageDir); err != nil || exists {
		t.Fatalf("ReadFailure() after %s = (_, %v, %v), want hidden", operation, exists, err)
	}
}
