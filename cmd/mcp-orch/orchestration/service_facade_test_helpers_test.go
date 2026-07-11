package orchestration

import (
	"log/slog"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
)

// newTestFacadeServiceWithRegistry builds a full facade around a hand-made registry.
// It keeps direct unit tests focused on the owner they exercise without bypassing
// the service-owned controller fields introduced by the facade split.
func newTestFacadeServiceWithRegistry(registry *agentRegistry) *service {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	if registry == nil {
		registry = newAgentRegistry()
	}
	svc.registry = registry
	if svc.turns != nil {
		svc.turns.registry = registry
	}
	if svc.reports != nil {
		svc.reports.registry = registry
	}
	return svc
}

func newTestFacadeServiceWithAgents(agents ...*agentRuntime) *service {
	registry := newAgentRegistry()
	for _, agent := range agents {
		if agent != nil {
			registry.agents[agent.id] = agent
		}
	}
	return newTestFacadeServiceWithRegistry(registry)
}

func setTestAgentThreads(svc *service, threads AgentThreadStore) {
	if svc == nil {
		return
	}
	if svc.lifecycle != nil {
		svc.lifecycle.agentThreads = threads
	}
	if svc.reports != nil {
		svc.reports.agentThreads = threads
	}
}

func newHookConsumer(svc *service, logger *slog.Logger) *hookConsumer {
	return newHookConsumerInternal(svc, logger, nil, nil, nil)
}

func newHookConsumerInternal(
	svc *service,
	logger *slog.Logger,
	tap NotifyTap,
	fallbackLookup taskdag.NodeSpawningThreadLookup,
	fallbackFlow taskdag.NodeFlowStore,
	opts ...hookConsumerOption,
) *hookConsumer {
	var reports HookReportPort
	var suppression HookSuppressionLookup
	var eventBus EventBus
	if svc != nil {
		reports = svc
		suppression = svc.registry
		eventBus = svc.eventBus
	}
	return newHookConsumerWithPorts(svc, reports, suppression, eventBus, logger, tap, fallbackLookup, fallbackFlow, opts...)
}

func newRunnerActorForTest(logger *slog.Logger, svc *service) platformrunner.Runner {
	return NewRunnerActor(RunnerActorParams{Logger: logger, Lifecycle: svc, Runtime: svc})
}
