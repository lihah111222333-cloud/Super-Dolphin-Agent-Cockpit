//go:build darwin

package appupdatefailure

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

type lockedStageDir struct {
	fd     int
	lockFD int
}

func withLockedStageDir(stageDir string, action func(*lockedStageDir) error) (err error) {
	dirFD, err := openPrivateStageDir(stageDir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, closeDescriptor(dirFD, "close app update stage dir")) }()
	lockFD, err := openPrivateLock(dirFD)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, closeDescriptor(lockFD, "close app update sidecar lock")) }()
	if err := unix.Flock(lockFD, unix.LOCK_EX); err != nil {
		return fmt.Errorf("lock app update failure sidecar: %w", err)
	}
	defer func() {
		if unlockErr := unix.Flock(lockFD, unix.LOCK_UN); unlockErr != nil {
			err = errors.Join(err, fmt.Errorf("unlock app update failure sidecar: %w", unlockErr))
		}
	}()
	return action(&lockedStageDir{fd: dirFD, lockFD: lockFD})
}

// openPrivateStageDir 从根目录可信锚逐组件打开并校验私有 StageDir。
func openPrivateStageDir(stageDir string) (fd int, err error) {
	if _, err := CanonicalPath(stageDir); err != nil {
		return -1, err
	}
	fd, err = unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, fmt.Errorf("open trusted filesystem anchor: %w", err)
	}
	for component := range strings.SplitSeq(strings.TrimPrefix(stageDir, "/"), "/") {
		next, openErr := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if openErr != nil {
			return -1, errors.Join(fmt.Errorf("open app update stage component: %w", openErr), closeDescriptor(fd, "close app update stage component"))
		}
		if closeErr := closeDescriptor(fd, "close app update stage component"); closeErr != nil {
			return -1, errors.Join(closeErr, closeDescriptor(next, "close app update stage component"))
		}
		fd = next
	}
	if err := requirePrivateDirectory(fd); err != nil {
		return -1, errors.Join(err, closeDescriptor(fd, "close unsafe app update stage dir"))
	}
	return fd, nil
}

func openPrivateLock(dirFD int) (int, error) {
	fd, err := unix.Openat(dirFD, LockFilename, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return -1, fmt.Errorf("open app update sidecar lock: %w", err)
	}
	if err := requirePrivateRegularFile(fd, 0); err != nil {
		return -1, errors.Join(err, closeDescriptor(fd, "close unsafe app update sidecar lock"))
	}
	return fd, nil
}

func requirePrivateDirectory(fd int) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("inspect app update stage dir handle: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Uid != uint32(os.Geteuid()) || stat.Mode&0o7777 != 0o700 {
		return errors.New("app update stage dir must be owned by the current user with mode 0700")
	}
	return nil
}

// requirePrivateRegularFile 校验句柄指向当前用户拥有的 0600 普通文件。
func requirePrivateRegularFile(fd int, maximumSize int64) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("inspect app update sidecar handle: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != uint32(os.Geteuid()) || stat.Mode&0o7777 != 0o600 {
		return errors.New("app update sidecar file must be owned by the current user with mode 0600")
	}
	if maximumSize > 0 && stat.Size > maximumSize {
		return errors.New("app update failure sidecar exceeds the size limit")
	}
	return nil
}

// readRecord 通过已锁定目录句柄安全读取并解析 sidecar。
func (dir *lockedStageDir) readRecord() (record, bool, error) {
	fd, err := unix.Openat(dir.fd, Filename, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return record{}, false, nil
	}
	if err != nil {
		return record{}, false, fmt.Errorf("open app update failure sidecar: %w", err)
	}
	raw, readErr := readBoundedRecord(fd)
	closeErr := closeDescriptor(fd, "close app update failure sidecar")
	if readErr != nil || closeErr != nil {
		return record{}, false, errors.Join(readErr, closeErr)
	}
	value, err := decodeRecord(raw)
	if err != nil {
		return record{}, false, err
	}
	return value, true, nil
}

// readBoundedRecord 循环读取至 EOF，并在超过固定上限时拒绝内容。
func readBoundedRecord(fd int) ([]byte, error) {
	if err := requirePrivateRegularFile(fd, maxSize); err != nil {
		return nil, err
	}
	raw := make([]byte, maxSize+1)
	total := 0
	for total < len(raw) {
		count, err := unix.Read(fd, raw[total:])
		if err != nil {
			return nil, fmt.Errorf("read app update failure sidecar: %w", err)
		}
		total += count
		if count == 0 {
			break
		}
	}
	if total > maxSize {
		return nil, errors.New("app update failure sidecar exceeds the size limit")
	}
	return raw[:total], nil
}

// writeRecord 通过临时文件和 renameat 原子发布 sidecar。
func (dir *lockedStageDir) writeRecord(value record) (err error) {
	if err := validateRecord(value); err != nil {
		return err
	}
	raw, err := encodeRecord(value)
	if err != nil {
		return err
	}
	tempName, err := randomTempName()
	if err != nil {
		return err
	}
	return dir.publishRecord(tempName, raw)
}

// publishRecord 创建并同步私有临时文件，再以 renameat 发布到固定名称。
func (dir *lockedStageDir) publishRecord(tempName string, raw []byte) (err error) {
	tempFD, err := unix.Openat(dir.fd, tempName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("create app update sidecar temp: %w", err)
	}
	keepTemp := true
	defer func() {
		if keepTemp {
			unlinkErr := unix.Unlinkat(dir.fd, tempName, 0)
			if unlinkErr != nil && !errors.Is(unlinkErr, unix.ENOENT) {
				err = errors.Join(err, fmt.Errorf("remove app update sidecar temp: %w", unlinkErr))
			}
		}
	}()
	if err := writeAndSync(tempFD, raw); err != nil {
		return errors.Join(err, closeDescriptor(tempFD, "close app update sidecar temp"))
	}
	if err := closeDescriptor(tempFD, "close app update sidecar temp"); err != nil {
		return err
	}
	if err := unix.Renameat(dir.fd, tempName, dir.fd, Filename); err != nil {
		return fmt.Errorf("publish app update failure sidecar: %w", err)
	}
	keepTemp = false
	if err := unix.Fsync(dir.fd); err != nil {
		return fmt.Errorf("sync app update stage dir: %w", err)
	}
	return nil
}

func writeAndSync(fd int, raw []byte) error {
	for len(raw) > 0 {
		written, err := unix.Write(fd, raw)
		if err != nil {
			return fmt.Errorf("write app update sidecar temp: %w", err)
		}
		if written == 0 {
			return errors.New("write app update sidecar temp made no progress")
		}
		raw = raw[written:]
	}
	if err := unix.Fsync(fd); err != nil {
		return fmt.Errorf("sync app update sidecar temp: %w", err)
	}
	return nil
}

func (dir *lockedStageDir) removeRecord() error {
	err := unix.Unlinkat(dir.fd, Filename, 0)
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("clear app update failure sidecar: %w", err)
	}
	if err := unix.Fsync(dir.fd); err != nil {
		return fmt.Errorf("sync app update stage dir: %w", err)
	}
	return nil
}

func randomTempName() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate app update sidecar temp name: %w", err)
	}
	return ".pre-journal-failure-" + hex.EncodeToString(value[:]), nil
}

func closeDescriptor(fd int, operation string) error {
	if err := unix.Close(fd); err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return nil
}
