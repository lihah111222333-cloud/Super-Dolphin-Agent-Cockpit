// Package rag 提供文本分块（chunking）能力，用于将长文本切割为适合向量化的语义单元。
package rag

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrInvalidOptions = errors.New("invalid chunk options")
	ErrInvalidText    = errors.New("invalid text")
	ErrEmptyText      = errors.New("empty text")
)

// ChunkOptions 控制文本分块的 token 数量边界。
type ChunkOptions struct {
	TargetTokens int // 目标块大小
	MinTokens    int // 最小块大小，低于此值不切割
	MaxTokens    int // 最大块大小，超过此值强制切割
}

// Chunk 表示文本中一个连续的分块片段，StartToken/EndToken 为原始 token 索引。
type Chunk struct {
	Text       string
	StartToken int
	EndToken   int
}

// DefaultChunkOptions 返回适合 RAG 索引的默认 token 分块窗口。
func DefaultChunkOptions() ChunkOptions {
	return ChunkOptions{
		TargetTokens: 500,
		MinTokens:    400,
		MaxTokens:    650,
	}
}

// SplitText 将 UTF-8 文本拆成连续 chunk，优先在段落或标点边界切分。
// 选项非法、文本非法或无有效 token 时返回明确错误。
func SplitText(text string, opts ChunkOptions) ([]Chunk, error) {
	if err := validateOptions(opts); err != nil {
		return nil, err
	}

	tokens, err := tokenizeText(text)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("%w: text contains no tokens", ErrEmptyText)
	}

	chunks := make([]Chunk, 0, (len(tokens)+opts.TargetTokens-1)/opts.TargetTokens)
	startToken := 0
	startByte := 0

	for startToken < len(tokens) {
		if len(tokens)-startToken <= opts.MaxTokens {
			chunks = append(chunks, Chunk{
				Text:       text[startByte:],
				StartToken: startToken,
				EndToken:   len(tokens),
			})
			break
		}

		boundary, ok := bestBoundary(text, tokens, startToken, opts)
		if !ok {
			boundary = targetBoundary(text, tokens, startToken, opts)
		}

		chunks = append(chunks, Chunk{
			Text:       text[startByte:boundary.endByte],
			StartToken: startToken,
			EndToken:   boundary.endToken,
		})
		startToken = boundary.endToken
		startByte = boundary.endByte
	}

	return chunks, nil
}

// targetBoundary 在找不到自然边界时按目标 token 数强制切分。
func targetBoundary(text string, tokens []chunkToken, startToken int, opts ChunkOptions) boundaryCandidate {
	endToken := startToken + opts.TargetTokens
	endByte := len(text)
	if endToken < len(tokens) {
		endByte = tokens[endToken].startByte
	}
	return boundaryCandidate{
		endToken: endToken,
		endByte:  endByte,
	}
}

// validateOptions 校验 token 窗口的基本顺序关系，避免分块循环无法推进。
func validateOptions(opts ChunkOptions) error {
	if opts.TargetTokens <= 0 || opts.MinTokens <= 0 || opts.MaxTokens <= 0 {
		return fmt.Errorf("%w: token limits must be positive", ErrInvalidOptions)
	}
	if opts.MinTokens > opts.TargetTokens {
		return fmt.Errorf("%w: min tokens exceeds target tokens", ErrInvalidOptions)
	}
	if opts.TargetTokens > opts.MaxTokens {
		return fmt.Errorf("%w: target tokens exceeds max tokens", ErrInvalidOptions)
	}
	return nil
}

// chunkToken 记录 tokenizer 产生的粗粒度 token 在原文中的 byte 范围。
// 分块时使用 byte 范围回切原文，避免重新拼接导致内容丢失。
type chunkToken struct {
	startByte int // token 在原文中的起始 byte 下标
	endByte   int // token 在原文中的结束 byte 下标（左闭右开）
}

// tokenizeText 将文本切成粗粒度 token，并保留原文 byte 边界用于无损切片。
func tokenizeText(text string) ([]chunkToken, error) {
	if !utf8.ValidString(text) {
		return nil, fmt.Errorf("%w: input is not valid UTF-8", ErrInvalidText)
	}

	tokens := make([]chunkToken, 0, len(text)/4)
	for pos := 0; pos < len(text); {
		r, size := utf8.DecodeRuneInString(text[pos:])
		if unicode.IsSpace(r) {
			pos += size
			continue
		}

		start := pos
		pos += size
		if isWordRune(r) {
			for pos < len(text) {
				next, nextSize := utf8.DecodeRuneInString(text[pos:])
				if !isWordRune(next) {
					break
				}
				pos += nextSize
			}
		}
		for pos < len(text) {
			next, nextSize := utf8.DecodeRuneInString(text[pos:])
			if !isBoundaryRune(next) {
				break
			}
			pos += nextSize
		}

		tokens = append(tokens, chunkToken{startByte: start, endByte: pos})
	}
	return tokens, nil
}

