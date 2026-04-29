package skilllibrary

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ReadSection 读取 cacheDir/<name>/references/<NN-anchor>.md。
// anchor 是 H2 标题原文（不含数字前缀），通过遍历目录后缀匹配找到对应文件。
//
// 错误规约：
//   - cacheDir/name/anchor 任意为空 → 错误
//   - skill 目录或 references 目录不存在 → fs.ErrNotExist
//   - anchor 在该 skill 下找不到对应文件 → fs.ErrNotExist
func ReadSection(cacheDir, name, anchor string) ([]byte, error) {
	if cacheDir == "" || name == "" || anchor == "" {
		return nil, errors.New("skilllibrary: ReadSection empty cacheDir/name/anchor")
	}
	refDir := filepath.Join(cacheDir, name, "references")
	entries, err := os.ReadDir(refDir)
	if err != nil {
		return nil, err
	}

	suffix := "-" + anchor + ".md"
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), suffix) {
			return os.ReadFile(filepath.Join(refDir, e.Name()))
		}
	}
	return nil, fmt.Errorf("skilllibrary: no section %q in skill %q: %w", anchor, name, fs.ErrNotExist)
}
