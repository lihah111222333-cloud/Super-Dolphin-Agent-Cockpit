package mcpcontrol

import platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"

// configFanoutWorkerAsRunner 把配置广播 worker 适配进 root run.Group runners 聚合。
func configFanoutWorkerAsRunner(w *configFanoutWorker) platformrunner.Runner {
	return platformrunner.AsRunner(w)
}
