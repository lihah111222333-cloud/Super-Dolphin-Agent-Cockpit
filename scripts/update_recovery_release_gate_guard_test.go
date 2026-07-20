package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateRecoveryReleaseGateUsesGuardForEveryRequiredPackage(t *testing.T) {
	root := updateRecoveryReleaseGateRepoRoot(t)
	script := updateRecoveryReleaseGateReadFile(t, filepath.Join(root, "scripts", "update_recovery_release_gate.sh"))

	for _, want := range []string{
		"#!/usr/bin/env bash",
		"set -euo pipefail",
		"./scripts/test_with_guard.sh ./internal/platform/appupdaterecovery ./cmd/super-dolphin-updater ./cmd/super-dolphin-guard ./internal/app -count=1",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("update recovery release gate missing %q", want)
		}
	}
	if strings.Contains(script, "go test") {
		t.Fatal("update recovery release gate must use test_with_guard, not direct go test")
	}
}

func TestUpdateRecoveryReleaseGateIsExposedByMake(t *testing.T) {
	root := updateRecoveryReleaseGateRepoRoot(t)
	makefile := updateRecoveryReleaseGateReadFile(t, filepath.Join(root, "Makefile"))

	want := "release-update-gate:\n\t./scripts/update_recovery_release_gate.sh"
	if !strings.Contains(makefile, want) {
		t.Fatalf("Makefile missing runnable release-update-gate target %q", want)
	}
}

func TestUpdateRecoveryReleaseGateCIRequiresNativeMacOSAndWindowsEvidence(t *testing.T) {
	root := updateRecoveryReleaseGateRepoRoot(t)
	workflow := updateRecoveryReleaseGateReadFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))

	for _, want := range []string{
		"update-recovery-release-gate-macos:",
		"runs-on: macos-latest",
		"run: make release-update-gate",
		"update-recovery-release-gate-windows:",
		"runs-on: windows-latest",
		"./scripts/test_with_guard.sh ./cmd/super-dolphin-updater -run 'Test(Guard|ConfigureGuard)' -count=1",
		"./scripts/test_with_guard.sh ./scripts -run 'Test(PackageWindows|VerifyPackagedAppWindows)' -count=1",
		"needs: [commit-guard, update-recovery-release-gate-macos, update-recovery-release-gate-windows]",
	} {
		if !strings.Contains(workflow, want) {
			t.Fatalf("CI update recovery release gate missing %q", want)
		}
	}
}

func TestUpdateRecoveryReleaseGateCIMarksLinuxAsSupplemental(t *testing.T) {
	root := updateRecoveryReleaseGateRepoRoot(t)
	workflow := updateRecoveryReleaseGateReadFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))

	for _, want := range []string{
		"update-recovery-release-gate-linux-supplemental:",
		"runs-on: ubuntu-latest",
		"continue-on-error: true",
		"name: Supplemental Linux update recovery evidence",
		"run: make release-update-gate",
	} {
		if !strings.Contains(workflow, want) {
			t.Fatalf("CI supplemental Linux evidence missing %q", want)
		}
	}
	linuxJob := strings.Index(workflow, "update-recovery-release-gate-linux-supplemental:")
	crossPlatformJob := strings.Index(workflow, "cross-platform-smoke:")
	if linuxJob < 0 || crossPlatformJob < 0 || linuxJob > crossPlatformJob {
		t.Fatal("supplemental Linux evidence must be declared before cross-platform packaging smoke")
	}
}

func updateRecoveryReleaseGateRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, ".."))
}

func updateRecoveryReleaseGateReadFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read required file %s: %v", path, err)
	}
	return string(body)
}
