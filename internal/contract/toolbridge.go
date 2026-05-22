package contract

import (
	"context"
	"encoding/json"

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

func WithToolLifecycleAlreadyPublished(ctx context.Context) context.Context {
	return context.WithValue(ctx, toolLifecycleAlreadyPublishedKey{}, true)
}

func ToolLifecycleAlreadyPublished(ctx context.Context) bool {
	value, _ := ctx.Value(toolLifecycleAlreadyPublishedKey{}).(bool)
	return value
}
