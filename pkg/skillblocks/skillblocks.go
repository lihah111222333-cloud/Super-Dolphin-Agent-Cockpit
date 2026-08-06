// Package skillblocks 解析并裁剪 provider 历史中的技能注入标记。
package skillblocks

import (
	"regexp"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/skillmetrics"
)

// skillBlockHeaderNewFormat 匹配带 footer 的 skill 注入块头行：
//
//	[skill:<name>::<mode>@v<version>]
//
// 捕获组：1=name, 2=mode, 3=version。
// name 规则对齐 validateSkillName（仅小写字母/数字/连字符，1-64 字符）；
// mode 接受任意小写字母串作为 rollout marker 标签；version 数字。
var skillBlockHeaderNewFormat = regexp.MustCompile(`^\[skill:([a-z0-9][a-z0-9-]{0,63})::([a-z]+)@v(\d+)\]\s*$`)

// skillBlockFooterNewFormat 匹配带 footer 的 skill 注入块结束标志。
// header/footer 必须严格同名、同模式、同版本，避免误删普通用户文本。
var skillBlockFooterNewFormat = regexp.MustCompile(`^\[/skill:([a-z0-9][a-z0-9-]{0,63})::([a-z]+)@v(\d+)\]\s*$`)

// skillBlockHeaderLegacy 识别无 footer 的旧格式 header：[skill:<anything>]。
//
// 行为等价还原 codexapp/claudecli 旧实现：
//
//	strings.HasPrefix(line, "[skill:") && strings.Contains(line, "]")
//
// 即旧格式 header 只要开头是 `[skill:` 且行内有 `]` 即认识。不作任何名字内容
// 制约（空、含 `:`、含空格均允许），因为：
//   - 新格式已在 ParseSkillBlockHeader 中优先匹配，如果 `[skill:foo::full@v1]`
//     先命中不会进旧格式分支。
//   - 旧格式判定仍要求后续命中 "摘要:" + "使用方式: " 两个标记，
//     用户文本里偶尔出现 [skill:foo] 不会误剥。
//
// 这样覆盖旧实现能识别但严格 regex 会漏掉的两个 edge case：
//
//	[skill:]           → 空 name
//	[skill:foo:bar]    → name 内部含 `:`
var skillBlockHeaderLegacy = regexp.MustCompile(`^\[skill:[^\]]*\]`)

// 旧格式注入块必须在 lookahead 窗口内同时命中这两个固定 marker。
// 它们是纯判定规则，不保留可变共享集合。
const (
	legacySkillSummaryMarker = "摘要:"
	legacySkillUsageMarker   = "使用方式: "
)

// legacySkillLookahead 是旧格式识别在 header 之后扫描的最大行数，复刻旧实现。
const legacySkillLookahead = 8

// SkillBlockFormat 表示识别出的 skill 注入块格式。
type SkillBlockFormat int

const (
	SkillBlockFormatNone SkillBlockFormat = iota
	// SkillBlockFormatLegacy 表示无 footer 的旧格式块，需配合中文摘要/使用方式标记确认。
	SkillBlockFormatLegacy
	// SkillBlockFormatNew 表示带 name/mode/version 和配对 footer 的格式。
	SkillBlockFormatNew
)

// SkillBlockHeader 是注入 skill 块头部的解析结果。
//
// Mode 用原始字符串作为 rollout 标签——由上游调用方（codexapp/claudecli）自行
// 转换。这样避免 `internal/module/skill` 反向依赖 `internal/dto/provider`（后者
// 在导入图上处于更高层）。有效值："full" / "summary" / "none"。
type SkillBlockHeader struct {
	Format  SkillBlockFormat
	Name    string // 带 footer 格式的 skill 名；旧格式为空字串
	Mode    string // 带 footer 格式的原始 mode；旧格式为空
	Version int    // 带 footer 格式的版本号；旧格式为 0
}

// ParseSkillBlockHeader 解析单行是否为 skill 注入块头部。
//
// 识别两种格式：
//  1. 新格式 `[skill:<name>::<mode>@v<ver>]`：字符级严格匹配，用户无合法理由
//     写出这种串，因此 Format=New 可直接视为注入块。
//  2. 旧格式 `[skill:<任意>]`：可能是用户正常文本（如引用工具名），必须配合
//     后续 lookahead 窗口 AND 命中两个固定 marker 才能判定注入块。
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

// ParseSkillBlockFooter 解析尾行。仅带 footer 的格式有尾标，旧格式没有尾标。
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

