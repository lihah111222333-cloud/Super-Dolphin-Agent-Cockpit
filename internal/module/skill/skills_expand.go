package skill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/anthropic-ai/super-agent-v3/pkg/skillmetrics"
)

// defaultExpandMaxBytes 是 ExpandBody/ReadResource 的默认返回上限（P20.1 §3.1）。
// 超出时截断并置 Truncated=true。
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

// findSkillRecordByName 按 name 在扫描结果中查找 skill。
//
// 当前实现每次调用都扫全盘；对 skill 数量级 < 10² 时可接受。Phase 8
// manifest cache 会提供 index，后续可替换为 O(1) 查询。
func (s *service) findSkillRecordByName(name string) (skillRecord, error) {
	normalized, err := validateSkillName(name)
	if err != nil {
		return skillRecord{}, err
	}
	records, err := s.scanSkills()
	if err != nil {
		return skillRecord{}, err
	}
	for _, rec := range records {
		if strings.EqualFold(strings.TrimSpace(rec.info.Name), normalized) {
			return rec, nil
		}
	}
	return skillRecord{}, fmt.Errorf("skill not found: %s", normalized)
}

// ExpandBody 实现 P20.1 §3.1 skill_expand_body：按 name 读 SKILL.md 正文，
// 可选按 Markdown H2/H3 锚点切片，按 MaxBytes 截断。
//
// 错误：
//   - name 不合法：ErrInvalidSkillName wrapped
//   - skill 不存在：fmt.Errorf("skill not found: ...")
//   - anchor 找不到：fmt.Errorf("anchor not found: ...")
//   - 文件过大超硬上限：fmt.Errorf("skill file too large: ...")
//
// TODO(P20.1 Phase 7 - SkillInjectionPort)：本方法目前未集成以下侧效，均由 Port 层接入后补齐：
//   1. ApprovalCache.LookupArtifact：未受信任项目级 skill 应在调用前弹审批
//      （按 artifactKind=body, locator=SKILL.md[#Anchor] 校验）
//   2. ExpandedArtifactState.MarkArtifact：记录注入历史供后续去重
//   3. SkillInfo.DisableModelInvocation：根据 frontmatter 拒绝模型自主调用
//   4. 返回值净化（P20.1 §3.3）：对 untrusted + unapproved skill，
//      - Summary 字段可能含攻击载荷，Port 层应替换为占位符或置空
//      - Path / Content 如果包含恶意指令同样需过滤
// 未集成时调用方（带格 Phase 7 Port）需自行保证该等策略。
func (s *service) ExpandBody(_ context.Context, p ExpandBodyParams) (ExpandBodyResult, error) {
	// P20.1 Phase 10 Step C: 计入每次调用（含失败路径），类似 rate counter。
	skillmetrics.IncSkillExpandInvoke()
	rec, err := s.findSkillRecordByName(p.Name)
	if err != nil {
		return ExpandBodyResult{}, err
	}
	maxBytes := resolveMaxBytes(p.MaxBytes)

	stat, err := os.Stat(rec.path)
	if err != nil {
		return ExpandBodyResult{}, err
	}
	if stat.Size() > maxSkillFileBytes {
		return ExpandBodyResult{}, fmt.Errorf("skill file too large: %s is %d bytes, limit %d", rec.path, stat.Size(), maxSkillFileBytes)
	}
	data, err := os.ReadFile(rec.path)
	if err != nil {
		return ExpandBodyResult{}, err
	}
	full := string(data)
	// Body 不包括 frontmatter（保持与 summarize 语义一致）。
	// splitFrontmatter 返回 (fm, tail, ok)：
	//   - ok=true  + tail=="" → SKILL.md 仅有 frontmatter，body 本来就是空
	//   - ok=false → 无 frontmatter，整个文件作为 body
	// 不能用 `if body == "" { body = full }` fallback，那样会把 frontmatter 泄漏为 body。
	fm, body, hasFM := splitFrontmatter(full)
	_ = fm
	if !hasFM {
		body = full
	}

	anchor := strings.TrimSpace(p.Anchor)
	var slice string
	if anchor != "" {
		sliceText, ok := sliceMarkdownSection(body, anchor)
		if !ok {
			return ExpandBodyResult{}, fmt.Errorf("anchor not found: %q", anchor)
		}
		slice = sliceText
	} else {
		slice = body
	}

	total := int64(len(slice))
	content, truncated := truncateBytes(slice, maxBytes)

	sum := sha256.Sum256(data)
	version := hex.EncodeToString(sum[:])
	if len(version) > 12 {
		version = version[:12]
	}

	return ExpandBodyResult{
		Name:       rec.info.Name,
		Path:       rec.path,
		Version:    version,
		Anchor:     anchor,
		Summary:    rec.info.Summary,
		Content:    content,
		Truncated:  truncated,
		TotalBytes: total,
	}, nil
}

