package rpc

import platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"

func pushWorkerAsRunner(w *pushNotificationWorker) platformrunner.Runner {
	return platformrunner.AsRunner(w)
}
