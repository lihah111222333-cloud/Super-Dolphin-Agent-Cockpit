package appupdate

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

// providePackageOwnedConfig 在 production profile 中只读取包内 trust。
func providePackageOwnedConfig(platformCfg *platformconfig.Config) (Config, bool, error) {
	if platformCfg == nil || platformCfg.Dependency.Profile != platformconfig.DependencyProfileProduction {
		return Config{}, false, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return Config{}, true, fmt.Errorf("resolve package executable: %w", err)
	}
	return providePackageOwnedConfigForExecutable(platformCfg, executable)
}

// providePackageOwnedConfigForExecutable 从 exact app executable 推导不可覆盖的 Resources。
func providePackageOwnedConfigForExecutable(_ *platformconfig.Config, executable string) (Config, bool, error) {
	if err := recovery.RejectPackageTrustOverrides(os.Environ()); err != nil {
		return Config{}, true, err
	}
	platform := runtimePlatform()
	supported, err := packageUpdateSupported(platform)
	if err != nil {
		return Config{}, true, err
	}
	if !supported {
		return Config{}, true, nil
	}
	resources, target, err := packageLayoutFromExecutable(executable)
	if err != nil {
		return Config{}, true, err
	}
	if err := validateConfiguredResources(resources); err != nil {
		return Config{}, true, err
	}
	trust, _, err := recovery.ResolveTransactionBoundPackageTrust(context.Background(), target, platform)
	if err != nil {
		return Config{}, true, err
	}
	if !trust.Enabled {
		return Config{}, true, nil
	}
	cfg, err := mapPackageTrustConfig(trust, resources, target)
	return cfg, true, err
}

// packageUpdateSupported 要求六目标 capability 的 check/install/publish 同时开放。
func packageUpdateSupported(platform string) (bool, error) {
	capability, err := recovery.UpdateCapabilityFor(platform)
	if err != nil {
		return false, err
	}
	return capability.Check && capability.Install && capability.Publish, nil
}

// validateConfiguredResources 允许未设置或与 executable-derived Resources 完全一致的 runtime contract。
func validateConfiguredResources(resources string) error {
	configured := strings.TrimSpace(os.Getenv(envRuntimeResources))
	if configured != "" {
		if err := recovery.RequireCanonicalExistingPath(configured); err != nil {
			return fmt.Errorf("%s must be canonical: %w", envRuntimeResources, err)
		}
	}
	if configured != "" && configured != resources {
		return fmt.Errorf("%s = %q, want executable-derived Resources %q", envRuntimeResources, configured, resources)
	}
	return nil
}

// packageLayoutFromExecutable 只接受 exact .app/Contents/MacOS 可执行布局。
func packageLayoutFromExecutable(executable string) (string, string, error) {
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable {
		return "", "", fmt.Errorf("package executable must be clean absolute: %q", executable)
	}
	if err := recovery.RequireCanonicalExistingPath(executable); err != nil {
		return "", "", fmt.Errorf("package executable must be canonical: %w", err)
	}
	macOSDir := filepath.Dir(executable)
	contents := filepath.Dir(macOSDir)
	target := filepath.Dir(contents)
	if filepath.Base(macOSDir) != "MacOS" || filepath.Base(contents) != "Contents" || !strings.EqualFold(filepath.Ext(target), ".app") {
		return "", "", fmt.Errorf("package executable is outside a macOS app layout: %q", executable)
	}
	info, err := os.Stat(executable)
	if err != nil {
		return "", "", fmt.Errorf("inspect package executable %q: %w", executable, err)
	}
	if info.IsDir() {
		return "", "", fmt.Errorf("package executable path is a directory: %q", executable)
	}
	resources := filepath.Join(contents, "Resources")
	if err := validateCanonicalPackageLayout(macOSDir, contents, target, resources); err != nil {
		return "", "", err
	}
	return resources, target, nil
}

func validateCanonicalPackageLayout(paths ...string) error {
	for _, path := range paths {
		if err := recovery.RequireCanonicalExistingPath(path); err != nil {
			return fmt.Errorf("package layout contains alias %q: %w", path, err)
		}
	}
	return nil
}

// mapPackageTrustConfig 将已验证 trust 映射为 appupdate Config。
func mapPackageTrustConfig(trust recovery.PackageTrust, resources, target string) (Config, error) {
	home := os.Getenv(envSuperDolphinHome)
	if home == "" || !filepath.IsAbs(home) {
		return Config{}, errors.New("SUPER_DOLPHIN_HOME must be an absolute path for package-owned updates")
	}
	publicKey, err := base64.StdEncoding.DecodeString(trust.ManifestPublicKey)
	if err != nil {
		return Config{}, fmt.Errorf("decode package-owned manifest public key: %w", err)
	}
	version, err := currentVersionFromInfoPlist(target)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		Enabled: true, PublicKey: publicKey, Channel: trust.Channel,
		StageDir: filepath.Join(home, "updates"), HelperPath: filepath.Join(resources, "bin", updaterHelperName),
		TargetAppPath: target, Platform: trust.Platform, CurrentVersion: version,
	}
	switch trust.Source.Kind {
	case recovery.UpdateSourceGitHub:
		cfg.GitHubRepo = trust.Source.Value
	case recovery.UpdateSourceManifest:
		cfg.ManifestURL = trust.Source.Value
	default:
		return Config{}, fmt.Errorf("unsupported package-owned source kind %q", trust.Source.Kind)
	}
	if err := validateConfig(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func runtimePlatform() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}
