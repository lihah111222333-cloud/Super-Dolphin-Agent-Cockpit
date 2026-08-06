package archtest

import (
	"os"
	"strings"
	"testing"
)

// TestObservabilityMetricAnchorsWired 固定三类 bootstrap 指标的声明和发射点。
// 这些计数器会跨 metrics 包和 bootstrap 注入点协作，测试用源码锚点防止重构时漏接标签维度。
//
// 这里选择源码形状检查，原因与 log-event guard 一致：重连、drain、heartbeat 循环很难低成本触发。
// 锚定生产文件中的字面量可以冻结接线位置，同时避免引入额外运行时依赖。
func TestObservabilityMetricAnchorsWired(t *testing.T) {
	// metrics 包必须保留三类计数器及其标签维度。
	metricsPath := "../../internal/platform/metrics/metrics.go"
	data, err := os.ReadFile(metricsPath)
	if err != nil {
		t.Fatalf("read %s: %v", metricsPath, err)
	}
	metricsSrc := string(data)
	required := []string{
		"\"bootstrap_heartbeat_failures_total\"",
		"\"bootstrap_report_queue_dropped_total\"",
		"\"bootstrap_reconnect_attempts_total\"",
		"\"binary_name\"",
		"\"client_kind\"",
		"\"outcome\"",
	}
	for _, tok := range required {
		if !strings.Contains(metricsSrc, tok) {
			t.Errorf("%s: expected metric declaration token %s (P22 P4 S6b / plan §322)", metricsPath, tok)
		}
	}

	// 每个生产者必须继续通过显式 owner 发射对应指标。
	producers := []struct {
		path   string
		tokens []string
	}{
		{
			path: "../../internal/mcpserver/common/bootstrap/heartbeat.go",
			tokens: []string{
				"c.metrics.IncHeartbeatFailure",
				"c.cfg.BinaryName",
				"c.cfg.ClientKind",
			},
		},
		{
			path: "../../internal/mcpserver/common/bootstrap/report_queue.go",
			tokens: []string{
				"c.metrics.IncReportQueueDropped",
			},
		},
		{
			path: "../../internal/mcpserver/common/bootstrap/reconnect.go",
			tokens: []string{
				"c.metrics.IncReconnectAttempt",
				"\"success\"",
				"\"fail\"",
			},
		},
	}
	for _, spec := range producers {
		raw, err := os.ReadFile(spec.path)
		if err != nil {
			t.Fatalf("read %s: %v", spec.path, err)
		}
		body := string(raw)
		for _, tok := range spec.tokens {
			if !strings.Contains(body, tok) {
				t.Errorf("%s: expected metric emit token %s to stay wired (P22 P4 S6b / plan §322)", spec.path, tok)
			}
		}
	}
}
