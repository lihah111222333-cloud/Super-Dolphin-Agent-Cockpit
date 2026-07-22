//go:build darwin

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdatefailure"
	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
)

func TestStagedSignatureFailureWritesPreJournalSidecar(t *testing.T) {
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
	beginUpdaterAttempt(t, stageDir)
	mountPoint := t.TempDir()
	createAppBundle(t, filepath.Join(mountPoint, "Super Dolphin.app"))
	target := filepath.Join(t.TempDir(), "Super Dolphin.app")
	err := installFromMount(installRequest{DMGPath: dmgPath, TargetAppPath: target, AllowUnsigned: true, Generation: recoveryTestGeneration}, mountPoint)
	if !errors.Is(err, recovery.ErrUpdateSignatureInvalid) {
		t.Fatalf("installFromMount() error = %v, want signature failure", err)
	}
	assertUpdaterSidecar(t, stageDir, "UPDATE_SIGNATURE_INVALID")
}

func TestCopiedSignatureFailureRewritesPreJournalSidecar(t *testing.T) {
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
	beginUpdaterAttempt(t, stageDir)
	mountPoint := t.TempDir()
	createAppBundle(t, filepath.Join(mountPoint, "Super Dolphin.app"))
	target := createAppBundle(t, filepath.Join(t.TempDir(), "Super Dolphin.app"))
	err := installFromMount(installRequest{DMGPath: dmgPath, TargetAppPath: target, AllowUnsigned: true, Restart: true, Generation: recoveryTestGeneration}, mountPoint)
	if !errors.Is(err, recovery.ErrUpdateSignatureInvalid) {
		t.Fatalf("installFromMount() error = %v, want copied signature failure", err)
	}
	assertUpdaterSidecar(t, stageDir, "UPDATE_SIGNATURE_INVALID")
}

func TestFirstReleaseCopiedSignatureFailureWritesPreJournalSidecar(t *testing.T) {
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
	beginUpdaterAttempt(t, stageDir)
	mountPoint := realUpdaterTempDir(t)
	createAppBundle(t, filepath.Join(mountPoint, "Super Dolphin.app"))
	target := filepath.Join(realUpdaterTempDir(t), "Super Dolphin.app")
	err := installFromMount(installRequest{DMGPath: dmgPath, TargetAppPath: target, AllowUnsigned: true, Restart: true, Generation: recoveryTestGeneration}, mountPoint)
	if !errors.Is(err, recovery.ErrUpdateSignatureInvalid) {
		t.Fatalf("installFromMount() error = %v, want copied signature failure", err)
	}
	assertUpdaterSidecar(t, stageDir, "UPDATE_SIGNATURE_INVALID")
}

func TestFirstReleaseClearFailureRemovesCandidateWithoutInstalling(t *testing.T) {
	stageDir, dmgPath := testUpdaterStage(t)
	beginUpdaterAttempt(t, stageDir)
	oldRunCommand := runCommand
	t.Cleanup(func() { runCommand = oldRunCommand })
	candidate := ""
	runCommand = func(_ context.Context, _ time.Duration, name string, args ...string) (commandResult, error) {
		if name != "ditto" {
			return commandResult{}, nil
		}
		candidate = args[1]
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
	target := filepath.Join(realUpdaterTempDir(t), "Super Dolphin.app")
	err := installFromMount(installRequest{DMGPath: dmgPath, TargetAppPath: target, AllowUnsigned: true, Restart: true, Generation: recoveryTestGeneration}, mountPoint)
	if err == nil || !strings.Contains(err.Error(), "clear app update pre-journal failure") {
		t.Fatalf("installFromMount() error = %v, want sidecar clear failure", err)
	}
	if _, statErr := os.Stat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target stat error = %v, want first release not installed", statErr)
	}
	if candidate == "" {
		t.Fatal("candidate path was not captured")
	}
	if _, statErr := os.Stat(candidate); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("candidate stat error = %v, want candidate removed", statErr)
	}
}

func TestPreJournalClearFailureRetainsPreparedTransaction(t *testing.T) {
	stageDir, dmgPath := testUpdaterStage(t)
	beginUpdaterAttempt(t, stageDir)
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
	beforeDigest, digestErr := recovery.ComputeReleaseDigest(target)
	if digestErr != nil {
		t.Fatal(digestErr)
	}
	helperStarted := false
	app := defaultUpdaterApp()
	app.startProbationCandidate = func(context.Context, recovery.Transaction) (*candidateHandle, error) {
		helperStarted = true
		return nil, errors.New("unexpected helper start")
	}
	err := app.installFromMount(context.Background(), installRequest{DMGPath: dmgPath, TargetAppPath: target, AllowUnsigned: true, Restart: true, Generation: recoveryTestGeneration}, mountPoint)
	assertPreJournalClearFailureOutcome(t, err, helperStarted, parent, target, beforeDigest)
}

