//go:build !windows

package main

import (
	"context"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
)

// runtimeASTGrepEnsurer 保留 Linux/macOS 的既有 PATH ast-grep 行为；Windows 才有锁定 cohort。
func runtimeASTGrepEnsurer(_ *installer.Provider, _ runtimeenv.LSPBundle, _ bool) (func(context.Context) (string, error), error) {
	return nil, nil
}
