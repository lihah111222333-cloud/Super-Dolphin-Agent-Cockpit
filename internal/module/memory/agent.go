package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	agentMemoryDirName           = "agents"
	agentMemoryLocalDirName      = "local"
	agentMemoryMaxLines          = 200
	agentMemoryMaxCodeUnits      = 25_000
	defaultChildAgentMemoryScope = MemoryScopeProject
	emptyAgentMemoryPrompt       = "Your MEMORY.md is currently empty. Save only durable, agent-specific context here."
	unreadableAgentMemoryMsg     = "WARNING: MEMORY.md could not be read; continuing with empty agent memory."
)

var (
	ErrInvalidAgentType  = errors.New("invalid agent type")
	ErrInvalidAgentScope = errors.New("invalid agent memory scope")
	ErrInvalidProjectDir = errors.New("invalid agent project root")
)

var _ prompt.DynamicSectionProvider = (*AgentMemoryPromptProvider)(nil)

type AgentMemoryManager struct {
	cfg *Config
}

type AgentMemoryPromptProvider struct {
	enabled bool
	manager *AgentMemoryManager
	logger  *slog.Logger
}

type childAgentStart struct {
	agentType string
	scope     MemoryScope
}

func NewAgentMemoryManager(cfg *Config) *AgentMemoryManager {
	if cfg == nil {
		cfg = &Config{}
	}
	return &AgentMemoryManager{cfg: cfg}
}

func NewAgentMemoryPromptProvider(cfg *Config, manager *AgentMemoryManager, logger *slog.Logger) *AgentMemoryPromptProvider {
	return &AgentMemoryPromptProvider{
		enabled: cfg != nil && cfg.IsMemoryEnabled(),
		manager: manager,
		logger:  logger,
	}
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

func (m *AgentMemoryManager) EnsureAgentMemoryDir(agentType string, scope MemoryScope) error {
	dir, err := m.GetAgentMemoryDir(agentType, scope)
	if err != nil {
		return err
	}
	return ensureAgentMemoryDir(dir)
}

func (m *AgentMemoryManager) LoadAgentMemoryPrompt(agentType string, scope MemoryScope) (string, error) {
	dir, err := m.GetAgentMemoryDir(agentType, scope)
	if err != nil {
		return "", err
	}
	_ = ensureAgentMemoryDir(dir)
	content, warning := loadAgentMemoryEntrypoint(memoryIndexPath(dir))
	rules := strings.TrimSpace(BuildMemoryLines(false, agentMemoryGuidelines(scope)))
	parts := nonEmpty([]string{rules, warning, "## MEMORY.md", content})
	return strings.Join(parts, "\n\n"), nil
}

func (p *AgentMemoryPromptProvider) SectionName() string {
	return prompt.DynamicSectionAgentMemory
}

func (p *AgentMemoryPromptProvider) Resolve(_ context.Context, input prompt.SectionContext) (*string, error) {
	if p == nil || !p.enabled || p.manager == nil {
		return nil, nil
	}
	meta, ok := resolveChildAgentStart(input)
	if !ok {
		return nil, nil
	}
	text, err := p.manager.LoadAgentMemoryPrompt(meta.agentType, meta.scope)
	if err != nil {
		if p.logger != nil {
			p.logger.Warn("agent memory prompt load failed",
				"agent_type", meta.agentType,
				"scope", string(meta.scope),
				"error", err,
			)
		}
		return nil, nil
	}
	if text = strings.TrimSpace(text); text == "" {
		return nil, nil
	}
	return &text, nil
}

func ensureAgentMemoryDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	entrypoint := memoryIndexPath(dir)
	info, err := os.Stat(entrypoint)
	if err == nil {
		if info.IsDir() {
			return fmt.Errorf("agent memory entrypoint is a directory: %s", entrypoint)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(entrypoint, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	return file.Close()
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
	parsed, err := ParseMemoryFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emptyAgentMemoryPrompt, ""
		}
		return emptyAgentMemoryPrompt, unreadableAgentMemoryMsg
	}
	if strings.TrimSpace(parsed.Content) == "" {
		return emptyAgentMemoryPrompt, ""
	}
	return parsed.Content, ""
}

type agentMemoryTruncation struct {
	content          string
	lineCount        int
	codeUnitCount    int
	wasLineTruncated bool
	wasByteTruncated bool
}

func truncateAgentMemoryContent(raw string) agentMemoryTruncation {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return agentMemoryTruncation{}
	}

	contentLines := strings.Split(trimmed, "\n")
	result := agentMemoryTruncation{
		content:       trimmed,
		lineCount:     len(contentLines),
		codeUnitCount: jsStringLength(trimmed),
	}
	result.wasLineTruncated = result.lineCount > agentMemoryMaxLines
	result.wasByteTruncated = result.codeUnitCount > agentMemoryMaxCodeUnits
	if !result.wasLineTruncated && !result.wasByteTruncated {
		return result
	}

	truncated := trimmed
	if result.wasLineTruncated {
		truncated = strings.Join(contentLines[:agentMemoryMaxLines], "\n")
	}
	if jsStringLength(truncated) > agentMemoryMaxCodeUnits {
		truncated = truncateAtCodeUnitLimit(truncated, agentMemoryMaxCodeUnits)
	}
	result.content = truncated + "\n\n> WARNING: MEMORY.md is " + truncateAgentMemoryReason(result) + ". Only part of it was loaded. Keep index entries to one line under ~200 chars; move detail into topic files."
	return result
}

