package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
)

func TestPackagedUpdateTrustRejectsEnvironmentAndCLIOverride(t *testing.T) {
	trust := updaterTestPackageTrust()
	helpers := recovery.HelperIdentity{UpdaterSHA256: trust.UpdaterSHA256, GuardSHA256: trust.GuardSHA256}
	req := installRequest{AllowUnsigned: true}
	if err := validatePackageOwnedInstall(req, trust, trust, "TEAM-A", helpers, helpers); err == nil || !strings.Contains(err.Error(), "allow-unsigned") {
		t.Fatalf("validatePackageOwnedInstall() error = %v, want CLI bypass rejection", err)
	}
	if err := recovery.RejectPackageTrustOverrides([]string{"SUPER_DOLPHIN_UPDATE_GITHUB_REPO=attacker/repo"}); err == nil {
		t.Fatal("RejectPackageTrustOverrides() error = nil")
	}
}

func TestPackagedUpdateRejectsWrongSignerAndMixedHelperRelease(t *testing.T) {
	oldTrust := updaterTestPackageTrust()
	candidateTrust := oldTrust
	helpers := recovery.HelperIdentity{UpdaterSHA256: oldTrust.UpdaterSHA256, GuardSHA256: oldTrust.GuardSHA256}
	if err := validatePackageOwnedInstall(installRequest{}, oldTrust, candidateTrust, "TEAM-B", helpers, helpers); err == nil || !strings.Contains(err.Error(), "signer") {
		t.Fatalf("wrong signer error = %v", err)
	}

	candidateTrust.GuardSHA256 = strings.Repeat("c", 64)
	if err := validatePackageOwnedInstall(installRequest{}, oldTrust, candidateTrust, "TEAM-A", helpers, helpers); err == nil || !strings.Contains(err.Error(), "helper") {
		t.Fatalf("mixed helper/release error = %v", err)
	}
}

func TestPackagedUpdateRejectsRogueUpdaterBeforeSideEffects(t *testing.T) {
	root := t.TempDir()
	canonical := filepath.Join(root, "Super Dolphin.app", packageUpdaterPath)
	if err := os.MkdirAll(filepath.Dir(canonical), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canonical, []byte("trusted package updater"), 0o700); err != nil {
		t.Fatal(err)
	}
	digest, err := recovery.ComputeReleaseDigest(canonical)
	if err != nil {
		t.Fatal(err)
	}
	trust := updaterTestPackageTrust()
	trust.UpdaterSHA256 = digest
	helpers := recovery.HelperIdentity{UpdaterSHA256: digest, GuardSHA256: trust.GuardSHA256}
	rogue := recovery.ProcessIdentity{
		PID: 123, StartToken: "rogue", ExecutableIdentity: filepath.Join(root, "rogue-updater"),
		ExecutableSHA256: strings.Repeat("f", 64),
	}
	if err := validateRunningPackageUpdater(rogue, trust, helpers, canonical); err == nil || !strings.Contains(err.Error(), "running updater") {
		t.Fatalf("validateRunningPackageUpdater() error = %v, want rogue updater rejection", err)
	}
	if _, err := os.Stat(filepath.Join(root, recovery.TransactionRootDirName)); !os.IsNotExist(err) {
		t.Fatalf("transaction side effect exists before updater rejection: %v", err)
	}
}

func TestValidateRunningPackageUpdaterBindsStableProcessIdentity(t *testing.T) {
	process, _, err := captureUpdaterProcessIdentity()
	if err != nil {
		t.Fatal(err)
	}
	trust := updaterTestPackageTrust()
	trust.UpdaterSHA256 = process.ExecutableSHA256
	helpers := recovery.HelperIdentity{UpdaterSHA256: process.ExecutableSHA256, GuardSHA256: trust.GuardSHA256}
	if err := validateRunningPackageUpdater(process, trust, helpers, process.ExecutableIdentity); err != nil {
		t.Fatalf("validateRunningPackageUpdater() error = %v", err)
	}
	process.StartToken += "-reused"
	if err := validateRunningPackageUpdater(process, trust, helpers, process.ExecutableIdentity); err == nil || !strings.Contains(err.Error(), "process identity") {
		t.Fatalf("validateRunningPackageUpdater() error = %v, want start-token mismatch", err)
	}
}

func updaterTestPackageTrust() recovery.PackageTrust {
	return recovery.PackageTrust{
		SchemaVersion: recovery.PackageTrustSchemaVersion,
		Enabled:       true, Production: true, Platform: "darwin-arm64",
		Source:            recovery.UpdateSource{Kind: recovery.UpdateSourceGitHub, Value: "super-dolphin/releases"},
		ManifestPublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		Channel:           "gray", SignerPolicy: recovery.PackageSignerPolicyExact,
		SignerIdentity: "TEAM-A", UpdaterSHA256: strings.Repeat("a", 64), GuardSHA256: strings.Repeat("b", 64),
	}
}
