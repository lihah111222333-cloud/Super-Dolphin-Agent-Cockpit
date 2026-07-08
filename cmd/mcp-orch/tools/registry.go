package tools

import (
	"context"
	"encoding/json"
	"fmt"

	commandcardstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/commandcard"
	promptstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/tools/modelregistry"
	workspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/workspace"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// ToolPorts 按工具消费面拆分 orchestration 依赖，避免 registry 持有完整服务边界。
type ToolPorts struct {
	AgentLaunch            agentLaunchPort
	AgentMessenger         sendMessagePort
	AgentLifecycle         contract.AgentLifecyclePort
	AgentRecovery          agentRecoverPort
	AgentInterrupt         agentInterruptPort
	AgentList              agentListPort
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
	tools   []ToolDefinition
	byName  map[string]ToolDefinition
	initErr error
}

// NewRegistry 汇总所有工具定义并建立名称索引。
func NewRegistry(deps Dependencies) Registry {
	tools := append(orchestrationToolDefinitions(deps.ToolPorts), taskToolDefinitions(deps.ToolPorts)...)
	tools = append(tools, workspaceToolDefinitions(deps.Workspace)...)
	tools = append(tools, promptToolDefinitions(deps.Prompt, deps.BuiltinPrompts)...)
	tools = append(tools, recallToolDefinitions(deps.Prompt)...)
	tools = append(tools, commandToolDefinitions(deps.CommandCard)...)
	tools = append(tools, sharedFileToolDefinitions(deps.SharedFile)...)
	tools = append(tools, registryToolDefinitions(deps.SharedFile, deps.ModelRegistry)...)
	tools = append(tools, ttsToolDefinitions()...)
	tools = append(tools, avMergeToolDefinitions()...)
	tools = append(tools, videoWithAudioToolDefinitions()...)
	if err := validateRegistryPathPolicies(tools); err != nil {
		return Registry{initErr: err}
	}
	byName := make(map[string]ToolDefinition, len(tools))
	for i, tool := range tools {
		tool = withToolPathPolicy(tool)
		tools[i] = tool
		byName[tool.Name] = tool
	}
	return Registry{tools: tools, byName: byName}
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
