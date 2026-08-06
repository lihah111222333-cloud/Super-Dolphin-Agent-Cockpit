package wails

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/metrics"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

// defaultHTTPAddr 是桌面 HTTP asset server 的默认 loopback 监听地址。
const defaultHTTPAddr = "127.0.0.1:4511"

// httpAddrEnv 允许本地开发覆盖 HTTP asset server 地址。
const httpAddrEnv = "SUPER_DOLPHIN_HTTP_ADDR"

// wailsWebSocketCookieName 保存 WebSocket 防跨站 token。
const wailsWebSocketCookieName = "super_dolphin_wails_ws"

// wailsWebSocketTokenEnv 是 WebSocket token 的显式环境变量名。
const wailsWebSocketTokenEnv = "SUPER_DOLPHIN_WAILS_WS_TOKEN"

// httpAssetServer 负责提供前端静态资源和 Wails RPC WebSocket。
type httpAssetServer struct {
	logger      *slog.Logger
	addr        string
	handler     http.Handler
	metrics     http.Handler
	server      *rpc.Server
	wsToken     string
	previewAddr *sharedFilePreviewHTTPAddr
	startupErr  error
}

// registerHTTPAssetRoutes 注册 metrics、WebSocket 和静态资源路由。
// 非静态入口必须通过 RoutePolicy 声明守卫，防止新增 route 绕过本地来源检查。
func registerHTTPAssetRoutes(mux *http.ServeMux, server *rpc.Server, assetHandler, metricsHandler http.Handler, wsToken string) error {
	if metrics.EnabledFromEnv() {
		if metricsHandler == nil {
			return errors.New("wails metrics handler is required")
		}
		if err := registerWailsHTTPRoute(mux, metrics.PrometheusMetricsPath, RoutePolicyMetricsGuarded, wailsMetricsRequestGuard(metricsHandler, wsToken)); err != nil {
			return err
		}
	}
	if err := registerWailsHTTPRoute(mux, "/wails/ws", RoutePolicyWailsRPC, wailsWebSocketRequestGuard(rpc.WSHandler(server, nil), wsToken)); err != nil {
		return err
	}
	return registerWailsHTTPRoute(mux, "/", RoutePolicyLocalAssetToken, wailsAssetCookieHandler(assetHandler, wsToken))
}

// NewHTTPAssetServer 创建同时服务前端资源和 JRPC WebSocket 的 runner。
// WebSocket token 在 runner 构造时确定，后续 route guard 和 asset cookie 共用同一值。
func NewHTTPAssetServer(p httpAssetServerParams) httpAssetRunnerResult {
	handler, startupErr := assetHandlerFromForMode(p.Frontend, isDebug(p.Config))
	localAssets, localErr := p.Binding.localImageAssets()
	sharedAssets, previewAddr, sharedErr := p.Binding.sharedFilePreviewAssets()
	startupErr = errors.Join(startupErr, localErr, sharedErr)
	if handler != nil {
		if localErr == nil && sharedErr == nil {
			handler = withSharedFilePreviewAssetsRegistry(withClipboardAssetsRegistry(handler, localAssets), sharedAssets)
		}
	}
	cronCollector, err := metrics.NewCronRecoveryCollector(p.Metrics)
	if err != nil {
		startupErr = errors.Join(startupErr, err)
	}
	dreamCollector, err := metrics.NewDreamCollector(metrics.DreamRegistry())
	if err != nil {
		startupErr = errors.Join(startupErr, err)
	}
	skillCollector, err := metrics.NewSkillCollector(p.SkillMetrics)
	if err != nil {
		startupErr = errors.Join(startupErr, err)
	}
	var metricsHandler http.Handler
	if cronCollector != nil && dreamCollector != nil && skillCollector != nil {
		metricsHandler, err = metrics.NewHandler(metrics.Collectors{
			Cron:      cronCollector,
			DAG:       p.DAGMetrics,
			Dream:     dreamCollector,
			Bootstrap: p.BootstrapMetrics,
			Skill:     skillCollector,
		})
		if err != nil {
			startupErr = errors.Join(startupErr, err)
		}
	}
	return httpAssetRunnerResult{
		Runner: &httpAssetServer{
			logger:      p.Logger,
			addr:        resolveHTTPAssetAddr(),
			handler:     handler,
			metrics:     metricsHandler,
			server:      p.Server,
			wsToken:     resolveWailsWebSocketToken(),
			previewAddr: previewAddr,
			startupErr:  startupErr,
		},
	}
}

// resolveHTTPAssetAddr 解析 HTTP asset server 监听地址。
// 环境变量只决定绑定地址，Run 阶段仍会校验必须是 loopback。
func resolveHTTPAssetAddr() string {
	if value := strings.TrimSpace(os.Getenv(httpAddrEnv)); value != "" {
		return value
	}
	return defaultHTTPAddr
}

