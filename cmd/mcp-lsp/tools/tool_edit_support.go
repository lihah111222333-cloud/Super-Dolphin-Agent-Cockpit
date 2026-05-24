package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	editpkg "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/edit"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/search"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

type lineEndingStyle string

const (
	lineEndingLF   lineEndingStyle = "\n"
	lineEndingCRLF lineEndingStyle = "\r\n"
)

type editableFile struct {
	content    string
	raw        string
	mode       os.FileMode
	lineEnding lineEndingStyle
}

func (f editableFile) diskContent(content string) string {
	return restoreLineEndings(content, f.lineEnding)
}

func parsePatchHunks(patch string) ([]editpkg.Hunk, error) {
	patch = normalizeLineEndings(patch)
	hunks, err := editpkg.ParseMulti(patch)
	if err != nil {
		return nil, err
	}
	if len(hunks) == 1 {
		single, err := editpkg.Parse(patch)
		if err != nil {
			return nil, err
		}
		return normalizeHunks([]editpkg.Hunk{single}), nil
	}
	return normalizeHunks(hunks), nil
}

func resolveMatchMode(content string, hunk editpkg.Hunk, fallback string) string {
	lines := splitLines(content)
	pattern := splitLines(hunk.OldText)
	if len(pattern) == 0 {
		return fallback
	}
	_, mode, err := editpkg.SeekSequence(lines, pattern, 0)
	if err != nil {
		return fallback
	}
	return string(mode)
}

func resolveFilePath(ctx context.Context, path string) (string, error) {
	pathInfo, err := toolResolvePath(ctx, path)
	if err != nil {
		return "", err
	}
	return pathInfo.AbsPath, nil
}

func resolveWorkspacePathInRoots(root string, roots []string, uri string) (string, error) {
	filePath, err := requireFilePath(uri)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(strings.TrimSpace(filePath), "file://") {
		resolved, err := format.AbsolutePathFromURI(filePath)
		if err != nil {
			return "", err
		}
		filePath = resolved
	}
	pathInfo, err := search.ResolvePathInRoots(root, roots, filePath)
	if err != nil {
		return "", err
	}
	resolved := pathInfo.AbsPath
	if evaluated, err := filepath.EvalSymlinks(resolved); err == nil {
		resolved = filepath.Clean(evaluated)
	}
	allowedRoots, err := search.NormalizeRootSet(root, roots)
	if err != nil {
		return "", err
	}
	if !pathWithinAnyRoot(allowedRoots, resolved) {
		return "", fmt.Errorf("path %q is outside workspace roots [%s]", resolved, strings.Join(allowedRoots, ", "))
	}
	return resolved, nil
}

func pathWithinAnyRoot(roots []string, target string) bool {
	for _, root := range roots {
		if platformshared.ContainsPath(root, target) {
			return true
		}
	}
	return false
}

func readFileWithMode(path string) (editableFile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return editableFile{}, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return editableFile{}, err
	}
	raw := string(content)
	return editableFile{
		content:    normalizeLineEndings(raw),
		raw:        raw,
		mode:       info.Mode().Perm(),
		lineEnding: detectLineEnding(raw),
	}, nil
}

func normalizeHunks(hunks []editpkg.Hunk) []editpkg.Hunk {
	for idx := range hunks {
		hunks[idx].OldText = normalizeLineEndings(hunks[idx].OldText)
		hunks[idx].NewText = normalizeLineEndings(hunks[idx].NewText)
	}
	return hunks
}

func normalizeLineEndings(content string) string {
	return strings.ReplaceAll(content, "\r\n", "\n")
}

func detectLineEnding(content string) lineEndingStyle {
	if strings.Contains(content, string(lineEndingCRLF)) {
		return lineEndingCRLF
	}
	return lineEndingLF
}

func restoreLineEndings(content string, lineEnding lineEndingStyle) string {
	if lineEnding == lineEndingCRLF {
		return strings.ReplaceAll(content, "\n", string(lineEndingCRLF))
	}
	return content
}

func functionBody(content string, start int, end int) string {
	lines := splitNormalizedLines(content)
	if start <= 0 || end < start || end > len(lines) {
		return ""
	}
	body := strings.Join(lines[start-1:end], "\n")
	if len(body) <= replaceRangeFuncBodyMax {
		return body
	}
	return body[:replaceRangeFuncBodyMax] + "\n...(truncated)"
}

func countLines(content string) int {
	lines := splitNormalizedLines(content)
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		return len(lines) - 1
	}
	return len(lines)
}

func splitLines(content string) []string {
	lines := splitNormalizedLines(content)
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

func joinHunkOldText(hunks []editpkg.Hunk) string {
	items := make([]string, 0, len(hunks))
	for _, hunk := range hunks {
		items = append(items, hunk.OldText)
	}
	return strings.Join(items, "\n")
}

func joinHunkNewText(hunks []editpkg.Hunk) string {
	items := make([]string, 0, len(hunks))
	for _, hunk := range hunks {
		items = append(items, hunk.NewText)
	}
	return strings.Join(items, "\n")
}

func uniqueStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; ok || item == "" {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}
