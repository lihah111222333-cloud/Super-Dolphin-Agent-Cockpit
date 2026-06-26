// Package edit 提供 patch 解析与匹配功能，用于 LSP replace_range 编辑操作。
package edit

import (
	"fmt"
	"sort"
	"strings"
)

// Match 描述一个 replace_range hunk 在目标内容中的最终落点。
// 工具层会把行号、偏移量和回显上下文返回给调用方，用于确认补丁实际作用范围。
type Match struct {
	MatchedBy           string
	ResolvedStartOffset int
	ResolvedEndOffset   int
	ResolvedLSPLine     int
	AffectedStartLine   int
	AffectedEndLine     int
	EditContext         string
}

type matchCandidate struct {
	startOffset    int
	endOffset      int
	startLine      int
	endLine        int
	startLineIndex int
	endLineIndex   int
	matchedBy      string
}

// AmbiguousMatchError 表示一个 hunk 命中多个候选位置。
// 候选列表会截断到固定上限，工具层可放进错误元数据，引导调用方补充更窄上下文。
type AmbiguousMatchError struct {
	HunkIndex  int
	Candidates []CandidateLocation
}

// CandidateLocation 是歧义匹配暴露给工具调用方的线级候选位置。
// 它只包含展示和消歧需要的行范围与匹配策略，不泄露完整文件内容。
type CandidateLocation struct {
	StartLine int
	EndLine   int
	MatchedBy string
}

const ambiguousCandidateCap = 5

// Error 返回带 hunk 序号和候选数量的歧义错误文本。
func (e *AmbiguousMatchError) Error() string {
	return fmt.Sprintf("%s: hunk %d matched %d locations", ErrAmbiguousMatch.Error(), e.HunkIndex+1, len(e.Candidates))
}

// Unwrap 保留 ErrAmbiguousMatch，便于上层用 errors.Is 做结构化分支。
func (e *AmbiguousMatchError) Unwrap() error { return ErrAmbiguousMatch }

func newAmbiguousMatchError(hunkIndex int, candidates []matchCandidate) *AmbiguousMatchError {
	out := make([]CandidateLocation, 0, min(len(candidates), ambiguousCandidateCap))
	seen := make(map[int]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, ok := seen[candidate.startLine]; ok {
			continue
		}
		seen[candidate.startLine] = struct{}{}
		out = append(out, CandidateLocation{
			StartLine: candidate.startLine,
			EndLine:   candidate.endLine,
			MatchedBy: candidate.matchedBy,
		})
		if len(out) >= ambiguousCandidateCap {
			break
		}
	}
	return &AmbiguousMatchError{HunkIndex: hunkIndex, Candidates: out}
}

// MatchContext 按顺序把 replace_range hunks 匹配到目标内容。
// 每个 hunk 都基于前一个 hunk 应用后的工作副本继续匹配，失败或歧义时直接返回错误。
func MatchContext(content string, hunks []Hunk) ([]Match, error) {
	if err := GuardContentAndReplacement(content, ""); err != nil {
		return nil, err
	}
	matches := make([]Match, 0, len(hunks))
	working := content
	for idx, hunk := range hunks {
		if err := GuardContentAndReplacement(working, hunk.NewText); err != nil {
			return nil, err
		}
		candidate, err := resolveContextMatch(working, hunk, idx)
		if err != nil {
			return nil, err
		}
		editContext, affectedStart, affectedEnd, err := BuildEditContext(working, candidate.startOffset, candidate.endOffset, hunk.NewText)
		if err != nil {
			return nil, err
		}
		matches = append(matches, Match{
			MatchedBy:           candidate.matchedBy,
			ResolvedStartOffset: candidate.startOffset,
			ResolvedEndOffset:   candidate.endOffset,
			ResolvedLSPLine:     candidate.startLine,
			AffectedStartLine:   affectedStart,
			AffectedEndLine:     affectedEnd,
			EditContext:         editContext,
		})
		working = working[:candidate.startOffset] + hunk.NewText + working[candidate.endOffset:]
	}
	return matches, nil
}

