package securefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RedactPath 只保留路径 basename，避免日志或错误把用户目录和敏感文件路径泄露出去。
func RedactPath(path string) string {
	base := filepath.Base(filepath.Clean(path))
	if strings.TrimSpace(base) == "" || base == "." || base == string(filepath.Separator) {
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
	redacted := RedactPath(path)
	for _, candidate := range []string{
		filepath.Clean(path),
		filepath.ToSlash(filepath.Clean(path)),
		path,
		filepath.ToSlash(path),
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
