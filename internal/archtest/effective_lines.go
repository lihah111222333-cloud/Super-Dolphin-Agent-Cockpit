package archtest

import "strings"

func clampEffectiveLineEnd(lineCount, end int) int {
	if end < 0 || end > lineCount {
		return lineCount
	}
	return end
}

func effectiveLineDelta(line string, inBlock bool) (int, bool) {
	if line == "" {
		return 0, inBlock
	}
	if inBlock {
		return blockCommentLineDelta(line)
	}
	if strings.HasPrefix(line, "//") {
		return 0, false
	}
	if strings.HasPrefix(line, "/*") {
		return leadingBlockCommentLineDelta(line)
	}
	return 1, false
}

func blockCommentLineDelta(line string) (int, bool) {
	_, after, found := strings.Cut(line, "*/")
	if !found {
		return 0, true
	}
	if hasTrailingCode(after) {
		return 1, false
	}
	return 0, false
}

func leadingBlockCommentLineDelta(line string) (int, bool) {
	_, after, found := strings.Cut(line[2:], "*/")
	if !found {
		return 0, true
	}
	if hasTrailingCode(after) {
		return 1, false
	}
	return 0, false
}

func hasTrailingCode(line string) bool {
	rest := strings.TrimSpace(line)
	return rest != "" && !strings.HasPrefix(rest, "//")
}
