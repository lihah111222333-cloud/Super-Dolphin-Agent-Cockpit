package shared

import "github.com/anthropic-ai/super-agent-v3/internal/util/pathutil"

// ProjectKeyFromCwd delegates to pathutil.ProjectKeyFromCwd.
// ProjectKeyFromCwd 从工作目录处理项目键。
func ProjectKeyFromCwd(cwd string) (string, error) { return pathutil.ProjectKeyFromCwd(cwd) }

// MemoryProjectKeyFromCwd delegates to pathutil.MemoryProjectKeyFromCwd.
// MemoryProjectKeyFromCwd 从工作目录处理记忆项目键。
func MemoryProjectKeyFromCwd(cwd string) (string, error) {
	return pathutil.MemoryProjectKeyFromCwd(cwd)
}

// SanitizeSkillProjectKey delegates to pathutil.SanitizeSkillProjectKey.
// SanitizeSkillProjectKey 清理技能项目键。
func SanitizeSkillProjectKey(raw string) string { return pathutil.SanitizeSkillProjectKey(raw) }

// SanitizeMemoryProjectKey delegates to pathutil.SanitizeMemoryProjectKey.
// SanitizeMemoryProjectKey 清理记忆项目键。
func SanitizeMemoryProjectKey(raw string) string { return pathutil.SanitizeMemoryProjectKey(raw) }
