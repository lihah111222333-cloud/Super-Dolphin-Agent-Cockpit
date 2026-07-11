package main

import (
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/wakeupreclaim"
	"go.uber.org/fx"
)

// orchestrationDAGOptions 装配 DAG runtime、subscriber 与定时 runner。
func orchestrationDAGOptions() fx.Option {
	return fx.Module("orchestration-dag",
		fx.Provide(
			provideSQLDAGScheduleStore,
			provideSQLiteRuntimeLocker,
			provideAgentThreadLookup,
			orchestration.ProvideWakeupDispatcher,
			fx.Annotate(orchestration.ProvideWakeupDispatcherRunner, fx.ResultTags(`group:"runners"`)),
			fx.Annotate(wakeupreclaim.ProvideWakeupReclaimerRunner, fx.ResultTags(`group:"runners"`)),
			fx.Annotate(provideScheduledDAGCronRunner, fx.ResultTags(`group:"runners"`)),
		),
		fx.Invoke(
			orchestration.RegisterDAGTurnCompletedSubscriber,
			orchestration.WireWakeupDispatcherRouter,
			orchestration.WireWakeupDispatcherRetryAlertSink,
		),
	)
}

func provideAgentThreadLookup(store orchestration.AgentThreadStore) orchestration.AgentThreadLookup {
	return store
}
