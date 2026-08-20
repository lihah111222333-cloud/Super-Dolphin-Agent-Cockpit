package main

import (
	"errors"
	"io"
	"sync"

	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// sidecarFileLoggerGate 把私有日志初始化延迟到平台策略允许的边界。
// 初始化只在成功后记忆；权限失败会原样返回，使宿主批准后的同一工具调用仅重试一次原操作。
type sidecarFileLoggerGate struct {
	mu         sync.Mutex
	ready      bool
	initialize func() error
}

// newSidecarFileLoggerGate 创建成功记忆、失败可重试的日志初始化门。
func newSidecarFileLoggerGate(initialize func() error) (*sidecarFileLoggerGate, error) {
	if initialize == nil {
		return nil, errors.New("mcp-lsp file logger initializer is required")
	}
	return &sidecarFileLoggerGate{initialize: initialize}, nil
}

// Ensure 确保私有日志已完成严格 owner-only 初始化。
func (g *sidecarFileLoggerGate) Ensure() error {
	if g == nil {
		return errors.New("mcp-lsp file logger gate is required")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ready {
		return nil
	}
	if err := g.initialize(); err != nil {
		return err
	}
	g.ready = true
	return nil
}

// prepareSidecarFileLogger 创建平台策略门；所有平台先同步初始化，仅 Windows typed
// Win32 5/1314 延迟到 tools/call，以便可信错误触发宿主授权后的单次重试。
func prepareSidecarFileLogger(logRuntime *pkglogger.Runtime, homeDir string, console io.Writer) (*sidecarFileLoggerGate, error) {
	return prepareSidecarFileLoggerWithInitializer(func() error {
		return initSidecarFileLogger(logRuntime, homeDir, console)
	})
}

func prepareSidecarFileLoggerAt(logRuntime *pkglogger.Runtime, logDir string, console io.Writer) (*sidecarFileLoggerGate, error) {
	return prepareSidecarFileLoggerWithInitializer(func() error {
		return initSidecarFileLoggerAt(logRuntime, logDir, console)
	})
}

// prepareSidecarFileLoggerWithInitializer 先执行正常启动初始化；仅 Windows typed 5/1314
// 可以保留同一 gate 到 tools/call，普通路径、配置和其他平台错误仍在启动期 fail-fast。
func prepareSidecarFileLoggerWithInitializer(initialize func() error) (*sidecarFileLoggerGate, error) {
	gate, err := newSidecarFileLoggerGate(initialize)
	if err != nil {
		return nil, err
	}
	if err := gate.Ensure(); err != nil {
		if sidecarFileLoggerCanDeferPermissionError(err) {
			return gate, nil
		}
		return nil, err
	}
	return gate, nil
}
