// Package taskhttp 提供只接受 task session 身份的独立 MCP HTTP 构件。
package taskhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

var (
	// ErrToolProviderInvalid 表示底层 provider、构造 context 或显式工具集合无效。
	ErrToolProviderInvalid = errors.New("task HTTP tool provider is invalid")
	// ErrToolDefinitionDrift 表示底层工具定义存在缺失、重复或名称漂移。
	ErrToolDefinitionDrift = errors.New("task HTTP tool definition drift")
	// ErrToolUnknown 表示显式 allowlist 引用了底层 provider 未发布的工具。
	ErrToolUnknown = errors.New("task HTTP allowed tool is unknown")
	// ErrToolNotAllowed 表示调用名称不属于构造期冻结的 exact 工具集合。
	ErrToolNotAllowed = errors.New("task HTTP tool is not allowed")
	// ErrTaskCapabilityManifestRequired 表示 task-only endpoint 尚未绑定
	// capability manifest，不能暴露会触达其它 task 的工具。
	ErrTaskCapabilityManifestRequired = errors.New("task HTTP task capability manifest is required")
)

// ExactToolProvider 将一个底层 provider 收敛为构造期冻结的有序 exact 工具集合。
type ExactToolProvider struct {
	base       common.ToolProvider
	tools      []common.MCPTool
	allowedSet map[string]struct{}
}

// NewExactToolProvider 在启动 context 中读取一次底层工具定义，并按显式 allowlist 顺序冻结结果。
func NewExactToolProvider(
	ctx context.Context,
	base common.ToolProvider,
	allowedTools []string,
) (*ExactToolProvider, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: construction context is required", ErrToolProviderInvalid)
	}
	if interfaceIsNil(base) {
		return nil, fmt.Errorf("%w: base provider is required", ErrToolProviderInvalid)
	}
	if allowedTools == nil {
		return nil, fmt.Errorf("%w: allowed tools must be explicit", ErrToolProviderInvalid)
	}
	allowedSet, err := validateAllowedTools(allowedTools)
	if err != nil {
		return nil, err
	}

	listed, err := base.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("list base tools: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list base tools: %w", err)
	}
	definitions, err := indexToolDefinitions(listed)
	if err != nil {
		return nil, err
	}

	frozen := make([]common.MCPTool, 0, len(allowedTools))
	for _, name := range allowedTools {
		tool, exists := definitions[name]
		if !exists {
			return nil, fmt.Errorf("%w: %q", ErrToolUnknown, name)
		}
		frozen = append(frozen, cloneToolDefinition(tool))
	}
	return &ExactToolProvider{base: base, tools: frozen, allowedSet: allowedSet}, nil
}

