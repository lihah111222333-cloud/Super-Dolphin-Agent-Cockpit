//go:build windows

package main

import "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"

// runtimeServerTrustedGoplsClientBinary 只为共享 Windows gopls 选择包内已验真的可执行文件。
func runtimeServerTrustedGoplsClientBinary(command multilsp.ServerCommand, binary string) (string, error) {
	if !runtimeServerUsesSharedGoplsDaemon(command) {
		return binary, nil
	}
	proof, err := runtimeServerTrustedWindowsGopls(binary)
	if err != nil {
		return "", err
	}
	return proof.Path, nil
}
