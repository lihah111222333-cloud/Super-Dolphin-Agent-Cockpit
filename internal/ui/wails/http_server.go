package wails

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"go.uber.org/fx"

	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

const defaultHTTPAddr = "127.0.0.1:4511"

// httpAssetRunnerResult mirrors app.RunnerResult to avoid an import cycle.
type httpAssetRunnerResult struct {
	fx.Out
	Runner platformrunner.Runner `group:"runners"`
}

type httpAssetServerParams struct {
	fx.In

	Logger   *slog.Logger
	Frontend FrontendFS     `optional:"true"`
	Config   *config.Config
	Server   *rpc.Server
}

type httpAssetServer struct {
	logger  *slog.Logger
	addr    string
	handler http.Handler
	server  *rpc.Server
}

// NewHTTPAssetServer creates a Runner that serves the embedded frontend
// assets and a WebSocket-based JRPC bridge on an HTTP port so that
// the application is accessible from a regular web browser.
func NewHTTPAssetServer(p httpAssetServerParams) httpAssetRunnerResult {
	handler := AssetHandlerFrom(p.Frontend)
	return httpAssetRunnerResult{
		Runner: &httpAssetServer{
			logger:  p.Logger,
			addr:    defaultHTTPAddr,
			handler: handler,
			server:  p.Server,
		},
	}
}

func (s *httpAssetServer) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.Handle("/wails/ws", rpc.WSHandler(s.server, nil))
	mux.Handle("/", s.handler)

	srv := &http.Server{
		Addr:         s.addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	defer listener.Close()

	s.logger.Info("http asset server listening", "addr", listener.Addr().String())

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(listener) }()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	}
}
