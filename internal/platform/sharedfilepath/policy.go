package sharedfilepath

import (
	"errors"
	"path"
	"path/filepath"
	"strings"
)

// sharedfilepath 定义 sharedfile 读写路径的 lexical 安全边界。
//
// 写路径只允许固定业务前缀：
//   handoff/   dag/   inbox/   reports/
//
// 读路径只做 traversal/absolute 校验（ValidateReadPath），不做白名单 ——
// 历史数据可能含计划之前的 prefix，但 traversal 已经够防御越界；强制白名
// 单会让 list/dashboard 上的旧行 read 直接 500。

const (
	// sharedfile 写路径允许的顶层前缀；_internal/ 是运行时保护根，不能由 sharedfile 写入口创建。
	prefixHandoff  = "handoff/"
	prefixDag      = "dag/"
	prefixInbox    = "inbox/"
	prefixReports  = "reports/"
	prefixInternal = "_internal/"
)

// 暴露给调用方做 errors.Is 判断，前端 / agent 看 friendly 文案时按 sentinel
// 分支化（prefix_not_allowed 提示 \"路径前缀必须是 handoff/ dag/ inbox/
// reports/ 之一\"）。
var (
	ErrPathEmpty            = errors.New("sharedfile: path is empty")
	ErrPathAbsolute         = errors.New("sharedfile: absolute path not allowed")
	ErrPathTraversal        = errors.New("sharedfile: path traversal not allowed")
	ErrPathPrefixNotAllowed = errors.New("sharedfile: path prefix not in whitelist")
	ErrPathProtectedRoot    = errors.New("sharedfile: protected root not writable")
)

// writePrefixes 是写入路径白名单；读路径只做 lexical 安全校验。
var writePrefixes = []string{
	prefixHandoff,
	prefixDag,
	prefixInbox,
	prefixReports,
}

// WritePrefixes 返回白名单写入路径前缀的拷贝。
// MCP 工具 (如 shared_file_list) 可用此向 AI / UI 暴露允许的路径前缀，
// 避免 “path prefix not in whitelist” 错误对调用方不透明。
func WritePrefixes() []string {
	copied := make([]string, len(writePrefixes))
	copy(copied, writePrefixes)
	return copied
}

// ValidateWritePath 校验完整写入 schema，返回标准化后的相对路径。
func ValidateWritePath(raw string) (string, error) {
	cleaned, err := cleanRelative(raw)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(cleaned, prefixInternal) {
		return "", ErrPathProtectedRoot
	}
	if !hasAnyPrefix(cleaned, writePrefixes) {
		return "", ErrPathPrefixNotAllowed
	}
	return cleaned, nil
}

// ValidateAgentWritePath 是 agent 写入路径入口，目前与普通写入共享同一白名单。
func ValidateAgentWritePath(raw string) (string, error) {
	return ValidateWritePath(raw)
}

// ValidateReadPath 只拒绝绝对路径和 traversal，保留读取旧前缀数据的兼容性。
func ValidateReadPath(raw string) (string, error) {
	return cleanRelative(raw)
}

// cleanRelative 标准化路径分隔符并拒绝绝对路径或父目录逃逸。
// 返回值是 disk/DB 层唯一可继续使用的路径形式，调用方不要复用 raw。
func cleanRelative(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ErrPathEmpty
	}
	// 先统一 Windows 分隔符；store 内部的规范形式始终使用 `/`。
	normalized := strings.ReplaceAll(trimmed, "\\", "/")
	// sandbox 始终相对 `<cwd>/.agnet/shared`，因此拒绝 `/abs/path` 和 `C:/...` 等绝对路径。
	if strings.HasPrefix(normalized, "/") || filepath.IsAbs(normalized) {
		return "", ErrPathAbsolute
	}
	cleaned := path.Clean(normalized)
	// path.Clean 会保留前导 `..` 段；这里直接拒绝，避免磁盘解析后逃出 sandbox。
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", ErrPathTraversal
	}
	// 防御性去掉前导 `/`，处理反斜杠转换后仍残留根路径形态的输入。
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" {
		return "", ErrPathEmpty
	}
	return cleaned, nil
}

// hasAnyPrefix 判断路径是否匹配任一白名单前缀。
func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
