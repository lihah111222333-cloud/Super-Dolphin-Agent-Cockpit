package bus

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	taskdto "github.com/anthropic-ai/super-agent-v3/internal/dto/task"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

// LogSink subscribes to known bus events and mirrors them to structured logs.
type LogSink struct {
	subs        *Subscription
	trace       *observability.Service
	traceMu     sync.Mutex
	traceCounts map[string]int64
}

type logSinkParams struct {
	fx.In

	Dispatcher *event.Dispatcher
	Logger     *pkglogger.Logger
	Trace      *observability.Service `optional:"true"`
}

func NewLogSink(p logSinkParams) *LogSink {
	sink := &LogSink{subs: NewSubscription(), trace: p.Trace, traceCounts: map[string]int64{}}
	if p.Dispatcher == nil || p.Logger == nil {
		return sink
	}
	sink.bindAgent(p.Dispatcher, p.Logger)
	sink.bindThread(p.Dispatcher, p.Logger)
	sink.bindTurn(p.Dispatcher, p.Logger)
	sink.bindTool(p.Dispatcher, p.Logger)
	sink.bindTask(p.Dispatcher, p.Logger)
	sink.bindUI(p.Dispatcher, p.Logger)
	return sink
}

func (s *LogSink) Close() {
	if s == nil || s.subs == nil {
		return
	}
	s.subs.CancelAll()
	s.subs = nil
}

func (s *LogSink) bindAgent(dispatcher *event.Dispatcher, logger *pkglogger.Logger) {
	s.subs.Add(logEvent[agentdto.StateChanged](dispatcher, logger, s.traceLifecycleEvent))
	s.subs.Add(logEvent[agentdto.AgentLaunched](dispatcher, logger, s.traceLifecycleEvent))
	s.subs.Add(logEvent[agentdto.AgentStopped](dispatcher, logger, s.traceLifecycleEvent))
	s.subs.Add(logDebugEvent[agentdto.AgentRecovering](dispatcher, logger, s.traceHighFrequencyLifecycleEvent))
	s.subs.Add(logDebugEvent[agentdto.AgentFailed](dispatcher, logger, s.traceHighFrequencyLifecycleEvent))
	s.subs.Add(logDebugEvent[agentdto.AgentRuntimeReported](dispatcher, logger, s.traceHighFrequencyLifecycleEvent))
	s.subs.Add(logEvent[agentdto.AgentWarning](dispatcher, logger, s.traceLifecycleEvent))
	s.subs.Add(logEvent[agentdto.AgentError](dispatcher, logger, s.traceLifecycleEvent))
}

func (s *LogSink) bindThread(dispatcher *event.Dispatcher, logger *pkglogger.Logger) {
	s.subs.Add(logEvent[threaddto.Started](dispatcher, logger, s.traceLifecycleEvent))
	s.subs.Add(logEvent[threaddto.Stopped](dispatcher, logger, s.traceLifecycleEvent))
	s.subs.Add(logDebugEvent[threaddto.MessagesPage](dispatcher, logger, s.traceHighFrequencyLifecycleEvent))
}

func (s *LogSink) bindTurn(dispatcher *event.Dispatcher, logger *pkglogger.Logger) {
	s.subs.Add(logEvent[turndto.TurnStarted](dispatcher, logger, s.traceLifecycleEvent))
	s.subs.Add(logEvent[turndto.TurnCompleted](dispatcher, logger, s.traceLifecycleEvent))
	s.subs.Add(logEvent[turndto.TurnInterrupted](dispatcher, logger, s.traceLifecycleEvent))
	s.subs.Add(logEvent[turndto.TurnStalled](dispatcher, logger, s.traceLifecycleEvent))
	s.subs.Add(logEvent[turndto.TurnResumed](dispatcher, logger, s.traceLifecycleEvent))
	s.subs.Add(logDebugEvent[turndto.TurnInputReceived](dispatcher, logger, s.traceHighFrequencyLifecycleEvent))
	s.subs.Add(logDebugEvent[turndto.TurnOutputDelta](dispatcher, logger, s.traceHighFrequencyLifecycleEvent))
	s.subs.Add(logDebugEvent[turndto.PlanDelta](dispatcher, logger, s.traceHighFrequencyLifecycleEvent))
	s.subs.Add(logEvent[turndto.PlanUpdated](dispatcher, logger, s.traceLifecycleEvent))
	s.subs.Add(logDebugEvent[turndto.ItemStarted](dispatcher, logger, s.traceHighFrequencyLifecycleEvent))
	s.subs.Add(logDebugEvent[turndto.ItemCompleted](dispatcher, logger, s.traceHighFrequencyLifecycleEvent))
}

func (s *LogSink) bindTool(dispatcher *event.Dispatcher, logger *pkglogger.Logger) {
	s.subs.Add(logDebugEvent[tooldto.ToolCallBegin](dispatcher, logger, s.traceHighFrequencyLifecycleEvent))
	s.subs.Add(logDebugEvent[tooldto.ToolCallEnd](dispatcher, logger, s.traceHighFrequencyLifecycleEvent))
	s.subs.Add(logDebugEvent[tooldto.ToolApprovalRequested](dispatcher, logger, s.traceHighFrequencyLifecycleEvent))
	s.subs.Add(logDebugEvent[tooldto.ToolApprovalResolved](dispatcher, logger, s.traceHighFrequencyLifecycleEvent))
}

