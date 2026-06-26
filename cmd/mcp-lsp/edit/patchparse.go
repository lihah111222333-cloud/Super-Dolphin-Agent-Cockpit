// Package edit 提供 patch 解析与匹配功能，用于 LSP replace_range 编辑操作。
package edit

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

const (
	maxPatchBytes = 256 * 1024
	maxPatchHunks = 64
	maxPatchLines = 8192
)

var (
	ErrEmptyPatch       = errors.New("patch is empty")
	ErrInvalidPatch     = errors.New("invalid patch")
	ErrSequenceNotFound = errors.New("sequence not found")
	ErrAmbiguousMatch   = errors.New("ambiguous match")
)

// Hunk 是 replace_range patch 解析后的内部契约。
// OldText/NewText 承载实际变更，BeforeContext/AfterContext 用于匹配时消歧。
type Hunk struct {
	OldText       string
	NewText       string
	ChangeContext []string
	BeforeContext []string
	AfterContext  []string
}

type patchBodyLine struct {
	kind byte
	text string
}

// Parse 解析单个 replace_range hunk。
// 它只接受隐式单 hunk 或一个显式 "@@" hunk，出现多个 hunk 会 fail-fast。
func Parse(patch string) (Hunk, error) {
	lines, err := normalizePatchLines(patch)
	if err != nil {
		return Hunk{}, err
	}
	lines = normalizeLLMPatchEnvelope(lines)
	if len(lines) == 0 {
		return Hunk{}, ErrEmptyPatch
	}
	if isPatchHeader(lines[0]) {
		headerLines, err := splitPatchHeaders(lines)
		if err != nil {
			return Hunk{}, err
		}
		if len(headerLines) != 1 {
			return Hunk{}, fmt.Errorf("%w: single-hunk parser does not accept multiple @@ headers", ErrInvalidPatch)
		}
		return parseHunkBody(headerLines[0])
	}
	if containsPatchHeader(lines[1:]) {
		return Hunk{}, fmt.Errorf("%w: leading implicit hunk is not allowed before a second @@ header", ErrInvalidPatch)
	}
	return parseHunkBody(lines)
}

// ParseMulti 解析 replace_range 的多 hunk 输入。
// 它兼容首个隐式 hunk，但会限制 hunk 数量，避免一次工具调用生成过大编辑。
func ParseMulti(patch string) ([]Hunk, error) {
	lines, err := normalizePatchLines(patch)
	if err != nil {
		return nil, err
	}
	lines = normalizeLLMPatchEnvelope(lines)
	if len(lines) == 0 {
		return nil, ErrEmptyPatch
	}
	if !isPatchHeader(lines[0]) {
		return parseImplicitHunk(lines)
	}

	blocks, err := splitPatchHeaders(lines)
	if err != nil {
		return nil, err
	}
	if len(blocks) > maxPatchHunks {
		return nil, fmt.Errorf("%w: patch exceeds %d hunks", ErrInvalidPatch, maxPatchHunks)
	}
	hunks := make([]Hunk, 0, len(blocks))
	for _, block := range blocks {
		hunk, err := parseHunkBody(block)
		if err != nil {
			return nil, err
		}
		hunks = append(hunks, hunk)
	}
	return hunks, nil
}

// parseImplicitHunk 解析首段没有 "@@" 头的补丁。
// 第一段作为隐式 hunk，后续显式 hunk 仍走统一解析和数量上限。
func parseImplicitHunk(lines []string) ([]Hunk, error) {
	headerIndex := slices.IndexFunc(lines[1:], isPatchHeader)
	if headerIndex < 0 {
		hunk, err := parseHunkBody(lines)
		if err != nil {
			return nil, err
		}
		return []Hunk{hunk}, nil
	}

	firstHeader := headerIndex + 1
	first, err := parseHunkBody(lines[:firstHeader])
	if err != nil {
		return nil, err
	}
	blocks, err := splitPatchHeaders(lines[firstHeader:])
	if err != nil {
		return nil, err
	}
	if len(blocks)+1 > maxPatchHunks {
		return nil, fmt.Errorf("%w: patch exceeds %d hunks", ErrInvalidPatch, maxPatchHunks)
	}
	hunks := make([]Hunk, 0, len(blocks)+1)
	hunks = append(hunks, first)
	for _, block := range blocks {
		hunk, err := parseHunkBody(block)
		if err != nil {
			return nil, err
		}
		hunks = append(hunks, hunk)
	}
	return hunks, nil
}

func normalizeLLMPatchEnvelope(lines []string) []string {
	trimmed := dropApplyPatchEnvelope(lines)
	return dropUnifiedDiffFileHeaders(trimmed)
}

// dropApplyPatchEnvelope 移除 Codex apply_patch 外壳。
// 这让 LSP edit 工具复用 hunk 解析逻辑，而不接受文件路径层面的越权信息。
func dropApplyPatchEnvelope(lines []string) []string {
	if len(lines) == 0 || lines[0] != "*** Begin Patch" {
		return lines
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines[1:] {
		switch {
		case line == "*** End Patch":
			return out
		case strings.HasPrefix(line, "*** Update File:"), strings.HasPrefix(line, "*** Begin Patch"):
			continue
		default:
			out = append(out, line)
		}
	}
	return out
}

func dropUnifiedDiffFileHeaders(lines []string) []string {
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "--- ") || !strings.HasPrefix(lines[1], "+++ ") {
		return lines
	}
	return lines[2:]
}

func containsPatchHeader(lines []string) bool {
	return slices.ContainsFunc(lines, isPatchHeader)
}

