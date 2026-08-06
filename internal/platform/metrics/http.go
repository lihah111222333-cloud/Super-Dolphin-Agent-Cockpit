package metrics

import (
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
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

// HandlerWithCronRecovery 将进程既有指标与指定 Cron 恢复 owner 的显式 registry 一起暴露。
func HandlerWithCronRecovery(collector *CronRecoveryCollector) (http.Handler, error) {
	if collector == nil {
		return nil, errors.New("cron recovery collector is required")
	}
	return promhttp.HandlerFor(prometheus.Gatherers{prometheus.DefaultGatherer, collector.Gatherer()}, promhttp.HandlerOpts{}), nil
}

// RegisterHTTPHandlers 在显式开启时挂载 /metrics，避免各 HTTP surface 默认暴露 promhttp。
func RegisterHTTPHandlers(mux *http.ServeMux) {
	if !EnabledFromEnv() {
		return
	}
	mux.Handle(PrometheusMetricsPath, Handler())
}
