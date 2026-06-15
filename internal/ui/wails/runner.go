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

type runner struct {
	app *application.App
}

// NewRunner 创建runner。
func NewRunner(app *application.App) platformrunner.Runner {
	return &runner{app: app}
}

// Run 启动桌面 UI 桥接后台流程。
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
