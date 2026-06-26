// Package bus 提供基于 kelindar/event 的进程内事件总线，封装 Dispatcher 的创建、
// 订阅生命周期管理和结构化日志追踪。
package bus

import (
	"context"
	"log/slog"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	taskdto "github.com/anthropic-ai/super-agent-v3/internal/dto/task"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"github.com/kelindar/event"
)

// LogSink 订阅总线上的已知事件类型，将其镜像到结构化日志，并按需记录追踪信息。
type LogSink struct {
	subs        *Subscription    // 订阅集合，Close 时统一注销
	trace       TraceRecorder    // 可选的追踪记录器
	traceMu     sync.Mutex       // 保护 traceCounts 的并发写
	traceCounts map[string]int64 // 高频事件采样计数器，按事件类型分组
}

// TraceStatus 表示追踪记录的状态类型。
type TraceStatus string

const (
	TraceStatusOK             TraceStatus = "ok"
	TraceStatusDroppedSummary TraceStatus = "dropped_summary"
)

// TraceCodeAnchor 记录追踪事件发生时的调用栈位置。
type TraceCodeAnchor struct {
	File     string // 源文件路径
	Function string // 函数名
	Line     int    // 行号
}

// TraceRecord 是写入追踪后端的单条追踪记录，字段对齐 OpenTelemetry span 语义。
type TraceRecord struct {
	SchemaVersion int             // 记录格式版本，当前为 1
	Timestamp     time.Time       // 事件发生时间（UTC）
	TraceID       string          // 分布式追踪 ID
	SpanID        string          // 当前 span ID
	ParentSpanID  string          // 父 span ID
	Kind          string          // span 类型，如 "bus_event"
	Method        string          // 业务方法标识
	ThreadID      string          // 关联的 thread ID
	AgentID       string          // 关联的 agent ID
	TurnID        string          // 关联的 turn ID
	CallID        string          // 关联的工具调用 ID
	ToolName      string          // 工具名称
	Status        TraceStatus     // 追踪状态
	Code          TraceCodeAnchor // 调用栈位置
	Metadata      map[string]any  // 附加元数据
}

// TraceRecorder 是写入追踪后端的接口，由外部实现并通过 fx 可选注入。
type TraceRecorder interface {
	RecordTrace(context.Context, TraceRecord) error
}

// LogSinkDeps 是 NewLogSink 的依赖参数结构。
type LogSinkDeps struct {
	Dispatcher *event.Dispatcher
	Logger     *pkglogger.Logger
	Trace      TraceRecorder
}

// NewLogSink 创建 LogSink 并订阅所有已知事件类型；dispatcher 或 logger 为 nil 时禁用事件日志。
func NewLogSink(p LogSinkDeps) *LogSink {
	sink := &LogSink{subs: NewSubscription(), trace: p.Trace, traceCounts: map[string]int64{}}
	if p.Dispatcher == nil || p.Logger == nil {
		slog.Warn("bus: NewLogSink called with nil dispatcher or logger, event logging disabled")
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

// Close 注销所有事件订阅，释放资源；幂等，重复调用安全。
func (s *LogSink) Close() {
	if s.subs == nil {
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
	s.recordTraceEvent(ev, TraceStatusOK, nil)
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
	s.recordTraceEvent(ev, TraceStatusDroppedSummary, map[string]any{
		"event_type":    eventType,
		"dropped_count": count,
	})
}

func (s *LogSink) recordTraceEvent(ev any, status TraceStatus, metadata map[string]any) {
	if s == nil || s.trace == nil {
		return
	}
	ids := busTraceIdentifiers(ev)
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["event_type"] = eventTypeName(ev)
	_ = s.trace.RecordTrace(context.Background(), TraceRecord{
		SchemaVersion: 1,
		Timestamp:     time.Now(),
		Kind:          "bus_event",
		Method:        "bus.event.lifecycle",
		TraceID:       ids.traceID,
		SpanID:        ids.spanID,
		ParentSpanID:  ids.parentSpanID,
		ThreadID:      ids.threadID,
		AgentID:       ids.agentID,
		TurnID:        ids.turnID,
		CallID:        ids.callID,
		ToolName:      ids.toolName,
		Status:        status,
		Code:          traceCodeAnchorFromCaller(0),
		Metadata:      metadata,
	})
}

func traceCodeAnchorFromCaller(skip int) TraceCodeAnchor {
	pc, file, line, ok := runtime.Caller(skip + 1)
	if !ok {
		return TraceCodeAnchor{}
	}
	function := ""
	if fn := runtime.FuncForPC(pc); fn != nil {
		function = fn.Name()
	}
	return TraceCodeAnchor{File: file, Function: function, Line: line}
}

type busTraceIDs struct{ traceID, spanID, parentSpanID, threadID, agentID, turnID, callID, toolName string }

func busTraceIdentifiers(ev any) busTraceIDs {
	value := reflect.Indirect(reflect.ValueOf(ev))
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return busTraceIDs{}
	}
	return busTraceIDs{
		traceID:      stringField(value, "TraceID"),
		spanID:       stringField(value, "SpanID"),
		parentSpanID: stringField(value, "ParentSpanID"),
		threadID:     stringField(value, "ThreadID"),
		agentID:      stringField(value, "AgentID"),
		turnID:       stringField(value, "TurnID"),
		callID:       stringField(value, "CallID"),
		toolName:     stringField(value, "ToolName"),
	}
}

// stringField 从事件结构体或嵌套结构体中读取指定 string 字段。
// trace 记录只消费字符串字段，其他类型会被忽略以避免反射 panic。
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
