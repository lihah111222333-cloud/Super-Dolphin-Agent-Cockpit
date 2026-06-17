package tools

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	commandcardstore "github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/store/commandcard"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/tools/modelregistry"
	workspace "github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/workspace"
)

// Dependencies describes a tools API type.
type Dependencies struct {
	Orchestration  contract.OrchestrationService
	Workspace      workspace.Service
	Prompt         promptstore.Store
	BuiltinPrompts contract.BuiltinPromptRegistry
	CommandCard    commandcardstore.Store
	SharedFile     sharedfilestore.Store
	ModelRegistry  modelregistry.Registry
}

// Registry describes a tools API type.
type Registry struct {
	tools  []ToolDefinition
	byName map[string]ToolDefinition
}

var legacyOrchestrationAliases = map[string]string{
	"orchestration_launch_agent":     "launch_agent",
	"orchestration_send_message":     "send_message",
	"orchestration_stop_agent":       "stop_agent",
	"orchestration_list_agents":      "list_agents",
	"orchestration_get_agent_report": "get_agent_report",
}

// NewRegistry 创建注册表。
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
	byName := make(map[string]ToolDefinition, len(tools))
	for _, tool := range tools {
		byName[tool.Name] = tool
	}
	return Registry{tools: tools, byName: byName}
}

// List 列出编排。
func (r Registry) List() []ToolDefinition {
	return append([]ToolDefinition(nil), r.tools...)
}

// Lookup 按名称查找注册项。
func (r Registry) Lookup(name string) (ToolDefinition, bool) {
	if canonical, ok := legacyOrchestrationAliases[name]; ok {
		name = canonical
	}
	tool, ok := r.byName[name]
	return tool, ok
}
