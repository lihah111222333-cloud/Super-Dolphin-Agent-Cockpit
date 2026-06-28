package tools

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	editpkg "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/edit"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/format"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/search"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

// lineEndingStyle 记录源文件原始换行风格，编辑后按原样写回磁盘。
type lineEndingStyle string

const (
	lineEndingLF   lineEndingStyle = "\n"
	lineEndingCRLF lineEndingStyle = "\r\n"
)

// editableFile 保存可编辑文件的规范化内容、原始内容和权限模式。
type editableFile struct {
	content    string
	raw        string
	mode       os.FileMode
	lineEnding lineEndingStyle
}

// diskContent 把规范化后的内容恢复成文件原有换行风格。
func (f editableFile) diskContent(content string) string {
	return restoreLineEndings(content, f.lineEnding)
}

// parsePatchHunks 解析单 hunk 或多 hunk patch，并统一换行为 LF。
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

// resolveMatchMode 判断 hunk 在当前内容中的匹配方式，失败时返回调用方提供的 fallback。
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

// resolveFilePath 把工具路径解析成绝对路径。
func resolveFilePath(ctx context.Context, path string) (string, error) {
	pathInfo, err := toolResolvePath(ctx, path)
	if err != nil {
		return "", err
	}
	return pathInfo.AbsPath, nil
}

// normalizeFilePathTarget 规范化 file_path 入参，支持 file:// URI 和已转义绝对路径。
func normalizeFilePathTarget(raw string) (string, error) {
	filePath, err := requireFilePath(raw)
	if err != nil {
		return "", err
	}
	trimmed := strings.TrimSpace(filePath)
	if hasFileURIScheme(trimmed) {
		resolved, err := format.AbsolutePathFromURI(trimmed)
		if err != nil {
			return "", err
		}
		return resolved, nil
	}
	if filepath.IsAbs(trimmed) && strings.Contains(trimmed, "%") {
		unescaped, err := url.PathUnescape(trimmed)
		if err != nil {
			return "", fmt.Errorf("decode file_path %q: %w", trimmed, err)
		}
		if strings.TrimSpace(unescaped) == "" {
			return "", fmt.Errorf("decode file_path %q: empty path", trimmed)
		}
		return unescaped, nil
	}
	return filePath, nil
}

// hasFileURIScheme 判断路径是否显式使用 file URI scheme。
func hasFileURIScheme(path string) bool {
	scheme, _, ok := strings.Cut(path, ":")
	return ok && strings.EqualFold(scheme, "file")
}

// resolveWorkspacePathInRoots 在允许的工作区根中解析 direct 写入目标。
// EvalSymlinks 后仍要重新做根目录校验，避免符号链接逃逸。
func resolveWorkspacePathInRoots(root string, roots []string, uri string) (string, error) {
	filePath, err := normalizeFilePathTarget(uri)
	if err != nil {
		return "", err
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
	if pathWithinAnyRoot(allowedRoots, resolved) {
		return resolved, nil
	}
	appRoots, err := platformshared.AppManagedDataRoots()
	if err != nil {
		return "", err
	}
	if pathWithinAnyRoot(appRoots, resolved) {
		return "", fmt.Errorf("path %q is inside app-managed data roots and requires app-managed write capability", resolved)
	}
	return "", fmt.Errorf("path %q is outside workspace roots [%s]", resolved, strings.Join(allowedRoots, ", "))
}

// trustedWorkspaceEditRoots 读取并规范化 WorkspaceEdit 可写入的可信根目录。
// rename/code_action 属于高风险批量写入口，缺少可信 roots 时必须在请求 LSP 前失败。
func trustedWorkspaceEditRoots(ctx context.Context) ([]string, error) {
	root, roots, err := toolWorkspaceRoots(ctx)
	if err != nil {
		return nil, fmt.Errorf("workspace edit requires trusted workspace roots: %w", err)
	}
	trustedRoots, err := search.NormalizeRootSet(root, roots)
	if err != nil {
		return nil, fmt.Errorf("workspace edit trusted roots: %w", err)
	}
	if len(trustedRoots) == 0 {
		return nil, fmt.Errorf("workspace edit requires trusted workspace roots: %w", common.ErrMissingWorkspaceRoots)
	}
	return trustedRoots, nil
}

// validateWorkspaceEditFiles 预校验 WorkspaceEdit 的全部目标文件，确保批量编辑 all-or-none。
// 校验会收集所有违规 URI；调用方必须在任何磁盘写入前调用它。
func validateWorkspaceEditFiles(ctx context.Context, roots []string, changes map[string][]protocol.TextEdit) error {
	if len(changes) == 0 {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("workspace edit requires trusted workspace roots: %w", common.ErrMissingWorkspaceRoots)
	}
	trustedRoots, err := normalizeWorkspaceEditRoots(roots)
	if err != nil {
		return err
	}
	violations := make([]string, 0)
	for uri := range changes {
		if err := validateWorkspaceEditURI(trustedRoots, uri); err != nil {
			violations = append(violations, fmt.Sprintf(`{"uri":%q,"reason":%q}`, uri, err.Error()))
		}
	}
	if len(violations) == 0 {
		return nil
	}
	sort.Strings(violations)
	return fmt.Errorf("workspace edit rejected: invalid_target_files=[%s]", strings.Join(violations, ", "))
}

// normalizeWorkspaceEditRoots 确认调用方传入的是非空可信根，并复用 search 的根规范化规则。
func normalizeWorkspaceEditRoots(roots []string) ([]string, error) {
	if len(roots) == 0 {
		return nil, fmt.Errorf("workspace edit requires trusted workspace roots: %w", common.ErrMissingWorkspaceRoots)
	}
	trustedRoots, err := search.NormalizeRootSet(roots[0], roots[1:])
	if err != nil {
		return nil, fmt.Errorf("workspace edit trusted roots: %w", err)
	}
	if len(trustedRoots) == 0 {
		return nil, fmt.Errorf("workspace edit requires trusted workspace roots: %w", common.ErrMissingWorkspaceRoots)
	}
	return trustedRoots, nil
}

// validateWorkspaceEditURI 确认单个 LSP file URI 指向可信根内的普通文件。
func validateWorkspaceEditURI(roots []string, uri string) error {
	absPath, err := format.AbsolutePathFromURI(uri)
	if err != nil {
		return err
	}
	pathInfo, err := search.ResolvePathInRoots(roots[0], roots[1:], absPath)
	if err != nil {
		return err
	}
	path := pathInfo.AbsPath
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("resolve symlink %s: %w", path, err)
		}
		resolved = filepath.Clean(resolved)
		if !pathWithinAnyRoot(roots, resolved) {
			return fmt.Errorf("symlink target %s is outside workspace roots [%s]", resolved, strings.Join(roots, ", "))
		}
		info, err = os.Stat(path)
		if err != nil {
			return fmt.Errorf("stat symlink target %s: %w", path, err)
		}
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("target %s must reference a regular file", path)
	}
	return nil
}

