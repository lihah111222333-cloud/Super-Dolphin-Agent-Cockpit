package main

import (
	"io"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func newTestLoggerRuntime() *pkglogger.Runtime {
	runtime := pkglogger.NewRuntime(pkglogger.RuntimeConfig{})
	runtime.InitWithConsoleWriter(io.Discard)
	return runtime
}

func newTestMCPServer(name, version string, transport *common.StdioTransport, tools common.ToolProvider, options ...common.ServerOption) *common.Server {
	options = append(options, common.WithLoggerRuntime(newTestLoggerRuntime()))
	return common.NewServer(name, version, transport, tools, options...)
}
