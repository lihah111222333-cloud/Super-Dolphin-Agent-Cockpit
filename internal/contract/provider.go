package contract

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// Driver 是 provider 适配器的统一入口，负责启动或恢复一个会话。
// 上层只依赖该契约，不直接感知 Claude/Codex 等具体实现。
type Driver interface {
	Name() string
	StartSession(ctx context.Context, req dto.StartSessionRequest) (Session, error)
	ResumeSession(ctx context.Context, req dto.ResumeSessionRequest) (Session, error)
}

// NativeToolFilterMode 表示 UI 和 prompt 层过滤原生工具时的强弱策略。
type NativeToolFilterMode string

const (
	NativeToolFilterModeHard NativeToolFilterMode = "hard"
	NativeToolFilterModeSoft NativeToolFilterMode = "soft"
)

// NativeToolEnforcement 描述禁用原生工具时需要由哪一层兜住执行效果。
type NativeToolEnforcement string

const (
	NativeToolEnforcementNativeHard NativeToolEnforcement = "native-hard"
	NativeToolEnforcementEffectHard NativeToolEnforcement = "effect-hard"
	NativeToolEnforcementSoftAudit  NativeToolEnforcement = "soft-audit"

	CodexNativeToolReadFile                 = "read_file"
	CodexNativeToolWriteNewFile             = "write_new_file"
	CodexNativeToolApplyPatch               = "apply_patch"
	CodexNativeToolShell                    = "shell"
	CodexNativeToolListDir                  = "list_dir"
	CodexNativeToolMultiAgent               = "multi_agent"
	CodexNativeToolMultiToolParallel        = "multi_tool_use.parallel"
	CodexNativeToolSpawnAgent               = "spawn_agent"
	CodexNativeToolSendInput                = "send_input"
	CodexNativeToolResumeAgent              = "resume_agent"
	CodexNativeToolWaitAgent                = "wait_agent"
	CodexNativeToolCloseAgent               = "close_agent"
	CodexNativeToolToolSearch               = "tool_search"
	CodexNativeToolWebSearch                = "web_search"
	CodexNativeToolImageGen                 = "image_generation"
	CodexNativeToolViewImage                = "view_image"
	CodexNativeToolRequestInput             = "request_user_input"
	CodexNativeToolRequestPerms             = "request_permissions"
	CodexNativeToolPluginInstall            = "request_plugin_install"
	CodexNativeToolListMCPResources         = "list_mcp_resources"
	CodexNativeToolListMCPResourceTemplates = "list_mcp_resource_templates"
	CodexNativeToolReadMCPResource          = "read_mcp_resource"
	CodexNativeToolBrowserUse               = "browser_use"
	CodexNativeToolBrowserUseExternal       = "browser_use_external"
	CodexNativeToolComputerUse              = "computer_use"
	CodexNativeToolWorkspaceDeps            = "workspace_dependencies"
	CodexNativeToolApps                     = "apps"
	CodexNativeToolPlugins                  = "plugins"
	CodexNativeToolGoals                    = "goals"
	CodexNativeToolUpdatePlan               = "update_plan"

	CodexFeatureShellTool              = "shell_tool"
	CodexFeatureUnifiedExec            = "unified_exec"
	CodexFeatureMultiAgent             = "multi_agent"
	CodexFeatureMultiAgentV2           = "multi_agent_v2"
	CodexFeatureEnableFanout           = "enable_fanout"
	CodexFeatureChildAgentsMD          = "child_agents_md"
	CodexFeatureToolSearch             = "tool_search"
	CodexFeatureWebSearchRequest       = "web_search_request"
	CodexFeatureWebSearchCached        = "web_search_cached"
	CodexFeatureImageGeneration        = "image_generation"
	CodexFeatureBrowserUse             = "browser_use"
	CodexFeatureBrowserUseExternal     = "browser_use_external"
	CodexFeatureComputerUse            = "computer_use"
	CodexFeatureWorkspaceDeps          = "workspace_dependencies"
	CodexFeatureApps                   = "apps"
	CodexFeaturePlugins                = "plugins"
	CodexFeatureRequestPermissionsTool = "request_permissions_tool"
	CodexFeatureGoals                  = "goals"
)

