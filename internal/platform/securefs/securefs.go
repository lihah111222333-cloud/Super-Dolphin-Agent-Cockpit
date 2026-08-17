package securefs

import (
	"errors"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"strings"
)


// RedactPath 只保留路径 basename，避免日志或错误把用户目录和敏感文件路径泄露出去。
func RedactPath(path string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(path), `\`, "/")
	base := pathpkg.Base(pathpkg.Clean(normalized))
	if strings.TrimSpace(base) == "" || base == "." || base == "/" {
		return "<redacted-path>"
	}
	return "<redacted:" + base + ">"
}

// SafeError 把 os.PathError 转成不含原始路径的短错误文本。
func SafeError(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		if pathErr.Err != nil {
			return pathErr.Op + ": " + pathErr.Err.Error()
		}
		return pathErr.Op
	}
	return err.Error()
}

// SafeErrorForPath 对错误文本中的目标路径做脱敏替换。
func SafeErrorForPath(err error, path string) string {
	text := SafeError(err)
	if strings.TrimSpace(path) == "" {
		return text
	}
	redacted := RedactPath(path)
	for _, candidate := range []string{
		path,
		filepath.Clean(path),
		filepath.ToSlash(filepath.Clean(path)),
		filepath.ToSlash(path),
		strings.ReplaceAll(path, `\`, "/"),
	} {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		text = strings.ReplaceAll(text, candidate, redacted)
	}
	return text
}

// ProbeWritableDir 通过创建并删除临时文件确认目录可写，失败时只返回脱敏路径。
func ProbeWritableDir(dir string) error {
	file, err := os.CreateTemp(dir, ".super-dolphin-write-test-*")
	if err != nil {
		return fmt.Errorf("probe writable directory %s: %s", RedactPath(dir), SafeError(err))
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("close writable probe in %s: %s", RedactPath(dir), SafeError(err))
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("remove writable probe in %s: %s", RedactPath(dir), SafeError(err))
	}
	return nil
}

// SyncDirectory 在支持目录 fsync 的平台持久化目录项（Unix 执行 dir.Sync()，Windows 校验有效性后返回 nil）。
func SyncDirectory(dir string) error {
	if runtime.GOOS == "windows" {
		if strings.TrimSpace(dir) == "" {
			return errors.New("directory path is required")
		}
		info, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("stat directory %s: %s", RedactPath(dir), SafeError(err))
		}
		if !info.IsDir() {
			return fmt.Errorf("path %s is not a directory", RedactPath(dir))
		}
		return nil
	}
	dirFile, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open directory %s: %s", RedactPath(dir), SafeError(err))
	}
	if err := dirFile.Sync(); err != nil {
		_ = dirFile.Close()
		return fmt.Errorf("sync directory %s: %s", RedactPath(dir), SafeError(err))
	}
	if err := dirFile.Close(); err != nil {
		return fmt.Errorf("close directory %s: %s", RedactPath(dir), SafeError(err))
	}
	return nil
}



