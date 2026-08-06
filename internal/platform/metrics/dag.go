package metrics

import (
	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/dreammetrics"
)

var dreamRegistry = dreammetrics.NewRegistry()

// DreamRegistry 返回由 metrics 包拥有的 Dream 进程级指标状态。
func DreamRegistry() *dreammetrics.Registry {
	return dreamRegistry
}
