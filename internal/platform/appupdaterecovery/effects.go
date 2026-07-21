package appupdaterecovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// InstallFirstRelease 持久化并原子安装首次发布，不创建回滚事务。
// staging 与 target 必须位于同一父目录，以保证 rename 的原子性。
func InstallFirstRelease(staging string, target string) error {
	if filepath.Dir(staging) != filepath.Dir(target) {
		return errors.New("first release staging and target must share a parent directory")
	}
	targetExists, err := pathExists(target)
	if err != nil {
		return err
	}
	if targetExists {
		return fmt.Errorf("first release target already exists: %s", target)
	}
	if err := syncRelease(staging); err != nil {
		return fmt.Errorf("persist first release candidate: %w", err)
	}
	if err := os.Rename(staging, target); err != nil {
		return fmt.Errorf("install first release candidate: %w", err)
	}
	if err := syncDirectory(filepath.Dir(target)); err != nil {
		return fmt.Errorf("persist first release directory entry: %w", err)
	}
	return nil
}

// reconcileBackupEffect 将 backup_pending 意图收敛到 exact backup 已保留。
func reconcileBackupEffect(ctx context.Context, journal journalPayload) error {
	targetExists, err := pathExists(journal.Paths.Target)
	if err != nil {
		return err
	}
	backupExists, err := pathExists(journal.Paths.Backup)
	if err != nil {
		return err
	}
	switch {
	case targetExists && !backupExists:
		if err := verifyRelease(ctx, journal.Paths.Target, journal.Identity.OldRelease); err != nil {
			return fmt.Errorf("verify old release before backup: %w", err)
		}
		if err := os.Rename(journal.Paths.Target, journal.Paths.Backup); err != nil {
			return fmt.Errorf("retain exact update backup: %w", err)
		}
		return syncDirectory(filepath.Dir(journal.Paths.Target))
	case !targetExists && backupExists:
		return verifyRelease(ctx, journal.Paths.Backup, journal.Identity.OldRelease)
	default:
		return fmt.Errorf("%w: backup intent target=%v backup=%v", contract.ErrUpdateTransactionAmbiguous, targetExists, backupExists)
	}
}

// reconcileInstallEffect 将 install_pending 意图收敛到 exact candidate 已安装。
func reconcileInstallEffect(ctx context.Context, journal journalPayload) error {
	targetExists, err := pathExists(journal.Paths.Target)
	if err != nil {
		return err
	}
	stagingExists, err := pathExists(journal.Paths.Staging)
	if err != nil {
		return err
	}
	if err := verifyRelease(ctx, journal.Paths.Backup, journal.Identity.OldRelease); err != nil {
		return fmt.Errorf("verify retained backup before candidate install: %w", err)
	}
	switch {
	case !targetExists && stagingExists:
		return installStagedCandidate(ctx, journal)
	case targetExists && !stagingExists:
		return verifyRelease(ctx, journal.Paths.Target, journal.Identity.CandidateRelease)
	default:
		return fmt.Errorf("%w: install intent target=%v staging=%v", contract.ErrUpdateTransactionAmbiguous, targetExists, stagingExists)
	}
}

// installStagedCandidate 验证 staging，执行同卷 rename，并复验 target。
func installStagedCandidate(ctx context.Context, journal journalPayload) error {
	if err := verifyRelease(ctx, journal.Paths.Staging, journal.Identity.CandidateRelease); err != nil {
		return fmt.Errorf("verify staged candidate: %w", err)
	}
	if err := os.Rename(journal.Paths.Staging, journal.Paths.Target); err != nil {
		return fmt.Errorf("install exact candidate release: %w", err)
	}
	if err := syncDirectory(filepath.Dir(journal.Paths.Target)); err != nil {
		return err
	}
	return verifyRelease(ctx, journal.Paths.Target, journal.Identity.CandidateRelease)
}

func verifyCommitCandidate(ctx context.Context, journal journalPayload) error {
	if err := verifyRelease(ctx, journal.Paths.Target, journal.Identity.CandidateRelease); err != nil {
		return fmt.Errorf("verify candidate before healthy commit: %w", err)
	}
	return nil
}

func verifyReleaseAtRootIdentity(ctx context.Context, path string, release ReleaseIdentity, expected discardRootIdentity) error {
	if err := requireRootIdentity(path, expected); err != nil {
		return err
	}
	if err := verifyRelease(ctx, path, release); err != nil {
		return err
	}
	return requireRootIdentity(path, expected)
}

func requireRootIdentity(path string, expected discardRootIdentity) error {
	actual, err := captureDiscardRootIdentity(path)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("backup discard root identity changed: got %+v, want %+v", actual, expected)
	}
	return nil
}

