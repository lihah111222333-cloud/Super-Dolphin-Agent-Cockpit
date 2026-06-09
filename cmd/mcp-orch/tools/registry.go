package tools

import (
	commandcardstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/commandcard"
	promptstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/tools/modelregistry"
	workspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/workspace"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type Dependencies struct {
	Orchestration contract.OrchestrationService
	Workspace     workspace.Service
	Prompt        promptstore.Store
	CommandCard   commandcardstore.Store
	SharedFile    sharedfilestore.Store
	ModelRegistry modelregistry.Registry
}

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

func NewRegistry(deps Dependencies) Registry {
	tools := append(orchestrationToolDefinitions(deps.Orchestration), taskToolDefinitions(deps.Orchestration)...)
	tools = append(tools, workspaceToolDefinitions(deps.Workspace)...)
	tools = append(tools, promptToolDefinitions(deps.Prompt)...)
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

func (r Registry) List() []ToolDefinition {
	return append([]ToolDefinition(nil), r.tools...)
}

func (r Registry) Lookup(name string) (ToolDefinition, bool) {
	if canonical, ok := legacyOrchestrationAliases[name]; ok {
		name = canonical
	}
	tool, ok := r.byName[name]
	return tool, ok
}