// knownCodexNativeToolIDs 是 Codex 运行时已知原生工具 ID 白名单。
var knownCodexNativeToolIDs = []string{
	CodexNativeToolReadFile,
	CodexNativeToolWriteNewFile,
	CodexNativeToolApplyPatch,
	CodexNativeToolShell,
	CodexNativeToolListDir,
	CodexNativeToolMultiAgent,
	CodexNativeToolMultiToolParallel,
	CodexNativeToolSpawnAgent,
	CodexNativeToolSendInput,
	CodexNativeToolResumeAgent,
	CodexNativeToolWaitAgent,
	CodexNativeToolCloseAgent,
	CodexNativeToolToolSearch,
	CodexNativeToolWebSearch,
	CodexNativeToolImageGen,
	CodexNativeToolViewImage,
	CodexNativeToolRequestInput,
	CodexNativeToolRequestPerms,
	CodexNativeToolPluginInstall,
	CodexNativeToolListMCPResources,
	CodexNativeToolListMCPResourceTemplates,
	CodexNativeToolReadMCPResource,
	CodexNativeToolBrowserUse,
	CodexNativeToolBrowserUseExternal,
	CodexNativeToolComputerUse,
	CodexNativeToolWorkspaceDeps,
	CodexNativeToolApps,
	CodexNativeToolPlugins,
	CodexNativeToolGoals,
	CodexNativeToolUpdatePlan,
}

// codexMultiAgentNativeToolIDs 归类会启动或控制子 agent 的原生工具。
var codexMultiAgentNativeToolIDs = []string{
	CodexNativeToolMultiAgent,
	CodexNativeToolMultiToolParallel,
	CodexNativeToolSpawnAgent,
	CodexNativeToolSendInput,
	CodexNativeToolResumeAgent,
	CodexNativeToolWaitAgent,
	CodexNativeToolCloseAgent,
}

// KnownCodexNativeToolIDs 返回已知 Codex 原生工具 ID 的副本。
// 调用方可安全排序或过滤返回值，不会污染全局白名单。
func KnownCodexNativeToolIDs() []string {
	return append([]string(nil), knownCodexNativeToolIDs...)
}

// ReadOnlyCodexNativeDeniedTools 返回只读/规划子 agent 必须禁用的 Codex 原生工具名。
// 包含执行写入工具和递归 agent 工具，返回副本避免调用方污染共享列表。
func ReadOnlyCodexNativeDeniedTools() []string {
	tools := []string{
		CodexNativeToolShell,
		CodexNativeToolApplyPatch,
		CodexNativeToolWriteNewFile,
		CodexNativeToolUpdatePlan,
	}
	return append(tools, codexMultiAgentNativeToolIDs...)
}

// IsKnownCodexNativeTool 判断工具 ID 是否属于当前可治理的 Codex 原生工具集合。
func IsKnownCodexNativeTool(id string) bool {
	switch strings.TrimSpace(id) {
	case CodexNativeToolShell, CodexNativeToolApplyPatch, CodexNativeToolWriteNewFile,
		CodexNativeToolReadFile, CodexNativeToolListDir, CodexNativeToolMultiAgent,
		CodexNativeToolMultiToolParallel, CodexNativeToolSpawnAgent, CodexNativeToolSendInput,
		CodexNativeToolResumeAgent, CodexNativeToolWaitAgent, CodexNativeToolCloseAgent,
		CodexNativeToolToolSearch, CodexNativeToolWebSearch, CodexNativeToolImageGen,
		CodexNativeToolViewImage, CodexNativeToolRequestInput, CodexNativeToolRequestPerms,
		CodexNativeToolPluginInstall, CodexNativeToolListMCPResources,
		CodexNativeToolListMCPResourceTemplates, CodexNativeToolReadMCPResource,
		CodexNativeToolBrowserUse, CodexNativeToolBrowserUseExternal, CodexNativeToolComputerUse,
		CodexNativeToolWorkspaceDeps, CodexNativeToolApps, CodexNativeToolPlugins,
		CodexNativeToolGoals, CodexNativeToolUpdatePlan:
		return true
	default:
		return false
	}
}

// CodexNativeToolPolicy 保存禁用工具后的分层执行策略。
// disabled 是输入清洗后的工具集合，tiers 和 appServerFeatures 由 assignEnforcement 派生。
type CodexNativeToolPolicy struct {
	disabled          map[string]struct{}
	tiers             map[string]NativeToolEnforcement
	appServerFeatures []string
}

// NewCodexNativeToolPolicy 根据已校验的禁用工具 ID 构造 Codex 原生工具策略。
// provider 启动入口负责拒绝未知 ID；这里仅把已知工具映射到执行策略。
func NewCodexNativeToolPolicy(disabled []string) CodexNativeToolPolicy {
	policy := CodexNativeToolPolicy{
		disabled: make(map[string]struct{}),
		tiers:    make(map[string]NativeToolEnforcement),
	}
	for _, value := range disabled {
		id := strings.TrimSpace(value)
		if IsKnownCodexNativeTool(id) {
			policy.disabled[id] = struct{}{}
		}
	}
	policy.assignEnforcement()
	return policy
}

// assignEnforcement 计算禁用工具对应的原生硬禁用、效果硬约束和审计层级。
func (p *CodexNativeToolPolicy) assignEnforcement() {
	p.assignExecEnforcement()
	p.assignMultiAgentEnforcement()
	p.assignFeatureBackedTools()
	p.assignSoftAuditTools()
}

