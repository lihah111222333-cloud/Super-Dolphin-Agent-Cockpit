//go:build !windows

package main

import (
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

// runtimeServerPrepareVueBridge 在非 Windows 平台严格保持参数与桥接为空，不改变既有 Node wiring。
func runtimeServerPrepareVueBridge(_ multilsp.LanguageAdapter, _ string, args []string) ([]string, *runtimeVueTSBridgeSpec, error) {
	return append([]string(nil), args...), nil, nil
}

// runtimeServerPrepareVueTSCompanionEnvironment 在非 Windows 平台严格保持环境不变；Vue bridge 仅由 Windows product-owned wiring 启用。
func runtimeServerPrepareVueTSCompanionEnvironment(env []string) ([]string, error) {
	return append([]string(nil), env...), nil
}
