package toolbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const ToolNameMemoryRead = "memory_read"

type MemoryReadHostToolOptions struct {
	Enabled      bool
	ToolsEnabled bool
}

type MemoryReadHostToolRegistry struct {
	reader contract.AgentMemoryReader
	opts   MemoryReadHostToolOptions
}

type memoryReadToolInput struct {
	Name  string `json:"name"`
	Path  string `json:"path,omitempty"`
	Scope string `json:"scope,omitempty"`
	Type  string `json:"type,omitempty"`
}

// NewMemoryReadHostToolRegistry 创建记忆readhost工具注册表。
func NewMemoryReadHostToolRegistry(reader contract.AgentMemoryReader, opts MemoryReadHostToolOptions) *MemoryReadHostToolRegistry {
	if reader == nil {
		return nil
	}
	return &MemoryReadHostToolRegistry{reader: reader, opts: opts}
}

// ListHostTools 列出host工具。
func (r *MemoryReadHostToolRegistry) ListHostTools() []mcpdto.MCPTool {
	if r == nil || r.reader == nil || !r.opts.Enabled || !r.opts.ToolsEnabled {
		return nil
	}
	schema, _ := json.Marshal(memoryReadInputSchema())
	return []mcpdto.MCPTool{{Name: ToolNameMemoryRead, Description: descriptionMemoryRead, InputSchema: schema}}
}

// HasTool 判断工具是否可用。
func (r *MemoryReadHostToolRegistry) HasTool(name string) bool {
	return r != nil && name == ToolNameMemoryRead
}

// CallHostTool 调用host工具。
func (r *MemoryReadHostToolRegistry) CallHostTool(ctx context.Context, call HostToolCall) (any, error) {
	if r == nil || r.reader == nil {
		return nil, contract.NewAgentMemoryError("reader_unavailable", fmt.Errorf("memory_read reader is not configured"))
	}
	if err := validateHostToolGuards(r.opts.Enabled, r.opts.ToolsEnabled, call.Name, ToolNameMemoryRead, "reader_unavailable"); err != nil {
		return nil, err
	}
	var input memoryReadToolInput
	if err := platformshared.DecodeInput(call.Arguments, &input); err != nil {
		return nil, contract.NewAgentMemoryError("invalid_input", err)
	}
	req, err := buildAgentMemoryReadRequest(input, call)
	if err != nil {
		return nil, err
	}
	return r.reader.ReadAgentMemory(ctx, req)
}

func buildAgentMemoryReadRequest(input memoryReadToolInput, call HostToolCall) (contract.MemoryReadRequest, error) {
	scope, err := parseMemoryReadScope(input.Scope)
	if err != nil {
		return contract.MemoryReadRequest{}, err
	}
	memType, err := parseMemoryReadType(input.Type)
	if err != nil {
		return contract.MemoryReadRequest{}, err
	}
	return contract.MemoryReadRequest{
		Name:     strings.TrimSpace(input.Name),
		Path:     strings.TrimSpace(input.Path),
		Scope:    scope,
		Type:     memType,
		AgentID:  strings.TrimSpace(call.AgentID),
		ThreadID: strings.TrimSpace(call.ThreadID),
		CWD:      strings.TrimSpace(call.CWD),
		CallID:   strings.TrimSpace(call.CallID),
	}, nil
}

func parseMemoryReadScope(raw string) (contract.MemoryScope, error) {
	scope := contract.ParseMemoryScope(raw)
	if !scope.Valid() {
		return "", contract.NewAgentMemoryError("invalid_input", fmt.Errorf("invalid memory_read scope"))
	}
	return scope, nil
}

func parseMemoryReadType(raw string) (contract.MemoryType, error) {
	trimmed := strings.TrimSpace(raw)
	memType := contract.ParseMemoryType(trimmed)
	if trimmed != "" && !memType.IsKnown() {
		return "", contract.NewAgentMemoryError("invalid_input", fmt.Errorf("invalid memory_read type"))
	}
	return memType, nil
}

func memoryReadInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":  map[string]any{"type": "string"},
			"path":  map[string]any{"type": "string"},
			"scope": map[string]any{"type": "string", "enum": []string{"user", "team"}},
			"type":  map[string]any{"type": "string", "enum": []string{"user", "feedback", "project", "reference"}},
		},
		"additionalProperties": false,
	}
}

const descriptionMemoryRead = "Read a durable memory entry by name or relative path. Uses host-direct memory roots and never falls back to mcp-orch peer memory tools."