// assignExecEnforcement 处理 shell/apply_patch/write_new_file 的联动禁用。
// 三者都禁用时才追加 unified_exec 特性，避免只禁写时误关读执行能力。
func (p *CodexNativeToolPolicy) assignExecEnforcement() {
	fullExecGroup := p.has(CodexNativeToolShell) &&
		p.has(CodexNativeToolApplyPatch) &&
		p.has(CodexNativeToolWriteNewFile)
	if p.has(CodexNativeToolShell) {
		p.addFeature(CodexFeatureShellTool)
		p.tiers[CodexNativeToolShell] = NativeToolEnforcementNativeHard
	}
	p.assignWriteTool(CodexNativeToolApplyPatch, fullExecGroup)
	p.assignWriteTool(CodexNativeToolWriteNewFile, fullExecGroup)
	if fullExecGroup {
		p.addFeature(CodexFeatureUnifiedExec)
	}
}

// assignMultiAgentEnforcement 处理子 agent 相关工具的硬禁用和 App Server 特性开关。
func (p *CodexNativeToolPolicy) assignMultiAgentEnforcement() {
	if !p.hasAny(codexMultiAgentNativeToolIDs...) {
		return
	}
	for _, id := range codexMultiAgentNativeToolIDs {
		if p.has(id) {
			p.tiers[id] = NativeToolEnforcementNativeHard
		}
	}
	for _, feature := range []string{
		CodexFeatureMultiAgent,
		CodexFeatureMultiAgentV2,
		CodexFeatureEnableFanout,
	} {
		p.addFeature(feature)
	}
}

// assignFeatureBackedTools 将有 App Server feature flag 的原生工具映射到启动参数。
func (p *CodexNativeToolPolicy) assignFeatureBackedTools() {
	p.assignFeatureTool(CodexNativeToolToolSearch, CodexFeatureToolSearch)
	p.assignFeatureTool(CodexNativeToolWebSearch, CodexFeatureWebSearchCached, CodexFeatureWebSearchRequest)
	p.assignFeatureTool(CodexNativeToolImageGen, CodexFeatureImageGeneration)
	p.assignFeatureTool(CodexNativeToolRequestPerms, CodexFeatureRequestPermissionsTool)
	p.assignFeatureTool(CodexNativeToolPluginInstall, CodexFeaturePlugins)
	p.assignFeatureTool(CodexNativeToolBrowserUse, CodexFeatureBrowserUse)
	p.assignFeatureTool(CodexNativeToolBrowserUseExternal, CodexFeatureBrowserUseExternal)
	p.assignFeatureTool(CodexNativeToolComputerUse, CodexFeatureComputerUse)
	p.assignFeatureTool(CodexNativeToolWorkspaceDeps, CodexFeatureWorkspaceDeps)
	p.assignFeatureTool(CodexNativeToolApps, CodexFeatureApps)
	p.assignFeatureTool(CodexNativeToolPlugins, CodexFeaturePlugins)
	p.assignFeatureTool(CodexNativeToolGoals, CodexFeatureGoals)
}

// assignSoftAuditTools 标记只需要审计禁用效果的读类或状态类工具。
func (p *CodexNativeToolPolicy) assignSoftAuditTools() {
	for _, id := range []string{
		CodexNativeToolReadFile,
		CodexNativeToolListDir,
		CodexNativeToolViewImage,
		CodexNativeToolRequestInput,
		CodexNativeToolListMCPResources,
		CodexNativeToolListMCPResourceTemplates,
		CodexNativeToolReadMCPResource,
		CodexNativeToolUpdatePlan,
	} {
		if p.has(id) {
			p.tiers[id] = NativeToolEnforcementSoftAudit
		}
	}
}

// assignWriteTool 根据是否形成完整执行工具组决定禁用层级。
func (p *CodexNativeToolPolicy) assignWriteTool(id string, nativeHard bool) {
	if !p.has(id) {
		return
	}
	if nativeHard {
		p.tiers[id] = NativeToolEnforcementNativeHard
		return
	}
	p.tiers[id] = NativeToolEnforcementEffectHard
}

// assignFeatureTool 把单个禁用工具映射为一个或多个 App Server feature flag。
func (p *CodexNativeToolPolicy) assignFeatureTool(id string, features ...string) {
	if !p.has(id) {
		return
	}
	p.tiers[id] = NativeToolEnforcementNativeHard
	for _, feature := range features {
		p.addFeature(feature)
	}
}

// has 判断工具 ID 是否出现在清洗后的禁用集合中。
func (p CodexNativeToolPolicy) has(id string) bool {
	_, ok := p.disabled[strings.TrimSpace(id)]
	return ok
}

