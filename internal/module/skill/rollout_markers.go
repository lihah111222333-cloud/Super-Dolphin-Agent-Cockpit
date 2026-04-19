package skill

import (
	"regexp"
	"strings"
)

// skillBlockHeaderNewFormat 匹配 P20 新格式的 skill 注入块头行：
//
//	[skill:<name>::<mode>@v<version>]
//
// 捕获组：1=name, 2=mode, 3=version。
// name 规则对齐 validateSkillName（仅小写字母/数字/连字符，1-64 字符）；
// mode 接受任意小写字母串，交给 SkillMode.Valid 检查；version 数字。
var skillBlockHeaderNewFormat = regexp.MustCompile(`^\[skill:([a-z0-9][a-z0-9-]{0,63})::([a-z]+)@v(\d+)\]\s*$`)

// skillBlockFooterNewFormat 匹配新格式结束标志。Phase 3 暂不做"仅剥此块保留
// 后续"的精细操作（沿袭 legacy "剪到文末" 语义），但保留识别能力供 Phase 4+
// 的多块场景使用。
var skillBlockFooterNewFormat = regexp.MustCompile(`^\[/skill:([a-z0-9][a-z0-9-]{0,63})::([a-z]+)@v(\d+)\]\s*$`)

// skillBlockHeaderLegacy 识别旧格式 header：[skill:<anything>]。不要求 header 独占一行
// （具有还原 codexapp/claudecli 旧实现的较宽容性：原版本仅检查
// `strings.HasPrefix(line, "[skill:")` + `strings.Contains(line, "]")`，
// 允许单行内同时包含 header 与 marker）。排除包含 "::"（属于新格式）。
// 用户文本里偶尔出现这种写法，因此 legacy 识别必须配合 AND
// 命中 legacySkillMarkers 才能判定为注入块。
var skillBlockHeaderLegacy = regexp.MustCompile(`^\[skill:[^\]:]+\]`)

// legacySkillMarkers 是旧格式注入块必须在 lookahead 窗口内 AND 命中的两个标记。
// 对齐 codexapp/history_rollout.go 与 claudecli/history_trim.go 的原始实现，
// 保证读取旧 rollout 文件时剥离行为不变。
var legacySkillMarkers = []struct {
	label         string
	allowContains bool
}{
	{label: "摘要:", allowContains: true},
	{label: "使用方式: ", allowContains: false},
}

// legacySkillLookahead 是旧格式识别在 header 之后扫描的最大行数，复刻旧实现。
const legacySkillLookahead = 8

// SkillBlockFormat 表示识别出的 skill 注入块格式。
type SkillBlockFormat int

const (
	SkillBlockFormatNone SkillBlockFormat = iota
	// SkillBlockFormatLegacy: [skill:<name>] + 后续 "摘要:" + "使用方式: " 标记
	SkillBlockFormatLegacy
	// SkillBlockFormatNew: [skill:<name>::<mode>@v<version>]
	SkillBlockFormatNew
)

// SkillBlockHeader 是注入 skill 块头部的解析结果。
//
// Mode 用原始字符串而非 dto.SkillMode——由上游调用方（codexapp/claudecli）自行
// 转换。这样避免 `internal/module/skill` 反向依赖 `internal/dto/provider`（后者
// 在导入图上处于更高层）。有效值："full" / "summary" / "none"。
type SkillBlockHeader struct {
	Format  SkillBlockFormat
	Name    string // 新格式 populate；legacy 为空字串
	Mode    string // 新格式 populate（原始字符串）；legacy 为空
	Version int    // 新格式 populate；legacy 为 0
}

