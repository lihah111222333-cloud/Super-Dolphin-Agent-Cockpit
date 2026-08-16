//go:build !windows

package main

import "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"

// runtimeServerTrustedGoplsClientBinary 保持非 Windows 的既有二进制选择语义。
func runtimeServerTrustedGoplsClientBinary(_ multilsp.ServerCommand, binary string) (string, error) {
	return binary, nil
}
