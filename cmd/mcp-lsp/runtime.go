package main

import (
	"context"
	"errors"
	"os"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/gopls"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
)

// Manager is the local runtime hook point for LSP and exec resources.
type Manager struct {
	goplsMgr gopls.Manager
	root     string
}

func newManager() (*Manager, error) {
	root, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	log := pkglogger.Get()
	goplsMgr := gopls.NewManager(gopls.Config{
		WorkspaceRoot: root,
		ClientFactory: gopls.ClientFactoryFunc(gopls.NewClient),
		Logger:        log,
	})
	return &Manager{goplsMgr: goplsMgr, root: root}, nil
}

func (m *Manager) Close() error {
	if m.goplsMgr != nil {
		return m.goplsMgr.Close()
	}
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
