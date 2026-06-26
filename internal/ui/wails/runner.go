package wails

import (
	"context"
	"errors"
	"time"

	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// runner 把 Wails application 适配成 platform runner。
type runner struct {
	app *application.App
}

// NewRunner 创建 Wails application runner。
// app 为空时保留到 Run 阶段 fail-fast 返回错误，方便 Fx 装配阶段仍可构建接口值。
func NewRunner(app *application.App) platformrunner.Runner {
	return &runner{app: app}
}

// Run 启动 Wails application，并在 context 取消时请求窗口退出。
// Wails Run 在独立 goroutine 中执行，取消后最多等待短窗口让底层清理完成。
func (r *runner) Run(ctx context.Context) error {
	if r == nil || r.app == nil {
		return errors.New("wails runner: application is not configured")
	}
	done := make(chan error, 1)
	runtimesafe.SafeGo(ctx, pkglogger.Get(), "wails.runner.appRun", func(context.Context) {
		done <- r.app.Run()
	})
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		r.app.Quit()
		return waitForQuit(done, ctx.Err())
	}
}

// waitForQuit 等待 Wails Run 返回，超时则保留上游取消错误。
func waitForQuit(done <-chan error, fallback error) error {
	select {
	case err := <-done:
		if err != nil {
			return err
		}
		return fallback
	case <-time.After(5 * time.Second):
		return fallback
	}
}
