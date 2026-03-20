package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/oklog/run"
)

type Runner interface {
	Run(ctx context.Context) error
}

func RunGroup(ctx context.Context, runners []Runner) error {
	if len(runners) == 0 {
		return errors.New("no runners registered")
	}

	rootCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var group run.Group
	group.Add(func() error {
		<-rootCtx.Done()
		return nil
	}, func(error) {
		cancel()
	})

	group.Add(func() error {
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

	for _, runner := range runners {
		current := runner
		group.Add(func() error {
			return current.Run(rootCtx)
		}, func(error) {
			cancel()
		})
	}

	return group.Run()
}
