package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/nodeexec"
	commandcardstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/commandcard"
	promptstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/prompt"
	sharedfilestore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sharedfile"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools/modelregistry"
	workspace "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/workspace"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/idgen"
)

// ToolPorts 按工具消费面拆分 orchestration 依赖，避免 registry 持有完整服务边界。
type ToolPorts struct {
	AgentLaunch            contract.AgentLaunchPort
	AgentMessenger         SendMessagePorts
	AgentStopWait          contract.AgentStopWaitPort
	AgentRecovery          contract.AgentRecoveryPort
	AgentInterrupt         contract.AgentInterruptPort
	AgentList              AgentListPorts
	AgentReports           agentReportPort
	DAGCreate              contract.DAGCreateRuntime
	DAGRuntime             contract.DAGRuntime
	DAGDelete              contract.DAGDeleteRuntime
	NodeStatus             taskNodeStatusUpdater
	NodeDispatch           taskNodeDispatcher
	WorkflowDiagnostics    workflowDiagnosticsPort
	WorkflowRecovery       workflowRecoveryPort
	DAGIdentityDiagnostics dagPromptIdentityDiagnosticsPort
}

// Dependencies 汇总 mcp-orch 工具注册所需的服务和 store 依赖。
type Dependencies struct {
	ToolPorts      ToolPorts
	Workspace      workspace.Service
	Prompt         promptstore.Store
	BuiltinPrompts contract.BuiltinPromptRegistry
	CommandCard    commandcardstore.Store
	SharedFile     sharedfilestore.Store
	ModelRegistry  modelregistry.Registry
}

// Registry 持有 MCP 工具定义快照和名称索引。
type Registry struct {
	tools        []ToolDefinition
	byName       map[string]ToolDefinition
	runtimeState *toolRuntimeState
	initErr      error
}

// toolRuntimeState 保存一个 Registry 构建期及其 handler 使用的运行时可变状态。
// 它必须由拥有工具定义的构造器创建，不能跨 Registry 共享。
type toolRuntimeState struct {
	agentIDReg                 *agentIDRegistry
	agentIDGenerator           *idgen.Generator
	applyOpsActionEnum         []string
	applyOpsOpEnum             []string
	creatableNodeTypeEnum      []string
	launchAgentProviderEnum    []string
	launchAgentContextModeEnum []string
	recallTopicNamePattern     *regexp.Regexp
	listDAGsStatusEnum         []string
	listRunsStatusEnum         []string
	startDAGTriggerEnum        []string
	updateNodeStatusEnum       []string
	recoveryActionEnum         []string
}

// newToolRuntimeState 构造一个工具定义所有者专属的运行时状态。
func newToolRuntimeState() *toolRuntimeState {
	return newToolRuntimeStateWithAgentIDGenerator(idgen.NewGenerator())
}

func newToolRuntimeStateWithAgentIDGenerator(generator *idgen.Generator) *toolRuntimeState {
	if generator == nil {
		panic("tools: agent id generator required")
	}
	return &toolRuntimeState{
		agentIDReg:                 &agentIDRegistry{},
		agentIDGenerator:           generator,
		applyOpsActionEnum:         []string{"update_dag", "add_node", "update_node", "remove_node", "apply_ops_raw"},
		applyOpsOpEnum:             []string{string(nodeexec.OpKindUpdateDAG), string(nodeexec.OpKindAddNode), string(nodeexec.OpKindUpdateNode), string(nodeexec.OpKindRemoveNode)},
		creatableNodeTypeEnum:      []string{"agent", "automation"},
		launchAgentProviderEnum:    []string{"codex", "claude"},
		launchAgentContextModeEnum: []string{launchContextModeMinimal, launchContextModeFocused, launchContextModeForked},
		recallTopicNamePattern:     regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`),
		listDAGsStatusEnum:         []string{"draft", "active", "ready", "running", "archived"},
		listRunsStatusEnum:         []string{"running", "succeeded", "failed", "cancelled"},
		startDAGTriggerEnum:        []string{"manual", "auto", "scheduled", "external"},
		updateNodeStatusEnum:       []string{"ready", "running", "retrying", "done", "failed", "cancelled"},
		recoveryActionEnum:         []string{"cancel_with_cleanup", "retry_failed_node"},
	}
}

// NewRegistry 汇总所有工具定义并建立名称索引。
func NewRegistry(deps Dependencies) Registry {
	return NewRegistryWithAgentIDGenerator(deps, idgen.NewGenerator())
}

// NewRegistryWithAgentIDGenerator 使用应用根持有的 generator 构造工具注册表。
func NewRegistryWithAgentIDGenerator(deps Dependencies, generator *idgen.Generator) Registry {
	runtimeState := newToolRuntimeStateWithAgentIDGenerator(generator)
	tools := append(orchestrationToolDefinitionsWithRuntimeState(deps.ToolPorts, runtimeState), taskToolDefinitionsWithRuntimeState(deps.ToolPorts, runtimeState)...)
	tools = append(tools, workspaceToolDefinitions(deps.Workspace)...)
	tools = append(tools, promptToolDefinitions(deps.Prompt, deps.BuiltinPrompts)...)
	tools = append(tools, recallToolDefinitionsWithRuntimeState(deps.Prompt, runtimeState)...)
	tools = append(tools, commandToolDefinitions(deps.CommandCard)...)
	tools = append(tools, sharedFileToolDefinitions(deps.SharedFile)...)
	tools = append(tools, registryToolDefinitions(deps.SharedFile, deps.ModelRegistry)...)
	tools = append(tools, ttsToolDefinitions()...)
	tools = append(tools, avMergeToolDefinitions()...)
	tools = append(tools, videoWithAudioToolDefinitions()...)
	if err := validateRegistryPathPolicies(tools); err != nil {
		return Registry{runtimeState: runtimeState, initErr: err}
	}
	byName := make(map[string]ToolDefinition, len(tools))
	for i, tool := range tools {
		tool = withToolPathPolicy(tool)
		tools[i] = tool
		byName[tool.Name] = tool
	}
	return Registry{tools: tools, byName: byName, runtimeState: runtimeState}
}

// List 返回工具定义副本，避免调用方修改 Registry 内部切片。
func (r Registry) List() ([]ToolDefinition, error) {
	if r.initErr != nil {
		return nil, fmt.Errorf("tool registry invalid: %w", r.initErr)
	}
	return append([]ToolDefinition(nil), r.tools...), nil
}

// Lookup 按工具名查找定义。
func (r Registry) Lookup(name string) (ToolDefinition, bool) {
	if r.initErr != nil {
		return ToolDefinition{
			Name: name,
			Handler: func(context.Context, json.RawMessage) (any, error) {
				return nil, fmt.Errorf("tool registry invalid: %w", r.initErr)
			},
		}, true
	}
	tool, ok := r.byName[name]
	return tool, ok
}
