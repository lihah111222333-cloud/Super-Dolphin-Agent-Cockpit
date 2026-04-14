package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"
)

const (
	agentMemoryDirName       = "agents"
	agentMemoryLocalDirName  = "local"
	agentMemoryMaxLines      = 200
	agentMemoryMaxBytes      = 25 * 1024
	emptyAgentMemoryPrompt   = "Your MEMORY.md is currently empty. Save only durable, agent-specific context here."
	unreadableAgentMemoryMsg = "WARNING: MEMORY.md could not be read; continuing with empty agent memory."
)

var (
	ErrInvalidAgentType  = errors.New("invalid agent type")
	ErrInvalidAgentScope = errors.New("invalid agent memory scope")
	ErrInvalidProjectDir = errors.New("invalid agent project root")
)

type AgentMemoryManager struct {
	cfg *Config
}

func NewAgentMemoryManager(cfg *Config) *AgentMemoryManager {
	if cfg == nil {
		cfg = &Config{}
	}
	return &AgentMemoryManager{cfg: cfg}
}

func (m *AgentMemoryManager) GetAgentMemoryDir(agentType string, scope MemoryScope) (string, error) {
	parent, err := m.scopeParentDir(scope)
	if err != nil {
		return "", err
	}
	dirName, err := resolveAgentDirName(parent, agentType)
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, dirName), nil
}

func (m *AgentMemoryManager) LoadAgentMemoryPrompt(agentType string, scope MemoryScope) (string, error) {
	dir, err := m.GetAgentMemoryDir(agentType, scope)
	if err != nil {
		return "", err
	}
	_ = os.MkdirAll(dir, 0o755)
	content, warning := loadAgentMemoryEntrypoint(memoryIndexPath(dir))
	rules := strings.TrimSpace(BuildMemoryLines(false, agentMemoryGuidelines(scope)))
	parts := nonEmpty([]string{rules, warning, "## MEMORY.md", content})
	return strings.Join(parts, "\n\n"), nil
}

func (m *AgentMemoryManager) scopeParentDir(scope MemoryScope) (string, error) {
	switch scope {
	case MemoryScopeUser:
		return m.userScopeParentDir()
	case MemoryScopeProject:
		root, err := m.projectRootDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(root, "memory", agentMemoryDirName), nil
	case MemoryScopeLocal:
		root, err := m.projectRootDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(root, "memory", agentMemoryLocalDirName), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidAgentScope, scope)
	}
}

func (m *AgentMemoryManager) userScopeParentDir() (string, error) {
	root, err := ValidateMemoryRoot(strings.TrimSpace(m.config().RootDir))
	if err != nil {
		return "", err
	}
	if root == "" {
		return "", fmt.Errorf("%w: empty root", ErrInvalidMemoryRoot)
	}
	return filepath.Join(strings.TrimSuffix(root, string(os.PathSeparator)), agentMemoryDirName), nil
}

func (m *AgentMemoryManager) projectRootDir() (string, error) {
	projectRoot := strings.TrimSpace(m.config().ProjectRoot)
	if projectRoot == "" {
		return "", ErrInvalidProjectDir
	}
	cleaned, err := cleanAbsolutePath(projectRoot)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidProjectDir, err)
	}
	return cleaned, nil
}

func (m *AgentMemoryManager) config() Config {
	if m == nil || m.cfg == nil {
		return Config{}
	}
	return *m.cfg
}

func resolveAgentDirName(parent, agentType string) (string, error) {
	sanitized := SanitizeAgentType(agentType)
	if sanitized == "" {
		return "", fmt.Errorf("%w: %q", ErrInvalidAgentType, strings.TrimSpace(agentType))
	}
	if hasAgentDirConflict(parent, sanitized) {
		return fallbackAgentTypeName(normalizeAgentType(agentType)), nil
	}
	return sanitized, nil
}

func hasAgentDirConflict(parent, dirName string) bool {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return false
	}
	want := CanonicalName(dirName)
	for _, entry := range entries {
		if entry.Name() != dirName && CanonicalName(entry.Name()) == want {
			return true
		}
	}
	return false
}

func agentMemoryGuidelines(scope MemoryScope) []string {
	guidelines := []string{
		"This prompt is for agent-specific memory. Keep it isolated from the main thread and other agent types.",
		"Searching past context: consult the MEMORY.md section below before assuming nothing has been remembered for this agent.",
	}
	switch scope {
	case MemoryScopeUser:
		return append(guidelines, "This user-scoped agent memory is shared across projects for the same agent type.")
	case MemoryScopeProject:
		return append(guidelines, "This project-scoped agent memory is isolated to the current project and can be shared with the repo.")
	case MemoryScopeLocal:
		return append(guidelines, "This local agent memory is isolated to the current project on this machine only.")
	default:
		return guidelines
	}
}

func loadAgentMemoryEntrypoint(path string) (string, string) {
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emptyAgentMemoryPrompt, ""
		}
		return emptyAgentMemoryPrompt, unreadableAgentMemoryMsg
	}
	trimmed := strings.TrimSpace(norm.NFC.String(string(content)))
	if trimmed == "" {
		return emptyAgentMemoryPrompt, ""
	}
	return truncateAgentMemoryContent(trimmed)
}

func truncateAgentMemoryContent(content string) (string, string) {
	warnings := make([]string, 0, 2)
	if trimmed, ok := truncateAgentMemoryLines(content); ok {
		content = trimmed
		warnings = append(warnings, "WARNING: MEMORY.md was truncated after 200 lines.")
	}
	if trimmed, ok := truncateAgentMemoryBytes(content); ok {
		content = trimmed
		warnings = append(warnings, "WARNING: MEMORY.md was truncated to 25KB.")
	}
	return content, strings.Join(warnings, "\n")
}

func truncateAgentMemoryLines(content string) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) <= agentMemoryMaxLines {
		return content, false
	}
	return strings.Join(lines[:agentMemoryMaxLines], "\n"), true
}

func truncateAgentMemoryBytes(content string) (string, bool) {
	if len(content) <= agentMemoryMaxBytes {
		return content, false
	}
	trimmed := trimToRuneBoundary([]byte(content), agentMemoryMaxBytes)
	return strings.TrimRight(string(trimmed), "\n"), true
}

func trimToRuneBoundary(content []byte, limit int) []byte {
	if limit <= 0 {
		return nil
	}
	if len(content) <= limit {
		return content
	}
	for limit > 0 && content[limit]&0xC0 == 0x80 {
		limit--
	}
	return content[:limit]
}
