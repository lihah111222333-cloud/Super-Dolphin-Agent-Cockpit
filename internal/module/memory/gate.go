package memory

import (
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/util/pathutil"
)

// AutoMemPathSource 标记当前 AutoMem 根目录来自默认派生、设置还是环境变量。
// gate 会把该来源带给 UI 和 provider 边界，用于解释路径信任级别。
type AutoMemPathSource string

const (
	AutoMemPathSourceDefault  AutoMemPathSource = "default"
	AutoMemPathSourceSettings AutoMemPathSource = "settings"
	AutoMemPathSourceEnv      AutoMemPathSource = "env"
)

// MemoryHarness 标识当前嵌入的底层 CLI harness。
// 该值只在服务端内部使用，UI 不展示；当底层 CLI 已自带等价记忆管线时，
// Super-Dolphin 的记忆注入会让位，避免重复注入。
type MemoryHarness string

const (
	MemoryHarnessGeneric    MemoryHarness = "generic"
	MemoryHarnessClaudeCode MemoryHarness = "claude_code"
	MemoryHarnessCodex      MemoryHarness = "codex"
)

// MemoryGateSnapshot 是一次 prompt/turn 组装时的 memory gate 决策快照。
// 它把配置、环境变量、会话 flag 和 provider overlay 统一收敛，避免各入口自行推导可见行为。
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
	// InjectPromptEntrypoint 控制 MemoryEntrypointProvider 是否把 MEMORY.md
	// 入口注入启动 prompt。它和 InjectMemoryIndex 分开保存，便于未来只关闭
	// prompt 入口但保留底层记忆存储能力。
	InjectPromptEntrypoint   bool
	EnableRelevantPrefetch   bool
	SearchPastContextEnabled bool
	RequestedMemoryMode      MemoryMode
	EffectiveMemoryMode      MemoryMode
	AutoMemPathSource        AutoMemPathSource
	TrustedAutoMemPathSource TrustedPathSettingSource
	Harness                  MemoryHarness
}

// SuppressForOverlay 判断当前是否应让位给底层 CLI 原生记忆管线。
// 目前 claude_code harness 会触发该边界；后续其它 overlay 能力可复用同一字段接入。
func (s MemoryGateSnapshot) SuppressForOverlay() bool {
	return s.Harness == MemoryHarnessClaudeCode
}

// RelevantPrefetchSurfacedState 记录当前 thread 已展示的相关记忆预算。
// 该状态只影响后续 turn 是否继续预取，不会写入磁盘或 prompt snapshot。
type RelevantPrefetchSurfacedState struct {
	TotalBytes int
}

// resolveMemoryGate 汇总配置、环境变量和会话 flag，生成记忆功能 gate 快照。
// 该快照统一决定入口注入、团队索引、相关记忆预取和有效记忆模式，避免各 provider 自行判断。
func resolveMemoryGate(buildCtx contract.BuildCtx, cfg *Config) MemoryGateSnapshot {
	cfg = memoryConfig(cfg)
	snapshot := baseMemoryGateSnapshot(buildCtx, cfg)
	snapshot.AutoEnabled = resolveAutoEnabled(snapshot, cfg)
	snapshot.RequestedMemoryMode = selectRequestedMemoryMode(snapshot)
	snapshot.KairosActive = snapshot.AutoEnabled && snapshot.RequestedMemoryMode == MemoryModeKairos
	snapshot.EffectiveMemoryMode = effectiveMemoryMode(snapshot.RequestedMemoryMode)
	overlay := snapshot.SuppressForOverlay()
	snapshot.InjectMemoryIndex = snapshot.AutoEnabled && !snapshot.SkipIndex && !overlay
	snapshot.InjectTeamMemIndex = snapshot.AutoEnabled && snapshot.TeamMemEnabled && !snapshot.SkipIndex && !snapshot.KairosActive && !overlay
	snapshot.InjectPromptEntrypoint = snapshot.InjectMemoryIndex
	snapshot.EnableRelevantPrefetch = resolveRelevantPrefetchGate(snapshot)
	return snapshot
}

// baseMemoryGateSnapshot 读取不依赖 AutoEnabled 的基础 gate 输入。
// 它只采集配置和运行环境，不推导注入字段，便于 resolveMemoryGate 按固定顺序计算。
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
		Harness:                   resolveHarnessFromConfig(cfg),
	}
}

// resolveHarnessFromConfig 优先使用 NewConfig 时冻结的 harness。
// 只有 cfg.Harness 为空时才读取环境变量，主要服务测试注入，生产路径不依赖实时环境漂移。
func resolveHarnessFromConfig(cfg *Config) MemoryHarness {
	if cfg != nil && cfg.Harness != "" {
		return cfg.Harness
	}
	return resolveMemoryHarness()
}

