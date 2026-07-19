package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
)

const (
	packageUpdaterPath = "Contents/Resources/bin/super-dolphin-updater"
	packageGuardPath   = "Contents/Resources/bin/super-dolphin-guard"
)

type recoveryCapsule struct {
	UpdaterPath string
	GuardPath   string
	TrustPath   string
	Helpers     recovery.HelperIdentity
}

// bindPackageOwnedTrust 将两代包内 trust 和 helper 真值绑定到事务。
func bindPackageOwnedTrust(ctx context.Context, req recovery.CreateRequest, install installRequest, expectedSigner string) (recovery.CreateRequest, bool, error) {
	platform := runtime.GOOS + "-" + runtime.GOARCH
	oldResources := filepath.Join(req.Paths.Target, "Contents", "Resources")
	candidateResources := filepath.Join(req.Paths.Staging, "Contents", "Resources")
	oldTrust, oldGeneration, candidateTrust, candidateGeneration, packageOwned, err := loadPackageTrustPair(oldResources, candidateResources, platform)
	if err != nil || !packageOwned {
		return req, packageOwned, err
	}
	if err := recovery.RejectPackageTrustOverrides(os.Environ()); err != nil {
		return recovery.CreateRequest{}, false, err
	}
	oldHelpers, err := helperIdentityForRelease(ctx, req.Paths.Target)
	if err != nil {
		return recovery.CreateRequest{}, false, err
	}
	candidateHelpers, err := helperIdentityForRelease(ctx, req.Paths.Staging)
	if err != nil {
		return recovery.CreateRequest{}, false, err
	}
	if err := validatePackageOwnedInstall(install, oldTrust, candidateTrust, expectedSigner, oldHelpers, candidateHelpers); err != nil {
		return recovery.CreateRequest{}, false, err
	}
	canonicalUpdater := filepath.Join(req.Paths.Target, packageUpdaterPath)
	if err := validateRunningPackageUpdaterContext(ctx, req.Identity.UpdaterProcess, oldTrust, oldHelpers, canonicalUpdater); err != nil {
		return recovery.CreateRequest{}, false, err
	}
	req.Identity.OldHelpers = oldHelpers
	req.Identity.CandidateHelpers = candidateHelpers
	req.Trust = recovery.TrustGeneration{
		PreviousGeneration: oldGeneration,
		Generation:         candidateGeneration,
		PackageSigner:      expectedSigner,
		State:              recovery.TrustPending,
	}
	return req, true, nil
}

// publishPackageRecoveryCapsule 发布并复核旧包 helper 与 trust 的 exact 快照。
func publishPackageRecoveryCapsule(ctx context.Context, transaction recovery.Transaction) error {
	oldResources := filepath.Join(transaction.Paths.Target, "Contents", "Resources")
	capsule, err := prepareRecoveryCapsuleContext(
		ctx,
		transaction.Paths.RecoveryDir,
		filepath.Join(transaction.Paths.Target, packageUpdaterPath),
		filepath.Join(transaction.Paths.Target, packageGuardPath),
		filepath.Join(oldResources, recovery.PackageTrustFilename),
	)
	if err != nil {
		return err
	}
	if capsule.Helpers != transaction.Identity.OldHelpers {
		return errors.Join(
			errors.New("recovery capsule helper identity changed during copy"),
			cleanupPackageRecoveryCapsule(transaction.Paths.RecoveryDir),
		)
	}
	trust, generation, err := recovery.LoadPackageTrust(transaction.Paths.RecoveryDir, runtime.GOOS+"-"+runtime.GOARCH)
	if err != nil {
		return errors.Join(err, cleanupPackageRecoveryCapsule(transaction.Paths.RecoveryDir))
	}
	if generation != transaction.Trust.PreviousGeneration ||
		trust.SignerIdentity != transaction.Trust.PackageSigner ||
		trust.UpdaterSHA256 != capsule.Helpers.UpdaterSHA256 ||
		trust.GuardSHA256 != capsule.Helpers.GuardSHA256 {
		return errors.Join(errors.New("recovery capsule trust identity changed during copy"), cleanupPackageRecoveryCapsule(transaction.Paths.RecoveryDir))
	}
	return nil
}

func cleanupPackageRecoveryCapsule(dir string) error {
	return errors.Join(removeRecoveryCapsule(dir+".pending"), removeRecoveryCapsule(dir))
}