func truncateAgentMemoryReason(result agentMemoryTruncation) string {
	switch {
	case result.wasByteTruncated && !result.wasLineTruncated:
		return fmt.Sprintf("%s (limit: %s) — index entries are too long", formatEntrypointSize(result.codeUnitCount), formatEntrypointSize(agentMemoryMaxCodeUnits))
	case result.wasLineTruncated && !result.wasByteTruncated:
		return fmt.Sprintf("%d lines (limit: %d)", result.lineCount, agentMemoryMaxLines)
	default:
		return fmt.Sprintf("%d lines and %s", result.lineCount, formatEntrypointSize(result.codeUnitCount))
	}
}

func truncateAtCodeUnitLimit(content string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if jsStringLength(content) <= limit {
		return content
	}

	bytePos := 0
	codeUnits := 0
	lastNewline := -1
	for bytePos < len(content) {
		r, size := utf8.DecodeRuneInString(content[bytePos:])
		units := utf16CodeUnits(r)
		if codeUnits+units > limit {
			break
		}
		if r == '\n' {
			lastNewline = bytePos
		}
		codeUnits += units
		bytePos += size
	}
	if lastNewline > 0 {
		return content[:lastNewline]
	}
	return content[:bytePos]
}

func jsStringLength(content string) int {
	count := 0
	for bytePos := 0; bytePos < len(content); {
		r, size := utf8.DecodeRuneInString(content[bytePos:])
		count += utf16CodeUnits(r)
		bytePos += size
	}
	return count
}

func utf16CodeUnits(r rune) int {
	if r > 0xFFFF {
		return 2
	}
	return 1
}

func formatEntrypointSize(size int) string {
	kb := float64(size) / 1024
	if kb < 1 {
		return fmt.Sprintf("%d bytes", size)
	}
	if kb < 1024 {
		return strings.TrimSuffix(fmt.Sprintf("%.1f", kb), ".0") + "KB"
	}
	mb := kb / 1024
	if mb < 1024 {
		return strings.TrimSuffix(fmt.Sprintf("%.1f", mb), ".0") + "MB"
	}
	gb := mb / 1024
	return strings.TrimSuffix(fmt.Sprintf("%.1f", gb), ".0") + "GB"
}

func resolveChildAgentStart(input prompt.SectionContext) (childAgentStart, bool) {
	if input.Start == nil || input.Turn != nil {
		return childAgentStart{}, false
	}
	if strings.TrimSpace(input.Start.ParentAgentID) == "" {
		return childAgentStart{}, false
	}
	agentType := strings.TrimSpace(platformshared.FirstNonEmpty(input.Start.AgentType, input.Start.Name))
	if agentType == "" {
		return childAgentStart{}, false
	}
	return childAgentStart{
		agentType: agentType,
		scope:     defaultChildAgentMemoryScope,
	}, true
}
