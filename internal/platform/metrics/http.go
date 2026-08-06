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

// Collectors declares every explicit metrics owner exposed by one HTTP surface.
type Collectors struct {
	Cron      *CronRecoveryCollector
	DAG       *DAGCollector
	Dream     *DreamCollector
	Bootstrap *BootstrapMetrics
	Skill     *SkillCollector
}

// EnabledFromEnv only accepts the explicit opt-in value 1.
func EnabledFromEnv() bool { return strings.TrimSpace(os.Getenv(EnableMetricsEnv)) == "1" }

// NewHandler composes the five explicit owner gatherers without DefaultGatherer.
func NewHandler(collectors Collectors) (http.Handler, error) {
	if collectors.Cron == nil || collectors.DAG == nil || collectors.Dream == nil || collectors.Bootstrap == nil || collectors.Skill == nil {
		return nil, errors.New("metrics: cron, DAG, dream, bootstrap, and skill collectors are required")
	}
	return promhttp.HandlerFor(prometheus.Gatherers{
		collectors.Cron.Gatherer(),
		collectors.DAG.Gatherer(),
		collectors.Dream.Gatherer(),
		collectors.Bootstrap.Gatherer(),
		collectors.Skill.Gatherer(),
	}, promhttp.HandlerOpts{}), nil
}

// RegisterHTTPHandlers mounts an explicit metrics handler only when enabled.
func RegisterHTTPHandlers(mux *http.ServeMux, handler http.Handler) error {
	if !EnabledFromEnv() {
		return nil
	}
	if handler == nil {
		return errors.New("metrics handler is required")
	}
	mux.Handle(PrometheusMetricsPath, handler)
	return nil
}
