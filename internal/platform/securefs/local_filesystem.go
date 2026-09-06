package securefs

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrNotLocalFilesystem indicates that a path cannot be proven to reside on a
// private local filesystem. Callers must fail closed on this error.
var ErrNotLocalFilesystem = errors.New("path is not on an approved local filesystem")

// CheckLocalFilesystem 拒绝网络/UNC 路径，并要求宿主 OS 明确证明父文件系统为本地；检测失败或未知类型一律 fail-closed。
func CheckLocalFilesystem(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrNotLocalFilesystem
	}
	if networkPathSyntax(path) {
		return fmt.Errorf("%w: network path syntax", ErrNotLocalFilesystem)
	}
	if err := checkLocalFilesystemNative(path); err != nil {
		return fmt.Errorf("%w: %v", ErrNotLocalFilesystem, err)
	}
	return nil
}

func networkPathSyntax(path string) bool {
	normalized := strings.ReplaceAll(path, `\`, "/")
	return strings.HasPrefix(normalized, "//") ||
		strings.HasPrefix(strings.ToUpper(normalized), "//?/UNC/") ||
		strings.HasPrefix(strings.ToUpper(normalized), "//./UNC/")
}
