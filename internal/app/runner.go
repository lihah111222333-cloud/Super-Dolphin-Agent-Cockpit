package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"go.uber.org/fx"

	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	uiwails "github.com/anthropic-ai/super-agent-v3/internal/ui/wails"
)

type RunnerResult struct {
	fx.Out
	Runner platformrunner.Runner `group:"runners"`
}

type runtimeParams struct {
	fx.In

	Logger            *slog.Logger
	Runners           []platformrunner.Runner `group:"runners"`
	Shutdowner        fx.Shutdowner
	Lifecycle         *uiwails.WailsLifecycle `optional:"true"`
	ExtractionDrainer interface {
		DrainPendingExtraction(ctx context.Context) error
	} `optional:"true"`
}

func BindRuntime(lc fx.Lifecycle, p runtimeParams) {
	var (
		cancel       context.CancelFunc
		shutdownOnce sync.Once
	)
	done := make(chan error, 1)
	requestShutdown := func() {
		shutdownOnce.Do(func() {
			platformshared.LogIgnoredError(p.Logger, "shutdown error", p.Shutdowner.Shutdown())
		})
	}

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			runCtx, runCancel := context.WithCancel(context.Background())
			cancel = runCancel

			runtimesafe.SafeGo(runCtx, p.Logger, "app.runtime.runGroup", func(context.Context) {
				err := platformrunner.RunGroup(runCtx, p.Runners, platformrunner.GroupOptions{
					EnableSignals: false,
				})
				done <- err
				close(done)

				if err != nil && !errors.Is(err, context.Canceled) {
					p.Logger.Error("runtime exited", "error", err)
					if p.Lifecycle != nil {
						p.Lifecycle.NotifyBackendFailed()
					}
				}

				// RunGroup returning means the runtime has ended; always stop fx.
				requestShutdown()
			})

			return nil
		},
		OnStop: func(ctx context.Context) error {
			if p.ExtractionDrainer != nil {
				platformshared.LogIgnoredError(p.Logger, "memory extraction drain failed", p.ExtractionDrainer.DrainPendingExtraction(ctx))
			}
			if cancel != nil {
				cancel()
			}

			select {
			case <-done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
}
