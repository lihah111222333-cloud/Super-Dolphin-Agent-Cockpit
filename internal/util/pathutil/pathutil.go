// Package pathutil provides path containment and sanitization helpers.
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

// ContainsPath reports whether target is inside root (or equal to it).
// ContainsPath 判断路径是否可用。
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

// NormalizeAbsolutePath returns a cleaned absolute path suitable for path-scope
// comparisons. On Windows it accepts file-URI drive aliases such as /C:/repo
// and \C:\repo, then resolves existing symlinks where possible.
// NormalizeAbsolutePath 规范化absolute路径。
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

// normalizeWithExistingAncestor 规范化带existingancestor的工具。
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

// windowsExistingPathWithSymlink 处理带symlink的Windowsexisting路径。
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

// windowsDriveAliasPrefixLen 处理Windowsdrivealiasprefixlen。
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

func isSlash(b byte) bool {
	return b == '/' || b == '\\'
}

func isWindowsDriveLetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

// SanitizeMemoryProjectKey is byte-for-byte compatible with the legacy
// memory/mcp-orch sanitizePath implementation: full-path dash slug, lowercase,
// collapsed separators, max-len trim, and 8-char hash suffix.
// SanitizeMemoryProjectKey 清理记忆项目键。
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

// SanitizeSkillProjectKey preserves the on-disk skill directory semantics:
// keep the last two path segments, preserve case, and join them with "_".
// SanitizeSkillProjectKey 清理技能项目键。
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

// ProjectKeyFromCwd resolves cwd to the skill-scoped project key.
// ProjectKeyFromCwd 从工作目录处理项目键。
func ProjectKeyFromCwd(cwd string) (string, error) {
	return projectKeyFromCwd(cwd, SanitizeSkillProjectKey)
}

// MemoryProjectKeyFromCwd resolves cwd to the legacy memory/mcp-orch project key.
// MemoryProjectKeyFromCwd 从工作目录处理记忆项目键。
func MemoryProjectKeyFromCwd(cwd string) (string, error) {
	return projectKeyFromCwd(cwd, SanitizeMemoryProjectKey)
}

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

// sanitizeSkillProjectKeySegment 清理技能项目键segment。
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

func shortProjectKeyHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:4])
}

// canonicalProjectRoot 处理canonical项目根目录。
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
