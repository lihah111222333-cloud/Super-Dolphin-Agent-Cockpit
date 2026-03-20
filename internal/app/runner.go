package app

import (
	"context"
	"errors"
	"log/slog"

	"go.uber.org/fx"

	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	uiwails "github.com/anthropic-ai/super-agent-v3/internal/ui/wails"
)

type RunnerResult struct {
	fx.Out
	Runner platformrunner.Runner `group:"runners"`
}

type runtimeParams struct {
	fx.In

	Logger     *slog.Logger
	Runners    []platformrunner.Runner `group:"runners"`
	Shutdowner fx.Shutdowner
	Lifecycle  *uiwails.WailsLifecycle `optional:"true"`
}

func BindRuntime(lc fx.Lifecycle, p runtimeParams) {
	var cancel context.CancelFunc
	done := make(chan error, 1)

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			runCtx, runCancel := context.WithCancel(context.Background())
			cancel = runCancel

			go func() {
				err := platformrunner.RunGroup(runCtx, p.Runners, platformrunner.GroupOptions{
					EnableSignals: p.Lifecycle == nil,
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
				_ = p.Shutdowner.Shutdown()
			}()

			return nil
		},
		OnStop: func(ctx context.Context) error {
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