func assertPreJournalClearFailureOutcome(t *testing.T, installErr error, helperStarted bool, parent string, target string, beforeDigest string) {
	t.Helper()
	if installErr == nil || !strings.Contains(installErr.Error(), "clear app update pre-journal failure") {
		t.Fatalf("installFromMount() error = %v, want sidecar clear failure", installErr)
	}
	if helperStarted {
		t.Fatal("probation helper started after pre-journal Clear failure")
	}
	journals, globErr := filepath.Glob(filepath.Join(parent, updateTransactionDirName, "*", "journal.json"))
	if globErr != nil || len(journals) != 1 {
		t.Fatalf("transaction journals = %v, error = %v, want one recoverable journal", journals, globErr)
	}
	candidates, globErr := filepath.Glob(filepath.Join(parent, ".Super Dolphin.staging-*.app"))
	if globErr != nil || len(candidates) != 1 {
		t.Fatalf("prepared candidates = %v, error = %v, want one recoverable candidate", candidates, globErr)
	}
	afterDigest, digestErr := recovery.ComputeReleaseDigest(target)
	if digestErr != nil || afterDigest != beforeDigest {
		t.Fatalf("target digest after Clear failure = %q, %v, want unchanged %q", afterDigest, digestErr, beforeDigest)
	}
}

func TestTransactionCreateSuccessConfirmsPreJournalSidecarAbsent(t *testing.T) {
	stubSuccessfulDitto(t)
	stageDir, _ := testUpdaterStage(t)
	beginUpdaterAttempt(t, stageDir)
	mountPoint := realUpdaterTempDir(t)
	staged := createAppBundle(t, filepath.Join(mountPoint, "Super Dolphin.app"))
	target := createAppBundle(t, filepath.Join(realUpdaterTempDir(t), "Super Dolphin.app"))
	if _, _, err := defaultUpdaterApp().replaceTargetAppTransactionContextWithStageDir(context.Background(), staged, target, "", true, true, stageDir, recoveryTestGeneration); err != nil {
		t.Fatalf("replaceTargetAppTransactionContextWithStageDir() error = %v", err)
	}
	if _, exists, err := appupdatefailure.ReadFailure(stageDir); err != nil || exists {
		t.Fatalf("ReadFailure() after Store.Create = (_, %v, %v), want absent", exists, err)
	}
}

func TestPostCreateSidecarCorruptionCannotOverrideJournalFailure(t *testing.T) {
	stubSuccessfulDitto(t)
	stageDir, dmgPath := testUpdaterStage(t)
	beginUpdaterAttempt(t, stageDir)
	mountPoint := realUpdaterTempDir(t)
	createAppBundle(t, filepath.Join(mountPoint, "Super Dolphin.app"))
	target := createAppBundle(t, filepath.Join(realUpdaterTempDir(t), "Super Dolphin.app"))
	wantErr := errors.New("post-create candidate start failed")
	app := defaultUpdaterApp()
	app.startProbationCandidate = func(context.Context, recovery.Transaction) (*candidateHandle, error) {
		if err := os.Chmod(filepath.Join(stageDir, appupdatefailure.LockFilename), 0o644); err != nil {
			return nil, err
		}
		return nil, wantErr
	}
	err := app.installFromMount(context.Background(), installRequest{DMGPath: dmgPath, TargetAppPath: target, AllowUnsigned: true, Restart: true, Generation: recoveryTestGeneration}, mountPoint)
	if !errors.Is(err, wantErr) || strings.Contains(err.Error(), "pre-journal") || strings.Contains(err.Error(), "sidecar") {
		t.Fatalf("installFromMount() error = %v, want journal-owned post-create failure", err)
	}
}

func TestExistingTargetPublishesJournalBeforeClearingPreJournalState(t *testing.T) {
	raw, err := os.ReadFile("install.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	createAt := strings.Index(source, "app.createRecoveryTransaction(store, ctx, request)")
	clearAt := strings.Index(source, "clearPreJournalFailure(stageDir, generation)")
	completeAt := strings.Index(source, "return app.completePreparedReleaseTransaction")
	if createAt < 0 || clearAt < 0 || completeAt < 0 || !(createAt < clearAt && clearAt < completeAt) {
		t.Fatalf("existing-target order create=%d clear=%d complete=%d, want journal -> Clear -> helper", createAt, clearAt, completeAt)
	}
}

func assertUpdaterSidecar(t *testing.T, stageDir string, code string) {
	t.Helper()
	failure, exists, err := appupdatefailure.ReadFailure(stageDir)
	if err != nil || !exists || failure.Code != code || failure.TransactionID != "" {
		t.Fatalf("Read() = (%#v, %v, %v), want code %s", failure, exists, err, code)
	}
}

func beginUpdaterAttempt(t *testing.T, stageDir string) {
	t.Helper()
	if err := appupdatefailure.Begin(stageDir, recoveryTestGeneration); err != nil {
		t.Fatal(err)
	}
}

func testUpdaterDMG(t *testing.T) string {
	t.Helper()
	stageDir, dmgPath := testUpdaterStage(t)
	beginUpdaterAttempt(t, stageDir)
	return dmgPath
}

func testUpdaterStage(t *testing.T) (string, string) {
	t.Helper()
	stageDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dmgPath := filepath.Join(stageDir, "update.dmg")
	if err := os.WriteFile(dmgPath, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	return stageDir, dmgPath
}
