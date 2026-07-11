package mcpcontrol

import platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"

// configFanoutWorkerAsRunner 把配置广播 worker 适配进 root run.Group runners 聚合。
func configFanoutWorkerAsRunner(w *configFanoutWorker) platformrunner.Runner {
	return platformrunner.AsRunner(w)
}
