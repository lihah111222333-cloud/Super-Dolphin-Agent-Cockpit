package metrics

import (
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/dreammetrics"
	"github.com/prometheus/client_golang/prometheus"
)

// DreamCollector exports one explicit Dream metrics registry.
type DreamCollector struct {
	registry *prometheus.Registry
}

// NewDreamCollector creates an isolated Prometheus exporter for source.
func NewDreamCollector(source *dreammetrics.Registry) (*DreamCollector, error) {
	if source == nil {
		return nil, fmt.Errorf("metrics: dream registry is required")
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "dream_success_total", Help: "Number of successful dream distillations."}, func() float64 { return float64(source.Read().SuccessTotal) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "dream_provider_skipped_total", Help: "Number of dream providers skipped because they were not configured."}, func() float64 { return float64(source.Read().ProviderSkippedTotal) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "dream_provider_failed_total", Help: "Number of dream providers that returned an error."}, func() float64 { return float64(source.Read().ProviderFailedTotal) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "dream_all_not_configured_total", Help: "Number of dream attempts with no configured provider."}, func() float64 { return float64(source.Read().AllNotConfiguredTotal) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "dream_prompt_oversize_total", Help: "Number of dream prompts rejected for exceeding the size limit."}, func() float64 { return float64(source.Read().PromptOversizeTotal) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "dream_tokens_input_total", Help: "Total input tokens used by dream providers."}, func() float64 { return float64(source.Read().TokensInputTotal) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "dream_tokens_output_total", Help: "Total output tokens used by dream providers."}, func() float64 { return float64(source.Read().TokensOutputTotal) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "dream_tokens_cache_read_total", Help: "Total cache-read tokens used by dream providers."}, func() float64 { return float64(source.Read().TokensCacheReadTotal) }),
	)
	return &DreamCollector{registry: registry}, nil
}

// Gatherer returns only the Dream registry's Prometheus series.
func (c *DreamCollector) Gatherer() prometheus.Gatherer { return c.registry }
