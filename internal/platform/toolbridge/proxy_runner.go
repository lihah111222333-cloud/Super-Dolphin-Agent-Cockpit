package toolbridge

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime/debug"
	"sync"

	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// ProxyRunner 持有 toolbridge HTTP proxy 的 serve loop。
// registerProxyLifecycle 只做 net.Listen 和地址发布，Run 负责阻塞 ServeProxy、
// 在 ctx 取消时关闭 listener，并等待内部 goroutine 退出，确保没有孤儿 serve loop。
type ProxyRunner struct {
	h      *Handler
	logger *pkglogger.Logger

	mu       sync.Mutex
	listener net.Listener
}

// NewProxyRunner 创建 proxy runner。
// Handler 可以为 nil；nil runner 只等待 ctx 结束，兼容无 toolbridge 的测试装配。
func NewProxyRunner(h *Handler) *ProxyRunner {
	logger := pkglogger.Get()
	if h != nil && h.logger != nil {
		logger = h.logger
	}
	return &ProxyRunner{h: h, logger: logger}
}

// asRunnerGroup 将 ProxyRunner 收窄为 run.Group runner 接口，供 Fx group tag 使用。
func asRunnerGroup(r *ProxyRunner) platformrunner.Runner { return r }

// SetListener 把 fx OnStart 已创建的 listener 交给 Run。
// Run 只在启动时读取一次 listener；之后再调用 SetListener 不影响已运行的 serve loop。
func (r *ProxyRunner) SetListener(ln net.Listener) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.listener = ln
	r.mu.Unlock()
}

// Run 启动 proxy serve loop，并在 ctx 取消时关闭 listener、等待内部 goroutine 退出。
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
		// listener 或 handler 缺失时没有可服务对象，仍等待 ctx 以保持 run.Group 生命周期一致。
		<-ctx.Done()
		return ctx.Err()
	}

	serveErr := startProxyServe(h, ln)

	select {
	case err := <-serveErr:
		// Serve 在未取消时返回说明 proxy 自身失败；ServeProxy 已过滤正常关闭错误。
		if err != nil {
			r.logger.Error("toolbridge: proxy serve failed", "error", err)
		}
		return err
	case <-ctx.Done():
	}

	// 关闭 listener 解除 ServeProxy 阻塞，再 join 内部 goroutine，避免它越过 Run 生命周期。
	if closeErr := ln.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
		r.logger.Warn("toolbridge: close proxy listener during shutdown", "error", closeErr)
	}
	if err := <-serveErr; err != nil {
		r.logger.Warn("toolbridge: proxy serve returned error during shutdown", "error", err)
	}
	return ctx.Err()
}

// startProxyServe 在独立 goroutine 中运行 ServeProxy，并把 panic 转成错误返回。
func startProxyServe(h *Handler, ln net.Listener) <-chan error {
	serveErr := make(chan error, 1)
	var serveWG sync.WaitGroup
	serveWG.Go(func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				pkglogger.Get().Error("toolbridge: proxy serve panic", "recovered", recovered, "stack", string(debug.Stack()))
				serveErr <- fmt.Errorf("toolbridge: proxy serve panic: %v", recovered)
			}
		}()
		serveErr <- h.ServeProxy(ln)
	})
	return serveErr
}
