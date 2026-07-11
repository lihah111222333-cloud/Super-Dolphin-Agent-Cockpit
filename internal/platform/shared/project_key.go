package shared

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/pathutil"

// ProjectKeyFromCwd 根据工作目录生成通用项目键，保持 shared 包旧入口兼容。
func ProjectKeyFromCwd(cwd string) (string, error) { return pathutil.ProjectKeyFromCwd(cwd) }

// MemoryProjectKeyFromCwd 根据工作目录生成 memory 专用项目键。
func MemoryProjectKeyFromCwd(cwd string) (string, error) {
	return pathutil.MemoryProjectKeyFromCwd(cwd)
}

// SanitizeSkillProjectKey 清理 skill 项目键中的不安全字符。
func SanitizeSkillProjectKey(raw string) string { return pathutil.SanitizeSkillProjectKey(raw) }

// SanitizeMemoryProjectKey 清理 memory 项目键中的不安全字符。
func SanitizeMemoryProjectKey(raw string) string { return pathutil.SanitizeMemoryProjectKey(raw) }
