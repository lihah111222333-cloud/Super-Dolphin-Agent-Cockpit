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

// startKeepaliveRelay 启动keepaliverelay。
func startKeepaliveRelay(dispatcher *event.Dispatcher, manager *Manager, logger *pkglogger.Logger) func() {
	if dispatcher == nil || manager == nil {
		return func() {}
	}
	if logger == nil {
		logger = pkglogger.Get()
	}

	launchCancel := platformbus.ResilientSubscribe(dispatcher, func(ev agentdto.AgentLaunched) {
		logger.Debug("cachekeepalive: agent launched event", "agent_id", ev.AgentID, "session_id", ev.SessionID, "thread_id", ev.ThreadID)
		manager.HandleAgentLaunched(ev)
	}, logger)
	stateCancel := platformbus.ResilientSubscribe(dispatcher, func(ev agentdto.StateChanged) {
		if strings.EqualFold(strings.TrimSpace(ev.NewState), "idle") {
			logger.Debug("cachekeepalive: agent idle, resetting timer", "agent_id", ev.AgentID)
			manager.ResetTimerByAgent(ev.AgentID)
		}
	}, logger)
	turnCancel := platformbus.ResilientSubscribe(dispatcher, func(ev turndto.TurnCompleted) {
		logger.Debug("cachekeepalive: turn completed, resetting timer", "agent_id", ev.AgentID)
		manager.ResetTimerByAgent(ev.AgentID)
	}, logger)
	stopCancel := platformbus.ResilientSubscribe(dispatcher, func(ev threaddto.Stopped) {
		logger.Debug("cachekeepalive: agent stopped, clearing timer", "agent_id", ev.AgentID)
		manager.StopTimerByAgent(ev.AgentID)
	}, logger)
	agentStopCancel := platformbus.ResilientSubscribe(dispatcher, func(ev agentdto.AgentStopped) {
		logger.Debug("cachekeepalive: agent stopped, clearing timer", "agent_id", ev.AgentID)
		manager.StopTimerByAgent(ev.AgentID)
	}, logger)

	return func() {
		launchCancel()
		stateCancel()
		turnCancel()
		stopCancel()
		agentStopCancel()
	}
}
