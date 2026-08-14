//go:build windows

package main

import (
	"errors"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

// runtimeServerArgsPlatform 从 durable record 读取唯一显式 daemon endpoint。
func runtimeServerArgsPlatform(command multilsp.ServerCommand, binary string, env []string, workspaceRoot ...string) ([]string, error) {
	if !runtimeServerUsesSharedGoplsDaemon(command) {
		return slices.Clone(command.Args), nil
	}
	if len(workspaceRoot) != 1 || strings.TrimSpace(workspaceRoot[0]) == "" {
		return nil, errors.New("Windows shared gopls daemon requires one workspace root")
	}
	config, err := runtimeServerGoplsRootCohortConfig(command, binary, workspaceRoot[0], env)
	if err != nil {
		return nil, err
	}
	endpoint, err := runtimeServerReadWindowsGoplsDaemonEndpoint(config)
	if err != nil {
		return nil, err
	}
	return []string{"-remote=" + endpoint.Endpoint}, nil
}
