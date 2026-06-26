// Package pathutil 提供路径包含判断、规范化和项目 key 生成工具。
package pathutil

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"golang.org/x/text/unicode/norm"
)

const (
	projectKeyMaxLen         = 96
	projectKeyResolveTimeout = 4 * time.Second
)

// ContainsPath 判断 target 是否位于 root 内部或与 root 相同。
// 比较前会解析绝对路径和可用 symlink，路径非法时返回 false。
func ContainsPath(root, target string) bool {
	rootPath, err := NormalizeAbsolutePath(root)
	if err != nil || rootPath == "" {
		return false
	}
	targetPath, err := NormalizeAbsolutePath(target)
	if err != nil || targetPath == "" {
		return false
	}
	if runtime.GOOS == "windows" {
		rootPath = strings.ToLower(rootPath)
		targetPath = strings.ToLower(targetPath)
	}
	rel, err := filepath.Rel(rootPath, targetPath)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// NormalizeAbsolutePath 返回适合路径边界比较的绝对规范路径。
// Windows 下会接受 /C:/repo 与 \C:\repo 这类 drive alias，并尽量解析已存在 symlink。
func NormalizeAbsolutePath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", nil
	}
	cleaned := filepath.Clean(normalizeWindowsDriveAlias(trimmed))
	if !filepath.IsAbs(cleaned) {
		absPath, err := filepath.Abs(cleaned)
		if err != nil {
			return "", fmt.Errorf("resolve absolute path: %w", err)
		}
		cleaned = filepath.Clean(normalizeWindowsDriveAlias(absPath))
	}
	if runtime.GOOS == "windows" {
		return normalizeWindowsSymlinkPath(cleaned), nil
	}
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		return filepath.Clean(normalizeWindowsDriveAlias(resolved)), nil
	}
	if resolved, ok := normalizeWithExistingAncestor(cleaned); ok {
		return resolved, nil
	}
	return cleaned, nil
}

// normalizeWithExistingAncestor 在目标路径不存在时解析最近已存在祖先，再拼回剩余后缀。
func normalizeWithExistingAncestor(cleaned string) (string, bool) {
	current := cleaned
	suffix := make([]string, 0)
	for {
		parent := filepath.Dir(current)
		if current == "" || parent == current {
			return "", false
		}
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			parts := append([]string{filepath.Clean(normalizeWindowsDriveAlias(resolved))}, suffix...)
			return filepath.Join(parts...), true
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", false
		}
		suffix = append([]string{filepath.Base(current)}, suffix...)
		current = parent
	}
}

// normalizeWindowsSymlinkPath 解析 Windows 路径中已存在部分的 symlink，并保留不存在后缀。
func normalizeWindowsSymlinkPath(cleaned string) string {
	existing, suffix, hasSymlink := windowsExistingPathWithSymlink(cleaned)
	if !hasSymlink {
		return cleaned
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return cleaned
	}
	resolved = filepath.Clean(normalizeWindowsDriveAlias(resolved))
	if suffix == "" {
		return resolved
	}
	return filepath.Clean(filepath.Join(resolved, suffix))
}

