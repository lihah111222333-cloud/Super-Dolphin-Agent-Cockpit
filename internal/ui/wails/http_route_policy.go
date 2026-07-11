package wails

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/metrics"
)

// RoutePolicy 声明 Wails HTTP route 的暴露边界，新增非静态 route 必须先选定策略。
type RoutePolicy string

const (
	RoutePolicyPublicStatic    RoutePolicy = "public_static"
	RoutePolicyWailsRPC        RoutePolicy = "wails_rpc"
	RoutePolicyMetricsGuarded  RoutePolicy = "metrics_guarded"
	RoutePolicyLocalAssetToken RoutePolicy = "local_asset_token"
)

// registerWailsHTTPRoute 在注册路由前校验策略，避免新入口绕过本地来源或 token 守卫。
func registerWailsHTTPRoute(mux *http.ServeMux, pattern string, policy RoutePolicy, handler http.Handler) error {
	if mux == nil {
		return fmt.Errorf("wails HTTP route mux is nil")
	}
	if handler == nil {
		return fmt.Errorf("wails HTTP route handler is nil")
	}
	if err := validateWailsHTTPRoutePolicy(pattern, policy); err != nil {
		return err
	}
	mux.Handle(pattern, handler)
	return nil
}

// validateWailsHTTPRoutePolicy 校验 route 与策略是否匹配；新增敏感入口时必须在这里显式分类。
func validateWailsHTTPRoutePolicy(pattern string, policy RoutePolicy) error {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return fmt.Errorf("wails HTTP route pattern is required")
	}
	if !isKnownWailsHTTPRoutePolicy(policy) {
		return fmt.Errorf("wails HTTP route %q declares unknown policy %q", pattern, policy)
	}
	switch pattern {
	case metrics.PrometheusMetricsPath:
		if policy != RoutePolicyMetricsGuarded {
			return fmt.Errorf("wails HTTP route %q must use %q policy", pattern, RoutePolicyMetricsGuarded)
		}
	case "/wails/ws":
		if policy != RoutePolicyWailsRPC {
			return fmt.Errorf("wails HTTP route %q must use %q policy", pattern, RoutePolicyWailsRPC)
		}
	case "/":
		if policy != RoutePolicyPublicStatic && policy != RoutePolicyLocalAssetToken {
			return fmt.Errorf("wails HTTP route %q must use static or local asset token policy", pattern)
		}
	}
	return nil
}

func isKnownWailsHTTPRoutePolicy(policy RoutePolicy) bool {
	switch policy {
	case RoutePolicyPublicStatic, RoutePolicyWailsRPC, RoutePolicyMetricsGuarded, RoutePolicyLocalAssetToken:
		return true
	default:
		return false
	}
}
