package hooks

import platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"

// hookWorkerAsRunner 将 hooks dispatch worker 交给统一 runner 生命周期托管。
func hookWorkerAsRunner(w *hookDispatchWorker) platformrunner.Runner {
	return platformrunner.AsRunner(w)
}
