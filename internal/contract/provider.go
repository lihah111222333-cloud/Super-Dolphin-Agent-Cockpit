package contract

import (
	"context"
	"fmt"
	"sort"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// Driver is the provider factory contract.
type Driver interface {
	Name() string
	StartSession(ctx context.Context, req dto.StartSessionRequest) (Session, error)
	ResumeSession(ctx context.Context, req dto.ResumeSessionRequest) (Session, error)
}

type NativeToolFilterMode string

const (
	NativeToolFilterModeHard NativeToolFilterMode = "hard"
	NativeToolFilterModeSoft NativeToolFilterMode = "soft"
)

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

var codexMultiAgentNativeToolIDs = []string{
	CodexNativeToolMultiAgent,
	CodexNativeToolMultiToolParallel,
	CodexNativeToolSpawnAgent,
	CodexNativeToolSendInput,
	CodexNativeToolResumeAgent,
	CodexNativeToolWaitAgent,
	CodexNativeToolCloseAgent,
}

// KnownCodexNativeToolIDs 处理knowncodexnative工具ids。
func KnownCodexNativeToolIDs() []string {
	return append([]string(nil), knownCodexNativeToolIDs...)
}

// IsKnownCodexNativeTool 判断knowncodexnative工具是否可用。
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

type CodexNativeToolPolicy struct {
	disabled          map[string]struct{}
	tiers             map[string]NativeToolEnforcement
	appServerFeatures []string
}

// NewCodexNativeToolPolicy 创建codexnative工具策略。
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

func (p *CodexNativeToolPolicy) assignEnforcement() {
	p.assignExecEnforcement()
	p.assignMultiAgentEnforcement()
	p.assignFeatureBackedTools()
	p.assignSoftAuditTools()
}

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

func (p *CodexNativeToolPolicy) assignFeatureTool(id string, features ...string) {
	if !p.has(id) {
		return
	}
	p.tiers[id] = NativeToolEnforcementNativeHard
	for _, feature := range features {
		p.addFeature(feature)
	}
}

func (p CodexNativeToolPolicy) has(id string) bool {
	_, ok := p.disabled[strings.TrimSpace(id)]
	return ok
}

func (p CodexNativeToolPolicy) hasAny(ids ...string) bool {
	for _, id := range ids {
		if p.has(id) {
			return true
		}
	}
	return false
}

func (p *CodexNativeToolPolicy) addFeature(feature string) {
	for _, item := range p.appServerFeatures {
		if item == feature {
			return
		}
	}
	p.appServerFeatures = append(p.appServerFeatures, feature)
	sort.Strings(p.appServerFeatures)
}

// Tier 处理tier。
func (p CodexNativeToolPolicy) Tier(id string) NativeToolEnforcement {
	return p.tiers[strings.TrimSpace(id)]
}

// HasProcessFlags 判断进程flags是否可用。
func (p CodexNativeToolPolicy) HasProcessFlags() bool {
	return len(p.appServerFeatures) != 0
}

// ProcessSignature 处理进程签名。
func (p CodexNativeToolPolicy) ProcessSignature() string {
	return strings.Join(p.appServerFeatures, ",")
}

// AppServerArgs 处理app服务端args。
func (p CodexNativeToolPolicy) AppServerArgs() []string {
	args := make([]string, 0, len(p.appServerFeatures)*2)
	for _, feature := range p.appServerFeatures {
		args = append(args, "--disable", feature)
	}
	return args
}

// RequiresReadOnlySandbox 处理requiresreadonly沙箱。
func (p CodexNativeToolPolicy) RequiresReadOnlySandbox() bool {
	for _, tier := range p.tiers {
		if tier == NativeToolEnforcementEffectHard {
			return true
		}
	}
	return false
}

// NativeToolDescriptor describes an upstream CLI built-in tool that the
// settings UI can render and the prompt/provider layers can filter.
type NativeToolDescriptor struct {
	ID              string
	Label           string
	Description     string
	DefaultDisabled bool
	Provider        string
	FilterMode      NativeToolFilterMode
}

// DriverFactory constructs Driver instances for DI registration.
type DriverFactory struct {
	Name        string
	Create      func() Driver
	NativeTools []NativeToolDescriptor
}

// Session is the unified provider session abstraction.
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

// TurnHandle is the handle for an in-flight turn.
type TurnHandle interface {
	LocalID() string
	ProviderID() string
	Done() <-chan struct{}
	Err() error
}

type CapabilityError struct {
	Capability string
	Driver     string
}

// Error 返回错误文本。
func (e *CapabilityError) Error() string {
	return fmt.Sprintf("capability %q is not supported by %s driver", e.Capability, e.Driver)
}

// NewCapabilityError 创建capability错误。
func NewCapabilityError(cap, driver string) error {
	return &CapabilityError{Capability: cap, Driver: driver}
}

// HasCapability 判断capability是否可用。
func HasCapability(caps dto.CapabilitySet, cap string) bool {
	if caps == nil {
		return false
	}
	return caps[cap]
}

// HasAllCapabilities 判断allcapabilities是否可用。
func HasAllCapabilities(caps dto.CapabilitySet, want ...string) bool {
	for _, cap := range want {
		if !HasCapability(caps, cap) {
			return false
		}
	}
	return true
}
