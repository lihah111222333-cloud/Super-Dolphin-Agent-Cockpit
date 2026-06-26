package rpc

import platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"

// pushWorkerAsRunner 把 push worker 适配为平台 Runner，交给根 run group 托管。
func pushWorkerAsRunner(w *pushNotificationWorker) platformrunner.Runner {
	return platformrunner.AsRunner(w)
}