// validateRunningPackageUpdater 拒绝不来自旧包规范 helper 路径的直接 CLI。
func validateRunningPackageUpdater(process recovery.ProcessIdentity, trust recovery.PackageTrust, helpers recovery.HelperIdentity, canonicalPath string) error {
	return validateRunningPackageUpdaterContext(context.Background(), process, trust, helpers, canonicalPath)
}

// validateRunningPackageUpdaterContext 在调用方期限内验证 updater 路径、进程和摘要身份。
func validateRunningPackageUpdaterContext(ctx context.Context, process recovery.ProcessIdentity, trust recovery.PackageTrust, helpers recovery.HelperIdentity, canonicalPath string) error {
	expectedPath, runningPath, err := canonicalUpdaterPathsContext(ctx, canonicalPath, process.ExecutableIdentity)
	if err != nil {
		return err
	}
	if runningPath != expectedPath {
		return fmt.Errorf("running updater executable = %q, want package helper %q", runningPath, expectedPath)
	}
	if err := validateStableUpdaterProcess(process, runningPath); err != nil {
		return err
	}
	if process.ExecutableSHA256 != trust.UpdaterSHA256 || process.ExecutableSHA256 != helpers.UpdaterSHA256 {
		return errors.New("running updater digest does not match package-owned trust")
	}
	digest, err := recovery.ComputeReleaseDigestContext(ctx, expectedPath)
	if err != nil {
		return fmt.Errorf("digest canonical package updater: %w", err)
	}
	if digest != process.ExecutableSHA256 {
		return errors.New("running updater identity does not match canonical package helper")
	}
	return nil
}

// canonicalUpdaterPathsContext 解析 package helper 与进程声明路径的 canonical existing identity。
func canonicalUpdaterPathsContext(ctx context.Context, expected, running string) (string, string, error) {
	expectedPath, err := recovery.CanonicalExistingPathContext(ctx, expected)
	if err != nil {
		return "", "", fmt.Errorf("canonicalize package updater executable: %w", err)
	}
	runningPath, err := recovery.CanonicalExistingPathContext(ctx, running)
	if err != nil {
		return "", "", fmt.Errorf("canonicalize running updater executable: %w", err)
	}
	return expectedPath, runningPath, nil
}

// validateStableUpdaterProcess 将 PID/start token 与内核 executable identity 重新绑定。
func validateStableUpdaterProcess(process recovery.ProcessIdentity, runningPath string) error {
	stable, err := pidregistry.CaptureStableProcessIdentity(process.PID)
	if err != nil {
		return fmt.Errorf("recapture running updater process identity: %w", err)
	}
	stablePath, err := recovery.CanonicalExistingPath(stable.ExecutableIdentity)
	if err != nil {
		return fmt.Errorf("canonicalize kernel updater executable identity: %w", err)
	}
	if stable.PID != process.PID || stable.ProcessStartToken != process.StartToken || stablePath != runningPath {
		return errors.New("running updater process identity changed before transaction")
	}
	return nil
}

func loadPackageTrustPair(oldResources, candidateResources, platform string) (recovery.PackageTrust, string, recovery.PackageTrust, string, bool, error) {
	oldTrust, oldGeneration, oldErr := recovery.LoadPackageTrust(oldResources, platform)
	candidateTrust, candidateGeneration, candidateErr := recovery.LoadPackageTrust(candidateResources, platform)
	if errors.Is(oldErr, os.ErrNotExist) && errors.Is(candidateErr, os.ErrNotExist) {
		return recovery.PackageTrust{}, "", recovery.PackageTrust{}, "", false, nil
	}
	if oldErr != nil {
		return recovery.PackageTrust{}, "", recovery.PackageTrust{}, "", true, fmt.Errorf("load installed package trust: %w", oldErr)
	}
	if candidateErr != nil {
		return recovery.PackageTrust{}, "", recovery.PackageTrust{}, "", true, fmt.Errorf("load candidate package trust: %w", candidateErr)
	}
	return oldTrust, oldGeneration, candidateTrust, candidateGeneration, true, nil
}

func captureUpdaterProcessIdentity() (recovery.ProcessIdentity, recovery.HelperIdentity, error) {
	return captureUpdaterProcessIdentityContext(context.Background())
}

