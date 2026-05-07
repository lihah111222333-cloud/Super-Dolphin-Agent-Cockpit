package contract

import (
	"context"
	"encoding/json"
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
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ToolCallRawMessage carries a raw JSON-RPC tool call message from a provider
// process. Lifted from internal/provider/codexapp.RawMessage so toolbridge
// does not import the provider package.
type ToolCallRawMessage struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

// SkillSectionReader is a pure-function port for reading a reference section
// file from the skill cache directory. The production implementation is
// skilllibrary.ReadSection.
type SkillSectionReader func(cacheDir, name, anchor string) ([]byte, error)

// SkillCallRecorder is a narrow port for recording skill call events used by
// FBSD (frequency-based skill disclosure). The production implementation is
// fbsd.Tracker. nil-safe: callers must guard nil before calling Record.
type SkillCallRecorder interface {
	Record(name, anchor string)
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