func renameBackupToDiscardAtIdentity(journal journalPayload, expected discardRootIdentity) error {
	if err := requireRootIdentity(journal.Paths.Backup, expected); err != nil {
		return fmt.Errorf("verify backup root immediately before discard rename: %w", err)
	}
	discard := backupDiscardPath(journal.Paths)
	if err := os.Rename(journal.Paths.Backup, discard); err != nil {
		return fmt.Errorf("rename committed update backup to discard: %w", err)
	}
	if err := syncDirectory(filepath.Dir(journal.Paths.Target)); err != nil {
		return err
	}
	if err := requireRootIdentity(discard, expected); err != nil {
		return fmt.Errorf("verify discard root after rename: %w", err)
	}
	return nil
}

// cleanupCommittedEffect 只清理根实例仍与持久身份一致的 committed discard。
func cleanupCommittedEffect(ctx context.Context, journal journalPayload, expected discardRootIdentity) error {
	if err := verifyRelease(ctx, journal.Paths.Target, journal.Identity.CandidateRelease); err != nil {
		return fmt.Errorf("verify candidate before committed cleanup: %w", err)
	}
	discard := backupDiscardPath(journal.Paths)
	exists, err := pathExists(discard)
	if err != nil {
		return err
	}
	if !exists {
		return syncDirectory(filepath.Dir(journal.Paths.Target))
	}
	actual, err := captureDiscardRootIdentity(discard)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("backup discard root identity changed: got %+v, want %+v", actual, expected)
	}
	if err := removeIfExists(discard); err != nil {
		return fmt.Errorf("remove committed backup discard: %w", err)
	}
	return syncDirectory(filepath.Dir(journal.Paths.Target))
}

func backupDiscardPath(paths Paths) string {
	return paths.Backup + ".discard"
}

// completeRollbackEffect 恢复 exact old release，并兼容 rename 后的 crash replay。
func completeRollbackEffect(ctx context.Context, journal journalPayload) error {
	backupExists, err := pathExists(journal.Paths.Backup)
	if err != nil {
		return err
	}
	targetExists, err := pathExists(journal.Paths.Target)
	if err != nil {
		return err
	}
	if backupExists {
		if err := restoreRetainedBackup(ctx, journal); err != nil {
			return err
		}
	} else if !targetExists {
		return errors.New("rollback intent has neither backup nor restored target")
	}
	return finalizeRollback(ctx, journal)
}

// validatePreparedRollback 在持久化终止意图前确认旧版本和 staging 仍与 exact transaction 一致。
func validatePreparedRollback(ctx context.Context, journal journalPayload) error {
	backupExists, err := pathExists(journal.Paths.Backup)
	if err != nil {
		return err
	}
	if backupExists {
		return errors.New("prepared rollback cannot start with a retained backup")
	}
	if err := verifyRelease(ctx, journal.Paths.Target, journal.Identity.OldRelease); err != nil {
		return fmt.Errorf("verify old release before prepared rollback: %w", err)
	}
	if err := verifyRelease(ctx, journal.Paths.Staging, journal.Identity.CandidateRelease); err != nil {
		return fmt.Errorf("verify candidate staging before prepared rollback: %w", err)
	}
	return nil
}

// restoreRetainedBackup 以 exact backup 替换 candidate target。
func restoreRetainedBackup(ctx context.Context, journal journalPayload) error {
	if err := verifyRelease(ctx, journal.Paths.Backup, journal.Identity.OldRelease); err != nil {
		return fmt.Errorf("verify backup before rollback: %w", err)
	}
	if err := removeIfExists(journal.Paths.Target); err != nil {
		return err
	}
	if err := removeIfExists(journal.Paths.Staging); err != nil {
		return err
	}
	if err := os.Rename(journal.Paths.Backup, journal.Paths.Target); err != nil {
		return fmt.Errorf("restore exact update backup: %w", err)
	}
	return nil
}

// finalizeRollback 同步父目录并确认 old release 已恢复。
func finalizeRollback(ctx context.Context, journal journalPayload) error {
	if err := removeIfExists(journal.Paths.Staging); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(journal.Paths.Target)); err != nil {
		return err
	}
	return verifyRelease(ctx, journal.Paths.Target, journal.Identity.OldRelease)
}

func pathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("inspect update path %s: %w", path, err)
}

func requirePath(path string) error {
	exists, err := pathExists(path)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("required update path is missing: %s", path)
	}
	return nil
}

func removeIfExists(path string) error {
	exists, err := pathExists(path)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove update path %s: %w", path, err)
	}
	return nil
}
