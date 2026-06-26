package hooks

import platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"

// hookWorkerAsRunner 将 hooks dispatch worker 交给统一 runner 生命周期托管。
func hookWorkerAsRunner(w *hookDispatchWorker) platformrunner.Runner {
	return platformrunner.AsRunner(w)
}