// hasAny 判断任一工具 ID 是否被禁用，用于同类工具组快速分支。
func (p CodexNativeToolPolicy) hasAny(ids ...string) bool {
	return slices.ContainsFunc(ids, p.has)
}

// addFeature 追加 App Server 禁用特性并保持稳定排序。
func (p *CodexNativeToolPolicy) addFeature(feature string) {
	if slices.Contains(p.appServerFeatures, feature) {
		return
	}
	p.appServerFeatures = append(p.appServerFeatures, feature)
	sort.Strings(p.appServerFeatures)
}

// Tier 返回指定工具的禁用执行层级；未命中时返回空值表示无需治理。
func (p CodexNativeToolPolicy) Tier(id string) NativeToolEnforcement {
	return p.tiers[strings.TrimSpace(id)]
}

// HasProcessFlags 表示该策略是否需要向 Codex App Server 追加 --disable 参数。
func (p CodexNativeToolPolicy) HasProcessFlags() bool {
	return len(p.appServerFeatures) != 0
}

// ProcessSignature 返回稳定的 feature 列表签名，用于区分可复用进程池。
func (p CodexNativeToolPolicy) ProcessSignature() string {
	return strings.Join(p.appServerFeatures, ",")
}

// AppServerArgs 将禁用 feature 转成 Codex App Server 启动参数。
func (p CodexNativeToolPolicy) AppServerArgs() []string {
	args := make([]string, 0, len(p.appServerFeatures)*2)
	for _, feature := range p.appServerFeatures {
		args = append(args, "--disable", feature)
	}
	return args
}

// RequiresReadOnlySandbox 表示策略需要用只读沙箱补足无法原生硬禁用的写入工具。
func (p CodexNativeToolPolicy) RequiresReadOnlySandbox() bool {
	if p.has(CodexNativeToolApplyPatch) || p.has(CodexNativeToolWriteNewFile) {
		return true
	}
	for _, tier := range p.tiers {
		if tier == NativeToolEnforcementEffectHard {
			return true
		}
	}
	return false
}

// NativeToolDescriptor 描述上游 CLI 内置工具，供设置页展示并供 prompt/provider 层过滤。
type NativeToolDescriptor struct {
	ID              string
	Label           string
	Description     string
	DefaultDisabled bool
	Provider        string
	FilterMode      NativeToolFilterMode
}

// DriverFactory 是 provider 驱动的 DI 注册单元，连同原生工具元数据一起暴露。
type DriverFactory struct {
	Name        string
	Create      func() Driver
	NativeTools []NativeToolDescriptor
}

// Session 是统一的 provider 会话抽象，封装 turn、thread 和配置操作。
// 调用方必须通过该接口关闭/中断会话，不能绕过 provider 直接操作底层进程。
type Session interface {
	ThreadID() string
	RolloutPath() string
	Capabilities() dto.CapabilitySet

	StartTurn(ctx context.Context, req dto.TurnRequest) (TurnHandle, error)
	Interrupt(ctx context.Context, req dto.InterruptRequest) error
	ForceComplete(ctx context.Context, req dto.ForceCompleteRequest) error

	ListThreads(ctx context.Context) ([]dto.ThreadRef, error)
	ForkThread(ctx context.Context, req dto.ForkRequest) (dto.ForkResult, error)
	ReadHistory(ctx context.Context, threadID string, limit int) ([]dto.Message, error)

	Configure(ctx context.Context, patch dto.ThreadConfigPatch) error
	Close(ctx context.Context) error
	ForceStop() error
}

// TurnHandle 表示正在执行的 turn，提供本地/上游 ID、完成信号和最终错误。
type TurnHandle interface {
	LocalID() string
	ProviderID() string
	Done() <-chan struct{}
	Err() error
}

// CapabilityError 表示驱动缺少调用方要求的 provider 能力。
type CapabilityError struct {
	Capability string
	Driver     string
}

// Error 返回缺失能力的可读错误文本。
func (e *CapabilityError) Error() string {
	return fmt.Sprintf("capability %q is not supported by %s driver", e.Capability, e.Driver)
}

// NewCapabilityError 构造 provider 能力缺失错误，供跨模块边界统一返回。
func NewCapabilityError(cap, driver string) error {
	return &CapabilityError{Capability: cap, Driver: driver}
}

// HasCapability 判断能力集合是否启用了指定能力；nil 集合按不支持处理。
func HasCapability(caps dto.CapabilitySet, cap string) bool {
	if caps == nil {
		return false
	}
	return caps[cap]
}

// HasAllCapabilities 要求能力集合同时具备所有指定能力，任一缺失即返回 false。
func HasAllCapabilities(caps dto.CapabilitySet, want ...string) bool {
	for _, cap := range want {
		if !HasCapability(caps, cap) {
			return false
		}
	}
	return true
}
