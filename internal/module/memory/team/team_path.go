package team

import (
	"fmt"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
	"unicode"

	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/util/pathutil"
	"golang.org/x/text/unicode/norm"
)

const maxTeamPathDecodePasses = 4

// teamPathError 把底层路径校验失败包装成 key 或写路径对应的公开错误，避免 UI/RPC 混淆读写边界。
type teamPathError func(string) error

// sanitizePathKey 对路径键进行字符串级规范化（Unicode NFKC、URL 解码、斜杠统一），
// 不做符号链接或根目录包含检查——那些校验由 validateTeamMemKey/validateTeamMemWritePath 完成。
func sanitizePathKey(raw string) (string, error) {
	return sanitizePathKeyWithWrap(raw, invalidTeamMemKey)
}

// validateTeamMemKey 规范化键并验证其在 root 下安全可寻址（无路径逃逸、无符号链接逃逸）。
func validateTeamMemKey(root, key string) (string, error) {
	sanitized, err := sanitizePathKey(key)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(root, filepath.FromSlash(sanitized))
	if _, err := validateTeamMemCandidate(root, candidate, invalidTeamMemKey); err != nil {
		return "", err
	}
	return sanitized, nil
}

// validateTeamMemWritePath 验证写操作目标文件路径在 root 下安全可寻址。
func validateTeamMemWritePath(root, file string) (string, error) {
	return validateTeamMemCandidate(root, file, invalidTeamMemWritePath)
}

// validateTeamMemWritePathBasic 在无 root 信息时对写路径做基础安全校验（无路径遍历、非绝对路径）。
func validateTeamMemWritePathBasic(file string) error {
	normalized, absolute, err := normalizeTeamWriteInput(file, invalidTeamMemWritePath)
	if err != nil {
		return err
	}
	if absolute {
		return nil
	}
	_, err = sanitizePathKeyWithWrap(normalized, invalidTeamMemWritePath)
	return err
}

// validateTeamMemCandidate 校验 team memory 写入候选路径并返回安全相对路径。
func validateTeamMemCandidate(root, file string, wrap teamPathError) (string, error) {
	rootDir, err := validateTeamMemRoot(root, wrap)
	if err != nil {
		return "", err
	}
	candidate, err := prepareTeamMemCandidate(rootDir, file, wrap)
	if err != nil {
		return "", err
	}
	if candidate == rootDir {
		return "", wrap("path must target a file under team root")
	}
	if !pathutil.ContainsPath(rootDir, candidate) {
		return "", wrap("path escapes team root")
	}
	rootReal, err := resolveTeamMemRealPath(rootDir, wrap)
	if err != nil {
		return "", err
	}
	candidateReal, err := resolveTeamMemRealPath(candidate, wrap)
	if err != nil {
		return "", err
	}
	if candidateReal == rootReal {
		return "", wrap("path must target a file under team root")
	}
	if !pathutil.ContainsPath(rootReal, candidateReal) {
		return "", wrap("path escapes team root via symlink")
	}
	return candidate, nil
}

// validateTeamMemRoot 规范化并返回 root 的绝对路径（去除末尾分隔符）。
func validateTeamMemRoot(root string, wrap teamPathError) (string, error) {
	cleaned, err := shared.CleanAbsolutePath(root)
	if err != nil {
		return "", wrap(err.Error())
	}
	if strings.TrimSpace(cleaned) == "" {
		return "", wrap("empty team root")
	}
	return strings.TrimSuffix(cleaned, string(os.PathSeparator)), nil
}

// prepareTeamMemCandidate 准备teammem候选项。
func prepareTeamMemCandidate(root, file string, wrap teamPathError) (string, error) {
	normalized, absolute, err := normalizeTeamWriteInput(file, wrap)
	if err != nil {
		return "", err
	}
	candidate := normalized
	if !absolute {
		key, err := sanitizePathKeyWithWrap(normalized, wrap)
		if err != nil {
			return "", err
		}
		candidate = filepath.Join(root, filepath.FromSlash(key))
	}
	candidate, err = shared.CleanAbsolutePath(candidate)
	if err != nil {
		return "", wrap(err.Error())
	}
	if err := shared.EnsureResolvablePath(root); err != nil {
		return "", wrap(err.Error())
	}
	if err := shared.EnsureResolvablePath(candidate); err != nil {
		return "", wrap(err.Error())
	}
	return candidate, nil
}

