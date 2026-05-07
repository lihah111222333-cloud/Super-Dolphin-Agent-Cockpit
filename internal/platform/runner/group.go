package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/oklog/run"
)

type Runner interface {
	Run(ctx context.Context) error
}

// NoopRunner is a Runner that blocks until its context is cancelled.
// Used as a placeholder when an optional component is disabled.
type NoopRunner struct{}

func (NoopRunner) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

type GroupOptions struct {
	EnableSignals bool
}

func RunGroup(ctx context.Context, runners []Runner, options GroupOptions) error {
	if len(runners) == 0 {
		return errors.New("no runners registered")
	}

	rootCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var group run.Group
	addContextActor(&group, rootCtx, cancel)
	if options.EnableSignals {
		addSignalActor(&group, rootCtx, cancel)
	}
	addRunnerActors(&group, rootCtx, cancel, runners)

	return group.Run()
}

func addActor(group *run.Group, execute func() error, interrupt func(error)) {
	group.Add(func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("runner actor panic: %v\n%s", r, debug.Stack())
			}
		}()
		return execute()
	}, interrupt)
}

func addContextActor(group *run.Group, rootCtx context.Context, cancel context.CancelFunc) {
	addActor(group, func() error {
		<-rootCtx.Done()
		return nil
	}, func(error) {
		cancel()
	})
}

func addSignalActor(group *run.Group, rootCtx context.Context, cancel context.CancelFunc) {
	addActor(group, func() error {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(signals)

		select {
		case <-rootCtx.Done():
			return nil
		case sig := <-signals:
			return fmt.Errorf("received signal: %s", sig)
		}
	}, func(error) {
		cancel()
	})
}

func addRunnerActors(group *run.Group, rootCtx context.Context, cancel context.CancelFunc, runners []Runner) {
	for _, runner := range runners {
		current := runner
		addActor(group, func() error {
			return current.Run(rootCtx)
		}, func(error) {
			cancel()
		})
	}
}
