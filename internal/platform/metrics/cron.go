package metrics

import (
	"errors"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/cronmetrics"
	"github.com/prometheus/client_golang/prometheus"
)

// CronRecoveryCollector 以独立 registry 暴露单个 cron metrics owner 的恢复计数器。
type CronRecoveryCollector struct {
	registry *prometheus.Registry
}

// NewCronRecoveryCollector 为指定的 Cron 恢复指标 owner 构造显式采集器。
func NewCronRecoveryCollector(source *cronmetrics.Metrics) (*CronRecoveryCollector, error) {
	if source == nil {
		return nil, errors.New("cron recovery metrics source is required")
	}
	registry := prometheus.NewRegistry()
	collectors := []prometheus.Collector{
		prometheus.NewCounterFunc(
			prometheus.CounterOpts{
				Name: "cron_recovery_finalize_conflict_total",
				Help: "Number of cron recovery finalization conflicts detected by fenced writes.",
			},
			func() float64 { return float64(source.Read().RecoveryFinalizeConflictTotal) },
		),
		prometheus.NewCounterFunc(
			prometheus.CounterOpts{
				Name: "cron_recovery_finalize_error_total",
				Help: "Number of cron recovery finalization errors after conflict reconciliation.",
			},
			func() float64 { return float64(source.Read().RecoveryFinalizeErrorTotal) },
		),
	}
	for _, collector := range collectors {
		if err := registry.Register(collector); err != nil {
			return nil, fmt.Errorf("register cron recovery collector: %w", err)
		}
	}
	return &CronRecoveryCollector{registry: registry}, nil
}

// Gatherer 返回仅包含该 Cron 恢复 owner 指标的显式 gatherer。
func (c *CronRecoveryCollector) Gatherer() prometheus.Gatherer { return c.registry }
