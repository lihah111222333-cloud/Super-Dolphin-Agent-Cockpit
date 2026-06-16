package mcpruntime

import (
	"io"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpwire"
)

// StdioTransport reads and writes MCP JSON-RPC messages over stdio streams.
type StdioTransport = mcpwire.StdioTransport

// NewStdioTransport creates a stdio MCP transport.
func NewStdioTransport(stdin io.Reader, stdout io.Writer) *StdioTransport {
	return mcpwire.NewStdioTransport(stdin, stdout)
}
