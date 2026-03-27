package main

import (
	"context"
	"errors"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
)

// Manager is the local runtime hook point for future LSP and exec resources.
type Manager struct{}

func newManager() *Manager {
	return &Manager{}
}

func (*Manager) Close() error {
	return nil
}

type stdioRunner struct {
	server  *common.Server
	manager *Manager
}

func newStdioRunner(server *common.Server, manager *Manager) platformrunner.Runner {
	return stdioRunner{server: server, manager: manager}
}

func (r stdioRunner) Run(ctx context.Context) error {
	if r.server == nil {
		return errors.New("mcp-lsp server is not configured")
	}
	defer func() {
		if r.manager != nil {
			_ = r.manager.Close()
		}
	}()
	return r.server.Run(ctx)
}
