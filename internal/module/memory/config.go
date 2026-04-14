package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"golang.org/x/text/unicode/norm"
)

const (
	envMemoryRoot                  = "MULTI_AGENT_MEMORY_DIR"
	envClaudeRemoteMemoryDir       = "CLAUDE_CODE_REMOTE_MEMORY_DIR"
	envMemoryPathOverride          = "MULTI_AGENT_MEMORY_PATH_OVERRIDE"
	envClaudeMemoryPathOverride    = "CLAUDE_COWORK_MEMORY_PATH_OVERRIDE"
	envEnableMemorySystem          = "ENABLE_MEMORY_SYSTEM"
	envClaudeDisableAutoMemory     = "CLAUDE_CODE_DISABLE_AUTO_MEMORY"
	envClaudeSimple                = "CLAUDE_CODE_SIMPLE"
	envClaudeRemote                = "CLAUDE_CODE_REMOTE"
	envEnableMemoryTools           = "ENABLE_MEMORY_TOOLS"
	envMemorySkipIndex             = "MULTI_AGENT_MEMORY_SKIP_INDEX"
	envMemoryExtractOnStop         = "MULTI_AGENT_MEMORY_EXTRACT_ON_STOP"
	envMemoryExtraGuidelines       = "MULTI_AGENT_MEMORY_EXTRA_GUIDELINES"
	envClaudeMemoryExtraGuidelines = "CLAUDE_COWORK_MEMORY_EXTRA_GUIDELINES"
	envFeatureKairos               = "MULTI_AGENT_MEMORY_FEATURE_KAIROS"
	envFeatureTeamMemory           = "MULTI_AGENT_MEMORY_FEATURE_TEAMMEM"
	envFeatureSearchPastContext    = "MULTI_AGENT_MEMORY_FEATURE_SEARCH_PAST_CONTEXT"
)

var (
	ErrTeamMemoryDisabled      = errors.New("team memory is disabled")
	ErrInvalidTeamMemWritePath = errors.New("invalid team memory write path")
	ErrInvalidTeamMemKey       = errors.New("invalid team memory key")
)

type MemoryFeatureFlags struct {
	Kairos            bool
	TeamMemory        bool
	SearchPastContext bool
}

type KairosConfig struct {
	Enabled bool
}

type NestedMemoryConfig struct {
	Enabled bool
}

type Config struct {
	Enabled             bool
	EnableTools         bool
	RootDir             string
	ProjectRoot         string
	AutoMemPathOverride string
	SkipIndex           bool
	ExtractOnStop       bool
	ExtraGuidelines     []string
	Features            MemoryFeatureFlags
	Kairos              KairosConfig
	NestedMemory        NestedMemoryConfig
}

type TeamMemoryManager struct {
	cfg *Config
}

func NewConfig(platformCfg *platformconfig.Config) *Config {
	kairosEnabled := parseBoolEnv(envFeatureKairos, false)
	cfg := &Config{
		Enabled:             parseBoolEnv(envEnableMemorySystem, false),
		EnableTools:         parseBoolEnv(envEnableMemoryTools, false),
		RootDir:             defaultRootDir(platformCfg),
		ProjectRoot:         defaultProjectRoot(platformCfg),
		AutoMemPathOverride: firstNonEmptyEnv(envMemoryPathOverride, envClaudeMemoryPathOverride),
		SkipIndex:           parseBoolEnv(envMemorySkipIndex, false),
		ExtractOnStop:       parseBoolEnv(envMemoryExtractOnStop, false),
		ExtraGuidelines:     parseGuidelinesEnv(envMemoryExtraGuidelines, envClaudeMemoryExtraGuidelines),
		Features: MemoryFeatureFlags{
			Kairos:            kairosEnabled,
			TeamMemory:        parseBoolEnv(envFeatureTeamMemory, false),
			SearchPastContext: parseBoolEnv(envFeatureSearchPastContext, false),
		},
		Kairos:       KairosConfig{Enabled: kairosEnabled},
		NestedMemory: NestedMemoryConfig{Enabled: false},
	}
	if root := firstNonEmptyEnv(envMemoryRoot, envClaudeRemoteMemoryDir); root != "" {
		cfg.RootDir = root
	}
	return cfg
}

func NewTeamMemoryManager(cfg *Config) *TeamMemoryManager {
	if cfg == nil {
		cfg = &Config{}
	}
	return &TeamMemoryManager{cfg: cfg}
}

func (c *Config) IsMemoryEnabled() bool {
	configEnabled := c != nil && c.Enabled
	if !parseBoolEnv(envEnableMemorySystem, configEnabled) {
		return false
	}
	if parseBoolEnv(envClaudeDisableAutoMemory, false) {
		return false
	}
	if parseBoolEnv(envClaudeSimple, false) {
		return false
	}
	if parseBoolEnv(envClaudeRemote, false) && !hasPersistentMemoryStorage(c) {
		return false
	}
	return configEnabled
}

func (m *TeamMemoryManager) GetTeamMemPath() string {
	return ""
}

func (m *TeamMemoryManager) IsTeamMemoryEnabled() bool {
	return false
}

func (m *TeamMemoryManager) ValidateTeamMemWritePath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("%w: empty path", ErrInvalidTeamMemWritePath)
	}
	if strings.ContainsRune(path, '\x00') {
		return fmt.Errorf("%w: null byte", ErrInvalidTeamMemWritePath)
	}
	return ErrTeamMemoryDisabled
}

