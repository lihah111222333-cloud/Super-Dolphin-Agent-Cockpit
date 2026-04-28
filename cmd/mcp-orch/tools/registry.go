package tools

import (
	commandcardstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/commandcard"
	promptstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sharedfile"
	workspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/workspace"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	cronstore "github.com/anthropic-ai/super-agent-v3/internal/store/cron"
)

type Dependencies struct {
	Orchestration contract.OrchestrationService
	Workspace     workspace.Service
	Prompt        promptstore.Store
	CommandCard   commandcardstore.Store
	SharedFile    sharedfilestore.Store
	Memory        contract.MemoryService
	Cron          cronstore.Store
}

type Registry struct {
	tools  []ToolDefinition
	byName map[string]ToolDefinition
}

func NewRegistry(deps Dependencies) Registry {
	tools := append(orchestrationToolDefinitions(deps.Orchestration), taskToolDefinitions(deps.Orchestration)...)
	tools = append(tools, workspaceToolDefinitions(deps.Workspace)...)
	tools = append(tools, promptToolDefinitions(deps.Prompt)...)
	tools = append(tools, commandToolDefinitions(deps.CommandCard)...)
	tools = append(tools, sharedFileToolDefinitions(deps.SharedFile)...)
	tools = append(tools, memoryToolDefinitions(deps.Memory)...)
	tools = append(tools, cronToolDefinitions(deps.Cron)...)
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
	tool, ok := r.byName[name]
	return tool, ok
}