// pathWithinAnyRoot 判断目标路径是否位于任一允许根目录下。
func pathWithinAnyRoot(roots []string, target string) bool {
	for _, root := range roots {
		if platformshared.ContainsPath(root, target) {
			return true
		}
	}
	return false
}

// readFileWithMode 读取文件并记录权限、原始文本和换行风格，供编辑失败回滚。
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

// normalizeHunks 统一 hunk 新旧文本的换行风格。
func normalizeHunks(hunks []editpkg.Hunk) []editpkg.Hunk {
	for idx := range hunks {
		hunks[idx].OldText = normalizeLineEndings(hunks[idx].OldText)
		hunks[idx].NewText = normalizeLineEndings(hunks[idx].NewText)
	}
	return hunks
}

// normalizeLineEndings 把 CRLF 文本转为内部统一的 LF。
func normalizeLineEndings(content string) string {
	return strings.ReplaceAll(content, "\r\n", "\n")
}

// detectLineEnding 识别文件主要换行风格；没有 CRLF 时按 LF 写回。
func detectLineEnding(content string) lineEndingStyle {
	if strings.Contains(content, string(lineEndingCRLF)) {
		return lineEndingCRLF
	}
	return lineEndingLF
}

// restoreLineEndings 把内部 LF 文本恢复成目标换行风格。
func restoreLineEndings(content string, lineEnding lineEndingStyle) string {
	if lineEnding == lineEndingCRLF {
		return strings.ReplaceAll(content, "\n", string(lineEndingCRLF))
	}
	return content
}

// functionBody 返回受影响函数正文，超过上限时裁剪，避免工具响应过大。
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

// countLines 返回规范化后的文件行数，末尾空行不计入总数。
func countLines(content string) int {
	lines := splitNormalizedLines(content)
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		return len(lines) - 1
	}
	return len(lines)
}

// splitLines 返回可用于 patch 匹配的行切片，空文本返回 nil。
func splitLines(content string) []string {
	lines := splitNormalizedLines(content)
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

// joinHunkOldText 拼接多个 hunk 的旧文本，供失败提示展示。
func joinHunkOldText(hunks []editpkg.Hunk) string {
	items := make([]string, 0, len(hunks))
	for _, hunk := range hunks {
		items = append(items, hunk.OldText)
	}
	return strings.Join(items, "\n")
}

// joinHunkNewText 拼接多个 hunk 的新文本，供失败提示展示。
func joinHunkNewText(hunks []editpkg.Hunk) string {
	items := make([]string, 0, len(hunks))
	for _, hunk := range hunks {
		items = append(items, hunk.NewText)
	}
	return strings.Join(items, "\n")
}

// uniqueStrings 去重并排序字符串，保证候选提示稳定。
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
