//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecoveryCapsuleSurvivesBackupRetainedCrashWindow(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "Super Dolphin.app")
	staging := filepath.Join(root, ".Super Dolphin.staging.app")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	updater := filepath.Join(root, "super-dolphin-updater")
	guard := filepath.Join(root, "super-dolphin-guard")
	trust := filepath.Join(root, "update-trust.json")
	if err := os.WriteFile(updater, []byte("independent updater artifact"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(guard, []byte("independent guard artifact"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trust, []byte("independent trust artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	capsuleDir := filepath.Join(root, "00112233445566778899aabbccddeeff", "recovery")
	capsule, err := prepareRecoveryCapsule(capsuleDir, updater, guard, trust)
	if err != nil {
		t.Fatalf("prepareRecoveryCapsule() error = %v", err)
	}
	if err := os.Rename(target, target+".backup"); err != nil {
		t.Fatal(err)
	}
	assertCapsuleExecutable(t, capsule.UpdaterPath)
	assertCapsuleExecutable(t, capsule.GuardPath)
}

func assertCapsuleExecutable(t *testing.T, path string) {
	t.Helper()
	if info, err := os.Stat(path); err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("recovery capsule executable %s unavailable: info=%v err=%v", path, info, err)
	}
}
