package orchestration

import (
	"context"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/nodeexec"
)

type noopNodeSpawnRecorder struct{}

func (noopNodeSpawnRecorder) RecordNodeSpawn(context.Context, string, string, int64, string) error {
	return nil
}

func newTestAgentExecutor(launcher nodeexec.AgentLauncher, opts ...nodeexec.Option) *nodeexec.AgentExecutor {
	if stub, ok := launcher.(*stubAgentLauncher); ok && stub.threadID == "" {
		stub.threadID = "thread-test"
	}
	all := append([]nodeexec.Option{nodeexec.WithRecorder(noopNodeSpawnRecorder{})}, opts...)
	return nodeexec.NewAgentExecutor(launcher, all...)
}
