package main

import (
	"os"

	"github.com/anthropic-ai/super-agent-v3/internal/app"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rlimit"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimeenv"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// main 初始化 headless owner 运行时并启动共享核心应用图。
func main() {
	rlimit.Init()
	if err := os.Setenv("SUPER_DOLPHIN_PROCESS_ROLE", "owner"); err != nil {
		pkglogger.Get().Error("agent-runtime startup env failed", "error", err)
		os.Exit(1)
	}
	if err := os.Setenv("SUPER_DOLPHIN_ENTRYPOINT", "agent-runtime"); err != nil {
		pkglogger.Get().Error("agent-runtime entrypoint env failed", "error", err)
		os.Exit(1)
	}
	if err := runtimeenv.ConfigurePackagedApp(); err != nil {
		pkglogger.Get().Error("agent-runtime packaged runtime env failed", "error", err)
		os.Exit(1)
	}
	if err := runtimeenv.LoadVideoEnv(); err != nil {
		pkglogger.Get().Error("agent-runtime video env failed", "error", err)
		os.Exit(1)
	}
	if err := app.Run(); err != nil {
		pkglogger.Get().Error("agent-runtime failed", "error", err)
		os.Exit(1)
	}
}
