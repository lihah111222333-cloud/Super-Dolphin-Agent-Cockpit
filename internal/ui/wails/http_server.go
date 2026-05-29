package wails

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/metrics"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

const defaultHTTPAddr = "127.0.0.1:4511"
const httpAddrEnv = "SUPER_DOLPHIN_HTTP_ADDR"

type httpAssetServer struct {
	logger  *slog.Logger
	addr    string
	handler http.Handler
	server  *rpc.Server
}

func registerHTTPAssetRoutes(mux *http.ServeMux, server *rpc.Server, assetHandler http.Handler) {
	metrics.RegisterHTTPHandlers(mux)
	mux.Handle("/wails/ws", rpc.WSHandler(server, nil))
	mux.Handle("/", assetHandler)
}

// NewHTTPAssetServer creates a Runner that serves the embedded frontend
// assets and a WebSocket-based JRPC bridge on an HTTP port so that
// the application is accessible from a regular web browser.
func NewHTTPAssetServer(p httpAssetServerParams) httpAssetRunnerResult {
	handler := withClipboardAssets(AssetHandlerFrom(p.Frontend))
	return httpAssetRunnerResult{
		Runner: &httpAssetServer{
			logger:  p.Logger,
			addr:    resolveHTTPAssetAddr(),
			handler: handler,
			server:  p.Server,
		},
	}
}

func resolveHTTPAssetAddr() string {
	if value := strings.TrimSpace(os.Getenv(httpAddrEnv)); value != "" {
		return value
	}
	return defaultHTTPAddr
}

func (s *httpAssetServer) Run(ctx context.Context) error {
	if err := validateHTTPAssetAddr(s.addr); err != nil {
		return err
	}

	mux := http.NewServeMux()
	registerHTTPAssetRoutes(mux, s.server, s.handler)

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
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				errCh <- fmt.Errorf("http asset server panic: %v", rec)
			}
		}()
		errCh <- srv.Serve(listener)
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutCtx, cancel := platformconfig.WithTimeout(context.Background(), platformconfig.ShutdownTimeout)
		defer cancel()
		return srv.Shutdown(shutCtx)
	}
}

func validateHTTPAssetAddr(addr string) error {
	addr = strings.TrimSpace(addr)
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("http asset server addr must be loopback: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "127.0.0.1", "::1":
		return nil
	default:
		return fmt.Errorf("http asset server addr must be loopback, got %q", addr)
	}
}
