//go:build windows

package main

import (
	"context"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
)

// runtimeASTGrepEnsurer 优先使用已由 runtimeenv 校验 SHA-256 的 bundle companion；缺失时走锁定安装器。
func runtimeASTGrepEnsurer(inst *installer.Provider, bundle runtimeenv.LSPBundle, _ bool) (func(context.Context) (string, error), error) {
	if companion, ok := bundle.ServerForLanguage(runtimeASTGrepLanguageID); ok {
		path := companion.Path
		return func(ctx context.Context) (string, error) {
			if ctx == nil {
				return "", errors.New("ast-grep runtime context is nil")
			}
			if path == "" {
				return "", errors.New("ast-grep bundle companion returned an empty explicit path")
			}
			return path, nil
		}, nil
	}
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
