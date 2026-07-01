package metrics

import (
	"net/http"
	"os"
	"strings"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	PrometheusMetricsPath = "/metrics"
	EnableMetricsEnv      = "SUPER_DOLPHIN_ENABLE_METRICS"
)

// EnabledFromEnv 只接受显式开关值 1，避免本地 HTTP surface 默认暴露 Prometheus 指标。
func EnabledFromEnv() bool {
	return strings.TrimSpace(os.Getenv(EnableMetricsEnv)) == "1"
}

// Handler 返回进程级 Prometheus handler；本包指标通过 promauto 注册到默认 gatherer。
func Handler() http.Handler {
	return promhttp.Handler()
}

// RegisterHTTPHandlers 在显式开启时挂载 /metrics，避免各 HTTP surface 默认暴露 promhttp。
func RegisterHTTPHandlers(mux *http.ServeMux) {
	if !EnabledFromEnv() {
		return
	}
	mux.Handle(PrometheusMetricsPath, Handler())
}
