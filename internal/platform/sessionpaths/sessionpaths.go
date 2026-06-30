package sessionpaths

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"unicode"
)

const (
	managedScratchpadNamespace = "super-agent-v3"
	managedScratchpadLeaf      = "scratchpad"
)

// CodexRolloutGlob 为指定 Codex home 和 threadID 构造 rollout JSONL glob。
// 调用方仍负责执行 glob、排序和读取；这里不读取 HOME/env，也不扫描文件系统。
func CodexRolloutGlob(codexHome, threadID string) (string, error) {
	root := strings.TrimSpace(codexHome)
	if root == "" {
		return "", errors.New("sessionpaths: codex home is required")
	}
	id := strings.TrimSpace(threadID)
	if id == "" {
		return "", errors.New("sessionpaths: thread id is required")
	}

	return filepath.Join(root, "sessions", "*", "*", "*", "rollout-*-"+id+".jsonl"), nil
}

// ManagedScratchpadDir 派生本进程管理的 session scratchpad 目录路径。
// 它只返回确定性路径，不创建目录，也不修改权限。
func ManagedScratchpadDir(tempRoot, projectRoot, threadID string) string {
	return filepath.Join(
		tempRoot,
		managedScratchpadNamespace,
		SanitizeProjectPath(projectRoot),
		strings.TrimSpace(threadID),
		managedScratchpadLeaf,
	)
}

// IsManagedScratchpadDir 判断 dir 是否位于传入 tempRoot 管理的 scratchpad 命名空间内。
func IsManagedScratchpadDir(tempRoot, dir string) bool {
	cleaned := filepath.Clean(strings.TrimSpace(dir))
	if cleaned == "." || cleaned == string(filepath.Separator) {
		return false
	}
	root := filepath.Join(tempRoot, managedScratchpadNamespace)
	rel, err := filepath.Rel(root, cleaned)
	if err != nil {
		return false
	}
	return rel != "." && rel != "" && !strings.HasPrefix(rel, "..")
}

// SanitizeProjectPath 将项目路径折叠成 scratchpad 路径段。
// 规则保持 thread 模块现状：保留 unicode 字母数字并小写，其他字符折叠为短横线。
func SanitizeProjectPath(raw string) string {
	normalized := filepath.ToSlash(strings.TrimSpace(raw))
	var builder strings.Builder
	lastDash := false
	for _, r := range normalized {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(unicode.ToLower(r))
			lastDash = false
		case lastDash:
		default:
			builder.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(builder.String(), "-")
	if slug != "" {
		return slug
	}
	hash := sha256.Sum256([]byte(normalized))
	return "project-" + hex.EncodeToString(hash[:4])
}
