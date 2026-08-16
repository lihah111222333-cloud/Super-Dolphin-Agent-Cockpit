//go:build !windows

package main

import (
	"slices"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

// runtimeServerArgsPlatform 为共享 gopls daemon 派生稳定 cohort 参数。
func runtimeServerArgsPlatform(command multilsp.ServerCommand, binary string, env []string, workspaceRoot ...string) ([]string, error) {
	args := slices.Clone(command.Args)
	if !runtimeServerUsesSharedGoplsDaemon(command) {
		return args, nil
	}
	return runtimeServerGoplsAutoDaemonArgs(command, binary, env, workspaceRoot...)
}