func (m *TeamMemoryManager) ValidateTeamMemKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("%w: empty key", ErrInvalidTeamMemKey)
	}
	if strings.ContainsRune(key, '\x00') {
		return fmt.Errorf("%w: null byte", ErrInvalidTeamMemKey)
	}
	if strings.HasPrefix(key, "/") || strings.Contains(key, `\\`) {
		return fmt.Errorf("%w: absolute or windows path separators are not allowed", ErrInvalidTeamMemKey)
	}
	if key == ".." || strings.HasPrefix(key, "../") || strings.Contains(key, "/../") || strings.HasSuffix(key, "/..") {
		return fmt.Errorf("%w: traversal segments are not allowed", ErrInvalidTeamMemKey)
	}
	return ErrTeamMemoryDisabled
}

func hasPersistentMemoryStorage(cfg *Config) bool {
	if firstNonEmptyEnv(
		envMemoryRoot,
		envClaudeRemoteMemoryDir,
		envMemoryPathOverride,
		envClaudeMemoryPathOverride,
	) != "" {
		return true
	}
	if cfg == nil {
		return false
	}
	return strings.TrimSpace(cfg.AutoMemPathOverride) != ""
}

func BuildDailyLogPrompt() string {
	return ""
}

func HandleDateChange() {}

func LoadNestedMemoryPaths() []string {
	return []string{}
}

func MatchTargetPath() bool {
	return false
}

func defaultRootDir(platformCfg *platformconfig.Config) string {
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".multi-agent", "memory")
	}
	if platformCfg != nil && strings.TrimSpace(platformCfg.ProjectRoot) != "" {
		return filepath.Join(platformCfg.ProjectRoot, ".multi-agent", "memory")
	}
	return ""
}

func defaultProjectRoot(platformCfg *platformconfig.Config) string {
	if platformCfg != nil && strings.TrimSpace(platformCfg.ProjectRoot) != "" {
		return platformCfg.ProjectRoot
	}
	if dir, err := os.Getwd(); err == nil {
		return dir
	}
	return ""
}

func parseBoolEnv(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func firstNonEmptyEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func parseGuidelinesEnv(keys ...string) []string {
	raw := firstNonEmptyEnv(keys...)
	if raw == "" {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	guidelines := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			guidelines = append(guidelines, trimmed)
		}
	}
	if len(guidelines) == 0 {
		return nil
	}
	return guidelines
}

const agentTypeMaxLen = 128

func SanitizeAgentType(raw string) string {
	normalized := normalizeAgentType(raw)
	switch {
	case normalized == "":
		return ""
	case utf8.RuneCountInString(normalized) > agentTypeMaxLen:
		return ""
	case containsTraversalSegment(normalized):
		return ""
	case needsHashedAgentType(normalized):
		return fallbackAgentTypeName(normalized)
	default:
		return normalized
	}
}

func normalizeAgentType(raw string) string {
	normalized := norm.NFC.String(strings.TrimSpace(raw))
	return strings.ReplaceAll(normalized, ":", "-")
}

func containsTraversalSegment(raw string) bool {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	for _, part := range parts {
		if part == "." || part == ".." {
			return true
		}
	}
	return false
}

func needsHashedAgentType(raw string) bool {
	if raw == "" || strings.HasPrefix(raw, ".") || strings.HasSuffix(raw, ".") {
		return true
	}
	if isReservedWindowsSegment(raw) {
		return true
	}
	for _, r := range raw {
		if unicode.IsControl(r) || isBidiControl(r) {
			return true
		}
		if !isPortableAgentRune(r) {
			return true
		}
	}
	return false
}

func isPortableAgentRune(r rune) bool {
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return true
	}
	switch r {
	case ' ', '.', '_', '-', '@':
		return true
	default:
		return false
	}
}

func isBidiControl(r rune) bool {
	switch {
	case r == 0x061C, r == 0x200E, r == 0x200F:
		return true
	case 0x202A <= r && r <= 0x202E:
		return true
	case 0x2066 <= r && r <= 0x2069:
		return true
	default:
		return false
	}
}

func isReservedWindowsSegment(raw string) bool {
	segment := raw
	if idx := strings.IndexRune(segment, '.'); idx >= 0 {
		segment = segment[:idx]
	}
	switch strings.ToUpper(strings.TrimSpace(segment)) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	default:
		return false
	}
}

func fallbackAgentTypeName(raw string) string {
	prefix := readableAgentTypePrefix(raw)
	if prefix == "" {
		prefix = "agent"
	}
	const hashLen = 8
	maxPrefixRunes := agentTypeMaxLen - hashLen - 1
	if utf8.RuneCountInString(prefix) > maxPrefixRunes {
		prefix = truncateRunes(prefix, maxPrefixRunes)
		prefix = strings.Trim(prefix, " ._-")
	}
	if prefix == "" {
		prefix = "agent"
	}
	return prefix + "-" + shortHash(raw)
}

func readableAgentTypePrefix(raw string) string {
	var builder strings.Builder
	lastSeparator := false
	for _, r := range raw {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(r)
			lastSeparator = false
		case r == '.' || r == '_' || r == '-' || r == '@':
			if lastSeparator {
				continue
			}
			builder.WriteRune(r)
			lastSeparator = true
		default:
			if lastSeparator {
				continue
			}
			builder.WriteByte('-')
			lastSeparator = true
		}
	}
	return strings.Trim(builder.String(), " ._-")
}
