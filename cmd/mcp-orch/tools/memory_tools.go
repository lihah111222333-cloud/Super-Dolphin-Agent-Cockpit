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

type memoryWriteInput struct {
	Name        string `json:"name"`
	Content     string `json:"content"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
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

func HandleMemoryWrite(svc contract.MemoryService) ToolHandler {
	return makeHandler(svc, "memory service", func(ctx context.Context, in memoryWriteInput) (contract.MemoryWriteResult, error) {
		memType := contract.ParseMemoryType(in.Type)
		if !memType.IsKnown() {
			memType = contract.MemoryTypeFeedback
		}
		return svc.Write(ctx, contract.MemoryWriteRequest{
			Name:        in.Name,
			Content:     in.Content,
			Type:        memType,
			Description: in.Description,
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
		defineTool("memory_write", "Save a memory entry. Use when you detect user preferences, corrections, decisions, or project context worth remembering.", ObjectSchema(map[string]Schema{
			"name":        StringSchema("Short descriptive name for the memory entry."),
			"content":     StringSchema("Memory content. For feedback/project types, include Why: and How to apply: sections."),
			"type":        EnumStringSchema("Memory type.", "feedback", "project"),
			"description": StringSchema("Optional one-line description (auto-generated from content if omitted)."),
		}, "name", "content"), HandleMemoryWrite(svc)),
	)
}
