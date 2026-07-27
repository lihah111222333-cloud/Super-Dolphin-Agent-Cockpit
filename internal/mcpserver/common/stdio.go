package common

import (
	"io"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge/mcpwire"
)

const (
	MaxStdioMessageBytes    = mcpwire.MaxStdioMessageBytes
	MaxStdioHeaderLineBytes = mcpwire.MaxStdioHeaderLineBytes
	MaxStdioHeaderBytes     = mcpwire.MaxStdioHeaderBytes
	MaxStdioHeaderLines     = mcpwire.MaxStdioHeaderLines
)

// StdioTransport 是中立 mcpwire transport 的兼容别名。
type StdioTransport = mcpwire.StdioTransport

// NewStdioTransport 保留 mcpserver/common 的兼容入口；实现单源位于 mcpwire。
func NewStdioTransport(stdin io.Reader, stdout io.Writer) *StdioTransport {
	return mcpwire.NewStdioTransport(stdin, stdout)
}
