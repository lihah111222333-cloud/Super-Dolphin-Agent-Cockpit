package tools

import (
	"context"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/format"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

// resolveFilePath 在当前可信工具作用域内解析文件路径。
func resolveFilePath(ctx context.Context, path string) (string, error) {
	pathInfo, err := toolResolvePath(ctx, path)
	if err != nil {
		return "", err
	}
	return pathInfo.AbsPath, nil
}

// normalizeFilePathTarget 规范化文件路径；百分号解码只发生在明确的 file URI 边界。
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
	return filePath, nil
}

func hasFileURIScheme(path string) bool {
	scheme, _, ok := strings.Cut(path, ":")
	return ok && strings.EqualFold(scheme, "file")
}

func pathWithinAnyRoot(roots []string, target string) bool {
	for _, root := range roots {
		if platformshared.ContainsPath(root, target) {
			return true
		}
	}
	return false
}
