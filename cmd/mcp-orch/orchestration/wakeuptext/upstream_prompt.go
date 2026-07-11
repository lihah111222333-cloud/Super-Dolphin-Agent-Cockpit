package wakeuptext

import (
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
)

// RenderUpstreamPromptHint 渲染upstreamprompthint。
func RenderUpstreamPromptHint(refs []taskdag.DownstreamUpstreamRef) string {
	if len(refs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("上游节点已完成，产出文件位于：\n")
	for _, ref := range refs {
		nodeKey := strings.TrimSpace(ref.NodeKey)
		path := strings.TrimSpace(ref.Path)
		if path == "" {
			continue
		}
		if nodeKey != "" {
			fmt.Fprintf(&b, "- %s: %s\n", nodeKey, path)
		} else {
			fmt.Fprintf(&b, "- %s\n", path)
		}
	}
	b.WriteString("\n请用 Read 工具读取以上文件并继续完成本节点任务。")
	return b.String()
}