// TrimInjectedSkillBlocks 扫描 text，剥离所有识别到的注入 skill 块。
//
// 当前裁剪策略：
//   - **新格式**按 header/footer **成对裁剪**：仅删除 [header..footer] 闭区间，
//     保留 block 后面的正常用户文本；支持同一 payload 内多个 block 顺序出现。
//   - 新格式 header 存在但 footer 缺失 → 走损坏兜底：从 header 剪到 EOF，并记录
//     `skill_trim_corruption_fallback_count` 指标（通过 *WithDiag 返回观测）。
//   - **旧格式**保留“剪到 EOF”语义（旧格式无 footer 概念）；仅用于兼容
//     旧 rollout 回放，新写端不得再产 legacy 块。
//
// 未命中返回原 text。调用方需要分类诊断时用 TrimInjectedSkillBlocksWithDiag。
func TrimInjectedSkillBlocks(text string) string {
	return TrimInjectedSkillBlocksWithDiag(text).Text
}

// TrimResult 包装 trim 操作的诊断信息，供调用方上报裁剪指标。
type TrimResult struct {
	// Text 是裁剪后的文本。
	Text string
	// NewBlocksTrimmed 是成功成对裁剪的新格式 skill 块数量。
	NewBlocksTrimmed int
	// LegacyTrimmed 表示是否触发了 legacy “剪到 EOF”语义。
	LegacyTrimmed bool
	// FooterMissingCount 是新格式 header 存在但找不到对应 footer，走损坏兜底裁剪的次数。
	// 该值对应 skill_trim_corruption_fallback_count 指标。
	FooterMissingCount int
}

// TrimInjectedSkillBlocksWithDiag 是带诊断的完整实现。见 TrimInjectedSkillBlocks
// 的策略注释；本函数额外返回监测数据。
func TrimInjectedSkillBlocksWithDiag(text string) TrimResult {
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	res := TrimResult{}

	i := 0
	for i < len(lines) {
		header := ParseSkillBlockHeader(lines[i])
		switch header.Format {
		case SkillBlockFormatNew:
			footerIdx := findMatchingSkillBlockFooter(lines, i+1, header)
			if footerIdx >= 0 {
				// 成对裁剪：跳过 [i..footerIdx]（含 header 与 footer）
				res.NewBlocksTrimmed++
				i = footerIdx + 1
				continue
			}
			// footer 缺失时剪到 EOF，并记录损坏兜底指标。
			res.FooterMissingCount++
			skillmetrics.IncTrimCorruptionFallback()
			res.Text = strings.TrimRight(strings.Join(kept, "\n"), "\n")
			return res
		case SkillBlockFormatLegacy:
			if looksLikeLegacyInjectedBlock(lines, i) {
				// 旧格式没有 footer，只能剪到 EOF；这是兼容路径，不计入损坏兜底指标。
				res.LegacyTrimmed = true
				res.Text = strings.TrimRight(strings.Join(kept, "\n"), "\n")
				return res
			}
			// 标记未全部命中时保留该行继续扫描。
			kept = append(kept, lines[i])
			i++
		default:
			kept = append(kept, lines[i])
			i++
		}
	}

	if res.NewBlocksTrimmed == 0 && !res.LegacyTrimmed && res.FooterMissingCount == 0 {
		// 未命中，返回原 text（保留 trailing 等原样不动）
		res.Text = text
		return res
	}
	res.Text = strings.TrimRight(strings.Join(kept, "\n"), "\n")
	return res
}

// findMatchingSkillBlockFooter 从 lines[start:] 扫描对应 header 的 footer（按 name/mode/
// version 严格匹配）。未找到返回 -1。
func findMatchingSkillBlockFooter(lines []string, start int, header SkillBlockHeader) int {
	for i := start; i < len(lines); i++ {
		footer, ok := ParseSkillBlockFooter(lines[i])
		if !ok {
			continue
		}
		if footer.Name == header.Name && footer.Mode == header.Mode && footer.Version == header.Version {
			return i
		}
	}
	return -1
}

// looksLikeLegacyInjectedBlock 识别无 footer 的旧格式注入块：
// 从 start 行起，向后扫描至多 legacySkillLookahead 行，累积两个固定 marker
// 的命中；遇到下一个 [skill:...] header 即停止。AND 全命中才判定为注入块。
func looksLikeLegacyInjectedBlock(lines []string, start int) bool {
	if start < 0 || start >= len(lines) {
		return false
	}
	summaryMatched, usageMatched := legacySkillMarkerMatches(strings.TrimSpace(lines[start]))
	for i := start + 1; i < len(lines) && i <= start+legacySkillLookahead; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[skill:") {
			break
		}
		summary, usage := legacySkillMarkerMatches(line)
		summaryMatched = summaryMatched || summary
		usageMatched = usageMatched || usage
		if summaryMatched && usageMatched {
			return true
		}
	}
	return summaryMatched && usageMatched
}

// legacySkillMarkerMatches 返回单行命中的旧格式 marker；不维护共享规则状态。
func legacySkillMarkerMatches(line string) (summary, usage bool) {
	return strings.Contains(line, legacySkillSummaryMarker), strings.HasPrefix(line, legacySkillUsageMarker)
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
