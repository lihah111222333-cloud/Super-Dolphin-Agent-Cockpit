package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const PrometheusMetricsPath = "/metrics"

// Handler 返回进程级 Prometheus handler；本包指标通过 promauto 注册到默认 gatherer。
func Handler() http.Handler {
	return promhttp.Handler()
}

// RegisterHTTPHandlers 在 mux 上挂载 /metrics，避免各 HTTP surface 重复硬编码 promhttp。
func RegisterHTTPHandlers(mux *http.ServeMux) {
	mux.Handle(PrometheusMetricsPath, Handler())
}
