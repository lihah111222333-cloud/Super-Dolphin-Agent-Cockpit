package mcpcontrol

import platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"

func configFanoutWorkerAsRunner(w *configFanoutWorker) platformrunner.Runner {
	return platformrunner.AsRunner(w)
}
