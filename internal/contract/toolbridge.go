package contract

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// Toolbridge 窄端口分组定义 platform/toolbridge 依赖的上层契约。
// 这些契约放在 contract 层，避免 platform/toolbridge 直接导入 module、provider 或 store。

// DynamicToolSchema 是 provider 无关的动态工具 schema。
// toolbridge 使用它向 Codex 和未来 provider 暴露工具，而不依赖 provider 私有协议类型。
type DynamicToolSchema struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
}

// SkillToolSurfaceTool 是 skill 模块发布到工具面的动态工具描述。
type SkillToolSurfaceTool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"inputSchema,omitempty"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
}

// SkillToolCall 是模型调用 skill 工具时传给 skill 模块的可信上下文。
// CWD/agent/thread/turn/call 字段用于权限校验和审计，调用方不得自行伪造缺失上下文。
type SkillToolCall struct {
	Name     string `json:"name"`
	CWD      string `json:"cwd"`
	AgentID  string `json:"agentId,omitempty"`
	ThreadID string `json:"threadId,omitempty"`
	TurnID   string `json:"turnId,omitempty"`
	CallID   string `json:"callId,omitempty"`
}

// SkillToolProvider 提供项目级 skill 工具列表，并执行具体 skill 工具调用。
type SkillToolProvider interface {
	ListSkillToolsForSurface(ctx context.Context, cwd string) ([]SkillToolSurfaceTool, error)
	CallSkillTool(ctx context.Context, call SkillToolCall) (string, error)
}

// 工具 surface 模式常量；auto 模式不暴露动态工具，chat/agent 模式会暴露。
const (
	ToolSurfaceModeChat  = "chat"
	ToolSurfaceModeAuto  = "auto"
	ToolSurfaceModeAgent = "agent"
)

// NormalizeToolSurfaceMode 规范化工具 surface 模式。
// 空值表示沿用默认模式，非法值会返回错误阻断配置落地。
func NormalizeToolSurfaceMode(value string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(value))
	if mode == "" {
		return "", nil
	}
	switch mode {
	case ToolSurfaceModeChat, ToolSurfaceModeAuto, ToolSurfaceModeAgent:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid tool surface mode %q", strings.TrimSpace(value))
	}
}

// ToolSurfaceModeUsesDynamicTools 判断指定 surface 模式是否应暴露动态工具。
func ToolSurfaceModeUsesDynamicTools(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", ToolSurfaceModeChat, ToolSurfaceModeAgent:
		return true
	case ToolSurfaceModeAuto:
		return false
	default:
		return false
	}
}

// ToolCallRawMessage 携带 provider 进程发来的原始 JSON-RPC 工具调用。
// 该类型隔离 provider 私有包，toolbridge 只解析通用 ID/method/params。
type ToolCallRawMessage struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

// CodexToolHandlerSetter 是向 Codex ServerManager 绑定工具处理回调的窄端口。
type CodexToolHandlerSetter interface {
	SetToolHandler(h func(context.Context, ToolCallRawMessage) (any, error))
}

// CodexListToolsSetter 是向 Codex DriverFactory 绑定动态工具列表函数的窄端口。
type CodexListToolsSetter interface {
	SetListTools(fn func(context.Context) ([]DynamicToolSchema, error))
}

// ToolbridgeReadinessProbe 在 provider 启动前检查工具桥接是否完成生产装配。
type ToolbridgeReadinessProbe interface {
	CheckToolbridgeReady(ctx context.Context, provider string) error
}

// CodexToolSurfaceScope 携带暴露和路由 Codex 动态工具所需的可信会话上下文。
type CodexToolSurfaceScope struct {
	SurfaceID        string
	AgentID          string
	UIThreadID       string
	LocalThreadID    string
	ProviderThreadID string
	CWD              string
	WorkspaceRoots   []string
	DisabledTools    []string
	Manifest         dto.MCPManifest
}

// toolLifecycleAlreadyPublishedKey 标记当前 context 已发布过工具生命周期事件。
type toolLifecycleAlreadyPublishedKey struct{}

// WithToolLifecycleAlreadyPublished 在 context 中记录工具生命周期事件已发布。
// toolbridge 用它防止同一次调用在多层包装中重复发 begin/end 事件。
func WithToolLifecycleAlreadyPublished(ctx context.Context) context.Context {
	return context.WithValue(ctx, toolLifecycleAlreadyPublishedKey{}, true)
}

// ToolLifecycleAlreadyPublished 判断当前 context 是否已发布工具生命周期事件。
func ToolLifecycleAlreadyPublished(ctx context.Context) bool {
	value, _ := ctx.Value(toolLifecycleAlreadyPublishedKey{}).(bool)
	return value
}
