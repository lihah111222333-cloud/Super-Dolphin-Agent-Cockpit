package memory

import (
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

type AutoMemPathSource string

const (
	AutoMemPathSourceDefault  AutoMemPathSource = "default"
	AutoMemPathSourceSettings AutoMemPathSource = "settings"
	AutoMemPathSourceEnv      AutoMemPathSource = "env"
)

type MemoryGateSnapshot struct {
	AutoEnabled               bool
	ForceEnabledByEnvFalsy    bool
	DisabledByEnvTruthy       bool
	SettingsAutoMemoryEnabled bool
	RemoteMode                bool
	HasPersistentStorage      bool
	BareMode                  bool
	HasAdditionalDirsForBare  bool
	DisableClaudeMds          bool
	SkipIndex                 bool
	KairosEnabled             bool
	TeamMemEnabled            bool
	InjectMemoryIndex         bool
	InjectTeamMemIndex        bool
	EnableRelevantPrefetch    bool
	SearchPastContextEnabled  bool
	RequestedMemoryMode       MemoryMode
	EffectiveMemoryMode       MemoryMode
	AutoMemPathSource         AutoMemPathSource
}

type RelevantPrefetchSurfacedState struct {
	TotalBytes int
}

func resolveMemoryGate(buildCtx contract.BuildCtx, cfg *Config) MemoryGateSnapshot {
	cfg = memoryConfig(cfg)
	snapshot := MemoryGateSnapshot{
		ForceEnabledByEnvFalsy:    isEnvDefinedFalsy(os.Getenv(envClaudeDisableAutoMemory)),
		DisabledByEnvTruthy:       isEnvTruthy(os.Getenv(envClaudeDisableAutoMemory)),
		SettingsAutoMemoryEnabled: settingsAutoMemoryEnabled(buildCtx),
		RemoteMode:                parseBoolEnv(envClaudeRemote, false),
		HasPersistentStorage:      hasPersistentMemoryStorage(cfg),
		BareMode:                  parseBoolEnv(envClaudeSimple, false) || gateFlagEnabled(buildCtx.SessionFlags, "bare_mode", "bareMode", "bare"),
		HasAdditionalDirsForBare:  len(buildCtx.AdditionalWorkingDirectories) > 0,
		DisableClaudeMds:          gateFlagEnabled(buildCtx.SessionFlags, "disable_claude_mds", "disableClaudeMds"),
		SkipIndex:                 cfg.SkipIndex || gateFlagEnabled(buildCtx.SessionFlags, "skip_index", "skipIndex", "tengu_moth_copse"),
		KairosEnabled:             cfg.Features.Kairos || gateFlagEnabled(buildCtx.SessionFlags, "memory_kairos", "kairos"),
		TeamMemEnabled:            cfg.Features.TeamMemory || gateFlagEnabled(buildCtx.SessionFlags, "team_memory", "teamMemory", "teammem"),
		SearchPastContextEnabled:  cfg.Features.SearchPastContext || gateFlagEnabled(buildCtx.SessionFlags, "search_past_context", "searchPastContext"),
		AutoMemPathSource:         resolveAutoMemPathSource(cfg),
	}
	snapshot.AutoEnabled = resolveAutoEnabled(snapshot, cfg)
	snapshot.RequestedMemoryMode = selectRequestedMemoryMode(snapshot)
	snapshot.EffectiveMemoryMode = effectiveMemoryMode(snapshot.RequestedMemoryMode)
	snapshot.InjectMemoryIndex = snapshot.AutoEnabled && !snapshot.SkipIndex
	snapshot.InjectTeamMemIndex = snapshot.AutoEnabled && snapshot.TeamMemEnabled && !snapshot.SkipIndex
	snapshot.EnableRelevantPrefetch = snapshot.AutoEnabled
	return snapshot
}

func shouldStartRelevantMemoryPrefetch(snapshot MemoryGateSnapshot, turnInput contract.TurnInput, surfacedState RelevantPrefetchSurfacedState) bool {
	if !snapshot.EnableRelevantPrefetch {
		return false
	}
	if strings.TrimSpace(turnInput.UserText) == "" {
		return false
	}
	if isSingleWordQuery(turnInput.UserText) {
		return false
	}
	return surfacedState.TotalBytes < defaultRelevantMemoryBudgetBytes
}

func memoryConfig(cfg *Config) *Config {
	if cfg != nil {
		return cfg
	}
	return &Config{}
}

func hasAutoMemPathOverride(cfg *Config) bool {
	if cfg == nil {
		return false
	}
	return strings.TrimSpace(cfg.AutoMemPathOverride) != ""
}

func isAutoMemPath(cfg *Config, path string) bool {
	if cfg == nil || strings.TrimSpace(path) == "" {
		return false
	}
	candidate, err := cleanAbsolutePath(path)
	if err != nil {
		return false
	}
	root, err := resolvedStoreRoot(cfg.RootDir, cfg.ProjectRoot, cfg.AutoMemPathOverride)
	if err != nil || strings.TrimSpace(root) == "" {
		return false
	}
	root, err = cleanAbsolutePath(root)
	if err != nil {
		return false
	}
	return platformshared.ContainsPath(root, candidate)
}

func resolveAutoEnabled(snapshot MemoryGateSnapshot, cfg *Config) bool {
	switch {
	case snapshot.DisabledByEnvTruthy:
		return false
	case snapshot.ForceEnabledByEnvFalsy:
		return true
	case cfg == nil || !cfg.Enabled:
		return false
	case !snapshot.SettingsAutoMemoryEnabled:
		return false
	case snapshot.BareMode:
		return false
	case snapshot.RemoteMode && !snapshot.HasPersistentStorage:
		return false
	default:
		return true
	}
}

func selectRequestedMemoryMode(snapshot MemoryGateSnapshot) MemoryMode {
	if !snapshot.AutoEnabled {
		return ""
	}
	switch {
	case snapshot.KairosEnabled:
		return MemoryModeKairos
	case snapshot.TeamMemEnabled:
		return MemoryModeCombined
	default:
		return MemoryModeStandard
	}
}

func effectiveMemoryMode(mode MemoryMode) MemoryMode {
	switch mode {
	case MemoryModeKairos, MemoryModeCombined:
		return MemoryModeStandard
	default:
		return mode
	}
}

func resolveAutoMemPathSource(cfg *Config) AutoMemPathSource {
	switch {
	case firstNonEmptyEnv(
		envMemoryPathOverride,
		envClaudeMemoryPathOverride,
		envMemoryRoot,
		envClaudeRemoteMemoryDir,
	) != "":
		return AutoMemPathSourceEnv
	case hasAutoMemPathOverride(cfg):
		return AutoMemPathSourceSettings
	default:
		return AutoMemPathSourceDefault
	}
}

func settingsAutoMemoryEnabled(buildCtx contract.BuildCtx) bool {
	value, ok := gateFlagValue(buildCtx.SessionFlags, "auto_memory_enabled", "autoMemoryEnabled")
	if ok {
		return value
	}
	return true
}

func gateFlagEnabled(flags map[string]bool, keys ...string) bool {
	value, ok := gateFlagValue(flags, keys...)
	return ok && value
}

func gateFlagValue(flags map[string]bool, keys ...string) (bool, bool) {
	for _, key := range keys {
		value, ok := flags[key]
		if ok {
			return value, true
		}
	}
	return false, false
}

func isSingleWordQuery(query string) bool {
	normalized, _ := searchTerms(query)
	if normalized == "" {
		return true
	}
	return len(strings.Fields(normalized)) <= 1
}

func isEnvTruthy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func isEnvDefinedFalsy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "0", "false", "no", "off":
		return true
	default:
		return false
	}
}