// isWordRune 判断 rune 是否属于非 CJK 的连续词字符。
func isWordRune(r rune) bool {
	if isCJKRune(r) {
		return false
	}
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// isCJKRune 判断 rune 是否属于常见 CJK 脚本；CJK 文本按单字推进。
func isCJKRune(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

// boundaryCandidate 描述一个可用切分点及其排序依据。
// 选择时先看语义边界 rank，再看它离目标 token 数的距离。
type boundaryCandidate struct {
	endToken int // 切分点后的 token 下标
	endByte  int // 原文 byte 切分位置
	rank     int // 标点/段落边界优先级，数值越小越好
	distance int // 与目标 token 数的距离
}

// bestBoundary 在 min/max token 窗口内寻找最接近目标大小的自然切分点。
func bestBoundary(text string, tokens []chunkToken, startToken int, opts ChunkOptions) (boundaryCandidate, bool) {
	left := startToken + opts.MinTokens
	right := startToken + opts.MaxTokens
	if right > len(tokens) {
		right = len(tokens)
	}
	target := startToken + opts.TargetTokens

	var best boundaryCandidate
	found := false
	for endToken := left; endToken <= right; endToken++ {
		endByte, rank, ok := boundaryAt(text, tokens, endToken)
		if !ok {
			continue
		}
		candidate := boundaryCandidate{
			endToken: endToken,
			endByte:  endByte,
			rank:     rank,
			distance: abs(endToken - target),
		}
		if !found || betterBoundary(candidate, best) {
			best = candidate
			found = true
		}
	}
	return best, found
}

// betterBoundary 先偏好接近目标 token 数，再偏好更强语义边界。
func betterBoundary(candidate, best boundaryCandidate) bool {
	if candidate.distance != best.distance {
		return candidate.distance < best.distance
	}
	if candidate.rank != best.rank {
		return candidate.rank < best.rank
	}
	return candidate.endToken < best.endToken
}

// boundaryAt 判断指定 token 之后是否存在可用边界，并返回切分 byte 位置和边界等级。
func boundaryAt(text string, tokens []chunkToken, endToken int) (int, int, bool) {
	if endToken <= 0 || endToken > len(tokens) {
		return 0, 0, false
	}

	previous := tokens[endToken-1]
	nextStart := len(text)
	if endToken < len(tokens) {
		nextStart = tokens[endToken].startByte
	}
	if hasParagraphBreak(text[previous.endByte:nextStart]) {
		return nextStart, 0, true
	}

	r, ok := lastNonSpaceRune(text[:previous.endByte])
	if !ok {
		return 0, 0, false
	}
	rank, ok := boundaryRank(r)
	if !ok {
		return 0, 0, false
	}
	return previous.endByte, rank, true
}

// hasParagraphBreak 判断 token 间隔中是否包含段落空行，段落边界优先级最高。
func hasParagraphBreak(text string) bool {
	return strings.Count(text, "\n") >= 2
}

// lastNonSpaceRune 返回片段末尾最后一个非空白 rune，用于判断标点边界。
func lastNonSpaceRune(text string) (rune, bool) {
	for len(text) > 0 {
		r, size := utf8.DecodeLastRuneInString(text)
		if !unicode.IsSpace(r) {
			return r, true
		}
		text = text[:len(text)-size]
	}
	return 0, false
}

// boundaryRank 给不同标点边界分级，数值越小表示越适合切分。
func boundaryRank(r rune) (int, bool) {
	switch {
	case strings.ContainsRune("。！？.!?", r):
		return 1, true
	case strings.ContainsRune("；;", r):
		return 2, true
	case strings.ContainsRune("，,", r):
		return 3, true
	case strings.ContainsRune(")）]】\"”'’", r):
		return 4, true
	default:
		return 0, false
	}
}

// isBoundaryRune 判断 rune 是否是 tokenizer 应附着到前一个 token 的边界标点。
func isBoundaryRune(r rune) bool {
	_, ok := boundaryRank(r)
	return ok
}

// abs 返回整数绝对值，用于比较候选边界与目标 token 数的距离。
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