func resolveContextMatch(content string, hunk Hunk, hunkIndex int) (matchCandidate, error) {
	index, err := indexContent(content)
	if err != nil {
		return matchCandidate{}, err
	}
	anchors := resolveContextAnchorStarts(index.lines, hunk.BeforeContext)
	candidates := collectLineSequenceCandidates(index, hunk, anchors)
	candidates = filterContextCandidates(index.lines, hunk, candidates)
	if len(candidates) == 0 {
		candidates = collectRawSubstringCandidates(index, hunk)
		candidates = filterContextCandidates(index.lines, hunk, candidates)
	}
	if len(candidates) == 0 {
		return matchCandidate{}, fmt.Errorf("hunk %d: %w: no candidate matched — re-read the target region with file action=read_file and copy exact text into patch context", hunkIndex+1, ErrSequenceNotFound)
	}
	if len(candidates) > 1 {
		return matchCandidate{}, newAmbiguousMatchError(hunkIndex, candidates)
	}
	return candidates[0], nil
}

// resolveContextAnchorStarts 根据 before 上下文推导允许插入或替换的起始行。
// 返回值用于过滤候选位置，避免相同 old_text 在文件其他位置被误命中。
func resolveContextAnchorStarts(lines []string, before []string) map[int]struct{} {
	if len(before) == 0 {
		return nil
	}
	anchors := make(map[int]struct{})
	for _, mode := range allSeekModes() {
		limit := len(lines) - len(before)
		if limit < 0 {
			return anchors
		}
		for idx := 0; idx <= limit; idx++ {
			if sequenceMatchAt(lines, before, idx, mode) {
				anchors[idx+len(before)] = struct{}{}
			}
		}
	}
	return anchors
}

// collectLineSequenceCandidates 用多种宽松匹配模式收集 old_text 候选位置。
// 如果 before 上下文存在，只保留锚点后的候选，防止跨块替换。
func collectLineSequenceCandidates(index contentIndex, hunk Hunk, anchors map[int]struct{}) []matchCandidate {
	oldLines := splitPatchText(hunk.OldText)
	if len(oldLines) == 0 {
		return collectPureInsertionCandidates(index, hunk, anchors)
	}
	positions, mode := collectSequenceMatches(index.lines, oldLines)
	if len(positions) == 0 && len(oldLines) > 1 && oldLines[len(oldLines)-1] == "" {
		oldLines = oldLines[:len(oldLines)-1]
		positions, mode = collectSequenceMatches(index.lines, oldLines)
	}
	if len(positions) == 0 {
		return nil
	}
	candidates := make([]matchCandidate, 0, len(positions))
	for _, pos := range positions {
		if anchors != nil {
			if _, ok := anchors[pos]; !ok {
				continue
			}
		}
		endIdx := pos + len(oldLines)
		candidates = append(candidates, matchCandidate{
			startOffset:    index.start[pos],
			endOffset:      index.end[endIdx-1],
			startLine:      pos + 1,
			endLine:        endIdx,
			startLineIndex: pos,
			endLineIndex:   endIdx,
			matchedBy:      string(mode),
		})
	}
	return dedupeCandidates(candidates)
}

// collectPureInsertionCandidates 处理 OldText 为空的纯插入 hunk。
// 纯插入必须依赖 before 或 after 上下文定位，候选位置的 startOffset 与 endOffset 相同。
func collectPureInsertionCandidates(index contentIndex, hunk Hunk, anchors map[int]struct{}) []matchCandidate {
	if len(hunk.BeforeContext) == 0 && len(hunk.AfterContext) == 0 {
		return nil
	}
	insertLines := resolveInsertionLines(index, hunk, anchors)
	if len(insertLines) == 0 {
		return nil
	}
	return buildInsertionCandidates(index, insertLines)
}

func resolveInsertionLines(index contentIndex, hunk Hunk, anchors map[int]struct{}) []int {
	if len(hunk.BeforeContext) > 0 && anchors != nil {
		lines := make([]int, 0, len(anchors))
		for lineIdx := range anchors {
			lines = append(lines, lineIdx)
		}
		return lines
	}
	return findAfterContextInsertions(index.lines, hunk.AfterContext)
}

// findAfterContextInsertions 根据 after 上下文查找插入点。
// 返回的是 after 块开始前的位置，供纯插入 hunk 构造零宽替换候选。
func findAfterContextInsertions(lines []string, afterCtx []string) []int {
	if len(afterCtx) == 0 {
		return nil
	}
	var result []int
	for _, mode := range allSeekModes() {
		limit := len(lines) - len(afterCtx)
		for idx := 0; idx <= limit; idx++ {
			if sequenceMatchAt(lines, afterCtx, idx, mode) {
				result = append(result, idx)
			}
		}
		if len(result) > 0 {
			return result
		}
	}
	return nil
}