func captureUpdaterProcessIdentityContext(ctx context.Context) (recovery.ProcessIdentity, recovery.HelperIdentity, error) {
	executable, err := os.Executable()
	if err != nil {
		return recovery.ProcessIdentity{}, recovery.HelperIdentity{}, fmt.Errorf("resolve updater executable: %w", err)
	}
	digest, err := recovery.ComputeReleaseDigestContext(ctx, executable)
	if err != nil {
		return recovery.ProcessIdentity{}, recovery.HelperIdentity{}, fmt.Errorf("digest updater executable: %w", err)
	}
	stable, err := pidregistry.CaptureStableProcessIdentity(os.Getpid())
	if err != nil {
		return recovery.ProcessIdentity{}, recovery.HelperIdentity{}, fmt.Errorf("capture updater process identity: %w", err)
	}
	process := recovery.ProcessIdentity{
		PID: stable.PID, StartToken: stable.ProcessStartToken,
		ExecutableIdentity: stable.ExecutableIdentity, ExecutableSHA256: digest,
	}
	return process, recovery.HelperIdentity{UpdaterSHA256: digest, GuardSHA256: digest}, nil
}

// validatePackageOwnedInstall 在任何事务或文件副作用前校验生产 trust。
func validatePackageOwnedInstall(
	req installRequest,
	oldTrust recovery.PackageTrust,
	candidateTrust recovery.PackageTrust,
	actualSigner string,
	oldHelpers recovery.HelperIdentity,
	candidateHelpers recovery.HelperIdentity,
) error {
	if req.AllowUnsigned {
		return errors.New("package-owned update trust rejects -allow-unsigned CLI bypass")
	}
	if !oldTrust.Enabled || !candidateTrust.Enabled || !oldTrust.Production || !candidateTrust.Production {
		return errors.New("package-owned production update requires enabled production trust in both releases")
	}
	if err := validatePackageSigner(oldTrust, candidateTrust, actualSigner); err != nil {
		return err
	}
	return validatePackageHelpers(oldTrust, candidateTrust, oldHelpers, candidateHelpers)
}

func validatePackageSigner(oldTrust, candidateTrust recovery.PackageTrust, actualSigner string) error {
	if oldTrust.SignerPolicy != recovery.PackageSignerPolicyExact || candidateTrust.SignerPolicy != recovery.PackageSignerPolicyExact {
		return errors.New("package-owned production update requires exact signer policy")
	}
	if oldTrust.SignerIdentity != actualSigner || candidateTrust.SignerIdentity != actualSigner {
		return fmt.Errorf("package-owned update signer mismatch: actual=%q old=%q candidate=%q", actualSigner, oldTrust.SignerIdentity, candidateTrust.SignerIdentity)
	}
	return nil
}

func validatePackageHelpers(oldTrust, candidateTrust recovery.PackageTrust, oldHelpers, candidateHelpers recovery.HelperIdentity) error {
	if oldTrust.UpdaterSHA256 != oldHelpers.UpdaterSHA256 || oldTrust.GuardSHA256 != oldHelpers.GuardSHA256 {
		return errors.New("installed package trust does not match old helper artifacts")
	}
	if candidateTrust.UpdaterSHA256 != candidateHelpers.UpdaterSHA256 || candidateTrust.GuardSHA256 != candidateHelpers.GuardSHA256 {
		return errors.New("candidate package trust does not match candidate helper artifacts")
	}
	return nil
}

func helperIdentityForRelease(ctx context.Context, release string) (recovery.HelperIdentity, error) {
	updater, err := recovery.ComputeReleaseDigestContext(ctx, filepath.Join(release, packageUpdaterPath))
	if err != nil {
		return recovery.HelperIdentity{}, fmt.Errorf("digest package updater helper: %w", err)
	}
	guard, err := recovery.ComputeReleaseDigestContext(ctx, filepath.Join(release, packageGuardPath))
	if err != nil {
		return recovery.HelperIdentity{}, fmt.Errorf("digest package Guard helper: %w", err)
	}
	return recovery.HelperIdentity{UpdaterSHA256: updater, GuardSHA256: guard}, nil
}

// prepareRecoveryCapsule 在 Prepared journal 后构建、同步并原子发布旧 helper 与 trust 快照。
func prepareRecoveryCapsule(dir string, updater string, guard string, trust string) (capsule recoveryCapsule, err error) {
	return prepareRecoveryCapsuleContext(context.Background(), dir, updater, guard, trust)
}

func prepareRecoveryCapsuleContext(ctx context.Context, dir string, updater string, guard string, trust string) (capsule recoveryCapsule, err error) {
	parent := filepath.Dir(dir)
	pending := dir + ".pending"
	if err := initializeRecoveryCapsule(pending, parent); err != nil {
		return recoveryCapsule{}, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, removeRecoveryCapsule(pending))
		}
	}()
	capsule, err = copyRecoveryCapsule(ctx, pending, updater, guard, trust)
	if err != nil {
		return recoveryCapsule{}, err
	}
	if err := publishRecoveryCapsule(dir, pending, parent); err != nil {
		return recoveryCapsule{}, err
	}
	return recoveryCapsuleAt(dir, capsule.Helpers), nil
}

