package tools

import common "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/runtime"

// ToolErrorEnvelope is re-exported for tool-level tests and helper code. The
// canonical structured error contract lives in internal/mcpserver/runtime so
// stdio, HTTP, and bootstrap tool-call surfaces render identical payloads.
type ToolErrorEnvelope = common.ToolErrorEnvelope

func newToolErrorEnvelope(toolName, languageID string, err error) ToolErrorEnvelope {
	return common.NewToolErrorEnvelopeWithMeta(toolName, languageID, err, nil)
}
