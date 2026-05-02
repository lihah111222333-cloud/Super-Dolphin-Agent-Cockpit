package tools

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type memoryReadInput struct {
	Name  string `json:"name"`
	Path  string `json:"path,omitempty"`
	Scope string `json:"scope,omitempty"`
	Type  string `json:"type,omitempty"`
}

func HandleMemoryRead(svc contract.MemoryService) ToolHandler {
	return makeHandler(svc, "memory service", func(ctx context.Context, in memoryReadInput) (contract.MemoryReadResult, error) {
		return svc.Read(ctx, contract.MemoryReadRequest{
			Name:  in.Name,
			Path:  in.Path,
			Scope: contract.ParseMemoryScope(in.Scope),
			Type:  contract.ParseMemoryType(in.Type),
		})
	})
}

func memoryToolDefinitions(svc contract.MemoryService) []ToolDefinition {
	return buildToolDefinitions(
		defineTool("memory_read", "Read a memory entry by name or debug path within the authorized scope.", ObjectSchema(map[string]Schema{
			"name":  StringSchema("Memory name. Preferred over path."),
			"path":  StringSchema("Optional debug path. Still goes through sanitize, resolve, and authorize."),
			"scope": EnumStringSchema("Memory scope.", "project", "user", "local"),
			"type":  EnumStringSchema("Optional memory type filter.", "user", "feedback", "project", "reference"),
		}), HandleMemoryRead(svc)),
	)
}
