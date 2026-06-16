package app

import (
	"context"
	"errors"
	"sync"

	"go.uber.org/fx"

	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimesafe"
	uiwails "github.com/anthropic-ai/super-agent-v3/internal/ui/wails"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// RunnerResult contributes a managed runner to the Fx runner group.
type RunnerResult struct {
	fx.Out
	Runner platformrunner.Runner `group:"runners"`
}

// RootCtxProvider supplies the application-owned root context to runtime
// actors. Run / RunDesktop create this root once and pass it through Fx so
// BindRuntime's run.Group tree is a child of the same owner as the Fx app
// shutdown path instead of an independent context.Background tree.
type RootCtxProvider interface {
	RootContext() context.Context
}

type runtimeDoneMarker interface {
	MarkRuntimeDone()
}

type runtimePreDrainRegistrar interface {
	RegisterRuntimePreDrain(func(context.Context) error)
	DrainRuntime(context.Context) error
}

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

// RootContext 处理根目录上下文。
func (o *appOwnerContext) RootContext() context.Context {
	if o == nil || o.ctx == nil {
		return context.Background()
	}
	return o.ctx
}

// Cancel 取消当前运行。
func (o *appOwnerContext) Cancel() {
	if o != nil && o.cancel != nil {
		o.cancel()
	}
}

// MarkRuntimeDone 标记运行时done。
func (o *appOwnerContext) MarkRuntimeDone() {
	if o == nil {
		return
	}
	o.runtimeDoneOnce.Do(func() { close(o.runtimeDone) })
}

// RegisterRuntimePreDrain 注册运行时predrain。
func (o *appOwnerContext) RegisterRuntimePreDrain(fn func(context.Context) error) {
	if o == nil || fn == nil {
		return
	}
	o.mu.Lock()
	o.runtimePreDrain = fn
	o.mu.Unlock()
}

// DrainRuntime 等待运行时收尾。
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

// WaitRuntimeDone 等待运行时完全退出。
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

// BindRuntime 绑定运行时。
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

func startRuntimeRunGroup(runCtx context.Context, done chan<- error, p runtimeParams, requestShutdown func()) {
	runtimesafe.SafeGo(runCtx, p.Logger, "app.runtime.runGroup", func(context.Context) {
		err := platformrunner.RunGroup(runCtx, p.Runners, platformrunner.GroupOptions{
			EnableSignals: false,
		})
		done <- err
		close(done)
		markRuntimeDone(p.RootCtx)
		reportRuntimeExit(err, p)

		// RunGroup returning means the runtime has ended; always stop fx.
		requestShutdown()
	})
}

func runtimeRootContext(root RootCtxProvider) context.Context {
	if root == nil {
		return context.Background()
	}
	return root.RootContext()
}

func markRuntimeDone(root RootCtxProvider) {
	if marker, ok := root.(runtimeDoneMarker); ok {
		marker.MarkRuntimeDone()
	}
}

func registerRuntimePreDrain(p runtimeParams) {
	registrar, ok := p.RootCtx.(runtimePreDrainRegistrar)
	if !ok || p.ExtractionDrainer == nil {
		return
	}
	registrar.RegisterRuntimePreDrain(func(ctx context.Context) error {
		return p.ExtractionDrainer.DrainPendingExtraction(ctx)
	})
}

func drainRuntimeBeforeStop(ctx context.Context, p runtimeParams) {
	if registrar, ok := p.RootCtx.(runtimePreDrainRegistrar); ok {
		platformshared.LogIgnoredError(p.Logger, "memory extraction drain failed", registrar.DrainRuntime(ctx))
		return
	}
	if p.ExtractionDrainer != nil {
		platformshared.LogIgnoredError(p.Logger, "memory extraction drain failed", p.ExtractionDrainer.DrainPendingExtraction(ctx))
	}
}

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

func waitForRuntimeDone(runtimeDone <-chan error, ctx context.Context) error {
	select {
	case err := <-runtimeDone:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type runtimeParams struct {
	fx.In

	Logger            *pkglogger.Logger
	Runners           []platformrunner.Runner `group:"runners"`
	Shutdowner        fx.Shutdowner
	RootCtx           RootCtxProvider         `optional:"true"`
	Lifecycle         *uiwails.WailsLifecycle `optional:"true"`
	ExtractionDrainer interface {
		DrainPendingExtraction(ctx context.Context) error
	} `optional:"true"`
}
