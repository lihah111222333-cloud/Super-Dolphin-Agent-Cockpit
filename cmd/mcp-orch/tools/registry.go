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

// Dependencies 汇总 mcp-orch 工具注册所需的服务和 store 依赖。
type Dependencies struct {
	Orchestration  contract.OrchestrationService
	Workspace      workspace.Service
	Prompt         promptstore.Store
	BuiltinPrompts contract.BuiltinPromptRegistry
	CommandCard    commandcardstore.Store
	SharedFile     sharedfilestore.Store
	ModelRegistry  modelregistry.Registry
}

// Registry 持有 MCP 工具定义快照和名称索引，Lookup 负责旧工具名兼容。
type Registry struct {
	tools   []ToolDefinition
	byName  map[string]ToolDefinition
	initErr error
}

var legacyOrchestrationAliases = map[string]string{
	"orchestration_launch_agent":     "launch_agent",
	"orchestration_send_message":     "send_message",
	"orchestration_stop_agent":       "stop_agent",
	"orchestration_recover_agent":    "recover_agent",
	"orchestration_interrupt_agent":  "interrupt_agent",
	"orchestration_list_agents":      "list_agents",
	"orchestration_get_agent_report": "get_agent_report",
}

// NewRegistry 汇总所有工具定义并建立名称索引；旧 orchestration_* 名称在 Lookup 时映射到新名。
func NewRegistry(deps Dependencies) Registry {
	tools := append(orchestrationToolDefinitions(deps.Orchestration), taskToolDefinitions(deps.Orchestration)...)
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

// Lookup 按工具名查找定义，并兼容旧 orchestration_* 别名。
func (r Registry) Lookup(name string) (ToolDefinition, bool) {
	if r.initErr != nil {
		return ToolDefinition{
			Name: name,
			Handler: func(context.Context, json.RawMessage) (any, error) {
				return nil, fmt.Errorf("tool registry invalid: %w", r.initErr)
			},
		}, true
	}
	if canonical, ok := legacyOrchestrationAliases[name]; ok {
		name = canonical
	}
	tool, ok := r.byName[name]
	return tool, ok
}
