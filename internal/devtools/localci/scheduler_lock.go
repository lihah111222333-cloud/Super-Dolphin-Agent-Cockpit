package localci

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

var errSchedulerOwned = errors.New("scheduler daemon is already owned")

type schedulerLock struct {
	file *os.File
}

// acquireSchedulerLock 取得 daemon identity 对应的进程级非阻塞独占锁。
func acquireSchedulerLock(lockPath string, identity daemonIdentity) (*schedulerLock, error) {
	if strings.TrimSpace(lockPath) == "" {
		return nil, errors.New("scheduler lock path is required")
	}
	if strings.TrimSpace(identity.key) == "" {
		return nil, errors.New("validated daemon identity is required")
	}
	file, err := openCurrentUIDPrivateFile(lockPath, identity.ownerUID)
	if err != nil {
		return nil, fmt.Errorf("open scheduler lock: %w", err)
	}
	if err := verifySchedulerLockFile(file, identity); err != nil {
		return nil, closeFileAfterError(file, err, "close scheduler lock after verification failure")
	}
	if err := lockSchedulerFile(file); err != nil {
		closeErr := file.Close()
		if schedulerLockAlreadyOwned(err) {
			return nil, errors.Join(errSchedulerOwned, closeErr)
		}
		return nil, errors.Join(fmt.Errorf("acquire scheduler lock: %w", err), closeErr)
	}
	return &schedulerLock{file: file}, nil
}

// verifySchedulerLockFile 校验私有 regular file 元数据和持久化 daemon identity。
func verifySchedulerLockFile(file *os.File, identity daemonIdentity) error {
	if err := verifySchedulerLockFileMetadata(file, identity.ownerUID); err != nil {
		return err
	}
	return verifySchedulerLockIdentity(file, identity)
}

// verifySchedulerLockFileMetadata 复核已打开 lock fd 的类型、owner 与权限。
func verifySchedulerLockFileMetadata(file *os.File, ownerUID int) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat scheduler lock: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("scheduler lock must be a regular file")
	}
	if err := validatePrivateOwnerAndMode(info, ownerUID, false); err != nil {
		return fmt.Errorf("scheduler lock ownership: %w", err)
	}
	if info.Mode().Perm() != privateSchedulerFileMode {
		return fmt.Errorf("scheduler lock mode is %04o, want 0600", info.Mode().Perm())
	}
	return nil
}

// verifySchedulerLockIdentity 拒绝复用属于其他 daemon identity 的锁文件。
func verifySchedulerLockIdentity(file *os.File, identity daemonIdentity) error {
	contents, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("read scheduler lock identity: %w", err)
	}
	storedKey := strings.TrimSpace(string(contents))
	if storedKey != "" && storedKey != identity.key {
		return errors.New("scheduler lock daemon identity mismatch")
	}
	if storedKey == identity.key {
		return nil
	}
	if _, err := file.WriteAt([]byte(identity.key+"\n"), 0); err != nil {
		return fmt.Errorf("write scheduler lock identity: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync scheduler lock identity: %w", err)
	}
	return nil
}

func (l *schedulerLock) close() error {
	if l == nil || l.file == nil {
		return errors.New("scheduler lock is not open")
	}
	unlockErr := unlockSchedulerFile(l.file)
	closeErr := l.file.Close()
	l.file = nil
	return errors.Join(unlockErr, closeErr)
}
