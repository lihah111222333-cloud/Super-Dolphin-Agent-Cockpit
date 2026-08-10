// Package main 是 mcp-lsp sidecar 进程的入口，通过 MCP stdio 协议暴露 LSP 工具能力。
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rlimit"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// binaryName 和 binaryVersion 标识本 sidecar 进程名称和版本。
const (
	binaryName    = "mcp-lsp"
	binaryVersion = "dev"
)

// main 初始化 sidecar 运行环境，保护 MCP stdout 通道后启动服务，异常时以非零码退出。
func main() {
	// 所有模式都先限制调度线程；内部监管路径异常时也不能占满宿主 CPU。
	if runtime.GOMAXPROCS(0) > 2 {
		runtime.GOMAXPROCS(2)
	}
	if handled, exitCode := hiddenexec.RunProcessSupervisorIfRequested(os.Args); handled {
		os.Exit(exitCode)
	}
	rlimit.Init()
	stdout := os.Stdout
	os.Stdout = os.Stderr
	logRuntime := pkglogger.NewRuntime(pkglogger.RuntimeConfig{ServiceName: binaryName, ServiceVersion: binaryVersion, LogFilePrefix: binaryName})

	homeDir, err := os.UserHomeDir()
	if err != nil {
		_, _ = os.Stderr.WriteString("mcp-lsp resolve user home failed: " + err.Error() + "\n")
		os.Exit(1)
	}
	if err := initSidecarFileLogger(logRuntime, homeDir, os.Stderr); err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
	if err := os.Setenv("SUPER_DOLPHIN_PROCESS_ROLE", "sidecar"); err != nil {
		logRuntime.Get().Error("mcp-lsp startup env failed", pkglogger.FieldError, err)
		os.Exit(1)
	}
	if err := runtimeenv.ConfigureSidecarRuntime(); err != nil {
		logRuntime.Get().Error("mcp-lsp sidecar runtime env failed", pkglogger.FieldError, err)
		os.Exit(1)
	}
	exitCode := runMain(stdout, logRuntime)
	logRuntime.ShutdownFileHandler()
	os.Exit(exitCode)
}

// initSidecarFileLogger 将 mcp-lsp 日志同时写入 stderr 和进程自有的私有文件。
func initSidecarFileLogger(logRuntime *pkglogger.Runtime, homeDir string, console io.Writer) error {
	if logRuntime == nil {
		return fmt.Errorf("initialize mcp-lsp file logger: logger runtime is required")
	}
	logDir := filepath.Join(homeDir, ".multi-agent", "log", binaryName)
	if err := logRuntime.InitWithFileOptions(logDir, pkglogger.FileOptions{
		Prefix:        binaryName,
		ConsoleWriter: console,
	}); err != nil {
		return fmt.Errorf("initialize mcp-lsp file logger: %w", err)
	}
	logRuntime.BindDefault()
	return nil
}

// runMain 启动 LSP sidecar 并把错误转换为进程退出码。
// 日志写 stderr，stdout 继续留给 MCP JSON-RPC 帧。
func runMain(stdout *os.File, logRuntime *pkglogger.Runtime) int {
	if logRuntime == nil {
		_, _ = os.Stderr.WriteString("mcp-lsp logger runtime is required\n")
		return 1
	}
	if err := run(stdout, logRuntime); err != nil {
		logRuntime.Get().Error("mcp-lsp failed", "error", err)
		return 1
	}
	return 0
}
