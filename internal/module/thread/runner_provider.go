package thread

import (
	"context"

	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
)

func threadBusWorkersAsRunner(svc *service) platformrunner.Runner {
	return platformrunner.AsRunner(&threadBusWorkerRunner{svc: svc})
}

type threadBusWorkerRunner struct {
	svc *service
}

func (r *threadBusWorkerRunner) Start() {
	if r == nil || r.svc == nil {
		return
	}
	r.svc.startBusWorkers()
}

func (r *threadBusWorkerRunner) Stop(ctx context.Context) error {
	if r == nil || r.svc == nil {
		return nil
	}
	r.svc.stopBusWorkers(ctx)
	return nil
}
