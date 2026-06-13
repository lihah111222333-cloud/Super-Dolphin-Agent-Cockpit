package toolbridge

import (
	"context"
	"errors"
	"net"
	"sync"

	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// ProxyRunner is the P22 P2 Finding 9 owner of the toolbridge HTTP proxy
// serve loop. Before P2 the proxy was started by
// `registerProxyLifecycle -> OnStart -> go h.ServeProxy(listener)` which
// left the serve goroutine unowned by run.Group and meant shutdown had to
// close the listener from a separate path.
//
// Ownership split after P2:
//
//   - registerProxyLifecycle stays in fx.Module and only does the synchronous
//     `net.Listen` + proxyAddr publish. It hands the listener to ProxyRunner
//     via SetListener before run.Group picks up the runner.
//   - ProxyRunner.Run(ctx) is registered in `group:"runners"`. It blocks on
//     h.ServeProxy(listener) while ctx is alive. When ctx cancels it closes
//     the listener itself (causing ServeProxy to return), joins the inner
//     serve goroutine, then returns ctx.Err().
//
// A small inner goroutine exists inside Run because net/http's Serve is not
// ctx-aware. It is owner-joined: we always block on `<-serveErr` before Run
// returns, so it never outlives the actor.
type ProxyRunner struct {
	h      *Handler
	logger *pkglogger.Logger

	mu       sync.Mutex
	listener net.Listener
}

// NewProxyRunner constructs the runner. Handler may be nil; a nil handler
// runner blocks on ctx and no-ops, mirroring the pre-P2 defensive branch.
// NewProxyRunner 创建proxyrunner。
func NewProxyRunner(h *Handler) *ProxyRunner {
	logger := pkglogger.Get()
	if h != nil && h.logger != nil {
		logger = h.logger
	}
	return &ProxyRunner{h: h, logger: logger}
}

// asRunnerGroup narrows *ProxyRunner to platformrunner.Runner for the
// group:"runners" tag output. Named helper (not an inline closure) so the
// module-level Provide list stays readable.
func asRunnerGroup(r *ProxyRunner) platformrunner.Runner { return r }

// SetListener hands over the listener that Run should serve. Called by
// registerProxyLifecycle.OnStart after net.Listen succeeds; the addr is
// published to proxyAddr in the same call so downstream lookup via
// provideProxyAddrFn returns the real address before Run starts.
//
// Calling SetListener after Run has already observed the listener is a
// no-op: Run reads once under the mutex and keeps the reference locally.
// SetListener 设置listener。
func (r *ProxyRunner) SetListener(ln net.Listener) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.listener = ln
	r.mu.Unlock()
}

// Run implements platformrunner.Runner. See the type-level doc for the
// shutdown contract.
// Run 启动平台toolbridge后台流程。
func (r *ProxyRunner) Run(ctx context.Context) error {
	if r == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	r.mu.Lock()
	ln := r.listener
	h := r.h
	r.mu.Unlock()
	if ln == nil || h == nil {
		// Either net.Listen failed during OnStart or the handler was not
		// provided. Nothing to serve; honor ctx cancellation so the
		// run.Group actor unwinds cleanly.
		<-ctx.Done()
		return ctx.Err()
	}

	serveErr := startProxyServe(h, ln)

	select {
	case err := <-serveErr:
		// Serve returned without us cancelling; ServeProxy already swallows
		// ErrServerClosed / ErrClosed to nil, so a non-nil err here is a
		// genuine failure worth surfacing to run.Group.
		if err != nil {
			r.logger.Error("toolbridge: proxy serve failed", "error", err)
		}
		return err
	case <-ctx.Done():
	}

	// Shutdown: close the listener to unblock ServeProxy, then join so the
	// inner goroutine never outlives Run.
	if closeErr := ln.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
		r.logger.Warn("toolbridge: close proxy listener during shutdown", "error", closeErr)
	}
	if err := <-serveErr; err != nil {
		r.logger.Warn("toolbridge: proxy serve returned error during shutdown", "error", err)
	}
	return ctx.Err()
}

func startProxyServe(h *Handler, ln net.Listener) <-chan error {
	serveErr := make(chan error, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				serveErr <- errors.New("toolbridge: proxy serve panic")
			}
		}()
		serveErr <- h.ServeProxy(ln)
	}()
	return serveErr
}