// initializeRecoveryCapsule 创建并同步 scanner 明确认识的 pending capsule。
func initializeRecoveryCapsule(pending, parent string) error {
	if err := removeRecoveryCapsule(pending); err != nil {
		return fmt.Errorf("remove stale recovery capsule staging: %w", err)
	}
	if err := os.MkdirAll(pending, 0o700); err != nil {
		return fmt.Errorf("create recovery capsule staging: %w", err)
	}
	return syncUpdaterDirectory(parent)
}

// copyRecoveryCapsule 逐文件 fsync，复算 helper SHA，并同步 pending 目录。
func copyRecoveryCapsule(ctx context.Context, pending, updater, guard, trust string) (recoveryCapsule, error) {
	capsule := recoveryCapsuleAt(pending, recovery.HelperIdentity{})
	if err := copyExecutable(updater, capsule.UpdaterPath); err != nil {
		return recoveryCapsule{}, err
	}
	if err := copyExecutable(guard, capsule.GuardPath); err != nil {
		return recoveryCapsule{}, err
	}
	if err := copyFile(trust, capsule.TrustPath, 0o600); err != nil {
		return recoveryCapsule{}, err
	}
	identity, err := helperIdentityForPaths(ctx, capsule.UpdaterPath, capsule.GuardPath)
	if err != nil {
		return recoveryCapsule{}, err
	}
	capsule.Helpers = identity
	if err := syncUpdaterDirectory(pending); err != nil {
		return recoveryCapsule{}, err
	}
	return capsule, nil
}

// publishRecoveryCapsule 拒绝覆盖并原子 rename 后同步 transaction 目录。
func publishRecoveryCapsule(dir, pending, parent string) error {
	if _, err := os.Stat(dir); err == nil {
		return errors.New("recovery capsule already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect recovery capsule destination: %w", err)
	}
	if err := os.Rename(pending, dir); err != nil {
		return fmt.Errorf("publish recovery capsule: %w", err)
	}
	return syncUpdaterDirectory(parent)
}

// recoveryCapsuleAt 返回指定目录下固定、不可扩展的 capsule 文件布局。
func recoveryCapsuleAt(dir string, helpers recovery.HelperIdentity) recoveryCapsule {
	return recoveryCapsule{
		UpdaterPath: filepath.Join(dir, "super-dolphin-updater"),
		GuardPath:   filepath.Join(dir, "super-dolphin-guard"),
		TrustPath:   filepath.Join(dir, recovery.PackageTrustFilename),
		Helpers:     helpers,
	}
}

func removeRecoveryCapsule(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove recovery capsule %s: %w", path, err)
	}
	parent := filepath.Dir(path)
	if _, err := os.Stat(parent); err == nil {
		return syncUpdaterDirectory(parent)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect recovery capsule parent: %w", err)
	}
	return nil
}

func helperIdentityForPaths(ctx context.Context, updater, guard string) (recovery.HelperIdentity, error) {
	updaterDigest, err := recovery.ComputeReleaseDigestContext(ctx, updater)
	if err != nil {
		return recovery.HelperIdentity{}, err
	}
	guardDigest, err := recovery.ComputeReleaseDigestContext(ctx, guard)
	if err != nil {
		return recovery.HelperIdentity{}, err
	}
	return recovery.HelperIdentity{UpdaterSHA256: updaterDigest, GuardSHA256: guardDigest}, nil
}

func copyExecutable(source, target string) (err error) {
	return copyFile(source, target, 0o700)
}

// copyFile 以 exclusive create 复制、fsync 并关闭单个 capsule 文件。
func copyFile(source, target string, mode os.FileMode) (err error) {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open recovery helper %s: %w", source, err)
	}
	defer func() { err = errors.Join(err, input.Close()) }()
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create recovery helper %s: %w", target, err)
	}
	if _, err := io.Copy(output, input); err != nil {
		return errors.Join(fmt.Errorf("copy recovery helper %s: %w", source, err), output.Close())
	}
	if err := output.Sync(); err != nil {
		return errors.Join(fmt.Errorf("sync recovery helper %s: %w", target, err), output.Close())
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close recovery helper %s: %w", target, err)
	}
	return nil
}

func syncUpdaterDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open recovery capsule directory for fsync %s: %w", path, err)
	}
	return errors.Join(dir.Sync(), dir.Close())
}
