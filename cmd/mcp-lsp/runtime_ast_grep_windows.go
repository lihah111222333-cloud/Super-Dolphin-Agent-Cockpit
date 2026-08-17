//go:build windows

package main

import (
	"context"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
)

// runtimeASTGrepEnsurer 返回 Windows ast-grep 的显式安装/解析闭包；它不读取 PATH。
func runtimeASTGrepEnsurer(inst *installer.Provider, _ bool) (func(context.Context) (string, error), error) {
	if inst == nil {
		inst = installer.NewProvider()
		productRoot, rootErr := runtimeenv.ResolveWindowsLSPProductRoot()
		registerWindowsASTGrepRuntimeInstaller(inst, productRoot, rootErr)
	}
	return func(ctx context.Context) (string, error) {
		if ctx == nil {
			return "", errors.New("ast-grep runtime context is nil")
		}
		result, err := inst.EnsureInstalledDetailed(installer.WithInstallCommandCapability(ctx), runtimeASTGrepLanguageID)
		if err != nil {
			return "", err
		}
		if result.Path == "" {
			return "", errors.New("ast-grep installer returned an empty explicit path")
		}
		return result.Path, nil
	}, nil
}
