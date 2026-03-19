// Package main is the V3 desktop agent terminal entry point.
//
// Architecture:
//   - uber-go/fx for DI and lifecycle management
//   - oklog/run for goroutine orchestration
//   - jrpc2 for RPC (via internal/apiserver)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/oklog/run"
	"go.uber.org/fx"

	"github.com/anthropic/super-agent-v3/internal/apiserver"
)

func main() {
	app := fx.New(
		fx.Provide(
			NewLogger,
			apiserver.NewServer,
		),
		fx.Invoke(bootstrap),
	)

	app.Run()
}

// NewLogger creates a structured logger for DI.
func NewLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
}

// bootstrap wires the goroutine lifecycle via oklog/run.
// fx.Lifecycle hooks ensure ordered startup/shutdown.
func bootstrap(lc fx.Lifecycle, srv *apiserver.Server, logger *slog.Logger) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			logger.Info("V3 agent-terminal starting")

			// oklog/run orchestrates concurrent actors
			var g run.Group

			// Actor 1: Signal handler
			{
				sigCh := make(chan os.Signal, 1)
				signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
				g.Add(
					func() error {
						sig := <-sigCh
						return fmt.Errorf("received signal: %v", sig)
					},
					func(error) {
						signal.Stop(sigCh)
						close(sigCh)
					},
				)
			}

			// Actor 2: RPC server (placeholder — will serve on listener)
			// TODO: P7 migration — add real HTTP listener

			go func() {
				if err := g.Run(); err != nil {
					logger.Warn("run group exited", slog.String("reason", err.Error()))
				}
			}()

			return nil
		},
		OnStop: func(ctx context.Context) error {
			logger.Info("V3 agent-terminal shutting down")
			return srv.Close()
		},
	})
}