// windowsExistingPathWithSymlink 返回 Windows 路径中已存在的前缀、剩余后缀和 symlink 标记。
func windowsExistingPathWithSymlink(cleaned string) (existing, suffix string, hasSymlink bool) {
	volume := filepath.VolumeName(cleaned)
	rest := strings.TrimPrefix(cleaned, volume)
	parts := strings.FieldsFunc(rest, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	if volume == "" {
		existing = string(filepath.Separator)
	} else {
		existing = volume + string(filepath.Separator)
	}
	for idx, part := range parts {
		next := filepath.Join(existing, part)
		info, err := os.Lstat(next)
		if err != nil {
			return existing, filepath.Join(parts[idx:]...), hasSymlink
		}
		existing = next
		if info.Mode()&os.ModeSymlink != 0 {
			hasSymlink = true
		}
	}
	return existing, "", hasSymlink
}

// normalizeWindowsDriveAlias 去掉 Windows drive alias 前缀，统一进入 filepath 解析。
func normalizeWindowsDriveAlias(path string) string {
	if runtime.GOOS != "windows" {
		return path
	}
	cleaned := strings.TrimSpace(path)
	for {
		prefixLen, ok := windowsDriveAliasPrefixLen(cleaned)
		if !ok {
			break
		}
		cleaned = cleaned[prefixLen:]
	}
	return cleaned
}

// windowsDriveAliasPrefixLen 识别 /C:/repo 或 //C:/repo 这类 drive alias 的前缀长度。
func windowsDriveAliasPrefixLen(path string) (int, bool) {
	if len(path) < 3 || !isSlash(path[0]) {
		return 0, false
	}
	if isWindowsDriveLetter(path[1]) && path[2] == ':' {
		return 1, true
	}
	if len(path) >= 4 && isSlash(path[1]) && isWindowsDriveLetter(path[2]) && path[3] == ':' {
		return 2, true
	}
	return 0, false
}

// isSlash 判断字节是否是任一平台路径分隔符。
func isSlash(b byte) bool {
	return b == '/' || b == '\\'
}

// isWindowsDriveLetter 判断字节是否可作为 Windows drive 字母。
func isWindowsDriveLetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

// SanitizeMemoryProjectKey 生成 memory/mcp-orch 兼容的项目 key。
// 它使用全路径小写 dash slug、折叠分隔符、长度裁剪和 8 字符 hash 后缀。
func SanitizeMemoryProjectKey(raw string) string {
	normalized := filepath.ToSlash(norm.NFC.String(strings.TrimSpace(raw)))
	var builder strings.Builder
	lastDash := false
	for _, r := range normalized {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(unicode.ToLower(r))
			lastDash = false
		case lastDash:
		default:
			builder.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(builder.String(), "-")
	if slug == "" {
		return "project-" + shortProjectKeyHash(normalized)
	}
	if len(slug) <= projectKeyMaxLen {
		return slug
	}
	prefix := strings.Trim(slug[:projectKeyMaxLen-9], "-")
	if prefix == "" {
		prefix = "project"
	}
	return prefix + "-" + shortProjectKeyHash(normalized)
}

// SanitizeSkillProjectKey 生成技能目录使用的项目 key。
// 它保留最后两个路径段和大小写，再用下划线连接，以匹配磁盘目录语义。
func SanitizeSkillProjectKey(raw string) string {
	normalized := filepath.ToSlash(norm.NFC.String(strings.TrimSpace(raw)))
	normalized = strings.Trim(normalized, "/")
	if normalized == "" {
		return "project-" + shortProjectKeyHash(raw)
	}
	parts := strings.FieldsFunc(normalized, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	candidateParts := make([]string, 0, len(parts))
	for _, part := range parts {
		if cleaned := sanitizeSkillProjectKeySegment(part); cleaned != "" {
			candidateParts = append(candidateParts, cleaned)
		}
	}
	candidate := strings.Join(candidateParts, "_")
	if candidate == "" {
		return "project-" + shortProjectKeyHash(normalized)
	}
	if len(candidate) <= projectKeyMaxLen {
		return candidate
	}
	prefix := strings.Trim(candidate[:projectKeyMaxLen-9], "_.-")
	if prefix == "" {
		prefix = "project"
	}
	return prefix + "-" + shortProjectKeyHash(candidate)
}

// ProjectKeyFromCwd 将 cwd 解析为技能作用域项目 key。
func ProjectKeyFromCwd(cwd string) (string, error) {
	return projectKeyFromCwd(cwd, SanitizeSkillProjectKey)
}

// MemoryProjectKeyFromCwd 将 cwd 解析为 memory/mcp-orch 兼容项目 key。
func MemoryProjectKeyFromCwd(cwd string) (string, error) {
	return projectKeyFromCwd(cwd, SanitizeMemoryProjectKey)
}

// projectKeyFromCwd 先解析项目根目录，再用传入的 sanitize 策略生成 key。
func projectKeyFromCwd(cwd string, sanitize func(string) string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", errors.New("project key cwd is required")
	}
	canonical, err := canonicalProjectRoot(context.Background(), cwd)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(canonical) == "" {
		return "", fmt.Errorf("resolve git root for %q returned empty path", cwd)
	}
	return sanitize(canonical), nil
}

// sanitizeSkillProjectKeySegment 清理单个技能项目 key 路径段，保留字母数字、短横线和点。
func sanitizeSkillProjectKeySegment(raw string) string {
	raw = norm.NFC.String(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	var builder strings.Builder
	lastUnderscore := false
	for _, r := range raw {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(r)
			lastUnderscore = false
		case r == '-' || r == '.':
			builder.WriteRune(r)
			lastUnderscore = false
		case lastUnderscore:
		default:
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(builder.String(), "_.-")
}

// shortProjectKeyHash 返回 8 字符 hash 后缀，用于裁剪或空 slug 的稳定区分。
func shortProjectKeyHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:4])
}

// canonicalProjectRoot 使用 git 输出解析项目根目录，并兼容 worktree 的 common dir。
func canonicalProjectRoot(ctx context.Context, cwd string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	fallback, err := cleanAbsoluteProjectPath(cwd)
	if err != nil {
		return "", err
	}
	gitCtx, cancel := platformconfig.WithTimeout(ctx, projectKeyResolveTimeout)
	defer cancel()

	cmd := exec.CommandContext(gitCtx, "git", "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir")
	cmd.Dir = fallback
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("resolve git root for %q: %w", fallback, err)
	}
	lines := strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return "", fmt.Errorf("resolve git root for %q returned empty output", fallback)
	}
	gitRoot := filepath.Clean(norm.NFC.String(strings.TrimSpace(lines[0])))
	if gitRoot == "" {
		return "", fmt.Errorf("resolve git root for %q returned empty root", fallback)
	}
	if len(lines) < 2 {
		return gitRoot, nil
	}
	commonDir := strings.TrimSpace(lines[1])
	if filepath.Base(commonDir) != ".git" {
		return gitRoot, nil
	}
	parent := filepath.Dir(filepath.Clean(commonDir))
	if parent == "" {
		return gitRoot, nil
	}
	return parent, nil
}

// cleanAbsoluteProjectPath 校验 cwd 非空并转为绝对路径，作为 git 命令工作目录。
func cleanAbsoluteProjectPath(raw string) (string, error) {
	cleaned := filepath.Clean(norm.NFC.String(strings.TrimSpace(raw)))
	if cleaned == "" || cleaned == "." {
		return "", exec.ErrNotFound
	}
	if !filepath.IsAbs(cleaned) {
		absolute, err := filepath.Abs(cleaned)
		if err != nil {
			return "", err
		}
		cleaned = filepath.Clean(absolute)
	}
	return cleaned, nil
}