// ListTools 返回构造期冻结定义的深拷贝，并保持显式 allowlist 顺序。
func (p *ExactToolProvider) ListTools(ctx context.Context) ([]common.MCPTool, error) {
	if p == nil {
		return nil, fmt.Errorf("%w: exact provider is nil", ErrToolProviderInvalid)
	}
	if ctx == nil {
		return nil, fmt.Errorf("%w: list context is required", ErrToolProviderInvalid)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tools := make([]common.MCPTool, len(p.tools))
	for index, tool := range p.tools {
		tools[index] = cloneToolDefinition(tool)
	}
	return tools, nil
}

// CallTool 只转发 exact 集合中的原样名称；未授权名称不会触达底层 provider。
func (p *ExactToolProvider) CallTool(
	ctx context.Context,
	name string,
	args json.RawMessage,
) (any, error) {
	if p == nil {
		return nil, fmt.Errorf("%w: exact provider is nil", ErrToolProviderInvalid)
	}
	if _, allowed := p.allowedSet[name]; !allowed {
		return nil, fmt.Errorf("%w: %q", ErrToolNotAllowed, name)
	}
	if ctx == nil {
		return nil, fmt.Errorf("%w: call context is required", ErrToolProviderInvalid)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return p.base.CallTool(ctx, name, args)
}

// validateAllowedTools 校验 allowlist 为 canonical、有序且无重复的显式集合。
func validateAllowedTools(names []string) (map[string]struct{}, error) {
	seen := make(map[string]struct{}, len(names))
	for index, name := range names {
		if name == "" || strings.TrimSpace(name) != name {
			return nil, fmt.Errorf("%w: allowed tools[%d] must be canonical nonempty text", ErrToolDefinitionDrift, index)
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("%w: duplicate allowed tool %q", ErrToolDefinitionDrift, name)
		}
		if isUnboundTaskCapability(name) {
			return nil, fmt.Errorf("%w: %q", ErrTaskCapabilityManifestRequired, name)
		}
		seen[name] = struct{}{}
	}
	return seen, nil
}

// isUnboundTaskCapability 列出没有 task capability manifest 时必须拒绝的
// legacy orchestration/direct-peer 与 unified 跨任务工具；safe local tools
// 仍可由调用方显式 allowlist。
func isUnboundTaskCapability(name string) bool {
	switch name {
	case "launch_agent", "send_message", "list_agents", "get_agent_report", "get_agent_reports",
		"stop_agent", "recover_agent", "interrupt_agent", "manage_direct_peer", "send_direct_peer",
		"read_direct_peer", "read_direct_peer_events", "wait_agents":
		return true
	default:
		return false
	}
}

// indexToolDefinitions 动态枚举底层定义，并拒绝 missing、duplicate 与名称漂移。
func indexToolDefinitions(tools []common.MCPTool) (map[string]common.MCPTool, error) {
	definitions := make(map[string]common.MCPTool, len(tools))
	for index, tool := range tools {
		if tool.Name == "" {
			return nil, fmt.Errorf("%w: tools[%d] name is missing", ErrToolDefinitionDrift, index)
		}
		if strings.TrimSpace(tool.Name) != tool.Name {
			return nil, fmt.Errorf("%w: tools[%d] name %q is not canonical", ErrToolDefinitionDrift, index, tool.Name)
		}
		if _, duplicate := definitions[tool.Name]; duplicate {
			return nil, fmt.Errorf("%w: duplicate base tool %q", ErrToolDefinitionDrift, tool.Name)
		}
		if err := validateRawToolName(tool); err != nil {
			return nil, err
		}
		if err := validateDefinitionSchema(tool.Name, "inputSchema", tool.InputSchema); err != nil {
			return nil, err
		}
		if err := validateDefinitionSchema(tool.Name, "outputSchema", tool.OutputSchema); err != nil {
			return nil, err
		}
		definitions[tool.Name] = cloneToolDefinition(tool)
	}
	return definitions, nil
}

// validateRawToolName 防止宽松 raw 投影名称与导出定义名称分叉。
func validateRawToolName(tool common.MCPTool) error {
	raw := bytes.TrimSpace(tool.RawJSON())
	if len(raw) == 0 {
		return nil
	}
	if !json.Valid(raw) || raw[0] != '{' {
		return fmt.Errorf("%w: tool %q raw definition must be a JSON object", ErrToolDefinitionDrift, tool.Name)
	}
	projected := mcpdto.NewRawTool(raw)
	if projected.Name != tool.Name {
		return fmt.Errorf(
			"%w: tool %q raw name is %q",
			ErrToolDefinitionDrift,
			tool.Name,
			projected.Name,
		)
	}
	return nil
}

// validateDefinitionSchema 在缓存前拒绝畸形 schema，避免把启动错误推迟到首个 tools/list。
func validateDefinitionSchema(toolName, field string, schema json.RawMessage) error {
	trimmed := bytes.TrimSpace(schema)
	if len(trimmed) == 0 {
		return nil
	}
	if !json.Valid(trimmed) || trimmed[0] != '{' {
		return fmt.Errorf("%w: tool %q %s must be a JSON object", ErrToolDefinitionDrift, toolName, field)
	}
	return nil
}

// cloneToolDefinition 深拷贝 raw 定义和两个 schema，阻断底层与调用方共享可变字节。
func cloneToolDefinition(tool common.MCPTool) common.MCPTool {
	clone := mcpdto.NewRawTool(tool.RawJSON())
	clone.Name = tool.Name
	clone.Description = tool.Description
	clone.InputSchema = append(json.RawMessage(nil), tool.InputSchema...)
	clone.OutputSchema = append(json.RawMessage(nil), tool.OutputSchema...)
	return clone
}

// interfaceIsNil 同时识别 nil interface 与 typed nil provider。
func interfaceIsNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
