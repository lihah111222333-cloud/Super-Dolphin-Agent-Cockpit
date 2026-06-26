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

// RunnerResult 将 platform runner 通过 fx group 暴露给 BindRuntime。
type RunnerResult struct {
	fx.Out
	Runner platformrunner.Runner `group:"runners"`
}

// RootCtxProvider 向 runtime actor 暴露应用拥有的根 context。
// Run/RunDesktop 只创建一次根 context，并通过 Fx 传入 BindRuntime，确保 runner 树和 Fx 停止路径共享同一取消源。
type RootCtxProvider interface {
	RootContext() context.Context
}

// runtimeDoneMarker 标记 runtime run.Group 已退出。
type runtimeDoneMarker interface {
	MarkRuntimeDone()
}

// runtimePreDrainRegistrar 注册 Fx 停止前需要先 drain 的 runtime 工作。
type runtimePreDrainRegistrar interface {
	RegisterRuntimePreDrain(func(context.Context) error)
	DrainRuntime(context.Context) error
}

// appOwnerContext 持有应用根 context 和 runtime 收尾钩子。
// 桌面和后台模式共享它，确保 Fx 停止和 runner 树使用同一取消源。
type appOwnerContext struct {
	ctx    context.Context
	cancel context.CancelFunc

	mu              sync.Mutex
	runtimePreDrain func(context.Context) error
	preDrainOnce    sync.Once
	preDrainErr     error
	runtimeDone     chan struct{}
	runtimeDoneOnce sync.Once
}

// newAppOwnerContext 创建应用所有者 context。
func newAppOwnerContext(parent context.Context) *appOwnerContext {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	return &appOwnerContext{
		ctx:         ctx,
		cancel:      cancel,
		runtimeDone: make(chan struct{}),
	}
}

// RootContext 返回应用所有者根 context。
func (o *appOwnerContext) RootContext() context.Context {
	if o == nil || o.ctx == nil {
		return context.Background()
	}
	return o.ctx
}

// Cancel 取消应用所有者根 context。
func (o *appOwnerContext) Cancel() {
	if o != nil && o.cancel != nil {
		o.cancel()
	}
}

// MarkRuntimeDone 标记 runtime run.Group 已退出。
func (o *appOwnerContext) MarkRuntimeDone() {
	if o == nil {
		return
	}
	o.runtimeDoneOnce.Do(func() { close(o.runtimeDone) })
}

// RegisterRuntimePreDrain 注册 runtime 停止前的 drain 函数。
func (o *appOwnerContext) RegisterRuntimePreDrain(fn func(context.Context) error) {
	if o == nil || fn == nil {
		return
	}
	o.mu.Lock()
	o.runtimePreDrain = fn
	o.mu.Unlock()
}

// DrainRuntime 执行一次 runtime pre-drain。
// sync.Once 保证 Wails 退出和 Fx OnStop 竞态时只 drain 一次。
func (o *appOwnerContext) DrainRuntime(ctx context.Context) error {
	if o == nil {
		return nil
	}
	o.preDrainOnce.Do(func() {
		o.mu.Lock()
		fn := o.runtimePreDrain
		o.mu.Unlock()
		if fn != nil {
			o.preDrainErr = fn(ctx)
		}
	})
	return o.preDrainErr
}

// WaitRuntimeDone 等待 runtime run.Group 完全退出。
func (o *appOwnerContext) WaitRuntimeDone(ctx context.Context) error {
	if o == nil || o.runtimeDone == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-o.runtimeDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// BindRuntime 将所有 platform runner 接入 Fx 生命周期。
// OnStop 会取消 runCtx、等待 runner 退出，再 drain 内存提取等收尾任务。
func BindRuntime(lc fx.Lifecycle, p runtimeParams) {
	var (
		cancel       context.CancelFunc
		shutdownOnce sync.Once
	)
	done := make(chan error, 1)
	registerRuntimePreDrain(p)
	requestShutdown := func() {
		shutdownOnce.Do(func() {
			platformshared.LogIgnoredError(p.Logger, "shutdown error", p.Shutdowner.Shutdown())
		})
	}

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			runCtx, runCancel := context.WithCancel(runtimeRootContext(p.RootCtx))
			cancel = runCancel
			startRuntimeRunGroup(runCtx, done, p, requestShutdown)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if cancel != nil {
				cancel()
			}

			runErr := waitForRuntimeDone(done, ctx)

			drainRuntimeBeforeStop(ctx, p)
			if errors.Is(runErr, context.Canceled) {
				return nil
			}
			return runErr
		},
	})
}

