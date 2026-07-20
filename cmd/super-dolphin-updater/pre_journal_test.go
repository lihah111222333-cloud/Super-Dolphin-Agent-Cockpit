package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdatefailure"
	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
)

func TestStagedSignatureFailureWritesPreJournalSidecar(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture requires macOS bundle permissions")
	}
	exitErr := exec.Command("sh", "-c", "exit 1").Run()
	oldRunCommand := runCommand
	t.Cleanup(func() { runCommand = oldRunCommand })
	runCommand = func(_ context.Context, _ time.Duration, name string, _ ...string) (commandResult, error) {
		if name == "codesign" {
			return commandResult{}, exitErr
		}
		return commandResult{}, nil
	}
	stageDir, dmgPath := testUpdaterStage(t)
	mountPoint := t.TempDir()
	createAppBundle(t, filepath.Join(mountPoint, "Super Dolphin.app"))
	target := filepath.Join(t.TempDir(), "Super Dolphin.app")
	err := installFromMount(installRequest{DMGPath: dmgPath, TargetAppPath: target, AllowUnsigned: true}, mountPoint)
	if !errors.Is(err, recovery.ErrUpdateSignatureInvalid) {
		t.Fatalf("installFromMount() error = %v, want signature failure", err)
	}
	assertUpdaterSidecar(t, stageDir, "UPDATE_SIGNATURE_INVALID")
}

func TestCopiedSignatureFailureRewritesPreJournalSidecar(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture requires macOS bundle permissions")
	}
	exitErr := exec.Command("sh", "-c", "exit 1").Run()
	oldRunCommand := runCommand
	t.Cleanup(func() { runCommand = oldRunCommand })
	codesignCalls := 0
	runCommand = func(_ context.Context, _ time.Duration, name string, args ...string) (commandResult, error) {
		switch name {
		case "codesign":
			codesignCalls++
			if codesignCalls == 2 {
				return commandResult{}, exitErr
			}
		case "ditto":
			return commandResult{}, os.Rename(args[0], args[1])
		}
		return commandResult{}, nil
	}
	stageDir, dmgPath := testUpdaterStage(t)
	mountPoint := t.TempDir()
	createAppBundle(t, filepath.Join(mountPoint, "Super Dolphin.app"))
	target := createAppBundle(t, filepath.Join(t.TempDir(), "Super Dolphin.app"))
	err := installFromMount(installRequest{DMGPath: dmgPath, TargetAppPath: target, AllowUnsigned: true, Restart: true}, mountPoint)
	if !errors.Is(err, recovery.ErrUpdateSignatureInvalid) {
		t.Fatalf("installFromMount() error = %v, want copied signature failure", err)
	}
	assertUpdaterSidecar(t, stageDir, "UPDATE_SIGNATURE_INVALID")
}

func TestPreJournalClearFailureBlocksTransactionCreate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture requires macOS bundle permissions")
	}
	stageDir, dmgPath := testUpdaterStage(t)
	oldRunCommand := runCommand
	t.Cleanup(func() { runCommand = oldRunCommand })
	runCommand = func(_ context.Context, _ time.Duration, name string, args ...string) (commandResult, error) {
		if name != "ditto" {
			return commandResult{}, nil
		}
		if err := os.Rename(args[0], args[1]); err != nil {
			return commandResult{}, err
		}
		realDir := stageDir + "-real"
		if err := os.Rename(stageDir, realDir); err != nil {
			return commandResult{}, err
		}
		return commandResult{}, os.Symlink(realDir, stageDir)
	}
	mountPoint := realUpdaterTempDir(t)
	createAppBundle(t, filepath.Join(mountPoint, "Super Dolphin.app"))
	parent := realUpdaterTempDir(t)
	target := createAppBundle(t, filepath.Join(parent, "Super Dolphin.app"))
	err := installFromMount(installRequest{DMGPath: dmgPath, TargetAppPath: target, AllowUnsigned: true, Restart: true}, mountPoint)
	if err == nil || !strings.Contains(err.Error(), "stage dir is not a regular directory") {
		t.Fatalf("installFromMount() error = %v, want sidecar clear failure", err)
	}
	entries, readErr := os.ReadDir(filepath.Join(parent, updateTransactionDirName))
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("transaction root entries = %v, error = %v, want no created transaction", entries, readErr)
	}
}

func TestTransactionCreateSuccessConfirmsPreJournalSidecarAbsent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture requires macOS bundle permissions")
	}
	stubSuccessfulDitto(t)
	stageDir, _ := testUpdaterStage(t)
	mountPoint := realUpdaterTempDir(t)
	staged := createAppBundle(t, filepath.Join(mountPoint, "Super Dolphin.app"))
	target := createAppBundle(t, filepath.Join(realUpdaterTempDir(t), "Super Dolphin.app"))
	if _, _, err := defaultUpdaterApp().replaceTargetAppTransactionContextWithStageDir(context.Background(), staged, target, "", true, true, stageDir); err != nil {
		t.Fatalf("replaceTargetAppTransactionContextWithStageDir() error = %v", err)
	}
	if _, exists, err := appupdatefailure.Read(stageDir); err != nil || exists {
		t.Fatalf("Read() after Store.Create = (_, %v, %v), want absent", exists, err)
	}
}

func assertUpdaterSidecar(t *testing.T, stageDir string, code string) {
	t.Helper()
	failure, exists, err := appupdatefailure.Read(stageDir)
	if err != nil || !exists || failure.Code != code || failure.TransactionID != "" {
		t.Fatalf("Read() = (%#v, %v, %v), want code %s", failure, exists, err, code)
	}
}

func testUpdaterDMG(t *testing.T) string {
	t.Helper()
	_, dmgPath := testUpdaterStage(t)
	return dmgPath
}

func testUpdaterStage(t *testing.T) (string, string) {
	t.Helper()
	stageDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dmgPath := filepath.Join(stageDir, "update.dmg")
	if err := os.WriteFile(dmgPath, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	return stageDir, dmgPath
}

func realUpdaterTempDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}