// normalizeTeamWriteInput 对写路径进行 Unicode 规范化、URL 解码和路径遍历检查，返回规范化结果和是否为绝对路径。
func normalizeTeamWriteInput(raw string, wrap teamPathError) (string, bool, error) {
	normalized, err := normalizeTeamPathInput(raw, wrap)
	if err != nil {
		return "", false, err
	}
	if containsTeamDotSegments(normalized) {
		return "", false, wrap("path traversal is not allowed")
	}
	if hasWindowsDrivePrefix(normalized) && os.PathSeparator != '\\' {
		return "", false, wrap("windows drive path is not allowed")
	}
	if !isTeamAbsolutePath(normalized) {
		return normalized, false, nil
	}
	cleaned, err := shared.CleanAbsolutePath(filepath.FromSlash(normalized))
	if err != nil {
		return "", false, wrap(err.Error())
	}
	return cleaned, true, nil
}

// sanitizePathKeyWithWrap 规范化路径键并拒绝绝对路径、路径遍历和空键，使用 wrap 包装错误。
func sanitizePathKeyWithWrap(raw string, wrap teamPathError) (string, error) {
	normalized, err := normalizeTeamPathInput(raw, wrap)
	if err != nil {
		return "", err
	}
	if isTeamAbsolutePath(normalized) {
		return "", wrap("absolute path is not allowed")
	}
	if containsTeamDotSegments(normalized) {
		return "", wrap("path traversal is not allowed")
	}
	cleaned := pathpkg.Clean(normalized)
	if cleaned == "." {
		return "", wrap("empty path key")
	}
	if hasTeamTraversal(cleaned) {
		return "", wrap("path traversal is not allowed")
	}
	return cleaned, nil
}

// normalizeTeamPathInput 对路径输入进行 Unicode NFKC 规范化、URL 解码和基础安全检查。
func normalizeTeamPathInput(raw string, wrap teamPathError) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", wrap("empty path")
	}
	if strings.ContainsRune(trimmed, '\x00') {
		return "", wrap("null byte")
	}
	decoded, err := decodeTeamPathEscapes(norm.NFKC.String(trimmed))
	if err != nil {
		return "", wrap(err.Error())
	}
	normalized := norm.NFKC.String(strings.TrimSpace(decoded))
	if strings.ContainsRune(normalized, '\x00') {
		return "", wrap("null byte")
	}
	normalized = strings.ReplaceAll(normalized, "\\", "/")
	if strings.TrimSpace(normalized) == "" {
		return "", wrap("empty path")
	}
	return normalized, nil
}

// decodeTeamPathEscapes 对路径进行最多 maxTeamPathDecodePasses 轮 URL 解码，防止双重编码绕过校验。
func decodeTeamPathEscapes(raw string) (string, error) {
	decoded := raw
	for i := 0; i < maxTeamPathDecodePasses; i++ {
		next, err := url.PathUnescape(decoded)
		if err != nil {
			return "", fmt.Errorf("invalid path escape: %v", err)
		}
		if next == decoded {
			return decoded, nil
		}
		decoded = next
	}
	return decoded, nil
}

// resolveTeamMemRealPath 解析路径的最深已存在祖先的真实路径，用于符号链接逃逸检查。
func resolveTeamMemRealPath(path string, wrap teamPathError) (string, error) {
	resolved, err := shared.RealPathDeepestExisting(path)
	if err != nil {
		return "", wrap(err.Error())
	}
	if strings.TrimSpace(resolved) == "" {
		return "", wrap("path could not be resolved")
	}
	return resolved, nil
}

// hasTeamTraversal 判断已清理的路径是否仍包含 .. 遍历段。
func hasTeamTraversal(cleaned string) bool {
	return cleaned == ".." || strings.HasPrefix(cleaned, "../")
}

// containsTeamDotSegments 检查路径的每个分段是否包含 . 或 .. 。
func containsTeamDotSegments(path string) bool {
	for _, segment := range strings.Split(path, "/") {
		switch segment {
		case ".", "..":
			return true
		}
	}
	return false
}

// isTeamAbsolutePath 判断路径是否为绝对路径（Unix 或 Windows 格式）。
func isTeamAbsolutePath(path string) bool {
	return strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || hasWindowsDrivePrefix(path)
}

// hasWindowsDrivePrefix 判断路径是否以 Windows 盘符前缀（如 C:/）开头。
func hasWindowsDrivePrefix(path string) bool {
	if len(path) < 3 || path[1] != ':' || path[2] != '/' {
		return false
	}
	return unicode.IsLetter(rune(path[0]))
}

// invalidTeamMemWritePath 将原因包装为 ErrInvalidTeamMemWritePath 错误。
func invalidTeamMemWritePath(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidTeamMemWritePath, reason)
}

// invalidTeamMemKey 将原因包装为 ErrInvalidTeamMemKey 错误。
func invalidTeamMemKey(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidTeamMemKey, reason)
}
