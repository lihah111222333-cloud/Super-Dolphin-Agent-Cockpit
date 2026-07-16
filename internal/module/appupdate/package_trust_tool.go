package appupdate

import (
	"fmt"
	"os"
	"path/filepath"

	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
)

// PackageTrustWriteRequest 是发布工具生成 package-owned trust 的显式输入。
type PackageTrustWriteRequest struct {
	OutputPath     string
	Platform       string
	Enabled        bool
	SourceKind     string
	SourceValue    string
	ManifestKey    string
	Channel        string
	SignerIdentity string
	UpdaterPath    string
	GuardPath      string
}

// WritePackageTrust 计算最终 helper 摘要并原子前置校验 package trust。
func WritePackageTrust(req PackageTrustWriteRequest) error {
	updaterDigest, err := recovery.ComputeReleaseDigest(req.UpdaterPath)
	if err != nil {
		return fmt.Errorf("digest packaged updater: %w", err)
	}
	guardDigest, err := recovery.ComputeReleaseDigest(req.GuardPath)
	if err != nil {
		return fmt.Errorf("digest packaged Guard: %w", err)
	}
	trust := recovery.PackageTrust{
		SchemaVersion: recovery.PackageTrustSchemaVersion, Enabled: req.Enabled,
		Platform: req.Platform, UpdaterSHA256: updaterDigest, GuardSHA256: guardDigest,
	}
	if req.Enabled {
		trust.Production = true
		trust.Source = recovery.UpdateSource{Kind: req.SourceKind, Value: req.SourceValue}
		trust.ManifestPublicKey = req.ManifestKey
		trust.Channel = req.Channel
		trust.SignerPolicy = recovery.PackageSignerPolicyExact
		trust.SignerIdentity = req.SignerIdentity
	} else {
		trust.SignerPolicy = recovery.PackageSignerPolicyDisabled
	}
	raw, err := recovery.EncodePackageTrust(trust)
	if err != nil {
		return err
	}
	if err := os.WriteFile(req.OutputPath, raw, 0o600); err != nil {
		return fmt.Errorf("write package-owned update trust: %w", err)
	}
	return nil
}

// VerifyPackageTrustBundle 严格校验 trust 与包内最终 helper 摘要。
func VerifyPackageTrustBundle(resources, platform string) error {
	_, err := verifiedPackageTrustBundle(resources, platform)
	return err
}

// VerifiedPackageTrustPublicKey 返回严格验证后的 production package-owned manifest key。
func VerifiedPackageTrustPublicKey(resources, platform string) (string, error) {
	trust, err := verifiedPackageTrustBundle(resources, platform)
	if err != nil {
		return "", err
	}
	if !trust.Enabled || !trust.Production || trust.SignerPolicy != recovery.PackageSignerPolicyExact {
		return "", fmt.Errorf("previous package trust must be enabled production trust with exact signer policy")
	}
	return trust.ManifestPublicKey, nil
}

// verifiedPackageTrustBundle 同时校验 trust schema 与 package 内两个 exact helper 摘要。
func verifiedPackageTrustBundle(resources, platform string) (recovery.PackageTrust, error) {
	trust, _, err := recovery.LoadPackageTrust(resources, platform)
	if err != nil {
		return recovery.PackageTrust{}, err
	}
	updaterDigest, err := recovery.ComputeReleaseDigest(filepath.Join(resources, "bin", updaterHelperName))
	if err != nil {
		return recovery.PackageTrust{}, err
	}
	guardDigest, err := recovery.ComputeReleaseDigest(filepath.Join(resources, "bin", "super-dolphin-guard"))
	if err != nil {
		return recovery.PackageTrust{}, err
	}
	if updaterDigest != trust.UpdaterSHA256 || guardDigest != trust.GuardSHA256 {
		return recovery.PackageTrust{}, fmt.Errorf("package-owned update trust helper digest mismatch")
	}
	return trust, nil
}
