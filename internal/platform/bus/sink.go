package bus

import (
	"log/slog"
	"reflect"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	taskdto "github.com/anthropic-ai/super-agent-v3/internal/dto/task"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/kelindar/event"
)

// LogSink subscribes to known bus events and mirrors them to slog.
type LogSink struct {
	subs *Subscription
}

func NewLogSink(dispatcher *event.Dispatcher, logger *slog.Logger) *LogSink {
	sink := &LogSink{subs: NewSubscription()}
	if dispatcher == nil || logger == nil {
		return sink
	}
	sink.bindAgent(dispatcher, logger)
	sink.bindThread(dispatcher, logger)
	sink.bindTurn(dispatcher, logger)
	sink.bindTool(dispatcher, logger)
	sink.bindTask(dispatcher, logger)
	sink.bindUI(dispatcher, logger)
	return sink
}

func (s *LogSink) Close() {
	if s == nil || s.subs == nil {
		return
	}
	s.subs.CancelAll()
	s.subs = nil
}

func (s *LogSink) bindAgent(dispatcher *event.Dispatcher, logger *slog.Logger) {
	s.subs.Add(logEvent[agentdto.StateChanged](dispatcher, logger))
	s.subs.Add(logEvent[agentdto.AgentLaunched](dispatcher, logger))
	s.subs.Add(logEvent[agentdto.AgentStopped](dispatcher, logger))
	s.subs.Add(logEvent[agentdto.AgentRecovering](dispatcher, logger))
	s.subs.Add(logEvent[agentdto.AgentFailed](dispatcher, logger))
	s.subs.Add(logEvent[agentdto.AgentRuntimeReported](dispatcher, logger))
	s.subs.Add(logEvent[agentdto.AgentWarning](dispatcher, logger))
	s.subs.Add(logEvent[agentdto.AgentError](dispatcher, logger))
}

func (s *LogSink) bindThread(dispatcher *event.Dispatcher, logger *slog.Logger) {
	s.subs.Add(logEvent[threaddto.Started](dispatcher, logger))
	s.subs.Add(logEvent[threaddto.Stopped](dispatcher, logger))
	s.subs.Add(logEvent[threaddto.MessagesPage](dispatcher, logger))
}

func (s *LogSink) bindTurn(dispatcher *event.Dispatcher, logger *slog.Logger) {
	s.subs.Add(logEvent[turndto.TurnStarted](dispatcher, logger))
	s.subs.Add(logEvent[turndto.TurnCompleted](dispatcher, logger))
	s.subs.Add(logEvent[turndto.TurnInterrupted](dispatcher, logger))
	s.subs.Add(logEvent[turndto.TurnStalled](dispatcher, logger))
	s.subs.Add(logEvent[turndto.TurnResumed](dispatcher, logger))
	s.subs.Add(logEvent[turndto.TurnInputReceived](dispatcher, logger))
	s.subs.Add(logEvent[turndto.TurnOutputDelta](dispatcher, logger))
	s.subs.Add(logEvent[turndto.PlanDelta](dispatcher, logger))
	s.subs.Add(logEvent[turndto.PlanUpdated](dispatcher, logger))
	s.subs.Add(logEvent[turndto.ItemStarted](dispatcher, logger))
	s.subs.Add(logEvent[turndto.ItemCompleted](dispatcher, logger))
}

func (s *LogSink) bindTool(dispatcher *event.Dispatcher, logger *slog.Logger) {
	s.subs.Add(logEvent[tooldto.ToolCallBegin](dispatcher, logger))
	s.subs.Add(logEvent[tooldto.ToolCallEnd](dispatcher, logger))
	s.subs.Add(logEvent[tooldto.ToolApprovalRequested](dispatcher, logger))
	s.subs.Add(logEvent[tooldto.ToolApprovalResolved](dispatcher, logger))
}

func (s *LogSink) bindTask(dispatcher *event.Dispatcher, logger *slog.Logger) {
	s.subs.Add(logEvent[taskdto.TaskDagCreated](dispatcher, logger))
	s.subs.Add(logEvent[taskdto.TaskNodeStatusChanged](dispatcher, logger))
	s.subs.Add(logEvent[taskdto.TaskWakeupDispatched](dispatcher, logger))
	s.subs.Add(logEvent[taskdto.TaskWakeupCompleted](dispatcher, logger))
}

func (s *LogSink) bindUI(dispatcher *event.Dispatcher, logger *slog.Logger) {
	s.subs.Add(logEvent[uidto.UIProjectionUpdated](dispatcher, logger))
	s.subs.Add(logEvent[uidto.UITimelineAppended](dispatcher, logger))
	s.subs.Add(logEvent[uidto.UITokensUpdated](dispatcher, logger))
}

func logEvent[T event.Event](dispatcher *event.Dispatcher, logger *slog.Logger) func() {
	if dispatcher == nil || logger == nil {
		return func() {}
	}
	return event.Subscribe(dispatcher, func(ev T) {
		logger.Info("bus event",
			slog.String("event_type", eventTypeName(ev)),
			slog.Any("event", ev),
		)
	})
}

func eventTypeName(ev any) string {
	if ev == nil {
		return "<nil>"
	}
	return reflect.TypeOf(ev).String()
}
