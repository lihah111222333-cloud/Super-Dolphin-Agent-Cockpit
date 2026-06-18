package contract

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// ---------------------------------------------------------------------------
// Toolbridge narrow ports — types and interfaces that toolbridge needs from
// higher layers. Defined here so platform/toolbridge never imports module/,
// provider/, or store/ directly.
// ---------------------------------------------------------------------------

// DynamicToolSchema is the provider-agnostic representation of a tool schema
// that toolbridge exposes to Codex (and any future provider). Lifted from
// internal/provider/codexapp/protocol so toolbridge does not import provider.
type DynamicToolSchema struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
}

// SkillToolSurfaceTool 描述一个由 Skill 包装出来的动态工具。
type SkillToolSurfaceTool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"inputSchema,omitempty"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
}

// SkillToolCall 是模型调用 Skill 工具时传给 skill 模块的可信上下文。
type SkillToolCall struct {
	Name     string `json:"name"`
	CWD      string `json:"cwd"`
	AgentID  string `json:"agentId,omitempty"`
	ThreadID string `json:"threadId,omitempty"`
	TurnID   string `json:"turnId,omitempty"`
	CallID   string `json:"callId,omitempty"`
}

// SkillToolProvider 提供项目级 Skill 工具列表，并在调用时返回 SKILL.md 全文。
type SkillToolProvider interface {
	ListSkillToolsForSurface(ctx context.Context, cwd string) ([]SkillToolSurfaceTool, error)
	CallSkillTool(ctx context.Context, call SkillToolCall) (string, error)
}

const (
	ToolSurfaceModeChat  = "chat"
	ToolSurfaceModeAuto  = "auto"
	ToolSurfaceModeAgent = "agent"
)

// NormalizeToolSurfaceMode 规范化工具surface模式。
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

// ToolSurfaceModeUsesDynamicTools 处理工具surface模式usesdynamic工具。
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

// ToolCallRawMessage carries a raw JSON-RPC tool call message from a provider
// process. Lifted from internal/provider/codexapp.RawMessage so toolbridge
// does not import the provider package.
type ToolCallRawMessage struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

// CodexToolHandlerSetter is the narrow port for binding a tool handler
// callback into the Codex ServerManager. The production implementation is
// codexapp.ServerManager.
type CodexToolHandlerSetter interface {
	SetToolHandler(h func(context.Context, ToolCallRawMessage) (any, error))
}

// CodexListToolsSetter is the narrow port for binding the dynamic tool
// listing function into the Codex DriverFactory. The production
// implementation is codexapp.DriverFactory.
type CodexListToolsSetter interface {
	SetListTools(fn func(context.Context) ([]DynamicToolSchema, error))
}

// CodexToolSurfaceScope carries the trusted per-session inputs used to expose
// and route Codex dynamic tools through stdio MCP sidecars.
type CodexToolSurfaceScope struct {
	SurfaceID        string
	AgentID          string
	UIThreadID       string
	LocalThreadID    string
	ProviderThreadID string
	CWD              string
	WorkspaceRoots   []string
	Manifest         dto.MCPManifest
}

type toolLifecycleAlreadyPublishedKey struct{}

// WithToolLifecycleAlreadyPublished 设置工具生命周期alreadypublished。
func WithToolLifecycleAlreadyPublished(ctx context.Context) context.Context {
	return context.WithValue(ctx, toolLifecycleAlreadyPublishedKey{}, true)
}

// ToolLifecycleAlreadyPublished 处理工具生命周期alreadypublished。
func ToolLifecycleAlreadyPublished(ctx context.Context) bool {
	value, _ := ctx.Value(toolLifecycleAlreadyPublishedKey{}).(bool)
	return value
}
