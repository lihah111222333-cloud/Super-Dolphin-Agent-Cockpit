//go:build darwin

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstallFromMountWaitsForAppExitBeforeReplacing(t *testing.T) {
	oldRunCommand := runCommand
	oldWaitForProcessExit := waitForProcessExit
	t.Cleanup(func() {
		runCommand = oldRunCommand
		waitForProcessExit = oldWaitForProcessExit
	})
	var events []string
	waitForProcessExit = func(pid int, timeout time.Duration) error {
		events = append(events, "wait")
		if pid != 12345 {
			t.Fatalf("wait pid = %d, want 12345", pid)
		}
		return nil
	}
	runCommand = func(_ context.Context, _ time.Duration, name string, args ...string) (commandResult, error) {
		if name == "ditto" {
			events = append(events, "copy")
			if len(args) != 2 {
				t.Fatalf("ditto args = %v, want source and target", args)
			}
			if err := os.Rename(args[0], args[1]); err != nil {
				t.Fatalf("fake ditto rename: %v", err)
			}
		}
		return commandResult{}, nil
	}
	mountPoint := t.TempDir()
	createAppBundle(t, filepath.Join(mountPoint, "Super Dolphin.app"))
	target := filepath.Join(t.TempDir(), "Super Dolphin.app")

	err := installFromMount(installRequest{
		DMGPath:       testUpdaterDMG(t),
		TargetAppPath: target,
		WaitPID:       12345,
		AllowUnsigned: true,
		Generation:    recoveryTestGeneration,
	}, mountPoint)
	if err != nil {
		t.Fatalf("installFromMount() error = %v", err)
	}
	if strings.Join(events, ",") != "wait,copy" {
		t.Fatalf("events = %v, want wait before copy", events)
	}
}

func TestFirstInstallUsesAtomicPathWithoutRollbackTransaction(t *testing.T) {
	stubSuccessfulDitto(t)
	mountPoint := t.TempDir()
	createAppBundle(t, filepath.Join(mountPoint, "Super Dolphin.app"))
	parent := t.TempDir()
	target := filepath.Join(parent, "Super Dolphin.app")
	if err := installFromMount(installRequest{DMGPath: testUpdaterDMG(t), TargetAppPath: target, AllowUnsigned: true, Generation: recoveryTestGeneration}, mountPoint); err != nil {
		t.Fatalf("installFromMount() error = %v", err)
	}
	if err := validateMountedApp(target); err != nil {
		t.Fatalf("first install target is invalid: %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, updateTransactionDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("first install created rollback transaction root: %v", err)
	}
	backups, err := filepath.Glob(filepath.Join(parent, ".Super Dolphin.backup-*.app"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 0 {
		t.Fatalf("first install created backups: %v", backups)
	}
}

func TestInstallKeepsTargetWhenDittoTimesOutBeforeTransaction(t *testing.T) {
	mountPoint := t.TempDir()
	createAppBundle(t, filepath.Join(mountPoint, "Super Dolphin.app"))
	target := createAppBundle(t, filepath.Join(t.TempDir(), "Super Dolphin.app"))
	originalMarker := filepath.Join(target, "Contents", "Resources", "original-marker.txt")
	if err := os.WriteFile(originalMarker, []byte("original"), 0o644); err != nil {
		t.Fatalf("write original marker: %v", err)
	}
	dittoDest := stubRunCommandWithTimedOutDitto(t)

	err := installFromMount(installRequest{
		DMGPath:       testUpdaterDMG(t),
		TargetAppPath: target,
		AllowUnsigned: true,
		Restart:       true,
		Generation:    recoveryTestGeneration,
	}, mountPoint)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("installFromMount() error = %v, want ditto timeout", err)
	}
	if *dittoDest == "" {
		t.Fatal("ditto was not called")
	}
	if *dittoDest == target {
		t.Fatalf("ditto destination = %q, want staged copy path before final replacement", *dittoDest)
	}
	if _, err := os.Stat(originalMarker); err != nil {
		t.Fatalf("original app changed after pre-transaction timeout: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "Contents", "Resources", "partial-copy.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial copy leaked into restored target: %v", err)
	}
	if _, err := os.Stat(*dittoDest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged copy path still exists after failed preparation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(target), updateTransactionDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pre-transaction timeout created transaction root: %v", err)
	}
}
