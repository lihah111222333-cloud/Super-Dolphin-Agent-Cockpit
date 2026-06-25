// Package main 是桌面终端应用的入口，负责初始化运行环境并启动 Wails 桌面 UI。
package main

import (
	"os"

	"github.com/anthropic-ai/super-agent-v3/internal/app"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rlimit"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimeenv"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// main 初始化文件描述符限制、设置进程角色环境变量，并启动桌面应用。
func main() {
	rlimit.Init()
	if err := os.Setenv("SUPER_DOLPHIN_PROCESS_ROLE", "desktop"); err != nil {
		pkglogger.Get().Error("agent-terminal startup env failed", "error", err)
		os.Exit(1)
	}
	if err := runtimeenv.ConfigurePackagedApp(); err != nil {
		pkglogger.Get().Error("agent-terminal packaged runtime env failed", "error", err)
		os.Exit(1)
	}
	if err := runtimeenv.LoadVideoEnv(); err != nil {
		pkglogger.Get().Error("agent-terminal video env failed", "error", err)
		os.Exit(1)
	}
	if err := app.RunDesktop(frontendDistFS()); err != nil {
		pkglogger.Get().Error("agent-terminal failed", "error", err)
		os.Exit(1)
	}
}
