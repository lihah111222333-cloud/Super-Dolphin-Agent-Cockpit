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
	SkipProjectLocalClaudeMd  bool
	SkipIndex                 bool
	KairosEnabled             bool
	KairosActive              bool
	TeamMemEnabled            bool
	InjectMemoryIndex         bool
	InjectTeamMemIndex        bool
	EnableRelevantPrefetch    bool
	SearchPastContextEnabled  bool
	RequestedMemoryMode       MemoryMode
	EffectiveMemoryMode       MemoryMode
	AutoMemPathSource         AutoMemPathSource
	TrustedAutoMemPathSource  TrustedPathSettingSource
}

type RelevantPrefetchSurfacedState struct {
	TotalBytes int
}

func resolveMemoryGate(buildCtx contract.BuildCtx, cfg *Config) MemoryGateSnapshot {
	cfg = memoryConfig(cfg)
	snapshot := baseMemoryGateSnapshot(buildCtx, cfg)
	snapshot.AutoEnabled = resolveAutoEnabled(snapshot, cfg)
	snapshot.RequestedMemoryMode = selectRequestedMemoryMode(snapshot)
	snapshot.KairosActive = snapshot.AutoEnabled && snapshot.RequestedMemoryMode == MemoryModeKairos
	snapshot.EffectiveMemoryMode = effectiveMemoryMode(snapshot.RequestedMemoryMode)
	snapshot.InjectMemoryIndex = snapshot.AutoEnabled && !snapshot.SkipIndex
	snapshot.InjectTeamMemIndex = snapshot.AutoEnabled && snapshot.TeamMemEnabled && !snapshot.SkipIndex && !snapshot.KairosActive
	snapshot.EnableRelevantPrefetch = resolveRelevantPrefetchGate(snapshot)
	return snapshot
}

func baseMemoryGateSnapshot(buildCtx contract.BuildCtx, cfg *Config) MemoryGateSnapshot {
	pathSource := resolveAutoMemPathSource(cfg)
	trustedSource := TrustedPathSettingSourceNone
	if pathSource == AutoMemPathSourceSettings {
		trustedSource = resolveTrustedAutoMemPathSource(cfg)
	}
	bareMode := resolveBareMode(buildCtx)
	hasAdditionalDirs := len(buildCtx.AdditionalWorkingDirectories) > 0
	return MemoryGateSnapshot{
		ForceEnabledByEnvFalsy:    isEnvDefinedFalsy(os.Getenv(envClaudeDisableAutoMemory)),
		DisabledByEnvTruthy:       isEnvTruthy(os.Getenv(envClaudeDisableAutoMemory)),
		SettingsAutoMemoryEnabled: settingsAutoMemoryEnabled(buildCtx),
		RemoteMode:                parseBoolEnv(envClaudeRemote, false),
		HasPersistentStorage:      hasPersistentMemoryStorage(cfg),
		BareMode:                  bareMode,
		HasAdditionalDirsForBare:  hasAdditionalDirs,
		DisableClaudeMds:          resolveDisableClaudeMds(buildCtx, bareMode, hasAdditionalDirs),
		SkipProjectLocalClaudeMd:  gateFlagEnabled(buildCtx.SessionFlags, "tengu_paper_halyard", "paper_halyard"),
		SkipIndex:                 resolveFlagOrConfig(buildCtx, cfg.SkipIndex, "skip_index", "skipIndex", "tengu_moth_copse"),
		KairosEnabled:             resolveFeatureFlag(buildCtx, cfg.Features.Kairos, "memory_kairos", "kairos"),
		TeamMemEnabled:            resolveFeatureFlag(buildCtx, cfg.Features.TeamMemory, "team_memory", "teamMemory", "teammem"),
		SearchPastContextEnabled:  resolveFeatureFlag(buildCtx, cfg.Features.SearchPastContext, "search_past_context", "searchPastContext"),
		AutoMemPathSource:         pathSource,
		TrustedAutoMemPathSource:  trustedSource,
	}
}

func resolveSimpleMode(buildCtx contract.BuildCtx) bool {
	return parseBoolEnv(envClaudeSimple, false) || gateFlagEnabled(buildCtx.SessionFlags, "simple_mode", "simpleMode", "simple")
}

func resolveBareMode(buildCtx contract.BuildCtx) bool {
	return resolveSimpleMode(buildCtx) || gateFlagEnabled(buildCtx.SessionFlags, "bare_mode", "bareMode", "bare")
}

func resolveDisableClaudeMds(buildCtx contract.BuildCtx, bareMode, hasAdditionalDirs bool) bool {
	if isEnvTruthy(os.Getenv(envClaudeDisableClaudeMds)) {
		return true
	}
	if gateFlagEnabled(buildCtx.SessionFlags, "disable_claude_mds", "disableClaudeMds") {
		return true
	}
	return bareMode && !hasAdditionalDirs
}

func resolveFlagOrConfig(buildCtx contract.BuildCtx, enabled bool, names ...string) bool {
	return enabled || gateFlagEnabled(buildCtx.SessionFlags, names...)
}

func resolveFeatureFlag(buildCtx contract.BuildCtx, enabled bool, names ...string) bool {
	return resolveFlagOrConfig(buildCtx, enabled, names...)
}

func resolveRelevantPrefetchGate(snapshot MemoryGateSnapshot) bool {
	if !snapshot.AutoEnabled {
		return false
	}
	return snapshot.SkipIndex
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
	return configuredAutoMemPathOverride(cfg) != ""
}

func isAutoMemPath(cfg *Config, path string) bool {
	if cfg == nil || strings.TrimSpace(path) == "" {
		return false
	}
	candidate, err := cleanAbsolutePath(path)
	if err != nil {
		return false
	}
	root, err := resolvedStoreRoot(cfg.RootDir, cfg.ProjectRoot, configuredAutoMemPathOverride(cfg))
	if err != nil || strings.TrimSpace(root) == "" {
		return false
	}
	root, err = cleanAbsolutePath(root)
	if err != nil {
		return false
	}
	return platformshared.ContainsPath(root, candidate)
}

func resolveAutoEnabled(snapshot MemoryGateSnapshot, _ *Config) bool {
	switch {
	case snapshot.DisabledByEnvTruthy:
		return false
	case snapshot.ForceEnabledByEnvFalsy:
		return true
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
	case MemoryModeKairos:
		return MemoryModeKairos
	case MemoryModeCombined:
		return MemoryModeStandard
	default:
		return mode
	}
}

func resolveAutoMemPathSource(cfg *Config) AutoMemPathSource {
	switch {
	case envAutoMemPathOverride(cfg) != "":
		return AutoMemPathSourceEnv
	case hasAutoMemPathOverride(cfg):
		return AutoMemPathSourceSettings
	case firstNonEmptyEnv(envMemoryRoot, envClaudeRemoteMemoryDir) != "":
		return AutoMemPathSourceEnv
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