// resolveMemoryHarness 从环境变量解析底层 CLI harness。
// 未识别值按 generic 处理，避免未知 harness 意外触发 overlay 抑制。
func resolveMemoryHarness() MemoryHarness {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(envHarnessKind)))
	switch raw {
	case "claude_code", "claude-code", "claudecode":
		return MemoryHarnessClaudeCode
	case "codex":
		return MemoryHarnessCodex
	default:
		return MemoryHarnessGeneric
	}
}

// resolveSimpleMode 判断当前会话是否启用 simple 模式。
// 环境变量和会话 flag 任一启用即生效。
func resolveSimpleMode(buildCtx contract.BuildCtx) bool {
	return parseBoolEnv(envClaudeSimple, false) || gateFlagEnabled(buildCtx.SessionFlags, "simple_mode", "simpleMode", "simple")
}

// resolveBareMode 判断当前会话是否启用 bare 模式。
// bare 继承 simple 语义，会关闭自动记忆和默认 CLAUDE.md 来源。
func resolveBareMode(buildCtx contract.BuildCtx) bool {
	return resolveSimpleMode(buildCtx) || gateFlagEnabled(buildCtx.SessionFlags, "bare_mode", "bareMode", "bare")
}

// resolveDisableClaudeMds 判断是否禁止加载 CLAUDE.md 来源。
// bare 模式下只有显式 additional dirs 时才允许继续加载，避免裸会话带入项目上下文。
func resolveDisableClaudeMds(buildCtx contract.BuildCtx, bareMode, hasAdditionalDirs bool) bool {
	if isEnvTruthy(os.Getenv(envClaudeDisableClaudeMds)) {
		return true
	}
	if gateFlagEnabled(buildCtx.SessionFlags, "disable_claude_mds", "disableClaudeMds") {
		return true
	}
	return bareMode && !hasAdditionalDirs
}

// resolveFlagOrConfig 合并配置开关和会话 flag。
// 该 helper 只做布尔或运算，不解析复杂 gate 依赖。
func resolveFlagOrConfig(buildCtx contract.BuildCtx, enabled bool, names ...string) bool {
	return enabled || gateFlagEnabled(buildCtx.SessionFlags, names...)
}

// resolveFeatureFlag 解析可通过配置或会话 flag 开启的功能开关。
// 保留独立函数名是为了让 gate 代码表达“功能级开关”语义。
func resolveFeatureFlag(buildCtx contract.BuildCtx, enabled bool, names ...string) bool {
	return resolveFlagOrConfig(buildCtx, enabled, names...)
}

// resolveRelevantPrefetchGate 判断是否允许相关记忆后台预取。
// 只有 AutoMem 开启、非 overlay 且跳过索引注入时才开启，避免和入口索引重复占用 prompt 预算。
func resolveRelevantPrefetchGate(snapshot MemoryGateSnapshot) bool {
	if !snapshot.AutoEnabled {
		return false
	}
	if snapshot.SuppressForOverlay() {
		return false
	}
	return snapshot.SkipIndex
}

// shouldStartRelevantMemoryPrefetch 判断本轮是否应启动相关记忆预取。
// 空文本、单词级查询或已展示内容超预算时跳过，避免后台检索制造低价值上下文。
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

// memoryConfig 返回非 nil 配置指针。
// 只用于读取默认零值，不能替代具体入口的配置完整性校验。
func memoryConfig(cfg *Config) *Config {
	if cfg != nil {
		return cfg
	}
	return &Config{}
}

// hasAutoMemPathOverride 判断是否配置了显式 AutoMem 根目录。
// 该信息用于区分默认项目派生路径和用户信任路径。
func hasAutoMemPathOverride(cfg *Config) bool {
	return configuredAutoMemPathOverride(cfg) != ""
}

// isAutoMemPath 判断文件路径是否位于当前 AutoMem 根目录内。
// 路径和根目录都会清理为绝对路径；解析失败时返回 false，避免误判工具写入已处理。
func isAutoMemPath(cfg *Config, path string) bool {
	if cfg == nil || strings.TrimSpace(path) == "" {
		return false
	}
	candidate, err := shared.CleanAbsolutePath(path)
	if err != nil {
		return false
	}
	root, err := resolvedStoreRoot(cfg.RootDir, cfg.ProjectRoot, configuredAutoMemPathOverride(cfg))
	if err != nil || strings.TrimSpace(root) == "" {
		return false
	}
	root, err = shared.CleanAbsolutePath(root)
	if err != nil {
		return false
	}
	return pathutil.ContainsPath(root, candidate)
}

// resolveAutoEnabled 根据环境变量、设置、bare/remote 状态决定 AutoMem 是否可用。
// 环境变量显式 falsy 可强制开启；远端模式缺少持久化存储时必须关闭。
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

// selectRequestedMemoryMode 根据 gate 选择用户请求的记忆模式。
// AutoMem 关闭时返回空；kairos 优先于 team combined，避免两个模式同时注入。
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

// effectiveMemoryMode 将请求模式规整为可执行模式。
// 未知或空模式回到 standard，保证 provider 只面对已知模式集合。
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
