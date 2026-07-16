package appupdaterecovery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
func reconcileBackupEffect(journal journalPayload) error {
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
		if err := verifyRelease(journal.Paths.Target, journal.Identity.OldRelease); err != nil {
			return fmt.Errorf("verify old release before backup: %w", err)
		}
		if err := os.Rename(journal.Paths.Target, journal.Paths.Backup); err != nil {
			return fmt.Errorf("retain exact update backup: %w", err)
		}
		return syncDirectory(filepath.Dir(journal.Paths.Target))
	case !targetExists && backupExists:
		return verifyRelease(journal.Paths.Backup, journal.Identity.OldRelease)
	default:
		return fmt.Errorf("backup intent has ambiguous filesystem state: target=%v backup=%v", targetExists, backupExists)
	}
}

// reconcileInstallEffect 将 install_pending 意图收敛到 exact candidate 已安装。
func reconcileInstallEffect(journal journalPayload) error {
	targetExists, err := pathExists(journal.Paths.Target)
	if err != nil {
		return err
	}
	stagingExists, err := pathExists(journal.Paths.Staging)
	if err != nil {
		return err
	}
	if err := verifyRelease(journal.Paths.Backup, journal.Identity.OldRelease); err != nil {
		return fmt.Errorf("verify retained backup before candidate install: %w", err)
	}
	switch {
	case !targetExists && stagingExists:
		return installStagedCandidate(journal)
	case targetExists && !stagingExists:
		return verifyRelease(journal.Paths.Target, journal.Identity.CandidateRelease)
	default:
		return fmt.Errorf("install intent has ambiguous filesystem state: target=%v staging=%v", targetExists, stagingExists)
	}
}

// installStagedCandidate 验证 staging，执行同卷 rename，并复验 target。
func installStagedCandidate(journal journalPayload) error {
	if err := verifyRelease(journal.Paths.Staging, journal.Identity.CandidateRelease); err != nil {
		return fmt.Errorf("verify staged candidate: %w", err)
	}
	if err := os.Rename(journal.Paths.Staging, journal.Paths.Target); err != nil {
		return fmt.Errorf("install exact candidate release: %w", err)
	}
	if err := syncDirectory(filepath.Dir(journal.Paths.Target)); err != nil {
		return err
	}
	return verifyRelease(journal.Paths.Target, journal.Identity.CandidateRelease)
}

// completeCommitEffect 在验证 candidate 与 backup 后删除已提交 backup。
func completeCommitEffect(journal journalPayload) error {
	if err := verifyRelease(journal.Paths.Target, journal.Identity.CandidateRelease); err != nil {
		return fmt.Errorf("verify candidate before healthy commit: %w", err)
	}
	backupExists, err := pathExists(journal.Paths.Backup)
	if err != nil {
		return err
	}
	if backupExists {
		if err := verifyRelease(journal.Paths.Backup, journal.Identity.OldRelease); err != nil {
			return fmt.Errorf("verify backup before healthy commit: %w", err)
		}
		if err := os.RemoveAll(journal.Paths.Backup); err != nil {
			return fmt.Errorf("remove committed update backup: %w", err)
		}
	}
	return syncDirectory(filepath.Dir(journal.Paths.Target))
}

// completeRollbackEffect 恢复 exact old release，并兼容 rename 后的 crash replay。
func completeRollbackEffect(journal journalPayload) error {
	backupExists, err := pathExists(journal.Paths.Backup)
	if err != nil {
		return err
	}
	targetExists, err := pathExists(journal.Paths.Target)
	if err != nil {
		return err
	}
	if backupExists {
		if err := restoreRetainedBackup(journal); err != nil {
			return err
		}
	} else if !targetExists {
		return errors.New("rollback intent has neither backup nor restored target")
	}
	return finalizeRollback(journal)
}

// restoreRetainedBackup 以 exact backup 替换 candidate target。
func restoreRetainedBackup(journal journalPayload) error {
	if err := verifyRelease(journal.Paths.Backup, journal.Identity.OldRelease); err != nil {
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
func finalizeRollback(journal journalPayload) error {
	if err := removeIfExists(journal.Paths.Staging); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(journal.Paths.Target)); err != nil {
		return err
	}
	return verifyRelease(journal.Paths.Target, journal.Identity.OldRelease)
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
