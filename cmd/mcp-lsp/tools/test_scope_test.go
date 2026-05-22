package tools

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

func testToolContext(root string) context.Context {
	return common.WithToolScope(context.Background(), common.ToolScope{
		CWD:            root,
		WorkspaceRoots: []string{root},
		Family:         "lsp",
	})
}
