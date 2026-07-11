package tools

import (
	"context"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func testToolContext(root string) context.Context {
	return common.WithToolScope(context.Background(), common.ToolScope{
		CWD:            root,
		WorkspaceRoots: []string{root},
		Family:         "lsp",
	})
}
