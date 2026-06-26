// Package parse 提供记忆文件解析的共享基元。
// memory、retrieval、nested 等子包必须复用这里的 BOM、frontmatter、HTML 注释和截断语义，
// 否则同一份 MEMORY.md 在不同入口可能被清洗成不同内容，造成注入防护或缓存键不一致。
// 本包保持标准库依赖，方便作为记忆模块内的叶子包引用。
package parse

import (
	"bufio"
	"io"
	"strings"
	"unicode/utf8"
)

// StripUTF8BOM 移除开头连续 UTF-8 BOM，正文其它位置保持不变。
// 循环清理可避免多重 BOM 让首行 `---` 失去 frontmatter 识别，从而把元数据误注入 prompt。
func StripUTF8BOM(content string) string {
	for strings.HasPrefix(content, "\ufeff") {
		content = strings.TrimPrefix(content, "\ufeff")
	}
	return content
}

// IsFence 判断单行是否为 YAML frontmatter 分隔符。
// 允许 `---` 后带空格或 tab，兼容编辑器保存时追加的尾随空白，避免 frontmatter
// 因轻微格式差异泄漏到 prompt。
func IsFence(line string) bool {
	return strings.TrimRight(line, " \t") == "---"
}

// ScanFrontmatterHeader 从 reader 读取最多 limit 字节的 frontmatter 头部候选。
// 返回内容包含第二个 `---` 分隔符，调用方可直接交给 SplitFrontmatter；首行 BOM 会先清理，
// 因此带 BOM 的文件不会被误判为无 frontmatter。
// 如果限制内没有完整闭合分隔符，则返回已读内容，让 SplitFrontmatter 按“无 frontmatter”处理。
func ScanFrontmatterHeader(r io.Reader, limit int) (string, error) {
	scanner := bufio.NewScanner(io.LimitReader(r, int64(limit)))
	scanner.Buffer(make([]byte, 0, 4096), limit)
	var builder strings.Builder
	openMarkers := 0
	first := true
	for scanner.Scan() {
		line := scanner.Text()
		if first {
			line = StripUTF8BOM(line)
			first = false
		}
		builder.WriteString(line)
		builder.WriteByte('\n')
		if IsFence(line) {
			openMarkers++
			if openMarkers >= 2 {
				break
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return builder.String(), nil
}

// SplitFrontmatter 解析文件顶部的 YAML frontmatter。
// 只有首行和闭合行都匹配 IsFence 才返回 ok=true；未闭合时保留原文并返回 ok=false，
// 防止半截 YAML 被当成正文或被悄悄丢弃。
func SplitFrontmatter(content string) (string, string, bool) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.SplitN(content, "\n", 2)
	if len(lines) < 2 || !IsFence(lines[0]) {
		return "", content, false
	}
	rest := lines[1]
	var frontmatter strings.Builder
	for {
		next := strings.SplitN(rest, "\n", 2)
		candidate := next[0]
		if IsFence(candidate) {
			body := ""
			if len(next) == 2 {
				body = next[1]
			}
			return strings.TrimRight(frontmatter.String(), "\n"), body, true
		}
		if len(next) < 2 {
			return "", content, false
		}
		frontmatter.WriteString(candidate)
		frontmatter.WriteByte('\n')
		rest = next[1]
	}
}

// StripHTMLComments 移除记忆文件中位于行首的 HTML 注释脚手架。
// 它保留行内注释、代码围栏内注释和 CDATA 内容，只删除形如行首 `<!-- ... -->`
// 的模板残留；未闭合注释会按普通文本保留，避免格式错误吞掉后续正文。
// 函数会迭代到固定点，保证重复调用不会继续改变内容。
func StripHTMLComments(content string) string {
	// 单次扫描删除后，左右残片可能拼出新的 `<!-- ... -->`；迭代到无变化可保证
	// StripHTMLComments(StripHTMLComments(x)) == StripHTMLComments(x)。
	// 每轮有效扫描至少删除一个字节，因此循环有明确上界。
	for {
		next, stripped := stripHTMLCommentsScan(content)
		if !stripped {
			return next
		}
		if next == content {
			return next
		}
		content = next
	}
}

// stripHTMLCommentsScan 执行一次 HTML 注释扫描。
// 返回 stripped=false 表示本轮没有可删除注释，外层固定点循环可停止。
func stripHTMLCommentsScan(content string) (string, bool) {
	// 快路径：没有注释起始和 CDATA 起始时无需进入逐行状态机。
	if !strings.Contains(content, "<!--") && !strings.Contains(content, "<![CDATA[") {
		return content, false
	}
	state := &htmlCommentStripState{}
	for _, line := range strings.SplitAfter(content, "\n") {
		state.processLine(line)
	}
	// EOF 前未闭合的 `<!--` 按普通文本输出，避免截断注释吞掉后续内容。
	if state.inComment {
		state.builder.WriteString(state.pendingComment.String())
	}
	return state.builder.String(), state.stripped
}

// markdownFenceState 记录当前是否处于 Markdown 代码围栏内。
// 围栏内的 HTML 注释必须原样保留，否则文档示例会被解析器误删。
type markdownFenceState struct {
	open   bool
	marker byte
}

// htmlCommentStripState 保存一次注释清理扫描的状态。
// pendingComment 缓冲未闭合的行首注释；inCDATA 优先级高于注释扫描。
type htmlCommentStripState struct {
	fence          markdownFenceState
	builder        strings.Builder
	pendingComment strings.Builder
	stripped       bool
	inComment      bool
	inCDATA        bool
}

// processLine 处理单行文本并更新注释、CDATA 与代码围栏状态。
// 行首注释会被移除或暂存，普通正文和受保护区域会直接写入 builder。
func (s *htmlCommentStripState) processLine(line string) {
	// CDATA 优先：CDATA span 内不做注释扫描，因此其中的 `<!--` 会原样保留。
	if s.inCDATA {
		s.builder.WriteString(line)
		if strings.Contains(line, "]]>") {
			s.inCDATA = false
		}
		return
	}
	if s.processPendingLine(line) {
		return
	}
	if lineInMarkdownFence(&s.fence, line) {
		s.builder.WriteString(line)
		return
	}
	if s.openCDATAIfUnclosed(line) {
		return
	}
	if !startsHTMLCommentBlock(line) {
		s.builder.WriteString(line)
		return
	}
	if s.stripInlineComment(line) {
		return
	}
	s.pendingComment.WriteString(line)
	s.inComment = true
}

// processPendingLine 继续处理上一行打开但尚未闭合的行首 HTML 注释。
// 找到第一个 `-->` 后结束注释，并把闭合后的残留交给 emitCommentResidue。
func (s *htmlCommentStripState) processPendingLine(line string) bool {
	if !s.inComment {
		return false
	}
	s.pendingComment.WriteString(line)
	// HTML 注释不按嵌套处理；第一个 `-->` 关闭注释，后续残留按正文保留。
	_, residue, ok := strings.Cut(line, "-->")
	if !ok {
		return true
	}
	s.stripped = true
	s.pendingComment.Reset()
	s.inComment = false
	s.emitCommentResidue(residue)
	return true
}

// openCDATAIfUnclosed 处理跨行 CDATA。
// 如果本行打开但未关闭 CDATA，整行原样输出并进入 inCDATA；单行闭合 CDATA 不需要特殊处理，
// 因为行内 `<!--` 本来就不会被当成可删除脚手架。
func (s *htmlCommentStripState) openCDATAIfUnclosed(line string) bool {
	idx := strings.Index(line, "<![CDATA[")
	if idx < 0 {
		return false
	}
	if strings.Contains(line[idx+len("<![CDATA["):], "]]>") {
		return false
	}
	s.builder.WriteString(line)
	s.inCDATA = true
	return true
}

// stripInlineComment 移除同一行内闭合的行首 HTML 注释。
// 注释后的尾部会继续交给 emitCommentResidue，确保残留中再次出现的闭合脚手架不会留到
// 下一次调用才被删除。
func (s *htmlCommentStripState) stripInlineComment(line string) bool {
	if !strings.Contains(line, "-->") {
		return false
	}
	s.stripped = true
	trimmed := strings.TrimLeft(line, " \t")
	afterOpen := trimmed[len("<!--"):]
	_, residue, ok := strings.Cut(afterOpen, "-->")
	if !ok {
		// 正常情况下 Cut 不会失败；若遇到异常输入，按未闭合注释处理以保留原始文本。
		s.pendingComment.WriteString(line)
		s.inComment = true
		return true
	}
	s.emitCommentResidue(residue)
	return true
}

// emitCommentResidue 处理 `-->` 后面的尾部文本。
// 已闭合的行首脚手架会继续删除，普通文本会保留；如果尾部打开了未闭合注释，
// 状态机会进入 inComment，等待下一行闭合或在 EOF 时按普通文本输出。
func (s *htmlCommentStripState) emitCommentResidue(residue string) {
	var plain strings.Builder
	pos := 0
	for {
		idx := strings.Index(residue[pos:], "<!--")
		if idx < 0 {
			plain.WriteString(residue[pos:])
			appendNonEmptyLine(&s.builder, plain.String())
			return
		}
		absIdx := pos + idx
		afterOpen := absIdx + len("<!--")
		closeIdx := strings.Index(residue[afterOpen:], "-->")
		if closeIdx < 0 {
			// 尾部打开未闭合注释：先保留普通前缀，再缓存注释片段等待下一行或 EOF。
			plain.WriteString(residue[pos:absIdx])
			appendNonEmptyLine(&s.builder, plain.String())
			s.pendingComment.WriteString(residue[absIdx:])
			s.inComment = true
			return
		}
		plain.WriteString(residue[pos:absIdx])
		pos = afterOpen + closeIdx + len("-->")
	}
}

// appendNonEmptyLine 仅在尾部残留含有效文本时写入 builder。
// 注释删除产生的纯空白不应制造新的空行。
func appendNonEmptyLine(builder *strings.Builder, line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	builder.WriteString(line)
}

// startsHTMLCommentBlock 判断一行裁剪左侧空白后是否以 HTML 注释开头。
// 只有这种脚手架形态会被删除，行内注释保持原样。
func startsHTMLCommentBlock(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), "<!--")
}

