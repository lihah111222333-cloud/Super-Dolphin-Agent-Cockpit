package toolbridge

import (
	"context"
	"fmt"
	"strings"

	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

// 退役 host 工具名仅用于让旧客户端得到确定性的本地拒绝，不能落到 MCP peer。
// wire 值暂时保留兼容性，但 memory owner 移除后不再有注册表实现这些能力。
const (
	ToolNameRetiredHostRead  = "memory_read"
	ToolNameRetiredHostWrite = "memory_write"
)

// CompositeHostToolRegistry 按声明顺序组合相互独立的 host-direct 注册表。
// 首个声明工具的注册表同时拥有列表项和调用；该聚合器不隐含 memory 或 skill owner。
type CompositeHostToolRegistry struct {
	registries []HostToolRegistry
}

// NewCompositeHostToolRegistry 过滤 nil 注册表并创建组合器。
// 返回 nil 表示当前没有装配任何 host-direct 能力。
func NewCompositeHostToolRegistry(registries ...HostToolRegistry) *CompositeHostToolRegistry {
	out := &CompositeHostToolRegistry{}
	for _, registry := range registries {
		if registry != nil {
			out.registries = append(out.registries, registry)
		}
	}
	if len(out.registries) == 0 {
		return nil
	}
	return out
}

// ListHostTools 按顺序合并 host-direct schema，丢弃重名或空名称项；重名时先注册者胜出。
func (r *CompositeHostToolRegistry) ListHostTools() []mcpdto.MCPTool {
	if r == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var out []mcpdto.MCPTool
	for _, registry := range r.registries {
		for _, tool := range registry.ListHostTools() {
			name := strings.TrimSpace(tool.Name)
			if name == "" {
				continue
			}
			if _, exists := seen[name]; exists {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, tool)
		}
	}
	return out
}

// HasTool 判断任一子注册表是否声明该名称。
func (r *CompositeHostToolRegistry) HasTool(name string) bool {
	if r == nil {
		return false
	}
	for _, registry := range r.registries {
		if registry.HasTool(name) {
			return true
		}
	}
	return false
}

// RequiresCWD 返回命中子注册表的 cwd 策略；未知名称保持要求 cwd 的失败关闭默认值。
func (r *CompositeHostToolRegistry) RequiresCWD(name string) bool {
	if r != nil {
		for _, registry := range r.registries {
			if registry.HasTool(name) {
				return hostToolRequiresCWD(registry, name)
			}
		}
	}
	return true
}

// CallHostTool 将调用分发给首个声明 call.Name 的子注册表。
func (r *CompositeHostToolRegistry) CallHostTool(ctx context.Context, call HostToolCall) (any, error) {
	if r != nil {
		for _, registry := range r.registries {
			if registry.HasTool(call.Name) {
				return registry.CallHostTool(ctx, call)
			}
		}
	}
	return nil, fmt.Errorf("host tools: unknown tool %q", call.Name)
}
