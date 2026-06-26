package retrieval

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/util/pathutil"
	"golang.org/x/text/unicode/norm"
)

const memoryIndexFileName = "MEMORY.md"

var (
	errInvalidMemoryReadPath = errors.New("invalid memory read path")
)

// normalizeStoreRoot 校验并规范化检索使用的记忆根目录。
// retrieval 只读扫描也必须复用 shared 根目录校验，避免读到未授权位置。
func normalizeStoreRoot(root string) (string, error) {
	return shared.ValidateMemoryRoot(root)
}

// validateMemoryReadPath 校验检索读取路径在记忆根目录内且是文件。
// 符号链接会解析到真实路径后再做包含关系判断，防止 manifest 扫描越界。
func validateMemoryReadPath(root, file string) (string, error) {
	validatedRoot, err := shared.ValidateMemoryRoot(root)
	if err != nil {
		return "", err
	}
	if validatedRoot == "" {
		return "", invalidMemoryReadPath("empty root")
	}
	rootDir, candidate, err := prepareMemoryPath(validatedRoot, file)
	if err != nil {
		return "", err
	}
	rootReal, err := resolveExistingMemoryPath(rootDir)
	if err != nil {
		return "", invalidMemoryReadPath(err.Error())
	}
	candidateReal, err := resolveExistingMemoryPath(candidate)
	if err != nil {
		return "", invalidMemoryReadPath(err.Error())
	}
	if !pathutil.ContainsPath(rootReal, candidateReal) {
		return "", invalidMemoryReadPath("path escapes root")
	}
	if info, err := os.Stat(candidateReal); err != nil {
		return "", invalidMemoryReadPath(err.Error())
	} else if info.IsDir() {
		return "", invalidMemoryReadPath("path is a directory")
	}
	return candidateReal, nil
}

// prepareMemoryPath 标准化检索读取的根目录和文件路径。
// 空路径、NUL 字节和不可解析路径都会被拒绝，后续再做真实路径包含校验。
func prepareMemoryPath(validatedRoot, file string) (string, string, error) {
	file = norm.NFC.String(strings.TrimSpace(file))
	if file == "" {
		return "", "", invalidMemoryReadPath("empty file path")
	}
	if strings.ContainsRune(file, '\x00') {
		return "", "", invalidMemoryReadPath("null byte")
	}
	rootDir := strings.TrimSuffix(validatedRoot, string(os.PathSeparator))
	candidate := file
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(rootDir, candidate)
	}
	candidate, err := shared.CleanAbsolutePath(candidate)
	if err != nil {
		return "", "", invalidMemoryReadPath(err.Error())
	}
	if err := shared.EnsureResolvablePath(rootDir); err != nil {
		return "", "", invalidMemoryReadPath(err.Error())
	}
	if err := shared.EnsureResolvablePath(candidate); err != nil {
		return "", "", invalidMemoryReadPath(err.Error())
	}
	return rootDir, candidate, nil
}

// resolveExistingMemoryPath 解析必须存在的真实路径。
// retrieval 只读取已存在文件，不允许不存在路径继续进入 manifest。
func resolveExistingMemoryPath(path string) (string, error) {
	resolved, err := shared.RealPathDeepestExisting(path)
	if err != nil {
		return "", err
	}
	if resolved == "" {
		return "", os.ErrNotExist
	}
	return resolved, nil
}

// invalidMemoryReadPath 包装检索读取路径错误。
// 统一错误类型便于上层区分路径拒绝和其它 I/O 失败。
func invalidMemoryReadPath(reason string) error {
	return fmt.Errorf("%w: %s", errInvalidMemoryReadPath, reason)
}
