// Package gateprivate contains infrastructure used only by super-dolphin-gate.
package gateprivate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

const sqliteWriteRetryDelay = 50 * time.Millisecond

// OpenSQLite 按单写者合同打开门禁协调器数据库。
func OpenSQLite(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// RestrictOwnerFile 将门禁状态文件限制为仅当前所有者可读写。
func RestrictOwnerFile(path string) error {
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("restrict owner file: %w", err)
	}
	return nil
}

// RetrySQLiteWrite 只在上下文时限内重试 SQLite busy 或 locked 冲突。
func RetrySQLiteWrite(ctx context.Context, maxAttempts int, fn func() error) error {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	var lastErr error
	for attempt := range maxAttempts {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = fn()
		if lastErr == nil || !isSQLiteBusyLocked(lastErr) {
			return lastErr
		}
		if attempt == maxAttempts-1 {
			break
		}
		timer := time.NewTimer(sqliteWriteRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return lastErr
}

func isSQLiteBusyLocked(err error) bool {
	type sqliteCoder interface{ Code() int }
	var coded sqliteCoder
	if !errors.As(err, &coded) {
		return false
	}
	code := coded.Code() & 0xff
	return code == sqlite3.SQLITE_BUSY || code == sqlite3.SQLITE_LOCKED
}

// CanonicalOwnerDirectory 校验绝对私有目录，并拒绝任何符号链接穿越。
func CanonicalOwnerDirectory(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", fmt.Errorf("directory must be canonical and absolute: %q", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve directory %q: %w", path, err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.Join(fmt.Errorf("directory %q must be private", path), err)
	}
	if resolved != path {
		return "", fmt.Errorf("directory must not traverse symlinks: %q", path)
	}
	return resolved, nil
}

// CanonicalOwnerFile 校验绝对私有普通文件及其父目录。
func CanonicalOwnerFile(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", fmt.Errorf("file must be canonical and absolute: %q", path)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("file must be a private regular file")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return "", errors.Join(errors.New("file must not traverse symlinks"), err)
	}
	if _, err := CanonicalOwnerDirectory(filepath.Dir(path)); err != nil {
		return "", err
	}
	return resolved, nil
}

// ReadOwnerFile 读取已验证的私有文件，并拒绝打开过程中的文件身份漂移。
func ReadOwnerFile(path string, maxBytes int64) ([]byte, error) {
	if maxBytes < 0 {
		return nil, errors.New("maximum bytes must not be negative")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open owner file: %w", err)
	}
	if err := validateOpenedOwnerFile(file, path); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("owner file exceeds size limit")
	}
	return data, nil
}

// validateOpenedOwnerFile 复验已打开文件与路径仍指向同一私有普通文件。
func validateOpenedOwnerFile(file *os.File, path string) error {
	opened, statErr := file.Stat()
	pathInfo, lstatErr := os.Lstat(path)
	if statErr != nil || lstatErr != nil {
		return errors.Join(errors.New("owner file changed while opening"), statErr, lstatErr)
	}
	if !os.SameFile(opened, pathInfo) {
		return errors.New("owner file changed while opening")
	}
	if !opened.Mode().IsRegular() || opened.Mode().Perm()&0o077 != 0 {
		return errors.New("owner file changed while opening")
	}
	return nil
}
