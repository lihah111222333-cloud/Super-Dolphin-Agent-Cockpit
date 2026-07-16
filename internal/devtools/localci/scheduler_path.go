package localci

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const privateSchedulerFileMode = 0o600

// validateCurrentUIDPrivatePath 强制第一版 current-UID 独占路径契约。
func validateCurrentUIDPrivatePath(targetPath string, ownerUID int) (bool, error) {
	currentUID, err := currentSchedulerOwnerUID()
	if err != nil {
		return false, err
	}
	if ownerUID != currentUID {
		return false, fmt.Errorf("scheduler owner UID %d does not match current UID %d", ownerUID, currentUID)
	}
	if err := validatePrivateSchedulerParent(targetPath, ownerUID); err != nil {
		return false, err
	}
	return validatePrivateSchedulerTarget(targetPath, ownerUID)
}

// validatePrivateSchedulerParent 拒绝非 canonical、含链接或共享的父目录。
func validatePrivateSchedulerParent(targetPath string, ownerUID int) error {
	if !filepath.IsAbs(targetPath) || filepath.Clean(targetPath) != targetPath {
		return errors.New("scheduler path must be canonical and absolute")
	}
	parentPath := filepath.Dir(targetPath)
	canonicalParent, err := filepath.EvalSymlinks(parentPath)
	if err != nil {
		return fmt.Errorf("canonicalize scheduler parent: %w", err)
	}
	if canonicalParent != parentPath {
		return errors.New("scheduler parent path must not contain symlinks")
	}
	parentInfo, err := os.Lstat(parentPath)
	if err != nil {
		return fmt.Errorf("lstat scheduler parent: %w", err)
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
		return errors.New("scheduler parent must be a real directory")
	}
	if err := validatePrivateOwnerAndMode(parentInfo, ownerUID, true); err != nil {
		return fmt.Errorf("scheduler parent: %w", err)
	}
	return nil
}

// validatePrivateSchedulerTarget 区分安全缺失文件和已存在的私有 regular file。
func validatePrivateSchedulerTarget(targetPath string, ownerUID int) (bool, error) {
	info, err := os.Lstat(targetPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lstat scheduler file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, errors.New("scheduler file must be a regular non-symlink file")
	}
	if err := validatePrivateOwnerAndMode(info, ownerUID, false); err != nil {
		return false, fmt.Errorf("scheduler file: %w", err)
	}
	return true, nil
}

// validatePrivateOwnerAndMode 校验路径 owner，并拒绝共享目录或非 0600 文件。
func validatePrivateOwnerAndMode(info os.FileInfo, ownerUID int, directory bool) error {
	actualUID, err := schedulerFileOwnerUID(info)
	if err != nil {
		return err
	}
	if actualUID != ownerUID {
		return fmt.Errorf("owner UID %d does not match required UID %d", actualUID, ownerUID)
	}
	if directory {
		if info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("mode is %04o, group and world permissions are forbidden", info.Mode().Perm())
		}
		return nil
	}
	if info.Mode().Perm() != privateSchedulerFileMode {
		return fmt.Errorf("mode is %04o, want 0600", info.Mode().Perm())
	}
	return nil
}

// openCurrentUIDPrivateFile 以 no-follow 语义打开并复核 current-UID 私有文件。
func openCurrentUIDPrivateFile(targetPath string, ownerUID int) (*os.File, error) {
	exists, err := validateCurrentUIDPrivatePath(targetPath, ownerUID)
	if err != nil {
		return nil, err
	}
	file, created, err := openSchedulerFileNoFollow(targetPath, ownerUID, exists)
	if err != nil {
		return nil, fmt.Errorf("open private scheduler file: %w", err)
	}
	if created {
		if err := file.Chmod(privateSchedulerFileMode); err != nil {
			return nil, closeFileAfterError(file, err, "close scheduler file after chmod failure")
		}
	}
	info, err := file.Stat()
	if err != nil {
		return nil, closeFileAfterError(file, err, "close scheduler file after stat failure")
	}
	if !info.Mode().IsRegular() {
		return nil, closeFileAfterError(file, errors.New("scheduler file descriptor is not regular"), "close non-regular scheduler file")
	}
	if err := validatePrivateOwnerAndMode(info, ownerUID, false); err != nil {
		return nil, closeFileAfterError(file, err, "close invalid scheduler file")
	}
	return file, nil
}

func closeFileAfterError(file *os.File, cause error, action string) error {
	if file == nil {
		return cause
	}
	if err := file.Close(); err != nil {
		return errors.Join(cause, fmt.Errorf("%s: %w", action, err))
	}
	return cause
}
