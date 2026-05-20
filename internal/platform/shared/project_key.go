package shared

import "github.com/anthropic-ai/super-agent-v3/internal/util/pathutil"

// ProjectKeyFromCwd delegates to pathutil.ProjectKeyFromCwd.
func ProjectKeyFromCwd(cwd string) (string, error) { return pathutil.ProjectKeyFromCwd(cwd) }

// MemoryProjectKeyFromCwd delegates to pathutil.MemoryProjectKeyFromCwd.
func MemoryProjectKeyFromCwd(cwd string) (string, error) {
	return pathutil.MemoryProjectKeyFromCwd(cwd)
}

// SanitizeSkillProjectKey delegates to pathutil.SanitizeSkillProjectKey.
func SanitizeSkillProjectKey(raw string) string { return pathutil.SanitizeSkillProjectKey(raw) }

// SanitizeMemoryProjectKey delegates to pathutil.SanitizeMemoryProjectKey.
func SanitizeMemoryProjectKey(raw string) string { return pathutil.SanitizeMemoryProjectKey(raw) }
