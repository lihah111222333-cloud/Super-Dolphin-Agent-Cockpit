package appupdate

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

const productionPackageTestPlatform = "darwin-arm64"

func TestProvideConfigUsesPackageOwnedProductionTrust(t *testing.T) {
	_, executable, resources, target, home := productionPackageConfigFixture(t)
	clearUpdateOverrideEnvironment(t)
	t.Setenv(envSuperDolphinHome, home)
	got, handled, err := providePackageOwnedConfigForExecutableOnPlatform(executable, productionPackageTestPlatform)
	if err != nil {
		t.Fatalf("ProvideConfig() error = %v", err)
	}
	if !handled {
		t.Fatal("production package config was not handled")
	}
	if got.GitHubRepo != testValidGitHubRepo || got.TargetAppPath != target || got.HelperPath != filepath.Join(resources, "bin", updaterHelperName) {
		t.Fatalf("ProvideConfig() = %+v, want package-owned source and paths", got)
	}
	if got.AllowUnsigned {
		t.Fatal("package-owned production config AllowUnsigned = true")
	}
}

func TestProvideConfigRejectsProductionUpdateOverride(t *testing.T) {
	_, executable, _, _, home := productionPackageConfigFixture(t)
	clearUpdateOverrideEnvironment(t)
	t.Setenv(envSuperDolphinHome, home)
	t.Setenv(envUpdateGitHubRepo, "attacker/repo")
	if _, _, err := providePackageOwnedConfigForExecutableOnPlatform(executable, productionPackageTestPlatform); err == nil || !strings.Contains(err.Error(), envUpdateGitHubRepo) {
		t.Fatalf("ProvideConfig() error = %v, want package override rejection", err)
	}
}

func TestProvideConfigAllowsExactRuntimeResources(t *testing.T) {
	_, executable, resources, target, home := productionPackageConfigFixture(t)
	clearUpdateOverrideEnvironment(t)
	t.Setenv(envSuperDolphinHome, home)
	t.Setenv(envRuntimeResources, resources)
	got, handled, err := providePackageOwnedConfigForExecutableOnPlatform(executable, productionPackageTestPlatform)
	if err != nil {
		t.Fatalf("providePackageOwnedConfigForExecutable() error = %v", err)
	}
	if !handled || got.TargetAppPath != target || got.HelperPath != filepath.Join(resources, "bin", updaterHelperName) {
		t.Fatalf("providePackageOwnedConfigForExecutable() = (%+v, %t), want executable-derived package paths", got, handled)
	}
}

func TestProvideConfigRejectsForgedRuntimeResources(t *testing.T) {
	_, executable, _, _, home := productionPackageConfigFixture(t)
	clearUpdateOverrideEnvironment(t)
	t.Setenv(envSuperDolphinHome, home)
	t.Setenv(envRuntimeResources, filepath.Join(t.TempDir(), "forged", "Resources"))
	if _, _, err := providePackageOwnedConfigForExecutableOnPlatform(executable, productionPackageTestPlatform); err == nil || !strings.Contains(err.Error(), envRuntimeResources) {
		t.Fatalf("providePackageOwnedConfigForExecutable() error = %v, want forged resources rejection", err)
	}
}

func productionPackageConfigFixture(t *testing.T) (*platformconfig.Config, string, string, string, string) {
	t.Helper()
	target := filepath.Join(canonicalTempDir(t), "Super Dolphin.app")
	resources := filepath.Join(target, "Contents", "Resources")
	executable := filepath.Join(target, "Contents", "MacOS", "agent-terminal")
	bin := filepath.Join(resources, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("package executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	updater := filepath.Join(bin, updaterHelperName)
	guard := filepath.Join(bin, "super-dolphin-guard")
	if err := os.WriteFile(updater, []byte("package updater"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(guard, []byte("package guard"), 0o700); err != nil {
		t.Fatal(err)
	}
	updaterDigest, err := recovery.ComputeReleaseDigest(updater)
	if err != nil {
		t.Fatal(err)
	}
	guardDigest, err := recovery.ComputeReleaseDigest(guard)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, _ := testManifestKeypair(t)
	trust := recovery.PackageTrust{
		SchemaVersion: recovery.PackageTrustSchemaVersion, Enabled: true, Production: true, Platform: productionPackageTestPlatform,
		Source:            recovery.UpdateSource{Kind: recovery.UpdateSourceGitHub, Value: testValidGitHubRepo},
		ManifestPublicKey: base64.StdEncoding.EncodeToString(publicKey), Channel: "gray",
		SignerPolicy: recovery.PackageSignerPolicyExact, SignerIdentity: "TEAM-A",
		UpdaterSHA256: updaterDigest, GuardSHA256: guardDigest,
	}
	raw, err := recovery.EncodePackageTrust(trust)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resources, recovery.PackageTrustFilename), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	writeInfoPlist(t, target, "1.2.5")
	cfg := &platformconfig.Config{ProjectRoot: t.TempDir(), Dependency: platformconfig.DependencyConfig{Profile: platformconfig.DependencyProfileProduction}}
	return cfg, executable, resources, target, t.TempDir()
}

func clearUpdateOverrideEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		envUpdateEnabled, envUpdateManifestURL, envUpdateGitHubRepo, envUpdatePublicKey, envUpdateChannel,
		envUpdateStageDir, envUpdateHelperPath, envUpdateTargetApp, envUpdatePlatform, envUpdateVersion,
		envUpdateAllowUnsigned, envUpdateWindowsPublisher, envUpdateWindowsThumbprint, envRuntimeResources,
	} {
		value, existed := os.LookupEnv(name)
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if existed {
				_ = os.Setenv(name, value)
			} else {
				_ = os.Unsetenv(name)
			}
		})
	}
}

