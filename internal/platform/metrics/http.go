package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const PrometheusMetricsPath = "/metrics"

// Handler returns the process-wide Prometheus handler backed by the default
// gatherer. The counters in this package register themselves via promauto, so
// mounting this handler is enough to expose skill/toolbridge rollout metrics.
// Handler 处理处理器。
func Handler() http.Handler {
	return promhttp.Handler()
}

// RegisterHTTPHandlers mounts metrics routes on mux. Keeping the route in this
// package avoids each HTTP surface hard-coding the path or promhttp dependency.
// RegisterHTTPHandlers 注册HTTP处理器。
func RegisterHTTPHandlers(mux *http.ServeMux) {
	mux.Handle(PrometheusMetricsPath, Handler())
}
