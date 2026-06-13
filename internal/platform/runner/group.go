package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/anthropic-ai/super-agent-v3/internal/util/safego"
)

type Runner interface {
	Run(ctx context.Context) error
}

// NoopRunner is a Runner that blocks until its context is cancelled.
// Used as a placeholder when an optional component is disabled.
type NoopRunner struct{}

// Run 启动平台runner后台流程。
func (NoopRunner) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

type GroupOptions struct {
	EnableSignals bool
}

// RunGroup 运行group。
func RunGroup(ctx context.Context, runners []Runner, options GroupOptions) error {
	if len(runners) == 0 {
		return errors.New("no runners registered")
	}

	rootCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	resultCh := make(chan error, len(runners))
	for _, runner := range runners {
		current := runner
		safego.Go(rootCtx, nil, "runner.group.runner", func(context.Context) {
			resultCh <- runOne(rootCtx, current)
		})
	}
	var signalCh <-chan error
	if options.EnableSignals {
		signalCh = startSignalWatcher(rootCtx)
	}
	ctxDone := rootCtx.Done()
	var firstErr error
	for remaining := len(runners); remaining > 0; {
		select {
		case err := <-resultCh:
			remaining--
			firstErr = preferRunGroupError(firstErr, err)
			cancel()
		case err := <-signalCh:
			firstErr = preferRunGroupError(firstErr, err)
			signalCh = nil
			cancel()
		case <-ctxDone:
			firstErr = preferRunGroupError(firstErr, context.Canceled)
			ctxDone = nil
			cancel()
		}
	}
	return firstErr
}

func runOne(ctx context.Context, runner Runner) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("runner actor panic: %v\n%s", r, debug.Stack())
		}
	}()
	return runner.Run(ctx)
}

func startSignalWatcher(rootCtx context.Context) <-chan error {
	errCh := make(chan error, 1)
	safego.Go(rootCtx, nil, "runner.group.signal", func(ctx context.Context) {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(signals)
		select {
		case <-ctx.Done():
			return
		case sig := <-signals:
			errCh <- fmt.Errorf("received signal: %s", sig)
		}
	})
	return errCh
}

func preferRunGroupError(current, next error) error {
	if next == nil {
		return current
	}
	if current == nil || errors.Is(current, context.Canceled) {
		return next
	}
	return current
}