func TestVerifiedPackageTrustPublicKeyRequiresProductionExactTrustAndHelpers(t *testing.T) {
	resources := canonicalTempDir(t)
	binDir := filepath.Join(resources, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	updaterPath := filepath.Join(binDir, updaterHelperName)
	guardPath := filepath.Join(binDir, "super-dolphin-guard")
	if err := os.WriteFile(updaterPath, []byte("updater-v1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(guardPath, []byte("guard-v1"), 0o700); err != nil {
		t.Fatal(err)
	}
	publicKey := base64.StdEncoding.EncodeToString(make([]byte, 32))
	if err := WritePackageTrust(PackageTrustWriteRequest{
		OutputPath: filepath.Join(resources, "update-trust.json"),
		Platform:   "darwin-arm64", Enabled: true,
		SourceKind: recovery.UpdateSourceGitHub, SourceValue: "owner/repo", ManifestKey: publicKey,
		Channel: "gray", SignerIdentity: "TEAM-EXACT",
		UpdaterPath: updaterPath, GuardPath: guardPath,
	}); err != nil {
		t.Fatalf("WritePackageTrust() error = %v", err)
	}

	got, err := VerifiedPackageTrustPublicKey(resources, "darwin-arm64")
	if err != nil {
		t.Fatalf("VerifiedPackageTrustPublicKey() error = %v", err)
	}
	if got != publicKey {
		t.Fatalf("public key = %q, want %q", got, publicKey)
	}
	if err := os.WriteFile(updaterPath, []byte("rogue-updater"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifiedPackageTrustPublicKey(resources, "darwin-arm64"); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("helper mismatch error = %v", err)
	}
}

func TestVerifiedPackageTrustPublicKeyRejectsDisabledTrust(t *testing.T) {
	resources := canonicalTempDir(t)
	binDir := filepath.Join(resources, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	updaterPath := filepath.Join(binDir, updaterHelperName)
	guardPath := filepath.Join(binDir, "super-dolphin-guard")
	for path, content := range map[string][]byte{updaterPath: []byte("updater"), guardPath: []byte("guard")} {
		if err := os.WriteFile(path, content, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := WritePackageTrust(PackageTrustWriteRequest{
		OutputPath: filepath.Join(resources, "update-trust.json"),
		Platform:   "darwin-arm64", UpdaterPath: updaterPath, GuardPath: guardPath,
	}); err != nil {
		t.Fatalf("WritePackageTrust() error = %v", err)
	}
	if _, err := VerifiedPackageTrustPublicKey(resources, "darwin-arm64"); err == nil || !strings.Contains(err.Error(), "enabled production trust") {
		t.Fatalf("disabled trust error = %v", err)
	}
}

func TestPackageLayoutRejectsExecutableAlias(t *testing.T) {
	root := canonicalTempDir(t)
	target := filepath.Join(root, "Super Dolphin.app")
	macOSDir := filepath.Join(target, "Contents", "MacOS")
	if err := os.MkdirAll(filepath.Join(target, "Contents", "Resources"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(macOSDir, 0o700); err != nil {
		t.Fatal(err)
	}
	realExecutable := filepath.Join(root, "real-agent-terminal")
	if err := os.WriteFile(realExecutable, []byte("real"), 0o700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(macOSDir, "agent-terminal")
	if err := os.Symlink(realExecutable, executable); err != nil {
		t.Fatal(err)
	}
	if _, _, err := packageLayoutFromExecutable(executable); err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("packageLayoutFromExecutable(executable alias) error = %v", err)
	}
}

func TestPackageLayoutRejectsResourcesAlias(t *testing.T) {
	root := canonicalTempDir(t)
	target := filepath.Join(root, "Super Dolphin.app")
	macOSDir := filepath.Join(target, "Contents", "MacOS")
	if err := os.MkdirAll(macOSDir, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(macOSDir, "agent-terminal")
	if err := os.WriteFile(executable, []byte("real"), 0o700); err != nil {
		t.Fatal(err)
	}
	realResources := filepath.Join(root, "attacker-resources")
	if err := os.MkdirAll(realResources, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realResources, filepath.Join(target, "Contents", "Resources")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := packageLayoutFromExecutable(executable); err == nil || !strings.Contains(err.Error(), "alias") {
		t.Fatalf("packageLayoutFromExecutable(Resources alias) error = %v", err)
	}
}

func TestVerifiedPackageTrustRejectsHelperAlias(t *testing.T) {
	resources := canonicalTempDir(t)
	binDir := filepath.Join(resources, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	updater := filepath.Join(binDir, updaterHelperName)
	guard := filepath.Join(binDir, "super-dolphin-guard")
	if err := os.WriteFile(updater, []byte("updater"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(guard, []byte("guard"), 0o700); err != nil {
		t.Fatal(err)
	}
	publicKey := base64.StdEncoding.EncodeToString(make([]byte, 32))
	if err := WritePackageTrust(PackageTrustWriteRequest{
		OutputPath: filepath.Join(resources, recovery.PackageTrustFilename), Platform: "darwin-arm64", Enabled: true,
		SourceKind: recovery.UpdateSourceGitHub, SourceValue: "owner/repo", ManifestKey: publicKey,
		Channel: "gray", SignerIdentity: "TEAM-EXACT", UpdaterPath: updater, GuardPath: guard,
	}); err != nil {
		t.Fatal(err)
	}
	realUpdater := filepath.Join(resources, "real-updater")
	if err := os.Rename(updater, realUpdater); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realUpdater, updater); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifiedPackageTrustPublicKey(resources, "darwin-arm64"); err == nil || !strings.Contains(err.Error(), "alias") {
		t.Fatalf("VerifiedPackageTrustPublicKey(helper alias) error = %v", err)
	}
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}