// lineInMarkdownFence 更新代码围栏状态并返回当前行是否受围栏保护。
// 受保护行不参与 HTML 注释删除，避免破坏 Markdown 示例。
func lineInMarkdownFence(state *markdownFenceState, line string) bool {
	marker, ok := markdownFenceMarker(line)
	if state.open {
		if ok && marker == state.marker {
			state.open = false
			state.marker = 0
		}
		return true
	}
	if !ok {
		return false
	}
	state.open = true
	state.marker = marker
	return true
}

// markdownFenceMarker 识别 Markdown 代码围栏起止标记。
// 这里只关心 ``` 和 ~~~ 的首三个字符，语言名或后缀不影响保护判断。
func markdownFenceMarker(line string) (byte, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if strings.HasPrefix(trimmed, "```") {
		return '`', true
	}
	if strings.HasPrefix(trimmed, "~~~") {
		return '~', true
	}
	return 0, false
}

// JSStringLength 返回 JavaScript String.length 口径的 UTF-16 code unit 长度。
// 记忆注入的截断预算需要与前端/底层 CLI 保持一致，不能直接使用 Go rune 或字节长度。
func JSStringLength(content string) int {
	count := 0
	for bytePos := 0; bytePos < len(content); {
		r, size := utf8.DecodeRuneInString(content[bytePos:])
		count += UTF16CodeUnits(r)
		bytePos += size
	}
	return count
}

// UTF16CodeUnits 返回单个 rune 在 UTF-16 中占用的 code unit 数。
// 基本多文种平面外字符在 JavaScript 中是代理对，因此计为 2。
func UTF16CodeUnits(r rune) int {
	if r > 0xFFFF {
		return 2
	}
	return 1
}

// TruncateAtCodeUnitLimit 按 UTF-16 code unit 上限截断内容。
// 优先在限制前最后一个换行处截断；如果第一行就超过限制，则按合法 UTF-8 边界截断，
// 保证调用方总能得到非超限前缀并继续追加提示。
func TruncateAtCodeUnitLimit(content string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if JSStringLength(content) <= limit {
		return content
	}
	bytePos := 0
	codeUnits := 0
	lastNewline := -1
	for bytePos < len(content) {
		r, size := utf8.DecodeRuneInString(content[bytePos:])
		units := UTF16CodeUnits(r)
		if codeUnits+units > limit {
			break
		}
		if r == '\n' {
			lastNewline = bytePos
		}
		codeUnits += units
		bytePos += size
	}
	if lastNewline >= 0 {
		return content[:lastNewline]
	}
	return content[:bytePos]
}