// ParseSkillBlockHeader 解析单行是否为 skill 注入块头部。
//
// 识别两种格式：
//  1. 新格式 `[skill:<name>::<mode>@v<ver>]`：字符级严格匹配，用户无合法理由
//     写出这种串，因此 Format=New 可直接视为注入块。
//  2. legacy `[skill:<任意>]`：可能是用户正常文本（如引用工具名），必须配合
//     后续 lookahead 窗口 AND 命中 legacySkillMarkers 才能判定注入块。
//
// 未匹配返回 Format=None。
func ParseSkillBlockHeader(line string) SkillBlockHeader {
	trimmed := strings.TrimSpace(line)
	if m := skillBlockHeaderNewFormat.FindStringSubmatch(trimmed); m != nil {
		return SkillBlockHeader{
			Format:  SkillBlockFormatNew,
			Name:    m[1],
			Mode:    m[2],
			Version: parseVersionString(m[3]),
		}
	}
	if skillBlockHeaderLegacy.MatchString(trimmed) {
		return SkillBlockHeader{Format: SkillBlockFormatLegacy}
	}
	return SkillBlockHeader{Format: SkillBlockFormatNone}
}

// ParseSkillBlockFooter 解析尾行。仅识别新格式尾标，legacy 无尾标。
// 返回 ok=false 表示非注入块尾行。
func ParseSkillBlockFooter(line string) (SkillBlockHeader, bool) {
	trimmed := strings.TrimSpace(line)
	if m := skillBlockFooterNewFormat.FindStringSubmatch(trimmed); m != nil {
		return SkillBlockHeader{
			Format:  SkillBlockFormatNew,
			Name:    m[1],
			Mode:    m[2],
			Version: parseVersionString(m[3]),
		}, true
	}
	return SkillBlockHeader{}, false
}

// TrimInjectedSkillBlocks 扫描 text，剥离首个识别到的注入 skill 块及其之后所有内容。
//
// 行为与 codexapp/history_rollout.go:trimInjectedSkillBlock 及
// claudecli/history_trim.go:trimInjectedClaudeSkillBlock 保持一致（"剪到文末"
// 语义），但扩展识别范围：
//   - 新格式（Phase 4 写端产出）：header 严格匹配即剥离，无需 footer 或 AND 标记
//   - legacy 格式（旧 rollout 回放）：保留原有 AND 标记 + lookahead 判定逻辑
//
// 未命中返回原 text。Phase 3 只做"剪到文末"单块处理，多块精细化留给 Phase 4+。
func TrimInjectedSkillBlocks(text string) string {
	lines := strings.Split(text, "\n")
	for i, raw := range lines {
		header := ParseSkillBlockHeader(raw)
		switch header.Format {
		case SkillBlockFormatNew:
			// 新格式严格正则匹配 → 直接剥离
			return strings.TrimRight(strings.Join(lines[:i], "\n"), "\n")
		case SkillBlockFormatLegacy:
			if looksLikeLegacyInjectedBlock(lines, i) {
				return strings.TrimRight(strings.Join(lines[:i], "\n"), "\n")
			}
		}
	}
	return text
}

// looksLikeLegacyInjectedBlock 复刻 codexapp/claudecli 双边原实现 looksLike* 逻辑：
// 从 start 行起，向后扫描至多 legacySkillLookahead 行，累积 legacySkillMarkers
// 的命中；遇到下一个 [skill:...] header 即停止。AND 全命中才判定为注入块。
func looksLikeLegacyInjectedBlock(lines []string, start int) bool {
	if start < 0 || start >= len(lines) {
		return false
	}
	matched := map[string]bool{}
	markLegacySkillMarkers(strings.TrimSpace(lines[start]), matched)
	for i := start + 1; i < len(lines) && i <= start+legacySkillLookahead; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[skill:") {
			break
		}
		markLegacySkillMarkers(line, matched)
		if len(matched) == len(legacySkillMarkers) {
			return true
		}
	}
	return len(matched) == len(legacySkillMarkers)
}

func markLegacySkillMarkers(line string, matched map[string]bool) {
	for _, marker := range legacySkillMarkers {
		if matchLegacySkillMarker(line, marker.label, marker.allowContains) {
			matched[marker.label] = true
		}
	}
}

func matchLegacySkillMarker(line, marker string, allowContains bool) bool {
	if allowContains {
		return strings.Contains(line, marker)
	}
	return strings.HasPrefix(line, marker)
}

// parseVersionString 把 "1", "2", ... 解析为 int。无效返回 0（不影响剥离决策）。
func parseVersionString(raw string) int {
	v := 0
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0
		}
		v = v*10 + int(r-'0')
		if v > 1<<20 { // 防溢出
			return 0
		}
	}
	return v
}
