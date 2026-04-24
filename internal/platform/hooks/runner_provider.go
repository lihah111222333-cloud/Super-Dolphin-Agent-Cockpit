package hooks

import platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"

func hookWorkerAsRunner(w *hookDispatchWorker) platformrunner.Runner {
	return platformrunner.AsRunner(w)
}
