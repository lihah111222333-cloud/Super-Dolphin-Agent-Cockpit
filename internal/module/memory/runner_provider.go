package memory

import (
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func newAutoDreamSchedulerProvider(p autoDreamSchedulerProviderParams) *autoDreamScheduler {
	return newAutoDreamScheduler(p.Hooks, pkglogger.Get())
}

func newNestedIngestWorkerProvider(p nestedIngestWorkerProviderParams) *nestedIngestWorker {
	return newNestedIngestWorker(p.NestedRuntime, pkglogger.Get())
}

func newTeamSyncCoordinatorProvider(p teamSyncCoordinatorProviderParams) *teamSyncCoordinator {
	return newTeamSyncCoordinator(p.TeamSync, p.ThreadStore, pkglogger.Get())
}

func autoDreamSchedulerAsRunner(s *autoDreamScheduler) platformrunner.Runner {
	return platformrunner.AsRunner(s)
}

func nestedIngestWorkerAsRunner(w *nestedIngestWorker) platformrunner.Runner {
	return platformrunner.AsRunner(w)
}

func teamSyncCoordinatorAsRunner(c *teamSyncCoordinator) platformrunner.Runner {
	return platformrunner.AsRunner(c)
}
