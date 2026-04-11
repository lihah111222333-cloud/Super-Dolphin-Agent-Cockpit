package difftracker

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

const rawDiffSessionKey = "__raw__"

func mergeIntoSession(session *agentDiffSession, diffText string, files []string) bool {
	return mergeSessionDiff(session, diffText, uniqueSorted(files))
}

func buildCumulativeDiff(session *agentDiffSession) string {
	if session == nil || len(session.files) == 0 {
		return ""
	}
	paths := sessionFilePaths(session)
	parts := make([]string, 0, len(paths))
	for _, path := range paths {
		part := strings.TrimSpace(session.files[path].Diff)
		if part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n") + "\n"
}

func mergeSessionDiff(session *agentDiffSession, diffText string, files []string) bool {
	if session == nil {
		return false
	}
	ensureSessionFiles(session)
	blocks := splitUnifiedDiffBlocks(diffText)
	touched, rawOnly := mergeTargets(diffText, files, blocks)
	if rawOnly {
		return mergeRawDiff(session, diffText)
	}
	if len(touched) == 0 {
		return false
	}
	return applyBlocks(session, diffText, touched, blocks)
}

func ensureSessionFiles(session *agentDiffSession) {
	if session.files == nil {
		session.files = make(map[string]*FileDiff)
	}
}

func mergeTargets(diffText string, files []string, blocks map[string]string) ([]string, bool) {
	touched := uniqueSorted(files)
	if len(touched) > 0 {
		return touched, false
	}
	if strings.TrimSpace(diffText) == "" {
		return nil, false
	}
	if len(blocks) == 0 {
		return nil, true
	}
	return sortedBlockPaths(blocks), false
}

func applyBlocks(session *agentDiffSession, diffText string, touched []string, blocks map[string]string) bool {
	changed := false
	for _, path := range touched {
		block := resolvedBlock(diffText, path, touched, blocks)
		if block == "" {
			changed = deleteSessionBlock(session, path) || changed
			continue
		}
		changed = upsertSessionBlock(session, path, block) || changed
	}
	return changed
}

func resolvedBlock(diffText, path string, touched []string, blocks map[string]string) string {
	block := strings.TrimSpace(blocks[path])
	if block == "" && len(touched) == 1 && len(blocks) == 0 {
		block = strings.TrimSpace(ensureUnifiedHeaders(diffText, []string{path}))
	}
	return ensureTrailingNewline(block)
}

func deleteSessionBlock(session *agentDiffSession, path string) bool {
	if _, ok := session.files[path]; !ok {
		return false
	}
	delete(session.files, path)
	return true
}

func upsertSessionBlock(session *agentDiffSession, path, block string) bool {
	current, ok := session.files[path]
	if ok && current != nil && current.Diff == block {
		return false
	}
	session.files[path] = &FileDiff{Path: path, Diff: block}
	return true
}

func mergeRawDiff(session *agentDiffSession, diffText string) bool {
	block := ensureTrailingNewline(strings.TrimSpace(diffText))
	if block == "" {
		return deleteSessionBlock(session, rawDiffSessionKey)
	}
	return upsertSessionBlock(session, rawDiffSessionKey, block)
}

func sessionFilePaths(session *agentDiffSession) []string {
	paths := make([]string, 0, len(session.files))
	for path, diff := range session.files {
		if diff != nil && strings.TrimSpace(diff.Diff) != "" {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths
}

func splitUnifiedDiffBlocks(diffText string) map[string]string {
	lines := splitLines(diffText)
	blocks := make(map[string]string)
	for i := 0; i < len(lines); {
		if !isUnifiedHeader(lines, i) {
			i++
			continue
		}
		start := i
		path := diffPath(lines[i], lines[i+1])
		i += 2
		for i < len(lines) && !isUnifiedHeader(lines, i) {
			i++
		}
		block := strings.Join(lines[start:i], "")
		if path != "" && strings.TrimSpace(block) != "" {
			blocks[path] = ensureTrailingNewline(block)
		}
	}
	return blocks
}

func sortedBlockPaths(blocks map[string]string) []string {
	paths := make([]string, 0, len(blocks))
	for path := range blocks {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func buildUnifiedDiffBlock(path, before, after string) string {
	return buildUnifiedDiffBlockWithState(path, before != "", before, after != "", after)
}

func buildUnifiedDiffBlockWithState(path string, tracked bool, before string, afterExists bool, after string) string {
	clean := normalizeDiffPath(path)
	if clean == "" || (tracked == afterExists && before == after) {
		return ""
	}
	fromFile := "/dev/null"
	toFile := "/dev/null"
	if tracked {
		fromFile = "a/" + clean
	}
	if afterExists {
		toFile = "b/" + clean
	}
	text, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(before),
		B:        difflib.SplitLines(after),
		FromFile: fromFile,
		ToFile:   toFile,
		Context:  3,
	})
	if err != nil || text == "" {
		return ""
	}
	return ensureTrailingNewline(text)
}

func normalizeDiffPath(path string) string {
	clean := filepath.ToSlash(filepath.Clean(strings.Trim(strings.TrimSpace(path), `"`)))
	switch clean {
	case "", ".", "/", "/dev/null":
		return ""
	default:
		clean = strings.TrimPrefix(clean, "./")
		return strings.TrimPrefix(strings.TrimPrefix(clean, "a/"), "b/")
	}
}

func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.SplitAfter(normalizeNewlines(text), "\n")
	if lines[len(lines)-1] == "" {
		return lines[:len(lines)-1]
	}
	return lines
}

func isUnifiedHeader(lines []string, index int) bool {
	if index+1 >= len(lines) {
		return false
	}
	return strings.HasPrefix(lines[index], "--- ") && strings.HasPrefix(lines[index+1], "+++ ")
}

func diffPath(fromHeader, toHeader string) string {
	candidates := []string{
		headerPath(strings.TrimSpace(strings.TrimPrefix(toHeader, "+++ "))),
		headerPath(strings.TrimSpace(strings.TrimPrefix(fromHeader, "--- "))),
	}
	for _, candidate := range candidates {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func headerPath(raw string) string {
	if raw == "" || raw == "/dev/null" {
		return ""
	}
	if strings.HasPrefix(raw, "a/") || strings.HasPrefix(raw, "b/") {
		raw = raw[2:]
	}
	return normalizeDiffPath(raw)
}

func ensureTrailingNewline(text string) string {
	trimmed := strings.TrimRight(text, "\n")
	if trimmed == "" {
		return ""
	}
	return trimmed + "\n"
}