// normalizePatchLines 统一换行并执行 patch 体积限制。
// 超过字节数或行数上限会立即报错，避免超大输入拖垮匹配器。
func normalizePatchLines(patch string) ([]string, error) {
	if patch == "" {
		return nil, ErrEmptyPatch
	}
	if len(patch) > maxPatchBytes {
		return nil, fmt.Errorf("%w: patch exceeds %d bytes", ErrInvalidPatch, maxPatchBytes)
	}
	normalized := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(patch)
	lines := strings.Split(normalized, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > maxPatchLines {
		return nil, fmt.Errorf("%w: patch exceeds %d lines", ErrInvalidPatch, maxPatchLines)
	}
	return lines, nil
}

// splitPatchHeaders 按 "@@" 头拆分显式 hunk。
// 头部格式会在拆分时先校验，避免后续解析阶段误接受坏块。
func splitPatchHeaders(lines []string) ([][]string, error) {
	var blocks [][]string
	var current []string
	for _, line := range lines {
		if isPatchHeader(line) {
			if _, err := parseHeaderLine(line); err != nil {
				return nil, err
			}
			if current != nil {
				blocks = append(blocks, current)
			}
			current = []string{line}
			continue
		}
		if current == nil {
			return nil, fmt.Errorf("%w: patch must begin with an @@ header", ErrInvalidPatch)
		}
		current = append(current, line)
	}
	if current != nil {
		blocks = append(blocks, current)
	}
	return blocks, nil
}

// parseHunkBody 把 hunk 正文拆成变更文本和前后锚点。
// 纯插入必须携带至少一行上下文，否则没有安全落点会被拒绝。
func parseHunkBody(lines []string) (Hunk, error) {
	if len(lines) == 0 {
		return Hunk{}, fmt.Errorf("%w: patch hunk body is empty", ErrInvalidPatch)
	}
	start := 0
	if isPatchHeader(lines[0]) {
		if _, err := parseHeaderLine(lines[0]); err != nil {
			return Hunk{}, err
		}
		start = 1
	}
	body := lines[start:]
	if len(body) == 0 {
		return Hunk{}, fmt.Errorf("%w: patch hunk body is empty", ErrInvalidPatch)
	}
	parsed, firstChange, lastChange, err := classifyBodyLines(body, start)
	if err != nil {
		return Hunk{}, err
	}

	before := takeTexts(parsed[:firstChange])
	after := takeTexts(parsed[lastChange+1:])
	oldLines, newLines, changeContext := foldChangeRegion(parsed[firstChange : lastChange+1])
	if len(oldLines) == 0 && len(before) == 0 && len(after) == 0 {
		return Hunk{}, fmt.Errorf("%w: insertion-only hunks require at least one context line for anchoring", ErrInvalidPatch)
	}
	return Hunk{
		OldText:       buildPatchText(oldLines),
		NewText:       buildPatchText(newLines),
		ChangeContext: changeContext,
		BeforeContext: before,
		AfterContext:  after,
	}, nil
}

// classifyBodyLines 校验并分类 hunk 正文的上下文、删除和新增行。
// 返回首尾变更位置，供 parseHunkBody 提取 before/after 锚点。
func classifyBodyLines(body []string, startOffset int) ([]patchBodyLine, int, int, error) {
	parsed := make([]patchBodyLine, 0, len(body))
	firstChange, lastChange := -1, -1
	for idx, line := range body {
		entry, err := parsePatchBodyLine(line)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("%w: line %d: %v", ErrInvalidPatch, startOffset+idx+1, err)
		}
		parsed = append(parsed, entry)
		if entry.kind != ' ' {
			if firstChange < 0 {
				firstChange = idx
			}
			lastChange = idx
		}
	}
	if firstChange < 0 {
		return nil, 0, 0, fmt.Errorf("%w: patch body must contain at least one changed line", ErrInvalidPatch)
	}
	return parsed, firstChange, lastChange, nil
}

func parseHeaderLine(line string) (string, error) {
	if !isPatchHeader(line) {
		return "", fmt.Errorf("%w: patch header must start with \"@@\"", ErrInvalidPatch)
	}
	rest := strings.TrimPrefix(line, "@@")
	return strings.TrimPrefix(rest, " "), nil
}

func isPatchHeader(line string) bool {
	return strings.HasPrefix(line, "@@")
}

func parsePatchBodyLine(line string) (patchBodyLine, error) {
	if line == "" {
		return patchBodyLine{}, errors.New("patch body lines must start with ' ', '-', or '+'")
	}
	prefix := line[0]
	switch prefix {
	case ' ', '-', '+':
		return patchBodyLine{kind: prefix, text: line[1:]}, nil
	default:
		return patchBodyLine{}, errors.New("patch body lines must start with ' ', '-', or '+'")
	}
}

func foldChangeRegion(lines []patchBodyLine) ([]string, []string, []string) {
	oldLines := make([]string, 0, len(lines))
	newLines := make([]string, 0, len(lines))
	changeContext := make([]string, 0, len(lines))
	for _, line := range lines {
		switch line.kind {
		case ' ':
			oldLines = append(oldLines, line.text)
			newLines = append(newLines, line.text)
			changeContext = append(changeContext, line.text)
		case '-':
			oldLines = append(oldLines, line.text)
		case '+':
			newLines = append(newLines, line.text)
		}
	}
	return oldLines, newLines, changeContext
}

func takeTexts(lines []patchBodyLine) []string {
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, line.text)
	}
	return out
}

func buildPatchText(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func splitPatchText(text string) []string {
	if text == "" {
		return nil
	}
	trimmed := strings.TrimSuffix(text, "\n")
	if trimmed == "" {
		return []string{""}
	}
	return strings.Split(trimmed, "\n")
}
