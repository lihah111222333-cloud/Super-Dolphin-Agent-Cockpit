package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

// Runner 是平台后台组件的统一启停 contract，Run 必须在 ctx 取消后尽快返回。
type Runner interface {
	Run(ctx context.Context) error
}

// NoopRunner 在上下文取消前保持阻塞，用作可选组件关闭时的占位 runner。
type NoopRunner struct{}

// Run 等待调用方取消上下文，不主动产生业务错误。
func (NoopRunner) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// GroupOptions 控制 RunGroup 的进程级行为。
type GroupOptions struct {
	// EnableSignals 允许 RunGroup 监听 SIGINT/SIGTERM 并触发统一取消。
	EnableSignals bool
}

// RunGroup 并发启动一组 runner，任一 runner、父上下文或系统信号结束都会取消其余成员。
// 返回值优先保留真实 runner 错误，避免仅把主动取消包装成根因。
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

// runOne 执行单个 runner，并把 panic 转成错误返回给 RunGroup 聚合。
// 这里不直接 recover 后吞错，调用方仍能按普通错误路径触发整体取消。
func runOne(ctx context.Context, runner Runner) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("runner actor panic: %v\n%s", r, debug.Stack())
		}
	}()
	return runner.Run(ctx)
}

// startSignalWatcher 在独立 goroutine 中等待终止信号，并把信号转成错误事件。
// rootCtx 取消后会停止 signal.Notify，避免测试或多 runner 进程残留订阅。
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

// preferRunGroupError 合并 runner、信号和上下文错误，优先保留第一个非取消根因。
func preferRunGroupError(current, next error) error {
	if next == nil {
		return current
	}
	if current == nil || errors.Is(current, context.Canceled) {
		return next
	}
	return current
}
