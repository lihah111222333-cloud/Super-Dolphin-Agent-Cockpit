package sharedfilepath

import (
	"errors"
	"path"
	"path/filepath"
	"strings"
)

// Phase 3.7 / 3C · sharedfile 路径 schema + sandbox 边界
//
// 五个白名单 prefix（详见 docs/plans/自动化2.md §3 路径 schema）：
//   handoff/   dag/   inbox/   reports/   _internal/
//
// `handoff/tasks/` 是系统保留段：后端 task_handoff worker / promote-task
// 仍可写（走 ValidateWritePath），agent 通过 MCP `shared_file_write` 调用必
// 须被 ValidateAgentWritePath 阻断（保留给系统 handoff 文件，避免 agent
// 覆写自己看到的 task 描述）。
//
// 读路径只做 traversal/absolute 校验（ValidateReadPath），不做白名单 ——
// 历史数据可能含计划之前的 prefix，但 traversal 已经够防御越界；强制白名
// 单会让 list/dashboard 上的旧行 read 直接 500。

const (
	prefixHandoff       = "handoff/"
	prefixDag           = "dag/"
	prefixInbox         = "inbox/"
	prefixReports       = "reports/"
	prefixInternal      = "_internal/"
	systemHandoffPrefix = "handoff/tasks/"
)

// 暴露给调用方做 errors.Is 判断，前端 / agent 看 friendly 文案时按 sentinel
// 分支化（system_reserved 提示 \"该路径只能由系统写\"，prefix_not_allowed 提
// 示 \"路径前缀必须是 handoff/ dag/ inbox/ reports/ _internal/ 之一\"）。
var (
	ErrPathEmpty            = errors.New("sharedfile: path is empty")
	ErrPathAbsolute         = errors.New("sharedfile: absolute path not allowed")
	ErrPathTraversal        = errors.New("sharedfile: path traversal not allowed")
	ErrPathPrefixNotAllowed = errors.New("sharedfile: path prefix not in whitelist")
	ErrPathSystemReserved   = errors.New("sharedfile: handoff/tasks/ is reserved for system writes")
)

var writePrefixes = []string{
	prefixHandoff,
	prefixDag,
	prefixInbox,
	prefixReports,
	prefixInternal,
}

// ValidateWritePath enforces the full schema (whitelist + traversal +
// absolute) and returns the canonical-cleaned relative path. Used by every
// store write entry point AND by backend writers (system handoff worker,
// auto-continue state); the system carve-out for handoff/tasks/ is layered
// on top by ValidateAgentWritePath.
func ValidateWritePath(raw string) (string, error) {
	cleaned, err := cleanRelative(raw)
	if err != nil {
		return "", err
	}
	if !hasAnyPrefix(cleaned, writePrefixes) {
		return "", ErrPathPrefixNotAllowed
	}
	return cleaned, nil
}

// ValidateAgentWritePath layers on top of ValidateWritePath the system
// reserved carve-out: agent-driven writes must NOT touch handoff/tasks/,
// since that path holds the canonical task description that the agent
// itself reads.
func ValidateAgentWritePath(raw string) (string, error) {
	cleaned, err := ValidateWritePath(raw)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(cleaned, systemHandoffPrefix) {
		return "", ErrPathSystemReserved
	}
	return cleaned, nil
}

// ValidateReadPath only enforces the lexical safety net (no traversal, no
// absolute path). Skipping the prefix whitelist on read keeps existing rows
// that pre-date the schema readable; their content still passes through
// downstream defenses (size guards / sandbox path resolution in 3.6).
func ValidateReadPath(raw string) (string, error) {
	return cleanRelative(raw)
}

// IsSystemHandoffPath reports whether the path lives under the system
// handoff carve-out. Helpers like loaders that want to distinguish
// system-managed rows from agent-managed rows can use this without
// re-implementing the prefix string.
func IsSystemHandoffPath(rel string) bool {
	cleaned, err := cleanRelative(rel)
	if err != nil {
		return false
	}
	return strings.HasPrefix(cleaned, systemHandoffPrefix)
}

// cleanRelative trims, normalizes path separators, rejects absolute paths
// and rejects any traversal escape after path.Clean. The returned cleaned
// value is what callers should hand off to disk / DB layers — never reuse
// the raw input.
func cleanRelative(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ErrPathEmpty
	}
	// Normalize Windows-style separators before any further check; the
	// canonical store form uses `/`.
	normalized := strings.ReplaceAll(trimmed, "\\", "/")
	// Reject any caller that hands us `/abs/path` or platform-absolute
	// (`C:/...`); the sandbox is always relative to <cwd>/.agnet/shared.
	if strings.HasPrefix(normalized, "/") || filepath.IsAbs(normalized) {
		return "", ErrPathAbsolute
	}
	cleaned := path.Clean(normalized)
	// path.Clean preserves leading `..` segments; reject them outright so
	// the resolved disk path can never escape the sandbox.
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", ErrPathTraversal
	}
	// path.Clean strips leading `./`; defensive trim of leading `/` in
	// case input was something like `\foo` that survived ReplaceAll.
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" {
		return "", ErrPathEmpty
	}
	return cleaned, nil
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
