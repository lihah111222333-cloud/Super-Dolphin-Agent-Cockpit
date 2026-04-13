package cachekeepalive

import (
	"strings"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

func startKeepaliveRelay(dispatcher *event.Dispatcher, manager *Manager, logger *pkglogger.Logger) func() {
	if dispatcher == nil || manager == nil {
		return func() {}
	}
	if logger == nil {
		logger = pkglogger.Get()
	}

	launchCancel := platformbus.ResilientSubscribe(dispatcher, func(ev agentdto.AgentLaunched) {
		manager.HandleAgentLaunched(ev)
	}, logger)
	stateCancel := platformbus.ResilientSubscribe(dispatcher, func(ev agentdto.StateChanged) {
		if strings.EqualFold(strings.TrimSpace(ev.NewState), "idle") {
			manager.ResetTimerByAgent(ev.AgentID)
		}
	}, logger)
	turnCancel := platformbus.ResilientSubscribe(dispatcher, func(ev turndto.TurnCompleted) {
		manager.ResetTimerByAgent(ev.AgentID)
	}, logger)
	stopCancel := platformbus.ResilientSubscribe(dispatcher, func(ev threaddto.Stopped) {
		manager.StopTimerByAgent(ev.AgentID)
	}, logger)

	return func() {
		launchCancel()
		stateCancel()
		turnCancel()
		stopCancel()
	}
}
