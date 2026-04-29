package skill

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

// defaultExpandMaxBytes 是 Expand 系列 helper 的默认返回上限。超出时截断并置 Truncated=true。
const defaultExpandMaxBytes = 20000

// resolveMaxBytes 规范化 MaxBytes 入参：
//   - <=0 → defaultExpandMaxBytes
//   - 超过 maxSkillFileBytes（1MB 硬上限）→ 截为硬上限
func resolveMaxBytes(p int64) int64 {
	if p <= 0 {
		return defaultExpandMaxBytes
	}
	if p > int64(maxSkillFileBytes) {
		return int64(maxSkillFileBytes)
	}
	return p
}

// headingPattern 匹配 Markdown 标题行：^(#{1,6})\s+(.+)$。
// 只识别 ATX 风格（# Title），不识别 setext（下划线风格）——简单且覆盖主流写法。
var headingPattern = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)

// sliceMarkdownSection 从 Markdown body 中提取指定 H2/H3/... 锚点下的段落。
//
// 规则：
//   - anchor 以 heading title（不含 #）匹配，不区分大小写
//   - 从首个匹配 heading 开始，直到下一个同级或更高级 heading 之前结束
//   - heading 本身包含在返回内容里
//   - 未找到 anchor 返回 "", false
func sliceMarkdownSection(body, anchor string) (string, bool) {
	if anchor == "" {
		return body, true
	}
	lowerAnchor := strings.ToLower(strings.TrimSpace(anchor))
	if lowerAnchor == "" {
		return body, true
	}
	lines := strings.Split(body, "\n")
	startIdx, startLevel, found := findAnchorLine(lines, lowerAnchor)
	if !found {
		return "", false
	}
	end := len(lines)
	for i := startIdx + 1; i < len(lines); i++ {
		level, _, ok := parseMarkdownHeading(lines[i])
		if ok && level <= startLevel {
			end = i
			break
		}
	}
	sliceText := strings.TrimRight(strings.Join(lines[startIdx:end], "\n"), "\n")
	return sliceText, true
}

// findAnchorLine 在 lines 中查找匹配 anchor 的标题行。
func findAnchorLine(lines []string, lowerAnchor string) (idx, level int, found bool) {
	for i, line := range lines {
		lvl, title, ok := parseMarkdownHeading(line)
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(title), lowerAnchor) ||
			strings.EqualFold(normalizeAnchorSlug(title), lowerAnchor) {
			return i, lvl, true
		}
	}
	return -1, 0, false
}

// parseMarkdownHeading 识别 ATX heading 行并返回 (level, title, ok)。
func parseMarkdownHeading(line string) (int, string, bool) {
	m := headingPattern.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return 0, "", false
	}
	return len(m[1]), m[2], true
}

// normalizeAnchorSlug 将 "Usage Guide" → "usage-guide" 便于匹配常见 slug 写法。
func normalizeAnchorSlug(title string) string {
	s := strings.ToLower(strings.TrimSpace(title))
	var b strings.Builder
	b.Grow(len(s))
	var prev rune
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prev = r
		case r == '-' || r == ' ' || r == '_':
			if prev != '-' {
				b.WriteRune('-')
				prev = '-'
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// truncateBytes 按字节数截断字符串，返回 (truncated, wasTruncated)。
func truncateBytes(s string, limit int64) (string, bool) {
	if limit <= 0 || int64(len(s)) <= limit {
		return s, false
	}
	return s[:limit], true
}

// resolveResourceTarget 解析 symlink、规范化路径并验证 target 未逃逸 skill 目录。
// 保留：被 skills_fs.go 中的 ReadLocal resource 路径复用。
func resolveResourceTarget(dir, relPath string) (target, skillDir string, err error) {
	skillDir = filepath.Clean(dir)
	resolvedSkillDir, resolveErr := filepath.EvalSymlinks(skillDir)
	if resolveErr != nil {
		return "", "", fmt.Errorf("resolve skill dir symlinks: %w", resolveErr)
	}
	skillDir = resolvedSkillDir
	joined := filepath.Clean(filepath.Join(skillDir, relPath))
	target, resolveErr = filepath.EvalSymlinks(joined)
	if resolveErr != nil {
		return "", "", fmt.Errorf("resolve resource path symlinks: %s: %w", relPath, resolveErr)
	}
	if !platformshared.ContainsPath(skillDir, target) {
		return "", "", fmt.Errorf("resource path escapes skill dir: %s", relPath)
	}
	return target, skillDir, nil
}
