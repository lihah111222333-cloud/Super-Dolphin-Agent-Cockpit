package skill

import (
	"regexp"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/pkg/skillmetrics"
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

// skillBlockHeaderLegacy 识别旧格式 header：[skill:<anything>]。
//
// 行为等价还原 codexapp/claudecli 旧实现：
//
//	strings.HasPrefix(line, "[skill:") && strings.Contains(line, "]")
//
// 即 legacy header 只要开头是 `[skill:` 且行内有 `]` 即认识。不作任何名字内容
// 制约（空、含 `:`、含空格均允许），因为：
//   - 新格式已在 ParseSkillBlockHeader 中优先匹配，如果 `[skill:foo::full@v1]`
//     先命中不会进 legacy 分支。
//   - legacy AND 判定仍要求后续命中 "摘要:" + "使用方式: " 两 marker，
//     用户文本里偶尔出现 [skill:foo] 不会误剥。
//
// 这样覆盖旧实现能识别但严格 regex 会漏掉的两个 edge case：
//
//	[skill:]           → 空 name
//	[skill:foo:bar]    → name 内部含 `:`
var skillBlockHeaderLegacy = regexp.MustCompile(`^\[skill:[^\]]*\]`)

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

// TrimInjectedSkillBlocks 扫描 text，剥离所有识别到的注入 skill 块。
//
// 策略（P20.1 §3.4 加固）：
//   - **新格式**按 header/footer **成对裁剪**：仅删除 [header..footer] 闭区间，
//     保留 block 后面的正常用户文本；支持同一 payload 内多个 block 顺序出现。
//   - 新格式 header 存在但 footer 缺失 → 走损坏兑底：从 header 剪到 EOF，并记录
//     `skill_trim_corruption_fallback_count` 指标（通过 *WithDiag 返回观测）。
//   - **legacy 格式**保留“剪到 EOF”旧语义（旧格式无 footer 概念）；仅用于兼容
//     旧 rollout 回放，新写端不得再产 legacy 块。
//
// 未命中返回原 text。调用方须诂类诊断时用 TrimInjectedSkillBlocksWithDiag。
func TrimInjectedSkillBlocks(text string) string {
	return TrimInjectedSkillBlocksWithDiag(text).Text
}

// TrimResult 包装 trim 操作的诊断信息（P20.1 Phase 10 指标用途）。
type TrimResult struct {
	// Text 是裁剪后的文本。
	Text string
	// NewBlocksTrimmed 是成功成对裁剪的新格式 skill 块数量。
	NewBlocksTrimmed int
	// LegacyTrimmed 表示是否触发了 legacy “剪到 EOF”语义。
	LegacyTrimmed bool
	// FooterMissingCount 是新格式 header 存在但找不到对应 footer，走损坏兑底裁的次数。
	// Phase 10 应将该值接入 skill_trim_corruption_fallback_count 指标。
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
			// footer 缺失 → 损坏兑底：剪到 EOF
			res.FooterMissingCount++
			// P20.1 Phase 10 Step C: 成对 footer 缺失 → trim 降级计数。
			skillmetrics.IncTrimCorruptionFallback()
			res.Text = strings.TrimRight(strings.Join(kept, "\n"), "\n")
			return res
		case SkillBlockFormatLegacy:
			if looksLikeLegacyInjectedBlock(lines, i) {
				// legacy 遗留语义：剪到 EOF。
				// 注：legacy 格式是预期的历史数据路径，不是 P20.1 §3.4 定义的
				// "corruption fallback"；因此不计入 skillmetrics.IncTrimCorruptionFallback()
				// ——后者仅涉及 pair-fenced footer 缺失这一真正的异常场景。
				res.LegacyTrimmed = true
				res.Text = strings.TrimRight(strings.Join(kept, "\n"), "\n")
				return res
			}
			// AND 不命中，保留该行继续扰描
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
