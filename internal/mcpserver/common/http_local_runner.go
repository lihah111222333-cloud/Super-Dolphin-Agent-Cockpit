package common

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

// ServeLocal 是独立服务的阻塞生命周期入口；所有工作归调用方 RunGroup 管理。
// listener 在调用前已绑定；取消后关闭会话、等待 HTTP 请求和所有内部 goroutine 退出。
func (h *HTTPServer) ServeLocal(ctx context.Context, listener net.Listener) error {
	if h.localProfile == nil || listener == nil {
		return errors.New("local MCP runner requires an explicit profile and listener")
	}
	server := &http.Server{
		Handler: http.HandlerFunc(h.handleMCP), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16 * 1024,
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers sync.WaitGroup
	finished := make(chan error, 1)
	workers.Go(func() { finished <- server.Serve(listener) })
	workers.Go(func() { h.reapLocalSessions(runCtx) })
	var serveErr error
	returned := false
	select {
	case serveErr = <-finished:
		returned = true
	case <-ctx.Done():
	}
	cancel()
	h.terminateAllSessions()
	stopCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	shutdownErr := server.Shutdown(stopCtx)
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, server.Close())
	}
	workers.Wait()
	if !returned {
		serveErr = <-finished
	}
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(serveErr, shutdownErr)
}

// reapLocalSessions 使用 runner 根 context 回收空闲连接，不启动 detached reaper。
func (h *HTTPServer) reapLocalSessions(ctx context.Context) {
	ticker := time.NewTicker(h.sessionReapInterval)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			h.reapIdleSessions(now)
		case <-ctx.Done():
			return
		}
	}
}
