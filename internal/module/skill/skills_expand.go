package skill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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
func (s *service) findSkillRecordByName(name, cwd string) (skillRecord, error) {
	rec, err := s.resolveSkillRecordByName(name, cwd)
	if err == nil {
		return rec, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return skillRecord{}, fmt.Errorf("skill not found: %s", strings.TrimSpace(name))
	}
	return skillRecord{}, err
}

func bodyArtifactLocator(anchor string) string {
	anchor = strings.TrimSpace(anchor)
	if anchor == "" {
		return "SKILL.md"
	}
	return "SKILL.md#" + anchor
}

type artifactApprovalCallMetadata struct {
	AgentID  string
	ThreadID string
	TurnID   string
	CallID   string
}

func expandBodyApprovalMetadata(p ExpandBodyParams) artifactApprovalCallMetadata {
	return artifactApprovalCallMetadata{
		AgentID:  strings.TrimSpace(p.AgentID),
		ThreadID: strings.TrimSpace(p.ThreadID),
		TurnID:   strings.TrimSpace(p.TurnID),
		CallID:   strings.TrimSpace(p.CallID),
	}
}

func readResourceApprovalMetadata(p ReadResourceParams) artifactApprovalCallMetadata {
	return artifactApprovalCallMetadata{
		AgentID:  strings.TrimSpace(p.AgentID),
		ThreadID: strings.TrimSpace(p.ThreadID),
		TurnID:   strings.TrimSpace(p.TurnID),
		CallID:   strings.TrimSpace(p.CallID),
	}
}

func (s *service) requireArtifactApproval(ctx context.Context, info SkillInfo, kind, locator, contentHash, cwd, method string, meta artifactApprovalCallMetadata) error {
	if info.Trust.Trusted() {
		return nil
	}
	approved, _ := s.LookupArtifactApproval(ctx, contractArtifactApprovalRequest(info, kind, locator, contentHash, cwd))
	if approved {
		return nil
	}
	req := s.buildArtifactApprovalRequest(info, kind, locator, contentHash, cwd, method, meta)
	if s.approvalRequester == nil {
		return SkillApprovalRequiredError{Request: req}
	}
	decision, err := s.approvalRequester.RequestApproval(ctx, req)
	if err != nil {
		return err
	}
	if decision.Approved == nil || !*decision.Approved {
		return deniedSkillApproval(decision)
	}
	if s.approval == nil {
		return errSkillApprovalProjectCacheMissing
	}
	_, err = s.approval.ApproveArtifact(ApprovalRequest{
		RepoFingerprint: RepoFingerprint(cwd),
		Name:            info.Name,
		ArtifactKind:    kind,
		ArtifactLocator: locator,
		ContentHash:     contentHash,
		Trust:           info.Trust,
		ApprovedBy:      approvalApprovedBy(decision),
	})
	return err
}

func contractArtifactApprovalRequest(info SkillInfo, kind, locator, contentHash, cwd string) contract.ArtifactApprovalRequest {
	return contract.ArtifactApprovalRequest{
		RepoFingerprint: RepoFingerprint(cwd),
		Name:            info.Name,
		ArtifactKind:    kind,
		ArtifactLocator: locator,
		ContentHash:     contentHash,
	}
}

