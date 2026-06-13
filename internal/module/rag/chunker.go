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

type ChunkOptions struct {
	TargetTokens int
	MinTokens    int
	MaxTokens    int
}

type Chunk struct {
	Text       string
	StartToken int
	EndToken   int
}

// DefaultChunkOptions 处理defaultchunk选项。
func DefaultChunkOptions() ChunkOptions {
	return ChunkOptions{
		TargetTokens: 500,
		MinTokens:    400,
		MaxTokens:    650,
	}
}

// SplitText 拆分文本。
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

// validateOptions 校验选项。
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

type chunkToken struct {
	startByte int
	endByte   int
}

// tokenizeText 处理tokenize文本。
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

func isWordRune(r rune) bool {
	if isCJKRune(r) {
		return false
	}
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func isCJKRune(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

type boundaryCandidate struct {
	endToken int
	endByte  int
	rank     int
	distance int
}

// bestBoundary 处理最佳boundary。
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

func betterBoundary(candidate, best boundaryCandidate) bool {
	if candidate.distance != best.distance {
		return candidate.distance < best.distance
	}
	if candidate.rank != best.rank {
		return candidate.rank < best.rank
	}
	return candidate.endToken < best.endToken
}

// boundaryAt 处理boundaryat。
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

func hasParagraphBreak(text string) bool {
	return strings.Count(text, "\n") >= 2
}

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

func isBoundaryRune(r rune) bool {
	_, ok := boundaryRank(r)
	return ok
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