func (s *LogSink) bindTask(dispatcher *event.Dispatcher, logger *pkglogger.Logger) {
	s.subs.Add(logEvent[taskdto.TaskDagCreated](dispatcher, logger, s.traceLifecycleEvent))
	s.subs.Add(logEvent[taskdto.TaskNodeStatusChanged](dispatcher, logger, s.traceLifecycleEvent))
	s.subs.Add(logEvent[taskdto.TaskWakeupDispatched](dispatcher, logger, s.traceLifecycleEvent))
	s.subs.Add(logEvent[taskdto.TaskWakeupCompleted](dispatcher, logger, s.traceLifecycleEvent))
}

func (s *LogSink) bindUI(dispatcher *event.Dispatcher, logger *pkglogger.Logger) {
	s.subs.Add(logDebugEvent[uidto.UIProjectionUpdated](dispatcher, logger, s.traceHighFrequencyLifecycleEvent))
	s.subs.Add(logDebugEvent[uidto.UITimelineAppended](dispatcher, logger, s.traceHighFrequencyLifecycleEvent))
	s.subs.Add(logDebugEvent[uidto.UITokensUpdated](dispatcher, logger, s.traceHighFrequencyLifecycleEvent))
}

func logEvent[T event.Event](dispatcher *event.Dispatcher, logger *pkglogger.Logger, trace func(any)) func() {
	if dispatcher == nil || logger == nil {
		return func() {}
	}
	return event.Subscribe(dispatcher, func(ev T) {
		logger.Info("bus event",
			pkglogger.String("event_type", eventTypeName(ev)),
			pkglogger.Any("event", ev),
		)
		if trace != nil {
			trace(ev)
		}
	})
}

func logDebugEvent[T event.Event](dispatcher *event.Dispatcher, logger *pkglogger.Logger, trace func(any)) func() {
	if dispatcher == nil || logger == nil {
		return func() {}
	}
	return event.Subscribe(dispatcher, func(ev T) {
		logger.Debug("bus event",
			pkglogger.String("event_type", eventTypeName(ev)),
			pkglogger.Any("event", ev),
		)
		if trace != nil {
			trace(ev)
		}
	})
}

func eventTypeName(ev any) string {
	if ev == nil {
		return "<nil>"
	}
	return reflect.TypeOf(ev).String()
}

const busHighFrequencyTraceEvery = 100

func (s *LogSink) traceLifecycleEvent(ev any) {
	s.recordTraceEvent(ev, observability.StatusOK, nil)
}

func (s *LogSink) traceHighFrequencyLifecycleEvent(ev any) {
	if s == nil || s.trace == nil {
		return
	}
	eventType := eventTypeName(ev)
	s.traceMu.Lock()
	s.traceCounts[eventType]++
	count := s.traceCounts[eventType]
	if count < busHighFrequencyTraceEvery {
		s.traceMu.Unlock()
		return
	}
	s.traceCounts[eventType] = 0
	s.traceMu.Unlock()
	s.recordTraceEvent(ev, observability.StatusDroppedSummary, map[string]any{
		"event_type":    eventType,
		"dropped_count": count,
	})
}

func (s *LogSink) recordTraceEvent(ev any, status observability.Status, metadata map[string]any) {
	if s == nil || s.trace == nil {
		return
	}
	ids := busTraceIdentifiers(ev)
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["event_type"] = eventTypeName(ev)
	_ = s.trace.Record(context.Background(), observability.TraceEvent{
		SchemaVersion: observability.SchemaVersion,
		Timestamp:     time.Now(),
		Kind:          "bus_event",
		Method:        "bus.event.lifecycle",
		ThreadID:      ids.threadID,
		AgentID:       ids.agentID,
		TurnID:        ids.turnID,
		CallID:        ids.callID,
		ToolName:      ids.toolName,
		Status:        status,
		Code:          observability.NewCodeAnchor("internal/platform/bus/sink.go", "LogSink.recordTraceEvent", 0),
		Metadata:      metadata,
	})
}

type busTraceIDs struct{ threadID, agentID, turnID, callID, toolName string }

func busTraceIdentifiers(ev any) busTraceIDs {
	value := reflect.Indirect(reflect.ValueOf(ev))
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return busTraceIDs{}
	}
	return busTraceIDs{
		threadID: stringField(value, "ThreadID"),
		agentID:  stringField(value, "AgentID"),
		turnID:   stringField(value, "TurnID"),
		callID:   stringField(value, "CallID"),
		toolName: stringField(value, "ToolName"),
	}
}

func stringField(value reflect.Value, name string) string {
	field := value.FieldByName(name)
	if !field.IsValid() && value.Kind() == reflect.Struct {
		for i := 0; i < value.NumField(); i++ {
			candidate := value.Field(i)
			if candidate.Kind() == reflect.Struct {
				if out := stringField(candidate, name); out != "" {
					return out
				}
			}
		}
		return ""
	}
	if !field.IsValid() || field.Kind() != reflect.String {
		return ""
	}
	return strings.TrimSpace(field.String())
}