func (s *service) buildArtifactApprovalRequest(info SkillInfo, kind, locator, contentHash, cwd, method string, meta artifactApprovalCallMetadata) contract.ApprovalRequest {
	callID := strings.TrimSpace(meta.CallID)
	if callID == "" {
		callID = s.nextApprovalCallID(info.Name)
	}
	payload := map[string]any{
		"name":             info.Name,
		"artifact_kind":    kind,
		"artifact_locator": locator,
		"content_hash":     contentHash,
		"repo_fingerprint": RepoFingerprint(cwd),
		"trust":            info.Trust,
		"skills_dir":       info.Dir,
		"project_root":     strings.TrimSpace(cwd),
		"toolName":         method,
		"sourceMethod":     method,
		"callId":           callID,
	}
	if value := strings.TrimSpace(meta.AgentID); value != "" {
		payload["agentId"] = value
	}
	if value := strings.TrimSpace(meta.ThreadID); value != "" {
		payload["threadId"] = value
	}
	if value := strings.TrimSpace(meta.TurnID); value != "" {
		payload["turnId"] = value
	}
	return contract.ApprovalRequest{
		CallID:       callID,
		ToolName:     method,
		AgentID:      strings.TrimSpace(meta.AgentID),
		ThreadID:     strings.TrimSpace(meta.ThreadID),
		TurnID:       strings.TrimSpace(meta.TurnID),
		Reason:       "skill artifact requires approval",
		Kind:         "skill_artifact",
		SourceMethod: method,
		Payload:      payload,
	}
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
// 本地项目级/用户级 skill 读取正文不需要 approval；仅确定不可信/异常来源
// 在读取正文前必须命中 artifact-level approval。trusted signed skill 仍按
// P20.1 默认信任策略直接放行。
func (s *service) ExpandBody(ctx context.Context, p ExpandBodyParams) (ExpandBodyResult, error) {
	// P20.1 Phase 10 Step C: 计入每次调用（含失败路径），类似 rate counter。
	skillmetrics.IncSkillExpandInvoke()
	cwd, err := requireCWD(ctx)
	if err != nil {
		return ExpandBodyResult{}, err
	}
	rec, err := s.findSkillRecordByName(p.Name, cwd)
	if err != nil {
		return ExpandBodyResult{}, err
	}
	anchor := strings.TrimSpace(p.Anchor)
	locator, err := NormalizeArtifactLocator(ArtifactKindBody, bodyArtifactLocator(anchor))
	if err != nil {
		return ExpandBodyResult{}, err
	}
	if err := s.requireArtifactApproval(ctx, rec.info, ArtifactKindBody, locator, rec.info.ContentHash, cwd, "skill_expand_body", expandBodyApprovalMetadata(p)); err != nil {
		return ExpandBodyResult{}, err
	}
	data, err := readSkillFileData(rec.path)
	if err != nil {
		return ExpandBodyResult{}, err
	}
	slice, err := bodySliceFromSkillData(data, anchor)
	if err != nil {
		return ExpandBodyResult{}, err
	}
	content, truncated := truncateBytes(slice, resolveMaxBytes(p.MaxBytes))
	version := shortSHA256Hex(data)
	total := int64(len(slice))

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

func readSkillFileData(path string) ([]byte, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if stat.Size() > maxSkillFileBytes {
		return nil, fmt.Errorf("skill file too large: %s is %d bytes, limit %d", path, stat.Size(), maxSkillFileBytes)
	}
	return os.ReadFile(path)
}

func bodySliceFromSkillData(data []byte, anchor string) (string, error) {
	body := skillBodyFromData(data)
	if strings.TrimSpace(anchor) == "" {
		return body, nil
	}
	slice, ok := sliceMarkdownSection(body, anchor)
	if !ok {
		return "", fmt.Errorf("anchor not found: %q", anchor)
	}
	return slice, nil
}

func skillBodyFromData(data []byte) string {
	full := string(data)
	_, body, hasFM := splitFrontmatter(full)
	if !hasFM {
		return full
	}
	return body
}

func fullSHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func shortSHA256Hex(data []byte) string {
	version := fullSHA256Hex(data)
	if len(version) > 12 {
		return version[:12]
	}
	return version
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
// 本地项目级/用户级 skill resource 不需要 approval；仅确定不可信/异常来源
// 按 resource 文件自身 hash 做 artifact-level approval。
func (s *service) ReadResource(ctx context.Context, p ReadResourceParams) (ReadResourceResult, error) {
	// P20.1 Phase 10 Step C: 与 ExpandBody 合计 SkillExpandInvokeRate。
	skillmetrics.IncSkillExpandInvoke()
	cwd, err := requireCWD(ctx)
	if err != nil {
		return ReadResourceResult{}, err
	}
	rec, err := s.findSkillRecordByName(p.Name, cwd)
	if err != nil {
		return ReadResourceResult{}, err
	}
	relPath, err := NormalizeArtifactLocator(ArtifactKindResource, p.Path)
	if err != nil {
		return ReadResourceResult{}, err
	}
	target, skillDir, err := resolveResourceTarget(rec.info.Dir, relPath)
	if err != nil {
		return ReadResourceResult{}, err
	}

	data, err := readResourceData(target, relPath)
	if err != nil {
		return ReadResourceResult{}, err
	}
	content, truncated := truncateBytes(string(data), resolveMaxBytes(p.MaxBytes))
	resourceHash := fullSHA256Hex(data)
	total := int64(len(data))
	if err := s.requireArtifactApproval(ctx, rec.info, ArtifactKindResource, relPath, resourceHash, cwd, "skill_read_resource", readResourceApprovalMetadata(p)); err != nil {
		return ReadResourceResult{}, err
	}
	version := resourceHash
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

func readResourceData(target, relPath string) ([]byte, error) {
	stat, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	if stat.IsDir() {
		return nil, fmt.Errorf("path is directory: %s", relPath)
	}
	if stat.Size() > maxSkillFileBytes {
		return nil, fmt.Errorf("resource file too large: %s is %d bytes, limit %d", relPath, stat.Size(), maxSkillFileBytes)
	}
	return os.ReadFile(target)
}

// resolveResourceTarget 解析 symlink、规范化路径并验证 target 未逃逸 skill 目录。
func resolveResourceTarget(dir, relPath string) (target, skillDir string, err error) {
	skillDir = filepath.Clean(dir)
	// EvalSymlinks 规范化 skillDir（macOS /tmp → /private/tmp 之类 symlink 场景）。
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