// resolveWailsWebSocketToken 解析 WebSocket token，未配置时生成一次性随机值。
func resolveWailsWebSocketToken() string {
	for _, key := range []string{wailsWebSocketTokenEnv, "GO_AGENT_CTL_SESSION_TOKEN", "GO_AGENT_MCP_SESSION_TOKEN"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return platformshared.NewID("wails_ws")
}

// Run 启动 HTTP asset server，并在 context 取消时按 shutdown 超时优雅停止。
func (s *httpAssetServer) Run(ctx context.Context) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	// 误判防护：validateHTTPAssetAddr 是 Go HTTP asset server 直连绑定的 loopback 守卫。
	if err := validateHTTPAssetAddr(s.addr); err != nil {
		return err
	}

	mux := http.NewServeMux()
	if err := registerHTTPAssetRoutes(mux, s.server, s.handler, s.metrics, s.wsToken); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:         s.addr,
		Handler:      withHTTPLogging(s.logger, mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	defer listener.Close()

	if s.previewAddr == nil {
		return errors.New("shared file preview HTTP address state is required")
	}
	s.previewAddr.set(listener.Addr().String())
	s.logger.Info("http asset server listening", "addr", listener.Addr().String())

	errCh := make(chan error, 1)
	safego.Go(ctx, nil, "wails.httpAssetServer.serve", func(context.Context) {
		errCh <- serveHTTPAssetServer(srv, listener)
	})

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

func serveHTTPAssetServer(srv *http.Server, listener net.Listener) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("http asset server panic: %v", rec)
		}
	}()
	return srv.Serve(listener)
}

// validateHTTPAssetAddr 确认 HTTP asset server 只监听 loopback 地址。
func validateHTTPAssetAddr(addr string) error {
	// 守卫规则：validateHTTPAssetAddr 只覆盖 Go HTTP asset server，不覆盖 Vite proxy 暴露路径。
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

// wailsWebSocketRequestGuard 在升级到 UI RPC WebSocket 前校验浏览器入口来源。
// 绑定地址只能限制监听面，Host/Origin 还要单独拦截 DNS rebinding 或跨站发起的本地请求。
func wailsWebSocketRequestGuard(next http.Handler, wsToken string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := validateWailsWebSocketRequest(r); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		if err := validateWailsWebSocketToken(r, wsToken); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// wailsMetricsRequestGuard 让 metrics route 复用 Wails RPC 的本地来源和 token 校验。
func wailsMetricsRequestGuard(next http.Handler, wsToken string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := validateWailsWebSocketRequest(r); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		if err := validateWailsWebSocketToken(r, wsToken); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// wailsAssetCookieHandler 给静态资源响应写入 WebSocket token cookie。
func wailsAssetCookieHandler(next http.Handler, wsToken string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token := strings.TrimSpace(wsToken); token != "" {
			http.SetCookie(w, &http.Cookie{
				Name:     wailsWebSocketCookieName,
				Value:    token,
				Path:     "/wails/ws",
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
			})
		}
		next.ServeHTTP(w, r)
	})
}

// validateWailsWebSocketRequest 校验 WebSocket 请求的 Host 和 Origin 都来自同一 loopback 来源。
func validateWailsWebSocketRequest(r *http.Request) error {
	if r == nil {
		return errors.New("wails websocket request must be present")
	}
	if !isLoopbackHTTPAuthority(r.Host) {
		return fmt.Errorf("wails websocket host must be loopback, got %q", r.Host)
	}
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" && !isLoopbackOrigin(origin) {
		return fmt.Errorf("wails websocket origin must be loopback, got %q", origin)
	}
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" && !isSameHTTPOrigin(r.Host, origin) {
		return fmt.Errorf("wails websocket origin must be same origin as host %q, got %q", r.Host, origin)
	}
	return nil
}

// validateWailsWebSocketToken 校验浏览器携带的 WebSocket token cookie。
func validateWailsWebSocketToken(r *http.Request, expectedToken string) error {
	expectedToken = strings.TrimSpace(expectedToken)
	if expectedToken == "" {
		return nil
	}
	cookie, err := r.Cookie(wailsWebSocketCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return errors.New("wails websocket token is required")
	}
	got := strings.TrimSpace(cookie.Value)
	if subtle.ConstantTimeCompare([]byte(got), []byte(expectedToken)) != 1 {
		return errors.New("wails websocket token is invalid")
	}
	return nil
}

// isLoopbackOrigin 判断 Origin 是否是本机 HTTP(S) 来源。
// 解析失败、空 Host 或非 HTTP(S) scheme 都按不可信处理。
func isLoopbackOrigin(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil || parsed.Host == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(parsed.Scheme)) {
	case "http", "https":
		return isLoopbackHost(parsed.Hostname())
	default:
		return false
	}
}

// isSameHTTPOrigin 比较 Host 与 Origin 的 authority，避免 localhost/127.0.0.1 互相冒充。
func isSameHTTPOrigin(host, origin string) bool {
	parsed, err := url.Parse(strings.TrimSpace(origin))
	if err != nil || parsed == nil || parsed.Host == "" {
		return false
	}
	return canonicalHTTPAuthority(host) == canonicalHTTPAuthority(parsed.Host)
}

// isLoopbackHTTPAuthority 判断 Host 或 host:port 是否指向本机。
func isLoopbackHTTPAuthority(raw string) bool {
	host := strings.TrimSpace(raw)
	if host == "" {
		return false
	}
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	}
	return isLoopbackHost(host)
}

func canonicalHTTPAuthority(raw string) string {
	host := strings.ToLower(strings.TrimSpace(raw))
	if splitHost, splitPort, err := net.SplitHostPort(host); err == nil {
		return strings.Trim(strings.ToLower(splitHost), "[]") + ":" + splitPort
	}
	return strings.Trim(host, "[]")
}

// isLoopbackHost 判断 host 字面量是否为允许的 loopback 名称。
func isLoopbackHost(host string) bool {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]")) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}