// ReadResource 实现 P20.1 §3.1 skill_read_resource：按 name + 相对路径读取
// skill 目录下的资源文件。
//
// 安全：
//   - NormalizeArtifactLocator 拒绝 `/abs`、`..` 段、空路径
//   - 归一化后再与 skill dir join，os.Stat + platformshared.ContainsPath 二次验证
//   - 按 maxBytes 截断
//
// 内容类型：Content 以 Go string 返回，**仅保证 UTF-8 文本文件正确性**
// （references/*.md、scripts/*.sh 等）。二进制资源（assets/*.png 等）会
// 被 JSON 序列化器按 UTF-8 校验转义为 \ufffd，不在本工具的爆护方案内。
// 未来如需支持二进制可扩展为 base64 encoding 或新工具 skill_read_asset。
//
// TODO(P20.1 Phase 7 - SkillInjectionPort)：与 ExpandBody 同，尚未集成：
//   1. ApprovalCache.LookupArtifact（artifactKind=resource, locator=相对路径）
//   2. ExpandedArtifactState.MarkArtifact
//   3. 返回值净化：SkillDir / Path / Content 对 untrusted skill 需按 trust 策略过滤
// 由 Port 层接入时补齐。
func (s *service) ReadResource(_ context.Context, p ReadResourceParams) (ReadResourceResult, error) {
	// P20.1 Phase 10 Step C: 与 ExpandBody 合计 SkillExpandInvokeRate。
	skillmetrics.IncSkillExpandInvoke()
	rec, err := s.findSkillRecordByName(p.Name)
	if err != nil {
		return ReadResourceResult{}, err
	}
	relPath, err := NormalizeArtifactLocator(ArtifactKindResource, p.Path)
	if err != nil {
		return ReadResourceResult{}, err
	}
	maxBytes := resolveMaxBytes(p.MaxBytes)

	target, skillDir, err := resolveResourceTarget(rec.info.Dir, relPath)
	if err != nil {
		return ReadResourceResult{}, err
	}

	stat, err := os.Stat(target)
	if err != nil {
		return ReadResourceResult{}, err
	}
	if stat.IsDir() {
		return ReadResourceResult{}, fmt.Errorf("path is directory: %s", relPath)
	}
	if stat.Size() > maxSkillFileBytes {
		return ReadResourceResult{}, fmt.Errorf("resource file too large: %s is %d bytes, limit %d", relPath, stat.Size(), maxSkillFileBytes)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return ReadResourceResult{}, err
	}
	total := int64(len(data))
	content, truncated := truncateBytes(string(data), maxBytes)

	resourceSum := sha256.Sum256(data)
	version := hex.EncodeToString(resourceSum[:])
	if len(version) > 12 {
		version = version[:12]
	}

	return ReadResourceResult{
		Name:       rec.info.Name,
		SkillDir:   skillDir,
		Path:       relPath,
		Version:    version,
		Content:    content,
		Truncated:  truncated,
		TotalBytes: total,
	}, nil
}

// resolveResourceTarget 解析 symlink、规范化路径并验证 target 未逃逸 skill 目录。
func resolveResourceTarget(dir, relPath string) (target, skillDir string, err error) {
	skillDir = filepath.Clean(dir)
	// EvalSymlinks 规范化 skillDir（macOS /tmp → /private/tmp 之类 symlink 场景）。
	if resolved, resolveErr := filepath.EvalSymlinks(skillDir); resolveErr == nil {
		skillDir = resolved
	}
	target = filepath.Clean(filepath.Join(skillDir, relPath))
	if resolved, resolveErr := filepath.EvalSymlinks(target); resolveErr == nil {
		target = resolved
	}
	if !platformshared.ContainsPath(skillDir, target) {
		return "", "", fmt.Errorf("resource path escapes skill dir: %s", relPath)
	}
	return target, skillDir, nil
}

// sliceMarkdownSection 从 Markdown body 中提取指定 H2/H3/... 锚点下的段落。
//
// 规则：
//   - anchor 以 heading title（不含 #）匹配，不区分大小写
//   - 从首个匹配 heading 开始，直到下一个同级或更高级 heading 之前结束
//   - heading 本身包含在返回内容里
//   - 未找到 anchor 返回 "", false
//
// 例：
//
//	body = "## Usage\ncontent\n### Sub\ndetail\n## Other\nfoo"
//	anchor = "Usage" → "## Usage\ncontent\n### Sub\ndetail"
//
// 简化实现：ATX 风格，行首 `#{1,6}\s+`。
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

// findAnchorLine 在 lines 中查找匹配 anchor 的标题行，返回索引、级别和是否找到。
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
// 同时去除尾部 # 锚点链接（GitHub 风格 `<a name="...">`）。
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
// 注意按字节而非 rune——多字节 UTF-8 字符可能在边界被截半，但对大多数场景
// （英文为主的 SKILL.md + 源码）这是可接受的；调用方若需要严格 rune 对齐
// 可在消费时自行修剪。
func truncateBytes(s string, limit int64) (string, bool) {
	if limit <= 0 || int64(len(s)) <= limit {
		return s, false
	}
	return s[:limit], true
}

