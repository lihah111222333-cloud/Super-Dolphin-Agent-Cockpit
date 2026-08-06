package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestMCPToolsListIncludesPromptRecall(t *testing.T) {
	t.Parallel()

	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var output bytes.Buffer
	registry := tools.NewRegistry(tools.Dependencies{})
	server := newTestMCPServer("mcp-orch", "dev", common.NewStdioTransport(input, &output), registryToolProvider{registry: registry})

	if err := server.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"name":"prompt_recall"`)) {
		t.Fatalf("tools/list output missing prompt_recall: %s", output.String())
	}
}