func buildInsertionCandidates(index contentIndex, insertLines []int) []matchCandidate {
	candidates := make([]matchCandidate, 0, len(insertLines))
	seen := make(map[int]struct{}, len(insertLines))
	for _, lineIdx := range insertLines {
		if _, ok := seen[lineIdx]; ok {
			continue
		}
		seen[lineIdx] = struct{}{}
		offset := insertionOffset(index, lineIdx)
		candidates = append(candidates, matchCandidate{
			startOffset:    offset,
			endOffset:      offset,
			startLine:      lineIdx + 1,
			endLine:        lineIdx,
			startLineIndex: lineIdx,
			endLineIndex:   lineIdx,
			matchedBy:      "pure_insertion",
		})
	}
	return dedupeCandidates(candidates)
}

func insertionOffset(index contentIndex, lineIdx int) int {
	if lineIdx >= len(index.lines) {
		return len(index.raw)
	}
	return index.start[lineIdx]
}

func collectRawSubstringCandidates(index contentIndex, hunk Hunk) []matchCandidate {
	if hunk.OldText == "" {
		return nil
	}
	var candidates []matchCandidate
	for start := 0; ; {
		rel := strings.Index(index.raw[start:], hunk.OldText)
		if rel < 0 {
			break
		}
		startOffset := start + rel
		endOffset := startOffset + len(hunk.OldText)
		startLine := offsetToLineIndex(index.start, startOffset)
		endLine := offsetToEndLineIndex(index.end, endOffset)
		candidates = append(candidates, matchCandidate{
			startOffset:    startOffset,
			endOffset:      endOffset,
			startLine:      startLine + 1,
			endLine:        endLine,
			startLineIndex: startLine,
			endLineIndex:   endLine,
			matchedBy:      "substring_exact",
		})
		start = startOffset + 1
	}
	return dedupeCandidates(candidates)
}

func filterContextCandidates(lines []string, hunk Hunk, candidates []matchCandidate) []matchCandidate {
	if len(candidates) == 0 {
		return nil
	}
	filtered := make([]matchCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !contextMatches(lines, hunk.BeforeContext, candidate.startLineIndex-len(hunk.BeforeContext)) {
			continue
		}
		if !contextMatches(lines, hunk.AfterContext, candidate.endLineIndex) {
			continue
		}
		filtered = append(filtered, candidate)
	}
	return dedupeCandidates(filtered)
}

// contextMatches 比较候选位置后的上下文是否仍与 hunk 锚点一致。
// 它只看行级文本，不修改工作副本，供候选过滤阶段消歧。
func contextMatches(lines []string, context []string, start int) bool {
	if len(context) == 0 {
		return true
	}
	if start < 0 || start+len(context) > len(lines) {
		return false
	}
	for _, mode := range allSeekModes() {
		if sequenceMatchAt(lines, context, start, mode) {
			return true
		}
	}
	return false
}

// dedupeCandidates 去重候选项。
func dedupeCandidates(candidates []matchCandidate) []matchCandidate {
	if len(candidates) < 2 {
		return candidates
	}
	sort.Slice(candidates, func(i int, j int) bool {
		if candidates[i].startOffset != candidates[j].startOffset {
			return candidates[i].startOffset < candidates[j].startOffset
		}
		if candidates[i].endOffset != candidates[j].endOffset {
			return candidates[i].endOffset < candidates[j].endOffset
		}
		return candidates[i].matchedBy < candidates[j].matchedBy
	})
	deduped := candidates[:1]
	for _, candidate := range candidates[1:] {
		prev := deduped[len(deduped)-1]
		if prev.startOffset == candidate.startOffset && prev.endOffset == candidate.endOffset {
			continue
		}
		deduped = append(deduped, candidate)
	}
	return deduped
}

func offsetToLineIndex(starts []int, offset int) int {
	if len(starts) == 0 {
		return 0
	}
	pos := sort.Search(len(starts), func(i int) bool {
		return starts[i] > offset
	})
	if pos == 0 {
		return 0
	}
	return pos - 1
}

func offsetToEndLineIndex(ends []int, offset int) int {
	if len(ends) == 0 {
		return 1
	}
	pos := sort.Search(len(ends), func(i int) bool {
		return ends[i] >= offset
	})
	if pos >= len(ends) {
		return len(ends)
	}
	return pos + 1
}
