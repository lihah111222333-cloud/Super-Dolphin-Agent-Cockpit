package archtest

import (
	"os"
	"strings"
	"testing"
)

// TestObservabilityLogEventAnchorsWired 固定稳定日志事件锚点与生产 emit 文件的绑定。
// reconnect、drain 和兼容回退事件难以用轻量单测完整触发，因此这里用源码形状守卫
// 确认锚点仍出现在归属文件中，契约移动时会给出清晰 diff。
func TestObservabilityLogEventAnchorsWired(t *testing.T) {
	anchors := []struct {
		producerPath string
		anchors      []string
	}{
		{
			producerPath: "../../internal/mcpserver/common/bootstrap/hooks.go",
			anchors: []string{
				"\"bootstrap.hook_replay.begin\"",
				"\"bootstrap.hook_replay.end\"",
			},
		},
		{
			producerPath: "../../internal/mcpserver/common/bootstrap/report_queue.go",
			anchors: []string{
				"\"bootstrap.report_queue.drain\"",
			},
		},
		{
			producerPath: "../../cmd/mcp-lsp/multilsp/transport_compat.go",
			anchors: []string{
				"\"gopls.compat_fallback.hit\"",
			},
		},
	}

	for _, spec := range anchors {
		data, err := os.ReadFile(spec.producerPath)
		if err != nil {
			t.Fatalf("read %s: %v", spec.producerPath, err)
		}
		text := string(data)
		for _, anchor := range spec.anchors {
			if !strings.Contains(text, anchor) {
				t.Errorf("%s: expected observability anchor %s to stay wired (P22 P4 S6a / plan §321)", spec.producerPath, anchor)
			}
			// producer 文件内必须至少使用一次 "event" key，避免锚点只残留在注释或无关字符串里。
			// 同一文件的 emit site 形状一致，单次 key 检查即可覆盖该文件。
			if !strings.Contains(text, "\"event\",") {
				t.Errorf("%s: expected slog-style \"event\", <anchor> emission shape to stay wired", spec.producerPath)
			}
		}
	}
}
