// Package skillhash 提供技能文件内容的哈希计算工具，用于检测技能文件是否发生变化。
package skillhash

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Content 计算字符串内容的 SHA-256 哈希，返回十六进制字符串。
func Content(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// Dir 递归计算目录下所有非 symlink 普通文件的内容哈希，排序后再哈希合并，保证结果与文件顺序无关。
func Dir(root string) (string, error) {
	var parts []string
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		parts = append(parts, filepath.ToSlash(rel)+"\x00"+Content(string(data)))
		return nil
	}); err != nil {
		return "", fmt.Errorf("skill dir content hash %q: %w", root, err)
	}
	sort.Strings(parts)
	return Content(strings.Join(parts, "\x00")), nil
}

// ExistingDir 与 Dir 相同，但目录不存在时返回空字符串而非错误。
func ExistingDir(root string) (string, error) {
	hash, err := Dir(root)
	switch {
	case err == nil:
		return hash, nil
	case errors.Is(err, os.ErrNotExist):
		return "", nil
	default:
		return "", err
	}
}