// startRuntimeRunGroup 在受保护 goroutine 中运行 platform runner group。
// runner group 退出后会请求 Fx shutdown，让后台和桌面模式都能统一收尾。
func startRuntimeRunGroup(runCtx context.Context, done chan<- error, p runtimeParams, requestShutdown func()) {
	runtimesafe.SafeGo(runCtx, p.Logger, "app.runtime.runGroup", func(context.Context) {
		err := platformrunner.RunGroup(runCtx, p.Runners, platformrunner.GroupOptions{
			EnableSignals: false,
		})
		done <- err
		close(done)
		markRuntimeDone(p.RootCtx)
		reportRuntimeExit(err, p)

		// RunGroup 返回意味着 runtime 已结束；无论错误与否都触发 Fx 统一收尾。
		requestShutdown()
	})
}

// runtimeRootContext 返回 runtime runner 的根 context。
func runtimeRootContext(root RootCtxProvider) context.Context {
	if root == nil {
		return context.Background()
	}
	return root.RootContext()
}

// markRuntimeDone 通知 owner runtime 已退出。
func markRuntimeDone(root RootCtxProvider) {
	if marker, ok := root.(runtimeDoneMarker); ok {
		marker.MarkRuntimeDone()
	}
}

// registerRuntimePreDrain 将内存提取 drain 注册到 owner context。
func registerRuntimePreDrain(p runtimeParams) {
	registrar, ok := p.RootCtx.(runtimePreDrainRegistrar)
	if !ok || p.ExtractionDrainer == nil {
		return
	}
	registrar.RegisterRuntimePreDrain(func(ctx context.Context) error {
		return p.ExtractionDrainer.DrainPendingExtraction(ctx)
	})
}

// drainRuntimeBeforeStop 执行 runtime 收尾 drain。
// 没有 owner registrar 时直接调用 drainer，保持旧装配路径可用。
func drainRuntimeBeforeStop(ctx context.Context, p runtimeParams) {
	if registrar, ok := p.RootCtx.(runtimePreDrainRegistrar); ok {
		platformshared.LogIgnoredError(p.Logger, "memory extraction drain failed", registrar.DrainRuntime(ctx))
		return
	}
	if p.ExtractionDrainer != nil {
		platformshared.LogIgnoredError(p.Logger, "memory extraction drain failed", p.ExtractionDrainer.DrainPendingExtraction(ctx))
	}
}

// reportRuntimeExit 将非预期 runtime 退出写日志并通知桌面生命周期。
func reportRuntimeExit(err error, p runtimeParams) {
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	// 误判防护：reportRuntimeExit 将非预期 runtime 退出升级给 WailsLifecycle.NotifyBackendFailed。
	p.Logger.Error("runtime exited", "error", err)
	if p.Lifecycle != nil {
		p.Lifecycle.NotifyBackendFailed()
	}
}

// waitForRuntimeDone 等待 runner group 退出或停止 context 超时。
func waitForRuntimeDone(runtimeDone <-chan error, ctx context.Context) error {
	select {
	case err := <-runtimeDone:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// runtimeParams 是 BindRuntime 所需的 Fx 参数集合。
type runtimeParams struct {
	fx.In

	Logger            *slog.Logger
	Runners           []platformrunner.Runner `group:"runners"`
	Shutdowner        fx.Shutdowner
	RootCtx           RootCtxProvider         `optional:"true"`
	Lifecycle         *uiwails.WailsLifecycle `optional:"true"`
	ExtractionDrainer interface {
		DrainPendingExtraction(ctx context.Context) error
	} `optional:"true"`
}
