package thread

import (
	"context"
	"strings"
	"time"

	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	"github.com/anthropic-ai/super-agent-v3/internal/util/idgen"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func (s *service) publishThreadStarted(state threadState) {
	if s == nil || s.emitStarted == nil {
		return
	}
	event := newThreadEvent(threadEventStartedKind, state.PublicThreadID, threadEventFields{State: state})
	if event == nil {
		return
	}
	s.emitStarted(event.(threaddto.Started))
}

func (s *service) publishThreadLaunched(state threadState) {
	if s == nil || s.emitLaunched == nil {
		return
	}
	event := newThreadEvent(threadEventLaunchedKind, state.PublicThreadID, threadEventFields{State: state})
	if event == nil {
		return
	}
	s.emitLaunched(event.(threaddto.Launched))
}

func (s *service) publishThreadStopped(threadID, agentID, status, reason string) {
	// 非用户主动停止的归档状态用 WARN 标记；正常停止只走 Info，避免告警噪声。
	if status == statusArchived {
		pkglogger.Warn("thread: publishThreadStopped ARCHIVED",
			"thread_id", threadID,
			"agent_id", agentID,
			"status", status,
			"reason", reason,
			"caller", archiveCallerStack(),
		)
	}
	if s == nil || s.emitStopped == nil {
		return
	}
	event := newThreadEvent(threadEventStoppedKind, threadID, threadEventFields{
		AgentID: agentID,
		Status:  status,
		Reason:  reason,
	})
	if event == nil {
		return
	}
	s.emitStopped(event.(threaddto.Stopped))
}

func (s *service) publishMessagesPage(threadID string, totalCount, pages int) {
	if s == nil || s.emitMessagesPage == nil {
		return
	}
	event := newThreadEvent(threadEventMessagesPageKind, threadID, threadEventFields{
		TotalCount: totalCount,
		Pages:      pages,
	})
	if event == nil {
		return
	}
	s.emitMessagesPage(event.(threaddto.MessagesPage))
}

// threadTraceSpan 保存一次 thread 操作的 trace 上下文和事件公共字段。
// Start 和 SpawnIfNeeded 会在执行前创建 span，结束时复用同一份字段记录 done/error。
type threadTraceSpan struct {
	ctx       context.Context
	trace     observability.TraceContext
	kind      string
	threadID  string
	agentID   string
	code      observability.CodeAnchor
	metadata  map[string]any
	startedAt time.Time
}

// beginThreadTraceSpan 为 thread start/spawn 创建子 span 并立即记录 begin 事件。
// 它会继承传入 context 的 trace id 和 parent span；没有上游 trace 时生成新的 trace id，
// 并把新 span 写回 context，保证后续 provider 启动、状态持久化和事件发布共享同一条观测链路。
func (s *service) beginThreadTraceSpan(
	ctx context.Context,
	kind, threadID, agentID string,
	code observability.CodeAnchor,
	metadata map[string]any,
) threadTraceSpan {
	ctx = util.NonNilContext(ctx)
	trace, ok := observability.TraceFromContext(ctx)
	parentSpanID := ""
	if ok {
		parentSpanID = trace.SpanID
	}
	if trace.TraceID == "" {
		trace.TraceID = idgen.NewID("trace")
	}
	trace.ParentSpanID = parentSpanID
	trace.SpanID = idgen.NewID("span")
	span := threadTraceSpan{
		ctx:       observability.ContextWithTrace(ctx, trace),
		trace:     trace,
		kind:      kind,
		threadID:  strings.TrimSpace(threadID),
		agentID:   strings.TrimSpace(agentID),
		code:      code,
		metadata:  metadata,
		startedAt: time.Now(),
	}
	s.recordThreadTraceEvent(span, "begin", observability.StatusOK, 0, "")
	return span
}

// finishThreadTraceSpan 根据最终错误状态记录 done 或 error 事件。
// 调用方通过 defer 执行它，因此即使 start/spawn 中途失败也能保留耗时和错误消息。
func (s *service) finishThreadTraceSpan(span threadTraceSpan, err error) {
	status := observability.StatusOK
	message := ""
	phase := "done"
	if err != nil {
		status = observability.StatusError
		message = err.Error()
		phase = "error"
	}
	s.recordThreadTraceEvent(span, phase, status, time.Since(span.startedAt).Milliseconds(), message)
}

// recordThreadTraceEvent 将 thread trace span 转换为 observability.TraceEvent 并写入观测服务。
// tracing 未装配时直接跳过；写入失败只记录 warning，不反向影响线程启动、恢复或事件发布主流程。
func (s *service) recordThreadTraceEvent(
	span threadTraceSpan,
	phase string,
	status observability.Status,
	durationMS int64,
	message string,
) {
	if s == nil || s.tracing == nil {
		return
	}
	event := observability.TraceEvent{
		SchemaVersion: observability.SchemaVersion,
		Timestamp:     time.Now(),
		TraceID:       span.trace.TraceID,
		SpanID:        span.trace.SpanID,
		ParentSpanID:  span.trace.ParentSpanID,
		Kind:          span.kind,
		Phase:         phase,
		Method:        span.kind,
		ThreadID:      span.threadID,
		AgentID:       span.agentID,
		DurationMS:    durationMS,
		Status:        status,
		Error:         message,
		Code:          span.code,
		Metadata:      span.metadata,
	}
	if err := s.tracing.Record(span.ctx, event); err != nil && s.logger != nil {
		s.logger.Warn("thread trace record failed", "kind", span.kind, "phase", phase, "error", err)
	}
}
